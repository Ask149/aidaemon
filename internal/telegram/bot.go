// Package telegram implements the Telegram bot frontend.
//
// It uses go-telegram/bot with long polling (works behind NAT, no public URL).
// Messages from unauthorized users are silently dropped.
// LLM responses are streamed via the edit-message pattern with adaptive debounce.
package telegram

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
)

// Bot wraps the Telegram bot with LLM integration.
type Bot struct {
	bot       *bot.Bot
	provider  provider.Provider
	store     *store.Store
	userID    int64
	model     string
	sysPrompt string
	convLimit int

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
}

// New creates a new Telegram bot. Call Start() to begin polling.
func New(cfg Config) (*Bot, error) {
	tb := &Bot{
		provider:  cfg.Provider,
		store:     cfg.Store,
		userID:    cfg.UserID,
		model:     cfg.Model,
		sysPrompt: cfg.SystemPrompt,
		convLimit: cfg.ConvLimit,
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
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	if !tb.isAuthorized(update.Message.Chat.ID) {
		return // silent drop
	}

	// Skip messages that start with "/" — they're handled by registered handlers.
	if strings.HasPrefix(update.Message.Text, "/") {
		return
	}

	chatID := update.Message.Chat.ID
	chatIDStr := strconv.FormatInt(chatID, 10)
	userText := update.Message.Text

	// Serialize LLM calls per chat.
	mu := tb.getChatMutex(chatID)
	mu.Lock()
	defer mu.Unlock()

	// Save user message.
	if err := tb.store.AddMessage(chatIDStr, "user", userText); err != nil {
		log.Printf("[telegram] save user msg: %v", err)
	}

	// Build messages: system prompt + conversation history.
	messages, err := tb.buildMessages(chatIDStr)
	if err != nil {
		log.Printf("[telegram] build messages: %v", err)
		tb.sendText(ctx, chatID, "❌ Error loading conversation history.")
		return
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

	// Stream LLM response.
	req := provider.ChatRequest{
		Model:    tb.model,
		Messages: messages,
	}

	stream, err := tb.provider.Stream(ctx, req)
	if err != nil {
		log.Printf("[telegram] stream error: %v", err)
		tb.editText(ctx, chatID, placeholder.ID, fmt.Sprintf("❌ %v", err))
		return
	}

	// Accumulate + edit-message streaming.
	fullText, usage := tb.streamToTelegram(ctx, chatID, placeholder.ID, stream)

	if fullText == "" {
		tb.editText(ctx, chatID, placeholder.ID, "⚠️ Empty response from AI.")
		return
	}

	// Save assistant response.
	if err := tb.store.AddMessage(chatIDStr, "assistant", fullText); err != nil {
		log.Printf("[telegram] save assistant msg: %v", err)
	}

	// Append usage footer on final edit.
	footer := ""
	if usage != nil {
		footer = fmt.Sprintf("\n\n<i>📊 %d→%d tokens | %s</i>",
			usage.InputTokens, usage.OutputTokens, tb.model)
	}
	tb.editHTML(ctx, chatID, placeholder.ID, escapeHTML(fullText)+footer)
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
	_, err := tb.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil && !isNotModifiedErr(err) {
		log.Printf("[telegram] edit html: %v", err)
	}
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
