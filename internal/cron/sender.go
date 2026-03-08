package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
)

// ChannelSender sends cron output to configured channel(s).
type ChannelSender struct {
	// TelegramFn sends a message to a Telegram chat.
	// Takes (ctx, chatID, text) and returns error.
	TelegramFn func(ctx context.Context, chatID int64, text string) error

	// TeamsFn sends a message to a Teams chat.
	// Takes (ctx, chatID, text) and returns error.
	TeamsFn func(ctx context.Context, chatID, text string) error
}

// SendCronOutput sends output to the appropriate channel.
func (s *ChannelSender) SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error {
	switch channelType {
	case "telegram":
		var meta struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := json.Unmarshal([]byte(channelMeta), &meta); err != nil {
			return fmt.Errorf("parse telegram meta: %w", err)
		}
		if meta.ChatID == 0 {
			// Try string format.
			var metaStr struct {
				ChatID string `json:"chat_id"`
			}
			json.Unmarshal([]byte(channelMeta), &metaStr)
			meta.ChatID, _ = strconv.ParseInt(metaStr.ChatID, 10, 64)
		}
		if meta.ChatID == 0 {
			return fmt.Errorf("telegram meta missing chat_id")
		}
		if s.TelegramFn == nil {
			log.Printf("[cron] telegram sender not configured")
			return nil
		}
		return s.TelegramFn(ctx, meta.ChatID, text)

	case "teams":
		var meta struct {
			ChatID string `json:"chat_id"`
		}
		if err := json.Unmarshal([]byte(channelMeta), &meta); err != nil {
			return fmt.Errorf("parse teams meta: %w", err)
		}
		if meta.ChatID == "" {
			return fmt.Errorf("teams meta missing chat_id")
		}
		if s.TeamsFn == nil {
			log.Printf("[cron] teams sender not configured")
			return nil
		}
		return s.TeamsFn(ctx, meta.ChatID, text)

	default:
		log.Printf("[cron] unsupported channel type %q — output stored in run history only", channelType)
		return nil
	}
}
