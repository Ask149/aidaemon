package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/imageutil"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/tools"
)

// fakeTool implements tools.Tool for testing.
type fakeTool struct {
	name   string
	result string
	err    error
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return "fake tool for testing" }
func (f *fakeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (f *fakeTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return f.result, f.err
}

func newRegistry(tt ...tools.Tool) *tools.Registry {
	r := tools.NewRegistry(nil)
	for _, t := range tt {
		r.Register(t)
	}
	return r
}

func TestImageAwareExecutor_NoImages(t *testing.T) {
	reg := newRegistry(&fakeTool{name: "read_file", result: "file content here"})

	var called bool
	exec := &ImageAwareExecutor{
		Registry: reg,
		OnImage: func(ctx context.Context, toolName string, images []imageutil.Image) {
			called = true
		},
	}

	calls := []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "read_file", Arguments: "{}"}},
	}
	results := exec.ExecuteToolCalls(context.Background(), calls)

	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Content != "file content here" {
		t.Errorf("content = %q, want %q", results[0].Content, "file content here")
	}
	if results[0].Role != "tool" {
		t.Errorf("role = %q, want %q", results[0].Role, "tool")
	}
	if results[0].ToolCallID != "c1" {
		t.Errorf("ToolCallID = %q, want %q", results[0].ToolCallID, "c1")
	}
	if called {
		t.Error("OnImage should not be called when no images present")
	}
}

func TestImageAwareExecutor_WithImages(t *testing.T) {
	reg := newRegistry(&fakeTool{
		name:   "mcp_playwright_browser_take_screenshot",
		result: "text [MCP_IMAGE:image/png:AAAA] more",
	})

	var mu sync.Mutex
	var gotImages []imageutil.Image
	var gotToolName string

	exec := &ImageAwareExecutor{
		Registry: reg,
		OnImage: func(ctx context.Context, toolName string, images []imageutil.Image) {
			mu.Lock()
			defer mu.Unlock()
			gotImages = images
			gotToolName = toolName
		},
	}

	calls := []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "mcp_playwright_browser_take_screenshot", Arguments: "{}"}},
	}
	results := exec.ExecuteToolCalls(context.Background(), calls)

	// Content should have markers replaced.
	if !strings.Contains(results[0].Content, "[Screenshot delivered]") {
		t.Errorf("content should contain placeholder, got %q", results[0].Content)
	}
	if strings.Contains(results[0].Content, "[MCP_IMAGE:") {
		t.Error("content should not contain raw MCP_IMAGE markers")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotImages) != 1 {
		t.Fatalf("OnImage received %d images, want 1", len(gotImages))
	}
	if gotImages[0].MimeType != "image/png" {
		t.Errorf("MimeType = %q, want %q", gotImages[0].MimeType, "image/png")
	}
	if gotImages[0].Base64Data != "AAAA" {
		t.Errorf("Base64Data = %q, want %q", gotImages[0].Base64Data, "AAAA")
	}
	if gotToolName != "mcp_playwright_browser_take_screenshot" {
		t.Errorf("toolName = %q, want %q", gotToolName, "mcp_playwright_browser_take_screenshot")
	}
}

func TestImageAwareExecutor_NilOnImage(t *testing.T) {
	reg := newRegistry(&fakeTool{
		name:   "some_tool",
		result: "text [MCP_IMAGE:image/png:AAAA] end",
	})

	exec := &ImageAwareExecutor{
		Registry: reg,
		OnImage:  nil, // nil should not panic
	}

	calls := []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "some_tool", Arguments: "{}"}},
	}
	results := exec.ExecuteToolCalls(context.Background(), calls)

	// Should still clean markers even without callback.
	if strings.Contains(results[0].Content, "[MCP_IMAGE:") {
		t.Error("content should not contain raw MCP_IMAGE markers even with nil OnImage")
	}
}

func TestImageAwareExecutor_ToolError(t *testing.T) {
	reg := newRegistry(&fakeTool{
		name: "failing_tool",
		err:  fmt.Errorf("permission denied"),
	})

	exec := &ImageAwareExecutor{
		Registry: reg,
	}

	calls := []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "failing_tool", Arguments: "{}"}},
	}
	results := exec.ExecuteToolCalls(context.Background(), calls)

	if !strings.Contains(results[0].Content, "Error:") {
		t.Errorf("content = %q, want Error prefix", results[0].Content)
	}
}

func TestImageAwareExecutor_NilRegistry(t *testing.T) {
	exec := &ImageAwareExecutor{Registry: nil}
	results := exec.ExecuteToolCalls(context.Background(), []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "test", Arguments: "{}"}},
	})
	if results != nil {
		t.Errorf("results = %v, want nil", results)
	}
}

func TestImageAwareExecutor_AutoScreenshot(t *testing.T) {
	// Register a fake Playwright click tool and a fake screenshot tool.
	reg := newRegistry(
		&fakeTool{name: "mcp_playwright_browser_click", result: "clicked button"},
		&fakeTool{name: "mcp_playwright_browser_take_screenshot", result: "[MCP_IMAGE:image/png:SCREENSHOT_DATA]"},
	)

	var mu sync.Mutex
	var deliveries []string // tool names from OnImage calls

	exec := &ImageAwareExecutor{
		Registry:        reg,
		ScreenshotDelay: time.Millisecond, // fast for tests
		OnImage: func(ctx context.Context, toolName string, images []imageutil.Image) {
			mu.Lock()
			defer mu.Unlock()
			deliveries = append(deliveries, toolName)
		},
	}

	calls := []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "mcp_playwright_browser_click", Arguments: "{}"}},
	}
	results := exec.ExecuteToolCalls(context.Background(), calls)

	// The click result should be unchanged (no images in it).
	if results[0].Content != "clicked button" {
		t.Errorf("content = %q, want %q", results[0].Content, "clicked button")
	}

	// Auto-screenshot should have triggered and delivered an image.
	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != 1 {
		t.Fatalf("OnImage called %d times, want 1 (auto-screenshot)", len(deliveries))
	}
	if deliveries[0] != "auto-screenshot" {
		t.Errorf("toolName = %q, want %q", deliveries[0], "auto-screenshot")
	}
}

func TestImageAwareExecutor_NoAutoScreenshotWithoutTool(t *testing.T) {
	// Register only the click tool, NOT the screenshot tool.
	reg := newRegistry(
		&fakeTool{name: "mcp_playwright_browser_click", result: "clicked"},
	)

	var called bool
	exec := &ImageAwareExecutor{
		Registry: reg,
		OnImage: func(ctx context.Context, toolName string, images []imageutil.Image) {
			called = true
		},
	}

	calls := []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "mcp_playwright_browser_click", Arguments: "{}"}},
	}
	exec.ExecuteToolCalls(context.Background(), calls)

	if called {
		t.Error("OnImage should not be called when screenshot tool is unavailable")
	}
}

func TestImageAwareExecutor_MultipleCallsMixed(t *testing.T) {
	reg := newRegistry(
		&fakeTool{name: "read_file", result: "plain text"},
		&fakeTool{name: "some_mcp", result: "data [MCP_IMAGE:image/jpeg:IMG1] rest"},
	)

	var mu sync.Mutex
	var imageCount int

	exec := &ImageAwareExecutor{
		Registry: reg,
		OnImage: func(ctx context.Context, toolName string, images []imageutil.Image) {
			mu.Lock()
			defer mu.Unlock()
			imageCount += len(images)
		},
	}

	calls := []provider.ToolCall{
		{ID: "c1", Function: provider.FuncCall{Name: "read_file", Arguments: "{}"}},
		{ID: "c2", Function: provider.FuncCall{Name: "some_mcp", Arguments: "{}"}},
	}
	results := exec.ExecuteToolCalls(context.Background(), calls)

	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Content != "plain text" {
		t.Errorf("results[0].Content = %q, want %q", results[0].Content, "plain text")
	}
	if strings.Contains(results[1].Content, "[MCP_IMAGE:") {
		t.Error("results[1] should have MCP_IMAGE markers stripped")
	}

	mu.Lock()
	defer mu.Unlock()
	if imageCount != 1 {
		t.Errorf("imageCount = %d, want 1", imageCount)
	}
}
