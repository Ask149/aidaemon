// Package testutil provides shared test helpers for AIDaemon.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
)

// TempStore creates a temporary SQLite store for testing.
// The database file is automatically cleaned up when the test finishes.
func TempStore(t *testing.T, limit int) *store.Store {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := store.New(dbPath, limit)
	if err != nil {
		t.Fatalf("TempStore: %v", err)
	}

	t.Cleanup(func() {
		st.Close()
	})

	return st
}

// --- MockProvider ---

// MockProvider is a configurable stub implementing provider.Provider.
// Set the fields to control what Chat/Stream return.
type MockProvider struct {
	// ChatFn is called for each Chat invocation. If nil, ChatResponse is returned.
	ChatFn func(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error)

	// ChatResponse is returned when ChatFn is nil.
	ChatResponse *provider.ChatResponse

	// ChatCalls records every ChatRequest received (for assertions).
	ChatCalls []provider.ChatRequest

	// StreamFn is called for each Stream invocation.
	StreamFn func(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error)

	// ModelList is returned by Models().
	ModelList []provider.ModelInfo

	// ProviderName is returned by Name().
	ProviderName string
}

func (m *MockProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.ChatCalls = append(m.ChatCalls, req)

	if m.ChatFn != nil {
		return m.ChatFn(ctx, req)
	}
	if m.ChatResponse != nil {
		return m.ChatResponse, nil
	}
	return &provider.ChatResponse{Content: "mock response"}, nil
}

func (m *MockProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	if m.StreamFn != nil {
		return m.StreamFn(ctx, req)
	}
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Delta: "mock", Done: true}
	close(ch)
	return ch, nil
}

func (m *MockProvider) Models() []provider.ModelInfo {
	if m.ModelList != nil {
		return m.ModelList
	}
	return []provider.ModelInfo{{ID: "test-model", Name: "Test Model"}}
}

func (m *MockProvider) Name() string {
	if m.ProviderName != "" {
		return m.ProviderName
	}
	return "mock"
}

// --- DummyTool ---

// DummyTool is a configurable stub implementing tools.Tool.
type DummyTool struct {
	ToolName   string
	ToolDesc   string
	ToolParams map[string]interface{}

	// ExecuteFn is called on Execute. If nil, returns Result.
	ExecuteFn func(ctx context.Context, args map[string]interface{}) (string, error)

	// Result is returned when ExecuteFn is nil.
	Result string

	// ExecuteCalls records all args received.
	ExecuteCalls []map[string]interface{}
}

func (d *DummyTool) Name() string { return d.ToolName }

func (d *DummyTool) Description() string {
	if d.ToolDesc != "" {
		return d.ToolDesc
	}
	return "A dummy tool for testing"
}

func (d *DummyTool) Parameters() map[string]interface{} {
	if d.ToolParams != nil {
		return d.ToolParams
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (d *DummyTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	d.ExecuteCalls = append(d.ExecuteCalls, args)

	if d.ExecuteFn != nil {
		return d.ExecuteFn(ctx, args)
	}
	if d.Result != "" {
		return d.Result, nil
	}
	return "dummy result", nil
}

// --- MemoryStore ---

// MemoryStore is an in-memory implementation of the store for testing.
// It stores messages in a map keyed by chat ID.
type MemoryStore struct {
	messages   map[string][]store.MessageWithID
	sessionsMu sync.RWMutex // Protects sessions slice
	sessions   []store.Session
	limit      int
	nextID     int64
}

// NewMemoryStore creates a new in-memory store.
func NewMemoryStore(limit int) *MemoryStore {
	return &MemoryStore{
		messages: make(map[string][]store.MessageWithID),
		limit:    limit,
		nextID:   1,
	}
}

func (m *MemoryStore) GetHistory(chatID string) ([]store.Message, error) {
	msgs := m.messages[chatID]
	start := 0
	if len(msgs) > m.limit {
		start = len(msgs) - m.limit
	}
	result := make([]store.Message, 0, len(msgs)-start)
	for _, msg := range msgs[start:] {
		result = append(result, store.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: msg.CreatedAt,
		})
	}
	return result, nil
}

func (m *MemoryStore) AddMessage(chatID, role, content string) error {
	m.messages[chatID] = append(m.messages[chatID], store.MessageWithID{
		ID:      m.nextID,
		Role:    role,
		Content: content,
	})
	m.nextID++

	// Trim.
	if len(m.messages[chatID]) > m.limit {
		m.messages[chatID] = m.messages[chatID][len(m.messages[chatID])-m.limit:]
	}
	return nil
}

func (m *MemoryStore) ClearChat(chatID string) error {
	delete(m.messages, chatID)
	return nil
}

func (m *MemoryStore) MessageCount(chatID string) (int, error) {
	return len(m.messages[chatID]), nil
}

func (m *MemoryStore) GetOldestN(chatID string, n int) ([]store.MessageWithID, error) {
	msgs := m.messages[chatID]
	if n > len(msgs) {
		n = len(msgs)
	}
	result := make([]store.MessageWithID, n)
	copy(result, msgs[:n])
	return result, nil
}

func (m *MemoryStore) ReplaceMessages(chatID string, deleteIDs []int64, role, content string) error {
	idSet := make(map[int64]bool)
	for _, id := range deleteIDs {
		idSet[id] = true
	}

	var remaining []store.MessageWithID
	for _, msg := range m.messages[chatID] {
		if !idSet[msg.ID] {
			remaining = append(remaining, msg)
		}
	}

	// Prepend summary.
	summary := store.MessageWithID{
		ID:      m.nextID,
		Role:    role,
		Content: content,
	}
	m.nextID++
	m.messages[chatID] = append([]store.MessageWithID{summary}, remaining...)
	return nil
}

func (m *MemoryStore) Limit() int {
	return m.limit
}

func (m *MemoryStore) Close() error {
	return nil
}

func (m *MemoryStore) ListSessions() ([]store.SessionInfo, error) {
	sessions := make([]store.SessionInfo, 0, len(m.messages))
	for chatID, msgs := range m.messages {
		var latest store.MessageWithID
		for _, msg := range msgs {
			if msg.CreatedAt.After(latest.CreatedAt) {
				latest = msg
			}
		}
		sessions = append(sessions, store.SessionInfo{
			ChatID:       chatID,
			MessageCount: len(msgs),
			LastActivity: latest.CreatedAt,
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastActivity.After(sessions[j].LastActivity)
	})
	return sessions, nil
}

func (m *MemoryStore) CreateSession(session store.Session) error {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()
	m.sessions = append(m.sessions, session)
	return nil
}

func (m *MemoryStore) GetSession(id string) (*store.Session, error) {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			sess := m.sessions[i] // Copy to avoid race
			return &sess, nil
		}
	}
	return nil, nil
}

func (m *MemoryStore) ActiveSession(channel string) (*store.Session, error) {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	for i := range m.sessions {
		if m.sessions[i].Channel == channel && m.sessions[i].Status == "active" {
			sess := m.sessions[i] // Copy to avoid race
			return &sess, nil
		}
	}
	return nil, nil
}

func (m *MemoryStore) UpdateSession(session store.Session) error {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()
	for i := range m.sessions {
		if m.sessions[i].ID == session.ID {
			m.sessions[i] = session
			return nil
		}
	}
	return nil
}

func (m *MemoryStore) ListAllSessions(channel string) ([]store.Session, error) {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	var result []store.Session
	for _, sess := range m.sessions {
		if channel == "" || sess.Channel == channel {
			result = append(result, sess) // Append copy
		}
	}
	// Sort by last_activity DESC.
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastActivity.After(result[j].LastActivity)
	})
	return result, nil
}

// TempDir creates a temporary directory for testing file operations.
// Returns the path. Cleaned up automatically when the test finishes.
func TempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// WriteTestFile creates a file with content in a temp directory for testing.
func WriteTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("WriteTestFile mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteTestFile: %v", err)
	}
	return path
}
