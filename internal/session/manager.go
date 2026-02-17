// Package session manages conversation session lifecycle: creation,
// rotation, memory flushing, and proactive context compaction.
//
// The Manager sits between channels and the engine, orchestrating
// when sessions start, rotate, and archive. Engine stays a simple
// chat-loop runner.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
)

// ManagerConfig holds dependencies for the session Manager.
type ManagerConfig struct {
	Store        store.Conversation
	Engine       *engine.Engine
	Model        string
	TokenLimit   int     // model context window (default 128000)
	Threshold    float64 // rotation threshold (default 0.8)
	WorkspaceDir string  // for daily logs
	// SystemPromptFunc returns the current system prompt.
	// Called per message to pick up workspace changes.
	SystemPromptFunc func() string
}

// HandleOptions configures a single HandleMessage call.
type HandleOptions struct {
	ToolExecutor  engine.ToolExecutor
	OnProgress    func(engine.ProgressUpdate)
	MaxIterations int
}

// Manager orchestrates session lifecycle.
type Manager struct {
	store      store.Conversation
	engine     *engine.Engine
	model      string
	tokenLimit int
	threshold  float64
	wsDir      string
	sysPrompt  func() string
}

// NewManager creates a session Manager with the given config.
func NewManager(cfg ManagerConfig) *Manager {
	tokenLimit := cfg.TokenLimit
	if tokenLimit <= 0 {
		tokenLimit = 128000
	}
	threshold := cfg.Threshold
	if threshold <= 0 || threshold >= 1 {
		threshold = 0.8
	}
	sysPrompt := cfg.SystemPromptFunc
	if sysPrompt == nil {
		sysPrompt = func() string { return "" }
	}
	return &Manager{
		store:      cfg.Store,
		engine:     cfg.Engine,
		model:      cfg.Model,
		tokenLimit: tokenLimit,
		threshold:  threshold,
		wsDir:      cfg.WorkspaceDir,
		sysPrompt:  sysPrompt,
	}
}

// HandleMessage processes a user message within the session lifecycle.
// It finds or creates the active session, checks the token threshold,
// triggers rotation if needed, runs the engine, and updates session metadata.
func (m *Manager) HandleMessage(ctx context.Context, channelID, text string, opts HandleOptions) (*engine.Result, error) {
	// Find or create active session.
	sess, err := m.getOrCreateSession(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	// Check token threshold — rotate if over limit.
	estimate := sess.TokenEstimate + len(text)/4
	if estimate > int(float64(m.tokenLimit)*m.threshold) && sess.TokenEstimate > 0 {
		log.Printf("[session] token threshold reached for %s (estimate=%d, limit=%d), rotating",
			channelID, estimate, m.tokenLimit)
		newID, err := m.RotateSession(ctx, channelID)
		if err != nil {
			log.Printf("[session] rotation failed: %v — continuing with current session", err)
		} else {
			newSess, getErr := m.store.GetSession(newID)
			if getErr != nil || newSess == nil {
				return nil, fmt.Errorf("get rotated session %s: %w", newID, getErr)
			}
			sess = newSess
		}
	}

	// Add user message to store.
	if err := m.store.AddMessage(sess.ID, "user", text); err != nil {
		return nil, fmt.Errorf("add message: %w", err)
	}

	// Build message history.
	history, err := m.store.GetHistory(sess.ID)
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}

	sysPrompt := m.sysPrompt()
	messages := make([]provider.Message, 0, len(history)+1)
	if sysPrompt != "" {
		messages = append(messages, provider.Message{Role: "system", Content: sysPrompt})
	}
	for _, msg := range history {
		messages = append(messages, provider.Message{Role: msg.Role, Content: msg.Content})
	}

	// Run engine.
	eng := m.engine
	if opts.ToolExecutor != nil {
		// Create a per-call engine copy with the custom executor.
		eng = &engine.Engine{
			Provider: m.engine.Provider,
			Registry: m.engine.Registry,
			Executor: opts.ToolExecutor,
		}
	}

	result, err := eng.Run(ctx, messages, engine.RunOptions{
		Model:         m.model,
		MaxIterations: opts.MaxIterations,
		OnProgress:    opts.OnProgress,
	})

	// Save assistant response even on partial results.
	if result != nil && result.Content != "" {
		if saveErr := m.store.AddMessage(sess.ID, "assistant", result.Content); saveErr != nil {
			log.Printf("[session] save assistant message: %v", saveErr)
		}
	}

	// Update session metadata.
	sess.TokenEstimate = m.estimateTokens(sess.ID)
	sess.LastActivity = time.Now()
	if updateErr := m.store.UpdateSession(*sess); updateErr != nil {
		log.Printf("[session] update session: %v", updateErr)
	}

	// Generate title after first exchange (async).
	if sess.Title == "" && result != nil && result.Content != "" {
		go m.generateTitle(ctx, sess.ID, text, result.Content)
	}

	if err != nil {
		if result != nil && result.Content != "" {
			return result, nil // partial result
		}
		return nil, err
	}

	return result, nil
}

// ActiveSession returns the active session for a channel, or nil.
func (m *Manager) ActiveSession(channelID string) (*store.Session, error) {
	return m.store.ActiveSession(channelID)
}

// GetSession returns any session by ID.
func (m *Manager) GetSession(sessionID string) (*store.Session, error) {
	return m.store.GetSession(sessionID)
}

// ListSessions returns all sessions for a channel.
func (m *Manager) ListSessions(channelID string) ([]store.Session, error) {
	return m.store.ListAllSessions(channelID)
}

// RenameSession sets the title of a session.
func (m *Manager) RenameSession(sessionID, title string) error {
	sess, err := m.store.GetSession(sessionID)
	if err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	sess.Title = title
	return m.store.UpdateSession(*sess)
}

// RotateSession closes the active session for a channel and creates a new one.
// The full rotation flow:
// 1. Memory flush turn — silent Engine.Run asking agent to save context
// 2. Summarize the old session
// 3. Write daily log to workspace/memory/YYYY-MM-DD.md
// 4. Close old session with summary
// 5. Create new session with carry-forward summary message
func (m *Manager) RotateSession(ctx context.Context, channelID string) (string, error) {
	old, err := m.store.ActiveSession(channelID)
	if err != nil {
		return "", fmt.Errorf("find active session: %w", err)
	}
	if old == nil {
		// No active session — just create a new one.
		sess, err := m.createSession(channelID)
		if err != nil {
			return "", err
		}
		return sess.ID, nil
	}

	// 1. Memory flush — run a silent turn asking agent to save context.
	m.memoryFlush(ctx, old)

	// 2. Summarize the conversation.
	summary := m.summarizeSession(ctx, old.ID)

	// 3. Write daily log.
	if m.wsDir != "" && summary != "" {
		m.writeDailyLog(old, summary)
	}

	// 4. Close old session.
	now := time.Now()
	old.Status = "closed"
	old.ClosedAt = &now
	old.Summary = summary
	if err := m.store.UpdateSession(*old); err != nil {
		return "", fmt.Errorf("close session: %w", err)
	}

	// 5. Create new session with carry-forward.
	newSess, err := m.createSession(channelID)
	if err != nil {
		return "", err
	}

	if summary != "" {
		carryForward := "[Carried forward from previous session]\n\n" + summary
		m.store.AddMessage(newSess.ID, "assistant", carryForward)
	}

	log.Printf("[session] rotated %s → %s for channel %s", old.ID, newSess.ID, channelID)
	return newSess.ID, nil
}

// getOrCreateSession finds the active session or creates one.
func (m *Manager) getOrCreateSession(ctx context.Context, channelID string) (*store.Session, error) {
	sess, err := m.store.ActiveSession(channelID)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}
	return m.createSession(channelID)
}

// createSession creates a new active session for a channel.
func (m *Manager) createSession(channelID string) (*store.Session, error) {
	now := time.Now()
	sess := store.Session{
		ID:           generateSessionID(),
		Channel:      channelID,
		Status:       "active",
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := m.store.CreateSession(sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	log.Printf("[session] created session %s for channel %s", sess.ID, channelID)
	return &sess, nil
}

// estimateTokens estimates the token count for a session.
// Uses chars/4 heuristic — no tokenizer dependency.
func (m *Manager) estimateTokens(sessionID string) int {
	history, err := m.store.GetHistory(sessionID)
	if err != nil {
		return 0
	}
	total := 0
	for _, msg := range history {
		total += len(msg.Content)
	}
	return total / 4
}

// generateSessionID creates a short random session ID like "s_a7x3k9f2".
func generateSessionID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return "s_" + hex.EncodeToString(b)
}

// memoryFlush runs a silent engine turn asking the agent to persist
// important context to MEMORY.md before session rotation.
func (m *Manager) memoryFlush(ctx context.Context, sess *store.Session) {
	history, err := m.store.GetHistory(sess.ID)
	if err != nil || len(history) < 2 {
		return // nothing to flush
	}

	sysPrompt := m.sysPrompt()
	messages := make([]provider.Message, 0, len(history)+2)
	if sysPrompt != "" {
		messages = append(messages, provider.Message{Role: "system", Content: sysPrompt})
	}
	for _, msg := range history {
		messages = append(messages, provider.Message{Role: msg.Role, Content: msg.Content})
	}

	flushPrompt := "This session is being archived. Save any important context, decisions, facts, " +
		"or action items to MEMORY.md using the write_workspace tool. Be thorough — this is your " +
		"last chance to persist information from this conversation. Do NOT reply with a user-facing " +
		"message — only use tools."
	messages = append(messages, provider.Message{Role: "user", Content: flushPrompt})

	// Run with limited iterations — just enough for tool calls.
	_, err = m.engine.Run(ctx, messages, engine.RunOptions{
		Model:         m.model,
		MaxIterations: 5,
	})
	if err != nil {
		log.Printf("[session] memory flush failed for %s: %v", sess.ID, err)
	}
}

// summarizeSession calls a cheap model to summarize a session's conversation.
func (m *Manager) summarizeSession(ctx context.Context, sessionID string) string {
	history, err := m.store.GetHistory(sessionID)
	if err != nil || len(history) == 0 {
		return ""
	}

	// Build transcript (truncate individual messages).
	var transcript strings.Builder
	for _, msg := range history {
		transcript.WriteString(msg.Role)
		transcript.WriteString(": ")
		content := msg.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		transcript.WriteString(content)
		transcript.WriteString("\n")
	}

	req := provider.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []provider.Message{
			{
				Role:    "system",
				Content: "Summarize this conversation in 2-3 concise paragraphs. Preserve key facts: decisions, file paths, action items, and outcomes.",
			},
			{
				Role:    "user",
				Content: transcript.String(),
			},
		},
	}

	resp, err := m.engine.Provider.Chat(ctx, req)
	if err != nil || resp.Content == "" {
		log.Printf("[session] summarize failed for %s: %v", sessionID, err)
		return ""
	}
	return resp.Content
}

// writeDailyLog appends a session summary to workspace/memory/YYYY-MM-DD.md.
func (m *Manager) writeDailyLog(sess *store.Session, summary string) {
	memDir := filepath.Join(m.wsDir, "memory")
	if err := os.MkdirAll(memDir, 0700); err != nil {
		log.Printf("[session] create memory dir: %v", err)
		return
	}

	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join(memDir, dateStr+".md")

	title := sess.Title
	if title == "" {
		title = "Untitled Session"
	}

	entry := fmt.Sprintf("\n## Session: %s\n**%s → %s**\n\n%s\n",
		title,
		sess.CreatedAt.Format("15:04"),
		time.Now().Format("15:04"),
		summary,
	)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[session] write daily log: %v", err)
		return
	}
	defer f.Close()

	// Write header if file is new.
	stat, _ := f.Stat()
	if stat.Size() == 0 {
		fmt.Fprintf(f, "# %s\n", dateStr)
	}
	fmt.Fprint(f, entry)
}

// generateTitle asynchronously generates a session title from the first exchange.
func (m *Manager) generateTitle(parentCtx context.Context, sessionID, userMsg, assistantMsg string) {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	input := userMsg
	if len(input) > 200 {
		input = input[:200]
	}
	reply := assistantMsg
	if len(reply) > 200 {
		reply = reply[:200]
	}

	// Check if title was already generated by another goroutine.
	sess, err := m.store.GetSession(sessionID)
	if err != nil || sess == nil || sess.Title != "" {
		return
	}

	req := provider.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []provider.Message{
			{
				Role:    "system",
				Content: "Generate a 3-6 word title for this conversation. Return ONLY the title, no quotes or punctuation.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("User: %s\nAssistant: %s", input, reply),
			},
		},
	}

	resp, err := m.engine.Provider.Chat(ctx, req)
	if err != nil || resp == nil || resp.Content == "" {
		// Fallback: use first 50 chars of user message.
		title := userMsg
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		resp = &provider.ChatResponse{Content: title}
	}

	title := strings.TrimSpace(resp.Content)
	if title == "" {
		return
	}

	sess, err = m.store.GetSession(sessionID)
	if err != nil || sess == nil {
		return
	}
	sess.Title = title
	if err := m.store.UpdateSession(*sess); err != nil {
		log.Printf("[session] failed to update title for %s: %v", sessionID, err)
		return
	}
	log.Printf("[session] generated title for %s: %q", sessionID, title)
}
