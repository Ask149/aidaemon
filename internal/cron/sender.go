package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
)

// TelegramSender sends cron output to Telegram chats.
type TelegramSender struct {
	// SendFn sends a message to a Telegram chat.
	// Takes (ctx, chatID, text) and returns error.
	SendFn func(ctx context.Context, chatID int64, text string) error
}

// SendCronOutput sends output to the appropriate channel.
func (s *TelegramSender) SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error {
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
		return s.SendFn(ctx, meta.ChatID, text)

	default:
		log.Printf("[cron] unsupported channel type %q — output stored in run history only", channelType)
		return nil
	}
}
