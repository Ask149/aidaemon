package tools

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Ask149/aidaemon/internal/permissions"
)

// dummyTool is a local test double (avoids circular import with testutil).
type dummyTool struct {
	name   string
	desc   string
	params map[string]interface{}
	result string
	err    error
	calls  int
}

func (d *dummyTool) Name() string                     { return d.name }
func (d *dummyTool) Description() string              { return d.desc }
func (d *dummyTool) Parameters() map[string]interface{} { return d.params }
func (d *dummyTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	d.calls++
	return d.result, d.err
}

func newDummy(name string) *dummyTool {
	return &dummyTool{
		name:   name,
		desc:   name + " tool",
		params: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		result: name + " result",
	}
}

// ------ Register / Get / List ------

func TestRegister(t *testing.T) {
	r := NewRegistry(nil)
	d := newDummy("alpha")
	r.Register(d)

	got := r.Get("alpha")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name() != "alpha" {
		t.Errorf("name = %q, want %q", got.Name(), "alpha")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("dup"))

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic on duplicate register")
		}
	}()
	r.Register(newDummy("dup"))
}

func TestGetNotFound(t *testing.T) {
	r := NewRegistry(nil)
	if got := r.Get("nope"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestList(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("a"))
	r.Register(newDummy("b"))
	r.Register(newDummy("c"))

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("list length = %d, want 3", len(list))
	}

	names := map[string]bool{}
	for _, t := range list {
		names[t.Name()] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !names[want] {
			t.Errorf("missing tool %q in list", want)
		}
	}
}

// ------ Definitions ------

func TestDefinitions(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("x"))
	r.Register(newDummy("y"))

	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions = %d, want 2", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Function.Name] = true
		if d.Type != "function" {
			t.Errorf("type = %q, want %q", d.Type, "function")
		}
	}
	for _, want := range []string{"x", "y"} {
		if !names[want] {
			t.Errorf("missing definition %q", want)
		}
	}
}

// ------ Execute ------

func TestExecuteSuccess(t *testing.T) {
	r := NewRegistry(nil)
	d := newDummy("greet")
	d.result = "hi there"
	r.Register(d)

	result, err := r.Execute(context.Background(), "greet", `{"name":"world"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hi there" {
		t.Errorf("result = %q, want %q", result, "hi there")
	}
	if d.calls != 1 {
		t.Errorf("calls = %d, want 1", d.calls)
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	r := NewRegistry(nil)
	_, err := r.Execute(context.Background(), "nope", "{}")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error = %q, want 'unknown tool'", err.Error())
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("x"))

	_, err := r.Execute(context.Background(), "x", "not json")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid arguments JSON") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestExecuteEmptyArgs(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("x"))

	result, err := r.Execute(context.Background(), "x", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "x result" {
		t.Errorf("result = %q", result)
	}
}

// ------ ExecuteAll ------

func TestExecuteAll(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("a"))
	r.Register(newDummy("b"))

	calls := []ToolCall{
		{ID: "1", Function: FunctionCall{Name: "a", Arguments: "{}"}},
		{ID: "2", Function: FunctionCall{Name: "b", Arguments: "{}"}},
	}

	results := r.ExecuteAll(context.Background(), calls)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].IsError {
		t.Errorf("result[0] is error: %s", results[0].Content)
	}
	if results[1].IsError {
		t.Errorf("result[1] is error: %s", results[1].Content)
	}
}

func TestExecuteAllWithError(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("good"))
	// "bad" doesn't exist, so Execute will return "unknown tool" error

	calls := []ToolCall{
		{ID: "1", Function: FunctionCall{Name: "good", Arguments: "{}"}},
		{ID: "2", Function: FunctionCall{Name: "bad", Arguments: "{}"}},
	}

	results := r.ExecuteAll(context.Background(), calls)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].IsError {
		t.Errorf("result[0] should not be error")
	}
	if !results[1].IsError {
		t.Error("result[1] should be error")
	}
}

// ------ Permissions ------

func TestCheckPermissions_PathDenied(t *testing.T) {
	rules := map[string]permissions.Rule{
		"read_file": {
			Mode:        permissions.ModeDeny,
			DeniedPaths: []string{"/etc/shadow"},
		},
	}
	r := NewRegistry(permissions.NewChecker(rules))
	r.Register(newDummy("read_file"))

	_, err := r.Execute(context.Background(), "read_file", `{"path":"/etc/shadow"}`)
	if err == nil {
		t.Fatal("expected permission denied error")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want 'denied'", err.Error())
	}
}

func TestCheckPermissions_CommandDenied(t *testing.T) {
	rules := map[string]permissions.Rule{
		"run_command": {
			Mode:           permissions.ModeDeny,
			DeniedCommands: []string{"rm"},
		},
	}
	r := NewRegistry(permissions.NewChecker(rules))
	r.Register(newDummy("run_command"))

	_, err := r.Execute(context.Background(), "run_command", `{"command":"rm -rf /"}`)
	if err == nil {
		t.Fatal("expected permission denied error")
	}
}

func TestCheckPermissions_AllowAll(t *testing.T) {
	rules := map[string]permissions.Rule{
		"read_file": {Mode: permissions.ModeAllowAll},
	}
	r := NewRegistry(permissions.NewChecker(rules))
	r.Register(newDummy("read_file"))

	result, err := r.Execute(context.Background(), "read_file", `{"path":"/anything"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "read_file result" {
		t.Errorf("result = %q", result)
	}
}

// ------ Audit logging ------

func TestAuditLogging(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("audited"))

	var buf bytes.Buffer
	r.SetAuditWriter(&buf)

	_, err := r.Execute(context.Background(), "audited", "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	log := buf.String()
	if !strings.Contains(log, "CALL") {
		t.Errorf("audit log missing CALL entry: %q", log)
	}
	if !strings.Contains(log, "OK") {
		t.Errorf("audit log missing OK entry: %q", log)
	}
	if !strings.Contains(log, "audited") {
		t.Errorf("audit log missing tool name: %q", log)
	}
}

func TestAuditLoggingDisabled(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(newDummy("silent"))

	// No audit writer set — should not panic
	_, err := r.Execute(context.Background(), "silent", "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
