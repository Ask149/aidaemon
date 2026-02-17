# Webhook Trigger Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a `POST /hooks/wake` endpoint that allows external services and scripts to trigger the daemon via HTTP, with async (fire-and-forget) and sync (wait-for-response) modes.

**Architecture:** New HTTP endpoints in `internal/httpapi/`. Webhook runs persisted in a `webhook_runs` SQLite table. Execution reuses the existing engine pattern (dedicated `engine.Engine` instance). Async output delivered to Telegram via the existing `CronSender` interface. No new Go package — handlers live in `httpapi`, store operations in `internal/store/`.

**Tech Stack:** Go stdlib + existing project deps (modernc.org/sqlite). No new external dependencies.

---

### Task 1: Store layer — WebhookRun struct, interface, migration, CRUD

**Files:**
- Modify: `internal/store/store.go` (add struct, interface methods, migration call)
- Create: `internal/store/webhook_runs.go`
- Create: `internal/store/webhook_runs_test.go`

**Step 1: Add WebhookRun struct to store.go**

In `internal/store/store.go`, add after the `CronRun` struct (after line 69, before `MessageWithID`):

```go
// WebhookRun records a single webhook invocation.
type WebhookRun struct {
	ID          string     `json:"id"`
	Prompt      string     `json:"prompt"`
	Payload     string     `json:"payload,omitempty"`     // JSON string of event payload
	Source      string     `json:"source,omitempty"`      // caller label (e.g., "github")
	ChannelType string     `json:"channel_type"`
	ChannelMeta string     `json:"channel_meta"`          // JSON
	Status      string     `json:"status"`                // "running", "completed", "failed"
	Output      string     `json:"output,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}
```

**Step 2: Add webhook methods to the Conversation interface**

In `internal/store/store.go`, add before `Close() error` (line 148), after the cron methods block:

```go
	// --- Webhook runs ---

	// CreateWebhookRun inserts a new webhook run record.
	CreateWebhookRun(run WebhookRun) error

	// UpdateWebhookRun updates a webhook run's status, output, and finished time.
	UpdateWebhookRun(id, status, output string, finishedAt time.Time) error

	// GetWebhookRun returns a webhook run by ID, or nil if not found.
	GetWebhookRun(id string) (*WebhookRun, error)

	// ListWebhookRuns returns recent webhook runs, newest first.
	ListWebhookRuns(limit, offset int) ([]WebhookRun, error)
```

**Step 3: Add migrateWebhookRuns call in migrate()**

In `internal/store/store.go`, in the `migrate()` method (line 205-232), add after the `migrateCronJobs()` call (after line 229):

```go
	// Migrate webhook runs table.
	if err := s.migrateWebhookRuns(); err != nil {
		return err
	}
```

**Step 4: Write failing tests**

Create `internal/store/webhook_runs_test.go`:

```go
package store

import (
	"testing"
	"time"
)

func TestCreateAndGetWebhookRun(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	run := WebhookRun{
		ID:          "wh_test1",
		Prompt:      "Review this alert",
		Payload:     `{"service":"api","status":"degraded"}`,
		Source:      "datadog",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":12345}`,
		Status:      "running",
		StartedAt:   now,
	}

	if err := s.CreateWebhookRun(run); err != nil {
		t.Fatalf("CreateWebhookRun: %v", err)
	}

	got, err := s.GetWebhookRun("wh_test1")
	if err != nil {
		t.Fatalf("GetWebhookRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetWebhookRun returned nil")
	}
	if got.Prompt != "Review this alert" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "Review this alert")
	}
	if got.Source != "datadog" {
		t.Errorf("Source = %q, want %q", got.Source, "datadog")
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want %q", got.Status, "running")
	}
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt should be nil, got %v", got.FinishedAt)
	}
}

func TestGetWebhookRun_NotFound(t *testing.T) {
	s := newTestStore(t, 100)
	got, err := s.GetWebhookRun("nonexistent")
	if err != nil {
		t.Fatalf("GetWebhookRun: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestUpdateWebhookRun(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	s.CreateWebhookRun(WebhookRun{
		ID:          "wh_update",
		Prompt:      "test",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":123}`,
		Status:      "running",
		StartedAt:   now,
	})

	finished := now.Add(5 * time.Second)
	if err := s.UpdateWebhookRun("wh_update", "completed", "LLM response here", finished); err != nil {
		t.Fatalf("UpdateWebhookRun: %v", err)
	}

	got, _ := s.GetWebhookRun("wh_update")
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
	if got.Output != "LLM response here" {
		t.Errorf("Output = %q, want %q", got.Output, "LLM response here")
	}
	if got.FinishedAt == nil {
		t.Fatal("FinishedAt should not be nil")
	}
}

func TestListWebhookRuns(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	// Create 5 runs.
	for i := 0; i < 5; i++ {
		s.CreateWebhookRun(WebhookRun{
			ID:          fmt.Sprintf("wh_%d", i),
			Prompt:      fmt.Sprintf("prompt %d", i),
			ChannelType: "telegram",
			ChannelMeta: "{}",
			Status:      "completed",
			StartedAt:   now.Add(time.Duration(i) * time.Minute),
		})
	}

	// List with limit 3.
	runs, err := s.ListWebhookRuns(3, 0)
	if err != nil {
		t.Fatalf("ListWebhookRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(runs))
	}
	// Should be newest first.
	if runs[0].ID != "wh_4" {
		t.Errorf("first run should be wh_4, got %s", runs[0].ID)
	}

	// List with offset.
	runs2, _ := s.ListWebhookRuns(3, 3)
	if len(runs2) != 2 {
		t.Fatalf("expected 2 runs with offset 3, got %d", len(runs2))
	}
}
```

Note: add `"fmt"` to the test imports.

**Step 5: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestCreate.*Webhook -v`
Expected: FAIL (methods not defined)

**Step 6: Implement webhook_runs.go**

Create `internal/store/webhook_runs.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// migrateWebhookRuns creates the webhook_runs table.
func (s *SQLiteStore) migrateWebhookRuns() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS webhook_runs (
			id           TEXT PRIMARY KEY,
			prompt       TEXT NOT NULL,
			payload      TEXT,
			source       TEXT,
			channel_type TEXT NOT NULL,
			channel_meta TEXT NOT NULL,
			status       TEXT NOT NULL,
			output       TEXT,
			started_at   INTEGER NOT NULL,
			finished_at  INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_webhook_runs_started
			ON webhook_runs(started_at DESC);
		CREATE INDEX IF NOT EXISTS idx_webhook_runs_status
			ON webhook_runs(status);
	`)
	if err != nil {
		return fmt.Errorf("migrate webhook runs: %w", err)
	}
	log.Printf("[store] webhook_runs table ready")
	return nil
}

// CreateWebhookRun inserts a new webhook run record.
func (s *SQLiteStore) CreateWebhookRun(run WebhookRun) error {
	_, err := s.db.Exec(`
		INSERT INTO webhook_runs (id, prompt, payload, source, channel_type, channel_meta, status, output, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.Prompt,
		nilIfEmpty(run.Payload),
		nilIfEmpty(run.Source),
		run.ChannelType,
		run.ChannelMeta,
		run.Status,
		nilIfEmpty(run.Output),
		run.StartedAt.Unix(),
		nilIfTime(run.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("create webhook run: %w", err)
	}
	log.Printf("[store] created webhook run %s (source=%s)", run.ID, run.Source)
	return nil
}

// UpdateWebhookRun updates a webhook run's status, output, and finished time.
func (s *SQLiteStore) UpdateWebhookRun(id, status, output string, finishedAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE webhook_runs
		SET status = ?, output = ?, finished_at = ?
		WHERE id = ?
	`,
		status,
		output,
		finishedAt.Unix(),
		id,
	)
	if err != nil {
		return fmt.Errorf("update webhook run: %w", err)
	}
	return nil
}

// GetWebhookRun returns a webhook run by ID, or nil if not found.
func (s *SQLiteStore) GetWebhookRun(id string) (*WebhookRun, error) {
	var run WebhookRun
	var payload, source, output sql.NullString
	var startedAt int64
	var finishedAt sql.NullInt64

	err := s.db.QueryRow(`
		SELECT id, prompt, payload, source, channel_type, channel_meta, status, output, started_at, finished_at
		FROM webhook_runs WHERE id = ?
	`, id).Scan(
		&run.ID, &run.Prompt, &payload, &source,
		&run.ChannelType, &run.ChannelMeta, &run.Status, &output,
		&startedAt, &finishedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get webhook run: %w", err)
	}

	run.Payload = payload.String
	run.Source = source.String
	run.Output = output.String
	run.StartedAt = time.Unix(startedAt, 0)
	if finishedAt.Valid {
		t := time.Unix(finishedAt.Int64, 0)
		run.FinishedAt = &t
	}

	return &run, nil
}

// ListWebhookRuns returns recent webhook runs, newest first.
func (s *SQLiteStore) ListWebhookRuns(limit, offset int) ([]WebhookRun, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.Query(`
		SELECT id, prompt, payload, source, channel_type, channel_meta, status, output, started_at, finished_at
		FROM webhook_runs
		ORDER BY started_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list webhook runs: %w", err)
	}
	defer rows.Close()

	var runs []WebhookRun
	for rows.Next() {
		var run WebhookRun
		var payload, source, output sql.NullString
		var startedAt int64
		var finishedAt sql.NullInt64

		err := rows.Scan(
			&run.ID, &run.Prompt, &payload, &source,
			&run.ChannelType, &run.ChannelMeta, &run.Status, &output,
			&startedAt, &finishedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan webhook run: %w", err)
		}

		run.Payload = payload.String
		run.Source = source.String
		run.Output = output.String
		run.StartedAt = time.Unix(startedAt, 0)
		if finishedAt.Valid {
			t := time.Unix(finishedAt.Int64, 0)
			run.FinishedAt = &t
		}

		runs = append(runs, run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhook runs: %w", err)
	}
	if runs == nil {
		runs = []WebhookRun{}
	}
	return runs, nil
}
```

**Step 7: Run all store tests**

Run: `go test ./internal/store/ -v -count=1`
Expected: ALL PASS

**Step 8: Commit**

```bash
git add internal/store/store.go internal/store/webhook_runs.go internal/store/webhook_runs_test.go
git commit -m "feat(store): add webhook_runs table with CRUD operations"
```

---

### Task 2: MemoryStore stubs in testutil.go

**Files:**
- Modify: `internal/testutil/testutil.go` (add webhook method stubs)

**Step 1: Add webhook method stubs**

In `internal/testutil/testutil.go`, add after the cron stubs (after line 339), before the `TempDir` function:

```go
// --- Webhook stubs (satisfy Conversation interface; not exercised by session tests) ---

func (m *MemoryStore) CreateWebhookRun(_ store.WebhookRun) error               { return nil }
func (m *MemoryStore) UpdateWebhookRun(_, _, _ string, _ time.Time) error       { return nil }
func (m *MemoryStore) GetWebhookRun(_ string) (*store.WebhookRun, error)        { return nil, nil }
func (m *MemoryStore) ListWebhookRuns(_, _ int) ([]store.WebhookRun, error)     { return nil, nil }
```

**Step 2: Run all tests to verify compilation**

Run: `go test ./... -count=1`
Expected: ALL PASS (no compilation errors)

**Step 3: Commit**

```bash
git add internal/testutil/testutil.go
git commit -m "feat(testutil): add webhook method stubs to MemoryStore"
```

---

### Task 3: HTTP webhook handler — POST /hooks/wake

**Files:**
- Modify: `internal/httpapi/httpapi.go` (add Config fields, routes, handler)

**Step 1: Add webhook-related fields to httpapi.Config**

In `internal/httpapi/httpapi.go`, add to the `Config` struct (after line 59, after `SessionManager`):

```go
	// WebhookSender delivers async webhook output to a channel.
	WebhookSender interface {
		SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error
	} `json:"-"`

	// WebhookChannelType is the default channel for async webhook delivery ("telegram").
	WebhookChannelType string `json:"-"`

	// WebhookChannelMeta is the default channel metadata (e.g., `{"chat_id":12345}`).
	WebhookChannelMeta string `json:"-"`
```

**Step 2: Register webhook routes**

In `internal/httpapi/httpapi.go`, in the `New()` function, add after the cron routes (after line 91):

```go
	mux.HandleFunc("POST /hooks/wake", api.requireAuth(api.handleWebhookWake))
	mux.HandleFunc("GET /hooks/runs", api.requireAuth(api.handleListWebhookRuns))
	mux.HandleFunc("GET /hooks/runs/{id}", api.requireAuth(api.handleGetWebhookRun))
```

**Step 3: Implement handleWebhookWake handler**

Add at the end of `internal/httpapi/httpapi.go` (before the closing):

```go
// --- Webhook handlers ---

type webhookWakeRequest struct {
	Prompt  string          `json:"prompt"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Source  string          `json:"source,omitempty"`
}

type webhookWakeResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
}

func (a *API) handleWebhookWake(w http.ResponseWriter, r *http.Request) {
	var req webhookWakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Prompt == "" {
		jsonError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	// Generate run ID.
	b := make([]byte, 8)
	rand.Read(b)
	runID := "wh_" + hex.EncodeToString(b)

	// Build the full prompt: user prompt + optional payload context.
	fullPrompt := req.Prompt
	if len(req.Payload) > 0 && string(req.Payload) != "null" {
		formatted, _ := json.MarshalIndent(json.RawMessage(req.Payload), "", "  ")
		fullPrompt = req.Prompt + "\n\nEvent payload:\n```json\n" + string(formatted) + "\n```"
	}

	// Determine channel config.
	channelType := a.cfg.WebhookChannelType
	channelMeta := a.cfg.WebhookChannelMeta
	if channelType == "" {
		channelType = "telegram"
	}
	if channelMeta == "" {
		channelMeta = "{}"
	}

	// Persist the run.
	run := store.WebhookRun{
		ID:          runID,
		Prompt:      req.Prompt,
		Payload:     string(req.Payload),
		Source:      req.Source,
		ChannelType: channelType,
		ChannelMeta: channelMeta,
		Status:      "running",
		StartedAt:   time.Now(),
	}
	if err := a.cfg.Store.CreateWebhookRun(run); err != nil {
		jsonError(w, http.StatusInternalServerError, "store error: "+err.Error())
		return
	}

	// Sync mode: block and return the result.
	sync := r.URL.Query().Get("sync") == "true"
	if sync {
		output, status := a.executeWebhook(r.Context(), runID, fullPrompt, channelType, channelMeta, false)
		jsonResp(w, http.StatusOK, webhookWakeResponse{
			RunID:  runID,
			Status: status,
			Output: output,
		})
		return
	}

	// Async mode: return 202 immediately, execute in background.
	jsonResp(w, http.StatusAccepted, webhookWakeResponse{
		RunID:  runID,
		Status: "running",
	})

	go a.executeWebhook(context.Background(), runID, fullPrompt, channelType, channelMeta, true)
}

// executeWebhook runs the LLM engine and updates the webhook run record.
// If sendToChannel is true, delivers output to the configured channel (Telegram).
// Returns the output and final status.
func (a *API) executeWebhook(ctx context.Context, runID, fullPrompt, channelType, channelMeta string, sendToChannel bool) (string, string) {
	// Build system prompt.
	sysPrompt := a.cfg.SysPrompt
	if a.cfg.WorkspaceDir != "" {
		ws := workspace.Load(a.cfg.WorkspaceDir, a.cfg.SkillsDir)
		sysPrompt = ws.SystemPrompt()
	}

	messages := []provider.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: fullPrompt},
	}

	result, err := a.engine.Run(ctx, messages, engine.RunOptions{
		Model:         a.cfg.Model,
		MaxIterations: 25,
	})

	var output, status string
	if err != nil {
		status = "failed"
		if result != nil && result.Content != "" {
			output = result.Content
		} else {
			output = err.Error()
		}
		log.Printf("[webhook] run %s failed: %v", runID, err)
	} else {
		status = "completed"
		output = result.Content
		log.Printf("[webhook] run %s completed (%s)", runID, result.Duration.Round(time.Millisecond))
	}

	// Update the run record.
	finished := time.Now()
	if updateErr := a.cfg.Store.UpdateWebhookRun(runID, status, output, finished); updateErr != nil {
		log.Printf("[webhook] update run %s: %v", runID, updateErr)
	}

	// Deliver to channel (async only).
	if sendToChannel && a.cfg.WebhookSender != nil && output != "" {
		prefix := "🔔 Webhook"
		// Include source in prefix if provided.
		run, _ := a.cfg.Store.GetWebhookRun(runID)
		if run != nil && run.Source != "" {
			prefix = "🔔 Webhook [" + run.Source + "]"
		}
		text := prefix + ":\n\n" + output
		if sendErr := a.cfg.WebhookSender.SendCronOutput(ctx, channelType, channelMeta, text); sendErr != nil {
			log.Printf("[webhook] send output for run %s: %v", runID, sendErr)
		}
	}

	return output, status
}
```

**Step 4: Run tests to verify compilation**

Run: `go build ./...`
Expected: BUILD SUCCESS

**Step 5: Commit**

```bash
git add internal/httpapi/httpapi.go
git commit -m "feat(httpapi): add POST /hooks/wake webhook endpoint"
```

---

### Task 4: HTTP run history handlers — GET /hooks/runs + GET /hooks/runs/{id}

**Files:**
- Modify: `internal/httpapi/httpapi.go` (add list/get handlers)

**Step 1: Implement handleListWebhookRuns and handleGetWebhookRun**

Add to `internal/httpapi/httpapi.go`, after the `executeWebhook` function:

```go
func (a *API) handleListWebhookRuns(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	runs, err := a.cfg.Store.ListWebhookRuns(limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, runs)
}

func (a *API) handleGetWebhookRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "run id is required")
		return
	}

	run, err := a.cfg.Store.GetWebhookRun(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		jsonError(w, http.StatusNotFound, "run not found")
		return
	}

	jsonResp(w, http.StatusOK, run)
}
```

Add `"strconv"` to the imports in httpapi.go.

**Step 2: Update the package doc comment**

In `internal/httpapi/httpapi.go`, update the package doc (lines 1-21) to add the webhook endpoints:

```go
//	POST /hooks/wake         — trigger a webhook (async or sync)
//	GET  /hooks/runs         — list recent webhook runs
//	GET  /hooks/runs/{id}    — get a specific webhook run
```

**Step 3: Run compilation check**

Run: `go build ./...`
Expected: BUILD SUCCESS

**Step 4: Commit**

```bash
git add internal/httpapi/httpapi.go
git commit -m "feat(httpapi): add webhook run history endpoints"
```

---

### Task 5: Wiring in main.go

**Files:**
- Modify: `cmd/aidaemon/main.go` (pass webhook sender + channel config to httpapi.Config)

**Step 1: Add webhook config to httpapi.New() call**

In `cmd/aidaemon/main.go`, in the HTTP API block (lines 243-264), add webhook fields to the `httpapi.Config`:

```go
		api := httpapi.New(httpapi.Config{
			Port:               cfg.Port,
			Token:              cfg.APIToken,
			Store:              st,
			Registry:           registry,
			Provider:           prov,
			Model:              cfg.ChatModel,
			SysPrompt:          initialPrompt,
			WorkspaceDir:       wsDir,
			SkillsDir:          skillsDir,
			WSHandler:          wsCh.Handler(),
			SessionManager:     mgr,
			WebhookSender:      cronSender,
			WebhookChannelType: "telegram",
			WebhookChannelMeta: fmt.Sprintf(`{"chat_id":%d}`, cfg.TelegramUserID),
		})
```

Note: `cronSender` is defined earlier (line 313-321) and may be nil if Telegram is not configured. This is fine — async delivery will just be skipped when nil.

Important: The HTTP API block (line 243) currently runs BEFORE the `cronSender` initialization (line 313). We need to move the HTTP API initialization to AFTER the cron sender is created, OR move the cronSender creation earlier.

The cleanest approach: move cronSender creation earlier, right after the Telegram bot is created (after line 295). This way both the HTTP API and cron scheduler can use it.

**Step 2: Run compilation check**

Run: `go build ./...`
Expected: BUILD SUCCESS

**Step 3: Run all tests**

Run: `go test ./... -count=1 -race`
Expected: ALL PASS

**Step 4: Commit**

```bash
git add cmd/aidaemon/main.go
git commit -m "feat(main): wire webhook sender and channel config into HTTP API"
```

---

### Task 6: Integration test

**Files:**
- Create: `internal/httpapi/webhook_test.go`

**Step 1: Write integration test**

Create `internal/httpapi/webhook_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ask149/aidaemon/internal/httpapi"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/tools"
)

func newTestAPI(t *testing.T) (*httpapi.API, *store.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(dir+"/test.db", 100)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Mock provider that returns a fixed response.
	mockProv := &mockProvider{response: "webhook response"}

	api := httpapi.New(httpapi.Config{
		Port:               0,
		Token:              "test-token",
		Store:              st,
		Registry:           tools.NewRegistry(nil),
		Provider:           mockProv,
		Model:              "test-model",
		SysPrompt:          "You are a test assistant.",
		WebhookChannelType: "telegram",
		WebhookChannelMeta: `{"chat_id":123}`,
	})

	return api, st
}

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	response string
}

func (m *mockProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: m.response}, nil
}

func (m *mockProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Delta: m.response, Done: true}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "test", Name: "Test"}}
}

func (m *mockProvider) Name() string { return "mock" }
```

Note: The test above sets up the scaffolding. The actual HTTP test will use `httptest.NewRecorder` against the API's handler. However, `httpapi.API` doesn't expose its handler directly — it wraps it in an `http.Server`. We may need to either:
1. Extract the mux as a public field/method, OR
2. Start the server on a random port and hit it with a real HTTP client.

Look at the existing code: the `API` struct has `server *http.Server` (private). The simplest approach is to start the server on port 0 and use `httptest` style testing. BUT the `Start` method blocks.

Alternative: create a helper that tests the handler directly by constructing the request/response. Since we can't access the private mux, we'll start the API on an ephemeral port in a goroutine.

Actually, the cleanest fix for testability: add a `Handler()` method to `API` that returns the `http.Handler`. This is a one-line addition:

```go
// Handler returns the HTTP handler for testing.
func (a *API) Handler() http.Handler {
	return a.server.Handler
}
```

Then tests can use `httptest.NewRecorder`.

**Full integration test:**

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ask149/aidaemon/internal/httpapi"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/tools"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	response string
}

func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: m.response}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Delta: m.response, Done: true}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "test", Name: "Test"}}
}

func (m *mockProvider) Name() string { return "mock" }

func newTestAPI(t *testing.T) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	api := httpapi.New(httpapi.Config{
		Port:               0,
		Token:              "test-token",
		Store:              st,
		Registry:           tools.NewRegistry(nil),
		Provider:           &mockProvider{response: "webhook response"},
		Model:              "test-model",
		SysPrompt:          "You are a test assistant.",
		WebhookChannelType: "telegram",
		WebhookChannelMeta: `{"chat_id":123}`,
	})

	return api.Handler(), st
}

func TestWebhookWake_Sync(t *testing.T) {
	handler, st := newTestAPI(t)

	body, _ := json.Marshal(map[string]interface{}{
		"prompt":  "Review this alert",
		"payload": map[string]string{"status": "degraded"},
		"source":  "datadog",
	})

	req := httptest.NewRequest("POST", "/hooks/wake?sync=true", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["status"] != "completed" {
		t.Errorf("status = %v, want completed", resp["status"])
	}
	if resp["output"] != "webhook response" {
		t.Errorf("output = %v, want 'webhook response'", resp["output"])
	}
	runID, _ := resp["run_id"].(string)
	if runID == "" {
		t.Fatal("expected non-empty run_id")
	}

	// Verify persisted.
	run, err := st.GetWebhookRun(runID)
	if err != nil {
		t.Fatalf("GetWebhookRun: %v", err)
	}
	if run == nil {
		t.Fatal("run not persisted")
	}
	if run.Status != "completed" {
		t.Errorf("persisted status = %q, want completed", run.Status)
	}
}

func TestWebhookWake_Async(t *testing.T) {
	handler, _ := newTestAPI(t)

	body, _ := json.Marshal(map[string]string{
		"prompt": "Do something",
	})

	req := httptest.NewRequest("POST", "/hooks/wake", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["status"] != "running" {
		t.Errorf("status = %v, want running", resp["status"])
	}
}

func TestWebhookWake_MissingPrompt(t *testing.T) {
	handler, _ := newTestAPI(t)

	body, _ := json.Marshal(map[string]string{})

	req := httptest.NewRequest("POST", "/hooks/wake", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestWebhookWake_Unauthorized(t *testing.T) {
	handler, _ := newTestAPI(t)

	body, _ := json.Marshal(map[string]string{"prompt": "test"})

	req := httptest.NewRequest("POST", "/hooks/wake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestWebhookRuns_ListAndGet(t *testing.T) {
	handler, st := newTestAPI(t)

	// Trigger a sync webhook to create a run.
	body, _ := json.Marshal(map[string]string{"prompt": "test"})
	req := httptest.NewRequest("POST", "/hooks/wake?sync=true", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var createResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &createResp)
	runID, _ := createResp["run_id"].(string)

	// List runs.
	listReq := httptest.NewRequest("GET", "/hooks/runs", nil)
	listReq.Header.Set("Authorization", "Bearer test-token")
	listW := httptest.NewRecorder()
	handler.ServeHTTP(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listW.Code)
	}

	var runs []interface{}
	json.Unmarshal(listW.Body.Bytes(), &runs)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	// Get specific run.
	getReq := httptest.NewRequest("GET", "/hooks/runs/"+runID, nil)
	getReq.Header.Set("Authorization", "Bearer test-token")
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body: %s", getW.Code, getW.Body.String())
	}

	// Get non-existent.
	get404 := httptest.NewRequest("GET", "/hooks/runs/nonexistent", nil)
	get404.Header.Set("Authorization", "Bearer test-token")
	w404 := httptest.NewRecorder()
	handler.ServeHTTP(w404, get404)

	if w404.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w404.Code)
	}
	_ = st // used indirectly
}
```

**Step 2: Add Handler() method to API**

In `internal/httpapi/httpapi.go`, add after the `Start` method:

```go
// Handler returns the HTTP handler (for testing).
func (a *API) Handler() http.Handler {
	return a.server.Handler
}
```

**Step 3: Run tests**

Run: `go test ./internal/httpapi/ -v -count=1 -race`
Expected: ALL PASS

**Step 4: Commit**

```bash
git add internal/httpapi/httpapi.go internal/httpapi/webhook_test.go
git commit -m "test(httpapi): add webhook integration tests"
```

---

### Task 7: Documentation — README + CHANGELOG

**Files:**
- Modify: `README.md` (add Webhook section)
- Modify: `CHANGELOG.md` (add v2.2.0 entry)

**Step 1: Update README.md**

Add a "Webhook Trigger" section after the Scheduled Tasks section, documenting the API endpoints.

**Step 2: Update CHANGELOG.md**

Add a v2.2.0 entry describing the webhook trigger feature.

**Step 3: Run full test suite**

Run: `go test ./... -count=1 -race`
Expected: ALL PASS

**Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: add webhook trigger documentation"
```
