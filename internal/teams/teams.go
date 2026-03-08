// Package teams implements the Microsoft Teams channel using the Graph API.
//
// It polls a specific Teams chat for new messages, filters out self-sent
// messages, strips HTML tags (Teams wraps messages in <p> tags), and
// delivers incoming text to the configured callback.
package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Ask149/aidaemon/internal/auth"
	"github.com/Ask149/aidaemon/internal/channel"
)

// Compile-time interface check.
var _ channel.Channel = (*Channel)(nil)

const defaultGraphBaseURL = "https://graph.microsoft.com/v1.0"

// Config holds the configuration for a Teams channel.
type Config struct {
	ChatID       string
	PollInterval time.Duration
	TokenManager *auth.EntraTokenManager
	OnMessage    func(ctx context.Context, sessionID string, text string)
	WebhookURL   string // if set, Send() uses Incoming Webhook instead of Graph API
}

// Channel implements channel.Channel for Microsoft Teams via Graph API polling.
type Channel struct {
	cfg          Config
	userID       string // populated from /me on Start
	lastSeen     time.Time
	graphBaseURL string
	httpClient   *http.Client
}

// New creates a new Teams channel. Call Start() to begin polling.
func New(cfg Config) *Channel {
	return &Channel{
		cfg:          cfg,
		graphBaseURL: defaultGraphBaseURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Name returns the channel identifier.
func (c *Channel) Name() string { return "teams" }

// Start fetches the current user ID, then polls for new messages until ctx is cancelled.
func (c *Channel) Start(ctx context.Context) error {
	// Fetch user ID from /me.
	if err := c.fetchUserID(ctx); err != nil {
		return fmt.Errorf("[teams] fetch user ID: %w", err)
	}
	log.Printf("[teams] started polling chat=%s user=%s interval=%s", c.cfg.ChatID, c.userID, c.cfg.PollInterval)

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	// Do an initial poll immediately.
	c.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[teams] stopped")
			return nil
		case <-ticker.C:
			c.poll(ctx)
		}
	}
}

// Send delivers a message to the Teams chat via the Graph API.
func (c *Channel) Send(ctx context.Context, sessionID string, text string) error {
	// Webhook path: bypass Graph API entirely.
	if c.cfg.WebhookURL != "" {
		log.Printf("[teams] sending via webhook (session=%s)", sessionID)
		return c.sendWebhook(ctx, text)
	}

	// Graph API path (existing code below, unchanged).
	token, err := c.getToken()
	if err != nil {
		return fmt.Errorf("[teams] get token: %w", err)
	}

	url := fmt.Sprintf("%s/me/chats/%s/messages", c.graphBaseURL, c.cfg.ChatID)

	body := map[string]interface{}{
		"body": map[string]string{
			"content":     text,
			"contentType": "text",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("[teams] marshal send body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("[teams] build send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("[teams] send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("[teams] send message HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// sendWebhook sends a message via Teams Incoming Webhook using Adaptive Card format.
func (c *Channel) sendWebhook(ctx context.Context, text string) error {
	card := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]interface{}{
					"type":    "AdaptiveCard",
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"version": "1.4",
					"body": []map[string]interface{}{
						{
							"type": "TextBlock",
							"text": text,
							"wrap": true,
						},
					},
				},
			},
		},
	}

	payload, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("[teams] marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.cfg.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("[teams] build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("[teams] webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("[teams] webhook HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// fetchUserID calls GET /me to retrieve the current user's ID.
func (c *Channel) fetchUserID(ctx context.Context) error {
	token, err := c.getToken()
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.graphBaseURL+"/me", nil)
	if err != nil {
		return fmt.Errorf("build /me request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("/me request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read /me response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("/me HTTP %d: %s", resp.StatusCode, string(body))
	}

	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return fmt.Errorf("parse /me response: %w", err)
	}

	c.userID = me.ID
	return nil
}

// poll fetches recent messages and processes any new ones.
func (c *Channel) poll(ctx context.Context) {
	token, err := c.getToken()
	if err != nil {
		log.Printf("[teams] get token: %v", err)
		return
	}

	url := fmt.Sprintf("%s/me/chats/%s/messages?$top=10&$orderby=createdDateTime%%20desc",
		c.graphBaseURL, c.cfg.ChatID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Printf("[teams] build poll request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("[teams] poll request: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[teams] read poll response: %v", err)
		return
	}

	if resp.StatusCode != 200 {
		log.Printf("[teams] poll HTTP %d: %s", resp.StatusCode, string(body))
		return
	}

	var msgsResp graphMessagesResponse
	if err := json.Unmarshal(body, &msgsResp); err != nil {
		log.Printf("[teams] parse messages: %v", err)
		return
	}

	c.processMessages(ctx, msgsResp.Value)
}

// processMessages filters and delivers new messages to the OnMessage callback.
func (c *Channel) processMessages(ctx context.Context, msgs []graphMessage) {
	// Messages may arrive in any order from the API. We collect all new
	// messages (those after lastSeen and not from self), sort by time
	// (oldest first), deliver them, then advance lastSeen.
	type parsed struct {
		msg graphMessage
		ts  time.Time
	}

	var newMsgs []parsed
	var maxTS time.Time

	for _, msg := range msgs {
		// Skip system/bot messages (no sender user ID).
		if msg.From.User.ID == "" {
			continue
		}

		// Skip own messages.
		if msg.From.User.ID == c.userID {
			continue
		}

		// Parse timestamp.
		ts, err := time.Parse(time.RFC3339, msg.CreatedDateTime)
		if err != nil {
			ts, err = time.Parse(time.RFC3339Nano, msg.CreatedDateTime)
			if err != nil {
				log.Printf("[teams] parse message time %q: %v", msg.CreatedDateTime, err)
				continue
			}
		}

		// Skip already-seen messages.
		if !ts.After(c.lastSeen) {
			continue
		}

		newMsgs = append(newMsgs, parsed{msg: msg, ts: ts})
		if ts.After(maxTS) {
			maxTS = ts
		}
	}

	// Sort oldest first for delivery order.
	for i := 0; i < len(newMsgs); i++ {
		for j := i + 1; j < len(newMsgs); j++ {
			if newMsgs[j].ts.Before(newMsgs[i].ts) {
				newMsgs[i], newMsgs[j] = newMsgs[j], newMsgs[i]
			}
		}
	}

	// Deliver new messages.
	sessionID := channel.SessionID("teams", c.cfg.ChatID)
	for _, pm := range newMsgs {
		text := pm.msg.Body.Content
		if pm.msg.Body.ContentType == "html" || strings.Contains(text, "<") {
			text = stripHTML(text)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		if c.cfg.OnMessage != nil {
			c.cfg.OnMessage(ctx, sessionID, text)
		}
	}

	// Advance lastSeen after all deliveries.
	if maxTS.After(c.lastSeen) {
		c.lastSeen = maxTS
	}
}

// getToken returns the current access token. If a TokenManager is configured,
// it refreshes the token if needed. For testing without a token manager,
// returns an empty string (test servers don't validate tokens).
func (c *Channel) getToken() (string, error) {
	if c.cfg.TokenManager == nil {
		return "", nil
	}

	tok, err := c.cfg.TokenManager.GetToken()
	if err != nil {
		return "", fmt.Errorf("get entra token: %w", err)
	}
	return tok.AccessToken, nil
}

// --- Graph API response types ---

type graphMessagesResponse struct {
	Value []graphMessage `json:"value"`
}

type graphMessage struct {
	ID              string           `json:"id"`
	CreatedDateTime string           `json:"createdDateTime"`
	Body            graphMessageBody `json:"body"`
	From            graphMessageFrom `json:"from"`
}

type graphMessageBody struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
}

type graphMessageFrom struct {
	User graphUser `json:"user"`
}

type graphUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

// --- HTML stripping ---

// stripHTML removes HTML tags and decodes basic HTML entities.
// Teams wraps messages in <p> tags; this is a simple char-walk stripper
// sufficient for that use case.
func stripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if inTag {
			continue
		}
		// Decode HTML entities.
		if ch == '&' {
			if entity, advance := decodeEntity(s[i:]); advance > 0 {
				b.WriteString(entity)
				i += advance - 1 // -1 because loop increments
				continue
			}
		}
		b.WriteByte(ch)
	}

	return b.String()
}

// htmlEntities maps basic HTML entities to their decoded equivalents.
var htmlEntities = [...]struct {
	encoded string
	decoded string
}{
	{"&amp;", "&"},
	{"&lt;", "<"},
	{"&gt;", ">"},
	{"&nbsp;", " "},
	{"&quot;", "\""},
}

// decodeEntity decodes a basic HTML entity at the start of s.
// Returns the decoded string and the number of bytes consumed, or ("", 0) if not recognized.
func decodeEntity(s string) (string, int) {
	for _, e := range htmlEntities {
		if strings.HasPrefix(s, e.encoded) {
			return e.decoded, len(e.encoded)
		}
	}
	return "", 0
}
