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

	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/mcp"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/tools"
)

// Bot wraps the Telegram bot with LLM integration.
type Bot struct {
	bot       *bot.Bot
	provider  provider.Provider
	store     *store.Store
	tools     *tools.Registry
	engine    *engine.Engine
	userID    int64
	model     string
	sysPrompt string
	convLimit int
	botToken  string // needed for file download URLs
	dataDir   string // root for media/logs/files persistence

	// Guards per-chat to prevent concurrent LLM calls for the same chat.
	chatMu sync.Map // map[int64]*sync.Mutex
}

// Config for creating a new Bot.
type Config struct {
	Token         string
	UserID        int64
	Provider      provider.Provider
	Store         *store.Store
	Model         string
	SystemPrompt  string
	ConvLimit     int
	ToolRegistry  *tools.Registry
	DataDir       string
}

// New creates a new Telegram bot. Call Start() to begin polling.
func New(cfg Config) (*Bot, error) {
	tb := &Bot{
		provider:  cfg.Provider,
		store:     cfg.Store,
		tools:     cfg.ToolRegistry,
		userID:    cfg.UserID,
		model:     cfg.Model,
		sysPrompt: cfg.SystemPrompt,
		convLimit: cfg.ConvLimit,
		botToken:  cfg.Token,
		dataDir:   cfg.DataDir,
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
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypePrefix, tb.handleHelp)

	return tb, nil
}

// Start begins long polling. Blocks until ctx is cancelled.
func (tb *Bot) Start(ctx context.Context) {
	log.Printf("[telegram] bot starting (user_id=%d, model=%s)", tb.userID, tb.model)
	tb.bot.Start(ctx)
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
	chatIDStr := strconv.FormatInt(chatID, 10)

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
		Text:   "🤔 Thinking...",
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

	// Delegate to the chat engine for the tool-call loop.
	result, err := tb.engine.Run(ctx, messages, engine.RunOptions{
		Model: tb.model,
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
	footer := ""
	if result.Usage != nil && (result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0) {
		footer = fmt.Sprintf("\n\n📊 %d→%d tokens | %s",
			result.Usage.InputTokens, result.Usage.OutputTokens, tb.model)
	}

	// Format as HTML for rich output (bold, code, links, etc.).
	formatted := FormatHTML(result.Content + footer)
	tb.editHTML(ctx, chatID, placeholder.ID, formatted)
}

func (tb *Bot) handleReset(ctx context.Context, b *bot.Bot, update *models.Update) {
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return
	}
	chatIDStr := strconv.FormatInt(update.Message.Chat.ID, 10)
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
	chatIDStr := strconv.FormatInt(update.Message.Chat.ID, 10)
	count, _ := tb.store.MessageCount(chatIDStr)
	text := fmt.Sprintf(
		"📊 <b>AIDaemon Status</b>\n\n"+
			"🤖 Model: <code>%s</code>\n"+
			"💬 Messages in context: %d/%d\n"+
			"📡 Provider: %s",
		tb.model, count, tb.convLimit, tb.provider.Name(),
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
		"/status — show current model and context size\n" +
		"/model — list available models\n" +
		"/model &lt;id&gt; — switch model\n" +
		"/reset — clear conversation history\n" +
		"/help — this message"
	tb.sendHTML(ctx, update.Message.Chat.ID, text)
}

// --- Message building ---

func (tb *Bot) buildMessages(chatIDStr string) ([]provider.Message, error) {
	history, err := tb.store.GetHistory(chatIDStr)
	if err != nil {
		return nil, err
	}

	// System prompt + history.
	msgs := make([]provider.Message, 0, len(history)+1)
	if tb.sysPrompt != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: tb.sysPrompt})
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
		buf       strings.Builder
		lastEdit  time.Time
		usage     *provider.Usage
		lastLen   int
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
	_, err := tb.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	})
	if err != nil && !isNotModifiedErr(err) {
		log.Printf("[telegram] edit: %v", err)
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
			log.Printf("[telegram] edit html: %v", err)
		}
		return
	}

	// Message too long — edit the first chunk into the placeholder,
	// then send remaining chunks as new messages.
	chunks := splitMessage(text, maxLen)

	// First chunk: edit the existing placeholder message.
	_, err := tb.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      chunks[0],
		ParseMode: models.ParseModeHTML,
	})
	if err != nil && !isNotModifiedErr(err) {
		if strings.Contains(err.Error(), "can't parse entities") {
			tb.editText(ctx, chatID, messageID, stripHTMLTags(text))
			return
		}
		log.Printf("[telegram] edit html (chunk 1): %v", err)
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
	"browser_navigate_back":  true,
	"browser_click":          true,
	"browser_type":           true,
	"browser_fill_form":      true,
	"browser_select_option":  true,
	"browser_drag":           true,
	"browser_press_key":      true,
	"browser_hover":          true,
	"browser_handle_dialog":  true,
	"browser_file_upload":    true,
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

	log.Printf("[telegram] auto-screenshot after %s", bareName)

	// Call browser_take_screenshot via the MCP client.
	result, err := mcpTool.Client().CallTool("browser_take_screenshot", map[string]interface{}{})
	if err != nil {
		log.Printf("[telegram] auto-screenshot failed: %v", err)
		return
	}

	// Extract and send any images from the result.
	text := mcp.ExtractText(result)
	if strings.Contains(text, "[MCP_IMAGE:") {
		tb.handleMCPImages(ctx, chatID, text, "auto-screenshot")
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
	chatIDStr := strconv.FormatInt(chatID, 10)
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
	result, err := tb.engine.Run(ctx, messages, engine.RunOptions{Model: tb.model})

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

	footer := ""
	if result.Usage != nil && (result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0) {
		footer = fmt.Sprintf("\n\n📊 %d→%d tokens | %s",
			result.Usage.InputTokens, result.Usage.OutputTokens, tb.model)
	}

	formatted := FormatHTML(result.Content + footer)
	tb.editHTML(ctx, chatID, placeholder.ID, formatted)
}
