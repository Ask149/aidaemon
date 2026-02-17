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
