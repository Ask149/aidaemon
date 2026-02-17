// Package telegram implements the Telegram bot frontend.
//
// It uses go-telegram/bot with long polling (works behind NAT, no public URL).
// Messages from unauthorized users are silently dropped.
// LLM responses are streamed via the edit-message pattern with adaptive debounce.
package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Ask149/aidaemon/internal/channel"
	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/mcp"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/tools"
	"github.com/Ask149/aidaemon/internal/workspace"
)

// Bot wraps the Telegram bot with LLM integration.
type Bot struct {
	bot          *bot.Bot
	provider     provider.Provider
	store        *store.Store
	tools        *tools.Registry
	engine       *engine.Engine
	sessionMgr   SessionManager // optional: routes through session lifecycle
	userID       int64
	model        string
	sysPrompt    string
	convLimit    int
	botToken     string // needed for file download URLs
	dataDir      string // root for media/logs/files persistence
	workspaceDir string // workspace directory for dynamic system prompts
	skillsDir    string // skills directory for skill loading

	// Guards per-chat to prevent concurrent LLM calls for the same chat.
	chatMu sync.Map // map[int64]*sync.Mutex
}

// Compile-time check: *Bot implements channel.Channel.
var _ channel.Channel = (*Bot)(nil)

// SessionManager provides session lifecycle management (optional).
type SessionManager interface {
	RotateSession(ctx context.Context, channelID string) (newSessionID string, err error)
	RenameSession(sessionID, title string) error
	ActiveSession(channelID string) (*store.Session, error)
}

// Config for creating a new Bot.
type Config struct {
	Token        string
	UserID       int64
	Provider     provider.Provider
	Store        *store.Store
	Model        string
	SystemPrompt string
	ConvLimit    int
	ToolRegistry *tools.Registry
	DataDir      string
	WorkspaceDir string
	SkillsDir    string
	SessionMgr   SessionManager // optional: enables /new and /title commands
}

// Name returns the channel identifier.
func (tb *Bot) Name() string { return "telegram" }

// New creates a new Telegram bot. Call Start() to begin polling.
func New(cfg Config) (*Bot, error) {
	tb := &Bot{
		provider:     cfg.Provider,
		store:        cfg.Store,
		tools:        cfg.ToolRegistry,
		sessionMgr:   cfg.SessionMgr,
		userID:       cfg.UserID,
		model:        cfg.Model,
		sysPrompt:    cfg.SystemPrompt,
		convLimit:    cfg.ConvLimit,
		botToken:     cfg.Token,
		dataDir:      cfg.DataDir,
		workspaceDir: cfg.WorkspaceDir,
		skillsDir:    cfg.SkillsDir,
	}

	// Initialize the chat engine for tool-call orchestration.
	tb.engine = &engine.Engine{
		Provider: cfg.Provider,
		Registry: cfg.ToolRegistry,
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(tb.handleMessage),
	}

	b, err := bot.New(cfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}
	tb.bot = b

	// Register commands.
	b.RegisterHandler(bot.HandlerTypeMessageText, "/reset", bot.MatchTypePrefix, tb.handleReset)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/status", bot.MatchTypePrefix, tb.handleStatus)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/model", bot.MatchTypePrefix, tb.handleModel)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/tools", bot.MatchTypePrefix, tb.handleTools)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/context", bot.MatchTypePrefix, tb.handleContext)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypePrefix, tb.handleHelp)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/new", bot.MatchTypePrefix, tb.handleNew)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/title", bot.MatchTypePrefix, tb.handleTitle)

	return tb, nil
}

// Start begins long polling. Blocks until ctx is cancelled.
func (tb *Bot) Start(ctx context.Context) error {
	log.Printf("[telegram] bot starting (user_id=%d, model=%s)", tb.userID, tb.model)
	tb.bot.Start(ctx)
	return nil
}

// Send delivers a message to the Telegram chat.
// Implements channel.Channel for server-initiated messages (heartbeat, etc.).
func (tb *Bot) Send(ctx context.Context, sessionID string, text string) error {
	// Parse chat ID from session ID (format: "telegram:<chatID>").
	chatIDStr := sessionID
	if idx := strings.Index(sessionID, ":"); idx >= 0 {
		chatIDStr = sessionID[idx+1:]
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID in session %q: %w", sessionID, err)
	}

	_, err = tb.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	return err
}

// --- Auth middleware ---

func (tb *Bot) isAuthorized(chatID int64) bool {
	return chatID == tb.userID
}

// getChatMutex returns a per-chat mutex to serialize LLM calls.
func (tb *Bot) getChatMutex(chatID int64) *sync.Mutex {
	mu, _ := tb.chatMu.LoadOrStore(chatID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// --- Handlers ---

func (tb *Bot) handleMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return // silent drop
	}

	chatID := update.Message.Chat.ID
	chatIDStr := channel.SessionID("telegram", strconv.FormatInt(chatID, 10))

	// Handle photos (image analysis).
	if len(update.Message.Photo) > 0 {
		tb.handlePhotoMessage(ctx, b, update)
		return
	}

	// Text messages only from here.
	if update.Message.Text == "" {
		return
	}

	// Skip messages that start with "/" — they're handled by registered handlers.
	if strings.HasPrefix(update.Message.Text, "/") {
		return
	}

	userText := update.Message.Text

	// Serialize LLM calls per chat.
	mu := tb.getChatMutex(chatID)
	mu.Lock()
	defer mu.Unlock()

	// Save user message.
	if err := tb.store.AddMessage(chatIDStr, "user", userText); err != nil {
		log.Printf("[telegram] save user msg: %v", err)
	}

	// Send placeholder.
	placeholder, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "⏳ Starting...",
	})
	if err != nil {
		log.Printf("[telegram] send placeholder: %v", err)
		return
	}

	// Build initial messages from conversation history.
	messages, err := tb.buildMessages(chatIDStr)
	if err != nil {
		log.Printf("[telegram] build messages: %v", err)
		tb.editText(ctx, chatID, placeholder.ID, "❌ Error loading conversation history.")
		return
	}

	// Set up Telegram-aware tool executor (handles MCP screenshots, etc.).
	tb.engine.Executor = &telegramToolExecutor{bot: tb, chatID: chatID}

	// Progress callback: update the placeholder message so the user
	// can see what's happening during multi-step tool execution.
	var lastProgressText string
	var lastProgressUpdate time.Time
	progressCb := func(update engine.ProgressUpdate) {
		// Skip if the text hasn't changed (avoids "not modified" errors).
		if update.Message == lastProgressText {
			return
		}
		// Rate-limit edits to avoid Telegram API throttling (~1/sec).
		if time.Since(lastProgressUpdate) < 1500*time.Millisecond {
			return
		}
		log.Printf("[telegram] progress: %s", update.Message)
		lastProgressText = update.Message
		lastProgressUpdate = time.Now()
		tb.editText(ctx, chatID, placeholder.ID, update.Message)
	}

	// Delegate to the chat engine for the tool-call loop.
	result, err := tb.engine.Run(ctx, messages, engine.RunOptions{
		Model:      tb.model,
		OnProgress: progressCb,
	})

	// Handle engine-level errors.
	if err != nil {
		log.Printf("[telegram] engine error: %v", err)
		if result != nil && result.Content != "" {
			// Partial result (e.g., summary after max iterations).
			goto sendResult
		}
		tb.editText(ctx, chatID, placeholder.ID, fmt.Sprintf("❌ %v", err))
		return
	}

	// Empty response.
	if result.Content == "" {
		tb.editText(ctx, chatID, placeholder.ID, "⚠️ Empty response from AI.")
		return
	}

sendResult:
	// Save final assistant response to persistent store.
	if err := tb.store.AddMessage(chatIDStr, "assistant", result.Content); err != nil {
		log.Printf("[telegram] save assistant msg: %v", err)
	}

	// Save a summary of tool usage for context continuity.
	if result.ToolIterations > 0 {
		if err := tb.store.AddMessage(chatIDStr, "assistant", "[Used tools to answer this question]"); err != nil {
			log.Printf("[telegram] save tool summary: %v", err)
		}
	}

	// Compact old messages if approaching the limit.
	go tb.compactIfNeeded(ctx, chatIDStr)

	// Append usage footer.
	footer := tb.buildStatsFooter(result)

	// Format as HTML for rich output (bold, code, links, etc.).
	formatted := FormatHTML(result.Content + footer)
	tb.editHTML(ctx, chatID, placeholder.ID, formatted)
}

// --- Stats footer ---

// buildStatsFooter creates a compact stats line appended to every response.
// Always shows at least timing and model name.
// Example outputs:
//
//	📊 1.2K→234 tok | ⏱ 3.4s | claude-sonnet-4.5
//	📊 45K→1.2K tok (68K total) | ⏱ 12.5s | 🔧 3 tools (read_file ×2, run_command) | 2 LLM calls | gpt-4o
//	📊 ~2.1K→89 tok | ⏱ 0.8s | claude-sonnet-4.5
func (tb *Bot) buildStatsFooter(result *engine.Result) string {
	if result == nil {
		return ""
	}

	var parts []string

	// Token usage — show API-reported counts, or estimated counts as fallback.
	hasAPITokens := result.Usage != nil && (result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0)

	if hasAPITokens {
		tokenStr := fmt.Sprintf("%s→%s",
			formatTokenCount(int(result.Usage.InputTokens)),
			formatTokenCount(int(result.Usage.OutputTokens)))

		// Show total across all LLM calls if there were multiple.
		if result.LLMCalls > 1 && result.TotalUsage.InputTokens > 0 {
			totalTokens := result.TotalUsage.InputTokens + result.TotalUsage.OutputTokens
			tokenStr += fmt.Sprintf(" (%s total)", formatTokenCount(int(totalTokens)))
		}

		if result.Usage.CachedTokens > 0 {
			tokenStr += fmt.Sprintf(" [%s cached]", formatTokenCount(int(result.Usage.CachedTokens)))
		}

		parts = append(parts, tokenStr+" tok")
	} else {
		// Estimate tokens from message content when API doesn't report usage.
		var inputEst, outputEst int
		for _, m := range result.Messages {
			inputEst += engine.EstimateMessageTokens(m)
		}
		outputEst = len(result.Content) / 4 // ~4 chars per token for natural text
		if inputEst > 0 || outputEst > 0 {
			parts = append(parts, fmt.Sprintf("~%s→%s tok",
				formatTokenCount(inputEst), formatTokenCount(outputEst)))
		}
	}

	// Timing.
	if result.Duration > 0 {
		parts = append(parts, fmt.Sprintf("⏱ %s", formatDuration(result.Duration)))
	}

	// Tool usage.
	if result.ToolIterations > 0 {
		toolStr := fmt.Sprintf("🔧 %d tool", len(result.ToolNames))
		if len(result.ToolNames) != 1 {
			toolStr += "s"
		}
		if uniqueNames := result.UniqueToolNames(); uniqueNames != "" {
			toolStr += " (" + uniqueNames + ")"
		}
		parts = append(parts, toolStr)
	}

	// LLM calls (only show if >1, since 1 is the norm).
	if result.LLMCalls > 1 {
		parts = append(parts, fmt.Sprintf("%d LLM calls", result.LLMCalls))
	}

	// Emergency summarization warnings.
	if result.SummarizeCount > 0 {
		parts = append(parts, fmt.Sprintf("⚠️ %d auto-compaction", result.SummarizeCount))
	}

	// Trimmed messages warning.
	if result.TrimmedMessages > 0 {
		parts = append(parts, fmt.Sprintf("✂️ %d msgs trimmed", result.TrimmedMessages))
	}

	// Always show at least model name — even with no other stats.
	if len(parts) == 0 {
		return fmt.Sprintf("\n\n📊 %s", tb.model)
	}

	// Model name goes at the end.
	return fmt.Sprintf("\n\n📊 %s | %s", strings.Join(parts, " | "), tb.model)
}

// --- New command handlers ---

// handleTools lists all available tools, grouped by source.
func (tb *Bot) handleTools(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}

	if tb.tools == nil || len(tb.tools.List()) == 0 {
		tb.sendText(ctx, update.Message.Chat.ID, "🔧 No tools registered.")
		return
	}

	// Group tools by source (built-in vs MCP server name).
	builtIn := make([]string, 0)
	mcpGroups := make(map[string][]string) // server → tool names
	mcpOrder := make([]string, 0)

	for _, t := range tb.tools.List() {
		name := t.Name()
		if strings.HasPrefix(name, "mcp_") {
			// Extract server name: "mcp_playwright_browser_click" → "playwright"
			parts := strings.SplitN(name, "_", 3)
			if len(parts) >= 3 {
				server := parts[1]
				shortName := parts[2]
				if _, exists := mcpGroups[server]; !exists {
					mcpOrder = append(mcpOrder, server)
				}
				mcpGroups[server] = append(mcpGroups[server], shortName)
			}
		} else {
			builtIn = append(builtIn, name)
		}
	}

	var sb strings.Builder
	sb.WriteString("🔧 <b>Available Tools</b>\n\n")

	if len(builtIn) > 0 {
		sb.WriteString("<b>⚡ Built-in</b>\n")
		for _, name := range builtIn {
			sb.WriteString(fmt.Sprintf("  • <code>%s</code>\n", name))
		}
		sb.WriteString("\n")
	}

	for _, server := range mcpOrder {
		tools := mcpGroups[server]
		sb.WriteString(fmt.Sprintf("<b>🔌 %s</b> (%d tools)\n", server, len(tools)))
		// Show first 10 tools, then summarize.
		showCount := len(tools)
		if showCount > 10 {
			showCount = 10
		}
		for _, name := range tools[:showCount] {
			sb.WriteString(fmt.Sprintf("  • <code>%s</code>\n", name))
		}
		if len(tools) > 10 {
			sb.WriteString(fmt.Sprintf("  <i>... and %d more</i>\n", len(tools)-10))
		}
		sb.WriteString("\n")
	}

	totalCount := len(builtIn)
	for _, t := range mcpGroups {
		totalCount += len(t)
	}
	sb.WriteString(fmt.Sprintf("<i>Total: %d tools across %d sources</i>", totalCount, len(mcpGroups)+1))

	tb.sendHTML(ctx, update.Message.Chat.ID, sb.String())
}

// handleContext shows detailed context window information.
func (tb *Bot) handleContext(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}

	chatIDStr := channel.SessionID("telegram", strconv.FormatInt(update.Message.Chat.ID, 10))
	count, _ := tb.store.MessageCount(chatIDStr)
	limit := tb.store.Limit()

	// Get actual messages to compute breakdown.
	history, err := tb.store.GetHistory(chatIDStr)

	var totalChars int
	var roleBreakdown [4]int // system, user, assistant, tool
	var hasSummary bool

	if err == nil {
		for _, m := range history {
			totalChars += len(m.Content)
			switch m.Role {
			case "system":
				roleBreakdown[0]++
				if strings.Contains(m.Content, "[Previous conversation summary") {
					hasSummary = true
				}
			case "user":
				roleBreakdown[1]++
			case "assistant":
				roleBreakdown[2]++
			case "tool":
				roleBreakdown[3]++
			}
		}
	}

	// Context usage.
	contextPct := 0
	if limit > 0 {
		contextPct = (count * 100) / limit
	}
	contextBar := progressBar(contextPct)

	// Token estimate.
	estimatedTokens := totalChars / 3 // conservative
	tokenLimitVal := modelTokenLimitStr(tb.model)

	// System prompt size.
	sysPrompt := tb.sysPrompt
	if tb.workspaceDir != "" {
		ws := workspace.Load(tb.workspaceDir, tb.skillsDir)
		sysPrompt = ws.SystemPrompt()
	}
	sysPromptTokens := len(sysPrompt) / 3

	var sb strings.Builder
	sb.WriteString("📐 <b>Context Window Details</b>\n\n")
	sb.WriteString(fmt.Sprintf("<b>Messages:</b> %d/%d %s (%d%%)\n", count, limit, contextBar, contextPct))
	sb.WriteString(fmt.Sprintf("<b>Est. tokens:</b> ~%s / %s\n", formatTokenCount(estimatedTokens), tokenLimitVal))
	sb.WriteString(fmt.Sprintf("<b>Characters:</b> %s\n\n", formatNumber(totalChars)))

	sb.WriteString("<b>Message Breakdown:</b>\n")
	sb.WriteString(fmt.Sprintf("  💻 System: %d", roleBreakdown[0]))
	if sysPromptTokens > 0 {
		sb.WriteString(fmt.Sprintf(" (~%s tok prompt)", formatTokenCount(sysPromptTokens)))
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  👤 User: %d\n", roleBreakdown[1]))
	sb.WriteString(fmt.Sprintf("  🤖 Assistant: %d\n", roleBreakdown[2]))
	if roleBreakdown[3] > 0 {
		sb.WriteString(fmt.Sprintf("  🔧 Tool: %d\n", roleBreakdown[3]))
	}

	sb.WriteString("\n<b>Status:</b>\n")
	if hasSummary {
		sb.WriteString("  📝 Contains compacted summary\n")
	}
	if contextPct >= 80 {
		sb.WriteString("  🔴 Auto-compact imminent (triggers at 80%)\n")
	} else if contextPct >= 50 {
		sb.WriteString("  🟡 Moderate usage — no action needed\n")
	} else {
		sb.WriteString("  🟢 Plenty of room\n")
	}

	sb.WriteString("\n<i>💡 /reset to clear context, or it auto-compacts at 80%</i>")

	tb.sendHTML(ctx, update.Message.Chat.ID, sb.String())
}

// --- Formatting helpers ---

// formatTokenCount formats a token count with K/M suffixes.
func formatTokenCount(tokens int) string {
	switch {
	case tokens >= 1000000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	case tokens >= 1000:
		return fmt.Sprintf("%.1fK", float64(tokens)/1000)
	default:
		return strconv.Itoa(tokens)
	}
}

// formatNumber formats a number with comma separators.
func formatNumber(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", n/1000000, (n/1000)%1000, n%1000)
}

// formatDuration formats a duration in a compact, readable way.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		min := int(d.Minutes())
		sec := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", min, sec)
	default:
		return d.Round(time.Second).String()
	}
}

// progressBar returns a text-based progress bar.
func progressBar(pct int) string {
	const total = 10
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	filled := pct * total / 100
	empty := total - filled
	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}

// modelTokenLimitStr returns the token limit as a formatted string.
func modelTokenLimitStr(model string) string {
	limit := engine.ModelTokenLimit(model)
	if limit == 0 {
		return "unknown"
	}
	return formatTokenCount(limit)
}

func (tb *Bot) handleReset(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}
	chatIDStr := channel.SessionID("telegram", strconv.FormatInt(update.Message.Chat.ID, 10))
	if err := tb.store.ClearChat(chatIDStr); err != nil {
		log.Printf("[telegram] clear chat: %v", err)
		tb.sendText(ctx, update.Message.Chat.ID, "❌ Error clearing conversation.")
		return
	}
	tb.sendText(ctx, update.Message.Chat.ID, "🗑️ Conversation cleared.")
}

func (tb *Bot) handleStatus(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}
	chatIDStr := channel.SessionID("telegram", strconv.FormatInt(update.Message.Chat.ID, 10))
	count, _ := tb.store.MessageCount(chatIDStr)
	limit := tb.store.Limit()

	// Context health indicator.
	contextPct := 0
	if limit > 0 {
		contextPct = (count * 100) / limit
	}
	contextBar := progressBar(contextPct)
	contextEmoji := "🟢"
	if contextPct >= 80 {
		contextEmoji = "🔴"
	} else if contextPct >= 50 {
		contextEmoji = "🟡"
	}

	// Model info.
	modelTier := "unknown"
	modelLimit := modelTokenLimitStr(tb.model)
	for _, m := range tb.provider.Models() {
		if m.ID == tb.model {
			if m.Premium {
				modelTier = "⭐ premium"
			} else {
				modelTier = "♾️ unlimited"
			}
			if m.MaxContextTokens > 0 {
				modelLimit = formatTokenCount(m.MaxContextTokens)
			}
			break
		}
	}

	// Tool count.
	toolCount := 0
	if tb.tools != nil {
		toolCount = len(tb.tools.List())
	}

	text := fmt.Sprintf(
		"📊 <b>AIDaemon Status</b>\n\n"+
			"🤖 <b>Model:</b> <code>%s</code> (%s)\n"+
			"📐 <b>Context:</b> %s tokens\n"+
			"%s <b>Messages:</b> %d/%d %s (%d%%)\n"+
			"🔧 <b>Tools:</b> %d registered\n"+
			"📡 <b>Provider:</b> %s\n\n"+
			"<i>💡 /help for commands, /tools to list tools, /context for details</i>",
		tb.model, modelTier, modelLimit,
		contextEmoji, count, limit, contextBar, contextPct,
		toolCount, tb.provider.Name(),
	)
	tb.sendHTML(ctx, update.Message.Chat.ID, text)
}

func (tb *Bot) handleModel(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}
	parts := strings.Fields(update.Message.Text)
	if len(parts) < 2 {
		// Show available models.
		var sb strings.Builder
		sb.WriteString("🤖 <b>Available Models</b>\n\n")
		for _, m := range tb.provider.Models() {
			tier := "unlimited"
			if m.Premium {
				tier = "premium"
			}
			current := ""
			if m.ID == tb.model {
				current = " ← current"
			}
			sb.WriteString(fmt.Sprintf("• <code>%s</code> (%s)%s\n", m.ID, tier, current))
		}
		sb.WriteString(fmt.Sprintf("\nUsage: <code>/model %s</code>", "gpt-4.1"))
		tb.sendHTML(ctx, update.Message.Chat.ID, sb.String())
		return
	}
	newModel := parts[1]
	tb.model = newModel
	tb.sendText(ctx, update.Message.Chat.ID, fmt.Sprintf("🤖 Model switched to: %s", newModel))
}

func (tb *Bot) handleHelp(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}
	text := "🤖 <b>AIDaemon Commands</b>\n\n" +
		"Just send a message to chat with the AI.\n\n" +
		"<b>💬 Chat</b>\n" +
		"/reset — clear conversation history\n" +
		"/model — list available models\n" +
		"/model &lt;id&gt; — switch model\n\n"

	// Add session management commands if available.
	if tb.sessionMgr != nil {
		text += "<b>🗂️ Sessions</b>\n" +
			"/new — start a new session (archives current)\n" +
			"/title &lt;name&gt; — rename current session\n\n"
	}

	text += "<b>📊 Monitoring</b>\n" +
		"/status — model, context health, tool count\n" +
		"/context — detailed context window breakdown\n" +
		"/tools — list all available tools\n\n" +
		"<b>💡 Tips</b>\n" +
		"• Send images for vision analysis\n" +
		"• Context auto-compacts at 80% capacity\n" +
		"• Token limit errors auto-recover via summarization\n" +
		"• Stats footer shows timing, tokens, and tools used\n" +
		"• Premium models (⭐) use your Copilot quota"
	tb.sendHTML(ctx, update.Message.Chat.ID, text)
}

func (tb *Bot) handleNew(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}

	// Check if session manager is configured.
	if tb.sessionMgr == nil {
		tb.sendText(ctx, update.Message.Chat.ID, "❌ Session management not enabled.")
		return
	}

	chatIDStr := channel.SessionID("telegram", strconv.FormatInt(update.Message.Chat.ID, 10))

	// Rotate the session.
	newSessionID, err := tb.sessionMgr.RotateSession(ctx, chatIDStr)
	if err != nil {
		log.Printf("[telegram] /new: rotation failed: %v", err)
		tb.sendText(ctx, update.Message.Chat.ID, fmt.Sprintf("❌ Failed to create new session: %v", err))
		return
	}

	log.Printf("[telegram] /new: rotated %s → %s", chatIDStr, newSessionID)
	tb.sendText(ctx, update.Message.Chat.ID, "✨ New session started. Previous conversation archived.")
}

func (tb *Bot) handleTitle(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}

	// Check if session manager is configured.
	if tb.sessionMgr == nil {
		tb.sendText(ctx, update.Message.Chat.ID, "❌ Session management not enabled.")
		return
	}

	// Parse the title from the command.
	text := update.Message.Text
	if !strings.HasPrefix(text, "/title ") {
		tb.sendText(ctx, update.Message.Chat.ID, "Usage: /title <new title>")
		return
	}
	title := strings.TrimSpace(strings.TrimPrefix(text, "/title "))
	if title == "" {
		tb.sendText(ctx, update.Message.Chat.ID, "❌ Title cannot be empty.")
		return
	}

	chatIDStr := channel.SessionID("telegram", strconv.FormatInt(update.Message.Chat.ID, 10))

	// Get the active session to find its ID.
	sess, err := tb.sessionMgr.ActiveSession(chatIDStr)
	if err != nil {
		log.Printf("[telegram] /title: get active session failed: %v", err)
		tb.sendText(ctx, update.Message.Chat.ID, fmt.Sprintf("❌ Failed to get active session: %v", err))
		return
	}
	if sess == nil {
		tb.sendText(ctx, update.Message.Chat.ID, "❌ No active session found.")
		return
	}

	// Rename the session.
	err = tb.sessionMgr.RenameSession(sess.ID, title)
	if err != nil {
		log.Printf("[telegram] /title: rename failed: %v", err)
		tb.sendText(ctx, update.Message.Chat.ID, fmt.Sprintf("❌ Failed to rename session: %v", err))
		return
	}

	log.Printf("[telegram] /title: renamed session %s to %q", sess.ID, title)
	tb.sendText(ctx, update.Message.Chat.ID, fmt.Sprintf("✅ Session renamed to: %s", title))
}

// --- Message building ---

func (tb *Bot) buildMessages(chatIDStr string) ([]provider.Message, error) {
	history, err := tb.store.GetHistory(chatIDStr)
	if err != nil {
		return nil, err
	}

	// System prompt + history.
	msgs := make([]provider.Message, 0, len(history)+1)
	// Re-read workspace files for fresh system prompt.
	sysPrompt := tb.sysPrompt
	if tb.workspaceDir != "" {
		ws := workspace.Load(tb.workspaceDir, tb.skillsDir)
		sysPrompt = ws.SystemPrompt()
	}
	if sysPrompt != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: sysPrompt})
	}
	for _, m := range history {
		msgs = append(msgs, provider.Message{Role: m.Role, Content: m.Content})
	}

	return msgs, nil
}

// compactIfNeeded checks if the conversation has hit the message limit
// and, if so, summarizes the oldest messages into a single summary message.
// This preserves context without hard-truncating old messages.
func (tb *Bot) compactIfNeeded(ctx context.Context, chatIDStr string) {
	count, err := tb.store.MessageCount(chatIDStr)
	if err != nil {
		log.Printf("[compact] count error: %v", err)
		return
	}

	limit := tb.store.Limit()
	// Trigger compaction when we're at 80% of limit.
	threshold := int(float64(limit) * 0.8)
	if count < threshold {
		return
	}

	// Get the oldest half of messages to summarize.
	compactCount := count / 2
	if compactCount < 4 {
		return // too few to bother
	}

	oldMsgs, err := tb.store.GetOldestN(chatIDStr, compactCount)
	if err != nil {
		log.Printf("[compact] get oldest: %v", err)
		return
	}

	// Skip if the first message is already a summary (avoid re-summarizing).
	if len(oldMsgs) > 0 && strings.HasPrefix(oldMsgs[0].Content, "[Previous conversation summary]") {
		return
	}

	// Build a transcript for summarization.
	var transcript strings.Builder
	for _, m := range oldMsgs {
		transcript.WriteString(m.Role)
		transcript.WriteString(": ")
		// Truncate very long messages in the transcript.
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		transcript.WriteString(content)
		transcript.WriteString("\n")
	}

	// Use a cheap model for summarization.
	summaryReq := provider.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []provider.Message{
			{
				Role:    "system",
				Content: "Summarize the following conversation in 2-3 concise paragraphs. Preserve key facts, decisions, file paths, and action items. This summary will replace the original messages in context.",
			},
			{
				Role:    "user",
				Content: transcript.String(),
			},
		},
	}

	summaryResp, err := tb.provider.Chat(ctx, summaryReq)
	if err != nil {
		log.Printf("[compact] summarize error: %v", err)
		return
	}

	if summaryResp.Content == "" {
		log.Printf("[compact] empty summary, skipping")
		return
	}

	// Collect IDs to delete.
	ids := make([]int64, len(oldMsgs))
	for i, m := range oldMsgs {
		ids[i] = m.ID
	}

	// Replace old messages with summary.
	summaryContent := "[Previous conversation summary]\n" + summaryResp.Content
	if err := tb.store.ReplaceMessages(chatIDStr, ids, "system", summaryContent); err != nil {
		log.Printf("[compact] replace error: %v", err)
		return
	}

	log.Printf("[compact] compacted %d messages → 1 summary for chat %s", len(oldMsgs), chatIDStr)
}

// --- Streaming ---

// streamToTelegram reads from the stream channel and periodically edits
// the Telegram message with accumulated text. Returns the full response
// and usage stats.
func (tb *Bot) streamToTelegram(
	ctx context.Context,
	chatID int64,
	messageID int,
	stream <-chan provider.StreamEvent,
) (string, *provider.Usage) {
	var (
		buf      strings.Builder
		lastEdit time.Time
		usage    *provider.Usage
		lastLen  int
	)

	for event := range stream {
		if event.Error != nil {
			log.Printf("[telegram] stream chunk error: %v", event.Error)
			buf.WriteString(fmt.Sprintf("\n\n❌ Stream error: %v", event.Error))
			break
		}

		if event.Delta != "" {
			buf.WriteString(event.Delta)
		}

		if event.Done {
			usage = event.Usage
			break
		}

		// Adaptive debounce: edit message periodically.
		currentLen := buf.Len()
		interval := tb.debounceInterval(currentLen)
		if currentLen != lastLen && time.Since(lastEdit) >= interval {
			text := buf.String() + " ▌"
			tb.editHTML(ctx, chatID, messageID, escapeHTML(text))
			lastEdit = time.Now()
			lastLen = currentLen
		}
	}

	return buf.String(), usage
}

// debounceInterval returns how often to edit based on response length.
// Telegram rate-limits edits to ~1/sec; we scale back as output grows.
func (tb *Bot) debounceInterval(length int) time.Duration {
	switch {
	case length < 1000:
		return 1 * time.Second
	case length < 3000:
		return 2 * time.Second
	default:
		return 3 * time.Second
	}
}

// --- Telegram helpers ---

func (tb *Bot) sendText(ctx context.Context, chatID int64, text string) {
	_, err := tb.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	if err != nil {
		log.Printf("[telegram] send: %v", err)
	}
}

func (tb *Bot) sendHTML(ctx context.Context, chatID int64, text string) {
	_, err := tb.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		log.Printf("[telegram] send html: %v", err)
	}
}

func (tb *Bot) editText(ctx context.Context, chatID int64, messageID int, text string) {
	const maxLen = 4096

	if len(text) <= maxLen {
		_, err := tb.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      text,
		})
		if err != nil && !isNotModifiedErr(err) {
			log.Printf("[telegram] edit: %v", err)
		}
		return
	}

	// Message too long — edit the first chunk into the placeholder,
	// then send remaining chunks as new messages.
	chunks := splitMessage(text, maxLen)

	_, err := tb.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      chunks[0],
	})
	if err != nil && !isNotModifiedErr(err) {
		log.Printf("[telegram] edit (chunk 1): %v", err)
	}

	for i := 1; i < len(chunks); i++ {
		_, err := tb.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   chunks[i],
		})
		if err != nil {
			log.Printf("[telegram] send chunk %d: %v", i+1, err)
		}
	}
}

func (tb *Bot) editHTML(ctx context.Context, chatID int64, messageID int, text string) {
	// Telegram messages are limited to 4096 characters.
	const maxLen = 4096

	if len(text) <= maxLen {
		_, err := tb.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      text,
			ParseMode: models.ParseModeHTML,
		})
		if err != nil && !isNotModifiedErr(err) {
			if strings.Contains(err.Error(), "can't parse entities") {
				log.Printf("[telegram] HTML parse failed, falling back to plain text: %v", err)
				tb.editText(ctx, chatID, messageID, stripHTMLTags(text))
				return
			}
			if strings.Contains(err.Error(), "MESSAGE_TOO_LONG") {
				// HTML tags pushed us over — fall back to plain text which is shorter.
				log.Printf("[telegram] HTML too long after formatting, falling back to plain text")
				tb.editText(ctx, chatID, messageID, stripHTMLTags(text))
				return
			}
			log.Printf("[telegram] edit html: %v", err)
		}
		return
	}

	// Message too long — edit the first chunk into the placeholder,
	// then send remaining chunks as new messages.
	chunks := splitMessage(text, maxLen)

	log.Printf("[telegram] splitting long message (%d bytes) into %d chunks", len(text), len(chunks))

	// First chunk: edit the existing placeholder message.
	_, err := tb.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      chunks[0],
		ParseMode: models.ParseModeHTML,
	})
	if err != nil && !isNotModifiedErr(err) {
		if strings.Contains(err.Error(), "can't parse entities") || strings.Contains(err.Error(), "MESSAGE_TOO_LONG") {
			log.Printf("[telegram] HTML chunk 1 failed, falling back to plain text: %v", err)
			// Fall back to plain text for THIS chunk only, not the full text.
			tb.editText(ctx, chatID, messageID, stripHTMLTags(chunks[0]))
		} else {
			log.Printf("[telegram] edit html (chunk 1): %v", err)
		}
	}

	// Remaining chunks: send as new messages.
	for i := 1; i < len(chunks); i++ {
		_, err := tb.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      chunks[i],
			ParseMode: models.ParseModeHTML,
		})
		if err != nil {
			// Fall back to plain text for this chunk.
			_, _ = tb.bot.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   stripHTMLTags(chunks[i]),
			})
		}
	}
}

// splitMessage splits a message into chunks of at most maxLen bytes.
// It tries to split on newlines to keep the output clean.
func splitMessage(text string, maxLen int) []string {
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		// Find the last newline within the limit.
		cut := maxLen
		if idx := strings.LastIndex(text[:cut], "\n"); idx > maxLen/2 {
			cut = idx + 1 // include the newline in this chunk
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return chunks
}

// isNotModifiedErr returns true for Telegram's "message is not modified" error.
// This happens when we try to edit a message with identical text — safe to ignore.
func isNotModifiedErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

// escapeHTML escapes characters that break Telegram's HTML parse mode.
// Only <, >, & need escaping — LLM output rarely contains these.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// stripHTMLTags removes HTML tags for plain-text fallback.
var reHTMLTag = regexp.MustCompile(`<[^>]+>`)

func stripHTMLTags(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return reHTMLTag.ReplaceAllString(s, "")
}

// --- Tool support ---

// telegramToolExecutor implements engine.ToolExecutor and delegates to
// executeToolsForChat so that MCP screenshots and Playwright auto-screenshots
// are sent to the correct Telegram chat.
type telegramToolExecutor struct {
	bot    *Bot
	chatID int64
}

// ExecuteToolCalls implements engine.ToolExecutor.
func (e *telegramToolExecutor) ExecuteToolCalls(ctx context.Context, calls []provider.ToolCall) []provider.Message {
	return e.bot.executeToolsForChat(ctx, e.chatID, calls)
}

// playwrightAutoScreenshotTools lists Playwright tool names that change
// visible page state and should trigger an automatic screenshot.
var playwrightAutoScreenshotTools = map[string]bool{
	"browser_navigate":      true,
	"browser_navigate_back": true,
	"browser_click":         true,
	"browser_type":          true,
	"browser_fill_form":     true,
	"browser_select_option": true,
	"browser_drag":          true,
	"browser_press_key":     true,
	"browser_hover":         true,
	"browser_handle_dialog": true,
	"browser_file_upload":   true,
}

// executeToolsForChat is the internal implementation that accepts a chatID
// for sending screenshots. chatID=0 means no Telegram forwarding.
func (tb *Bot) executeToolsForChat(ctx context.Context, chatID int64, toolCalls []provider.ToolCall) []provider.Message {
	if tb.tools == nil {
		return nil
	}

	results := make([]provider.Message, len(toolCalls))

	for i, call := range toolCalls {
		log.Printf("[telegram] executing tool: %s (id=%s, args=%s)",
			call.Function.Name, call.ID, call.Function.Arguments)

		result, err := tb.tools.Execute(ctx, call.Function.Name, call.Function.Arguments)

		content := result
		if err != nil {
			content = fmt.Sprintf("Error: %v", err)
			log.Printf("[telegram] tool error: %s: %v", call.Function.Name, err)
		} else {
			// Check for MCP image markers and send screenshots to Telegram.
			if chatID != 0 && strings.Contains(content, "[MCP_IMAGE:") {
				content = tb.handleMCPImages(ctx, chatID, content, call.Function.Name)
			}

			// Auto-screenshot after state-changing Playwright actions.
			if chatID != 0 {
				tb.maybeAutoScreenshot(ctx, chatID, call.Function.Name)
			}

			// Truncate log output for readability.
			logContent := content
			if len(logContent) > 200 {
				logContent = logContent[:200] + "..."
			}
			log.Printf("[telegram] tool result: %s → %s", call.Function.Name, logContent)
		}

		results[i] = provider.Message{
			Role:       "tool",
			Content:    content,
			ToolCallID: call.ID,
		}
	}

	return results
}

// maybeAutoScreenshot takes a screenshot after state-changing Playwright
// tool calls and sends it to the Telegram chat.
// It waits briefly for page rendering to settle and retries once on failure.
func (tb *Bot) maybeAutoScreenshot(ctx context.Context, chatID int64, toolName string) {
	// Only trigger for Playwright tools.
	if !strings.HasPrefix(toolName, "mcp_playwright_") {
		return
	}

	// Extract the bare tool name (strip "mcp_playwright_" prefix).
	bareName := strings.TrimPrefix(toolName, "mcp_playwright_")
	if !playwrightAutoScreenshotTools[bareName] {
		return
	}

	// Get the Playwright MCP client from the registry.
	screenshotTool := tb.tools.Get("mcp_playwright_browser_take_screenshot")
	if screenshotTool == nil {
		return
	}
	mcpTool, ok := screenshotTool.(*tools.MCPTool)
	if !ok {
		return
	}

	// Wait for the page to settle after state-changing actions.
	// Navigation and click events may trigger rendering/network activity
	// that takes a moment to complete.
	time.Sleep(800 * time.Millisecond)

	log.Printf("[telegram] auto-screenshot after %s", bareName)

	// Try taking screenshot with one retry on failure.
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := mcpTool.Client().CallTool("browser_take_screenshot", map[string]interface{}{})
		if err != nil {
			log.Printf("[telegram] auto-screenshot attempt %d failed: %v", attempt, err)
			if attempt < 2 {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return
		}

		// Check if the tool reported an error.
		if result.IsError {
			errText := mcp.ExtractText(result)
			log.Printf("[telegram] auto-screenshot attempt %d returned error: %s", attempt, errText)
			if attempt < 2 {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return
		}

		// Extract and send any images from the result.
		text := mcp.ExtractText(result)
		if strings.Contains(text, "[MCP_IMAGE:") {
			tb.handleMCPImages(ctx, chatID, text, "auto-screenshot")
		} else {
			log.Printf("[telegram] auto-screenshot: no image content in result (len=%d)", len(text))
		}
		return // success or no image — done
	}
}

// handleMCPImages extracts [MCP_IMAGE:mime:base64] markers from tool output,
// sends each image as a Telegram photo, and replaces markers with text.
func (tb *Bot) handleMCPImages(ctx context.Context, chatID int64, content string, toolName string) string {
	for {
		start := strings.Index(content, "[MCP_IMAGE:")
		if start == -1 {
			break
		}
		end := strings.Index(content[start:], "]")
		if end == -1 {
			break
		}
		end += start

		// Parse [MCP_IMAGE:mime:base64data]
		marker := content[start+len("[MCP_IMAGE:") : end]
		parts := strings.SplitN(marker, ":", 2)
		if len(parts) != 2 {
			break
		}

		// Decode base64 and send as photo.
		imgData, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			log.Printf("[telegram] decode MCP image: %v", err)
			content = content[:start] + "[Screenshot: decode error]" + content[end+1:]
			continue
		}

		// Save to disk.
		var savedPath string
		if tb.dataDir != "" {
			ext := ".png"
			if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
				ext = ".jpg"
			}
			fname := fmt.Sprintf("%s_screenshot%s", time.Now().Format("2006-01-02_150405"), ext)
			savedPath = filepath.Join(tb.dataDir, "media", fname)
			if err := os.WriteFile(savedPath, imgData, 0644); err != nil {
				log.Printf("[telegram] save screenshot: %v", err)
			}
		}

		// Send as Telegram photo.
		_, err = tb.bot.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:  chatID,
			Photo:   &models.InputFileUpload{Filename: "screenshot.png", Data: strings.NewReader(string(imgData))},
			Caption: fmt.Sprintf("📸 Screenshot from %s", toolName),
		})
		if err != nil {
			log.Printf("[telegram] send screenshot: %v", err)
		}

		// Replace marker with text reference.
		replacement := "[Screenshot sent to chat]"
		if savedPath != "" {
			replacement = fmt.Sprintf("[Screenshot sent to chat, saved: %s]", savedPath)
		}
		content = content[:start] + replacement + content[end+1:]
	}

	return content
}

// handlePhotoMessage processes image messages with vision models.
func (tb *Bot) handlePhotoMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	chatIDStr := channel.SessionID("telegram", strconv.FormatInt(chatID, 10))
	caption := update.Message.Caption
	if caption == "" {
		caption = "What's in this image?"
	}

	// Serialize LLM calls per chat.
	mu := tb.getChatMutex(chatID)
	mu.Lock()
	defer mu.Unlock()

	// Get the highest quality photo.
	photo := update.Message.Photo[len(update.Message.Photo)-1]

	// Send placeholder.
	placeholder, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "🖼️ Analyzing image...",
	})
	if err != nil {
		log.Printf("[telegram] send placeholder: %v", err)
		return
	}

	// Download photo from Telegram.
	file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: photo.FileID})
	if err != nil {
		log.Printf("[telegram] get file: %v", err)
		tb.editText(ctx, chatID, placeholder.ID, "❌ Failed to download image from Telegram.")
		return
	}

	// Build download URL and fetch the image bytes.
	fileURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", tb.botToken, file.FilePath)
	imgResp, err := http.Get(fileURL)
	if err != nil {
		log.Printf("[telegram] download image: %v", err)
		tb.editText(ctx, chatID, placeholder.ID, "❌ Failed to download image.")
		return
	}
	defer imgResp.Body.Close()

	imgData, err := io.ReadAll(imgResp.Body)
	if err != nil {
		log.Printf("[telegram] read image: %v", err)
		tb.editText(ctx, chatID, placeholder.ID, "❌ Failed to read image data.")
		return
	}

	// Determine MIME type from file extension.
	mimeType := "image/jpeg"
	if strings.HasSuffix(file.FilePath, ".png") {
		mimeType = "image/png"
	} else if strings.HasSuffix(file.FilePath, ".gif") {
		mimeType = "image/gif"
	} else if strings.HasSuffix(file.FilePath, ".webp") {
		mimeType = "image/webp"
	}

	// Base64 encode.
	b64 := base64.StdEncoding.EncodeToString(imgData)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)

	log.Printf("[telegram] image: %s, %d bytes, %s", file.FilePath, len(imgData), mimeType)

	// Persist image to disk.
	savedPath := ""
	if tb.dataDir != "" {
		ts := time.Now().Format("2006-01-02_150405")
		ext := filepath.Ext(file.FilePath)
		if ext == "" {
			ext = ".jpg"
		}
		fname := fmt.Sprintf("%s_%s%s", ts, photo.FileID[:8], ext)
		savedPath = filepath.Join(tb.dataDir, "media", fname)
		if err := os.WriteFile(savedPath, imgData, 0644); err != nil {
			log.Printf("[telegram] save image to disk: %v", err)
			savedPath = "" // don't reference if save failed
		} else {
			log.Printf("[telegram] image saved: %s", savedPath)
		}
	}

	// Save user caption to store for context (include local path if saved).
	storeContent := "[Image] " + caption
	if savedPath != "" {
		storeContent = fmt.Sprintf("[Image: %s] %s", savedPath, caption)
	}
	if err := tb.store.AddMessage(chatIDStr, "user", storeContent); err != nil {
		log.Printf("[telegram] save image msg: %v", err)
	}

	// Build messages with image content.
	messages, err := tb.buildMessages(chatIDStr)
	if err != nil {
		log.Printf("[telegram] build messages: %v", err)
		tb.editText(ctx, chatID, placeholder.ID, "❌ Error loading conversation history.")
		return
	}

	// Replace the last user message (plain "[Image] caption") with multi-modal content.
	if len(messages) > 0 {
		messages[len(messages)-1] = provider.Message{
			Role: "user",
			ContentParts: []provider.ContentPart{
				{Type: "text", Text: caption},
				{Type: "image_url", ImageURL: &provider.ImageURL{URL: dataURL}},
			},
		}
	}

	// Delegate to the chat engine for the tool-call loop.
	tb.engine.Executor = &telegramToolExecutor{bot: tb, chatID: chatID}

	// Progress callback for image analysis.
	var lastImgProgressText string
	var lastImgProgress time.Time
	imgProgressCb := func(update engine.ProgressUpdate) {
		if update.Message == lastImgProgressText {
			return
		}
		if time.Since(lastImgProgress) < 1500*time.Millisecond {
			return
		}
		log.Printf("[telegram] progress: %s", update.Message)
		lastImgProgressText = update.Message
		lastImgProgress = time.Now()
		tb.editText(ctx, chatID, placeholder.ID, update.Message)
	}

	result, err := tb.engine.Run(ctx, messages, engine.RunOptions{
		Model:      tb.model,
		OnProgress: imgProgressCb,
	})

	if err != nil {
		log.Printf("[telegram] image engine error: %v", err)
		if result != nil && result.Content != "" {
			goto sendImageResult
		}
		tb.editText(ctx, chatID, placeholder.ID, fmt.Sprintf("❌ %v", err))
		return
	}

	if result.Content == "" {
		tb.editText(ctx, chatID, placeholder.ID, "⚠️ Empty response from AI.")
		return
	}

sendImageResult:
	if err := tb.store.AddMessage(chatIDStr, "assistant", result.Content); err != nil {
		log.Printf("[telegram] save assistant msg: %v", err)
	}

	if result.ToolIterations > 0 {
		if err := tb.store.AddMessage(chatIDStr, "assistant", "[Used tools to answer this question]"); err != nil {
			log.Printf("[telegram] save tool summary: %v", err)
		}
	}

	footer := tb.buildStatsFooter(result)

	formatted := FormatHTML(result.Content + footer)
	tb.editHTML(ctx, chatID, placeholder.ID, formatted)
}
