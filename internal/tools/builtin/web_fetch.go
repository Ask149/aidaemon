// Package builtin provides built-in tools for AIDaemon.
package builtin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// WebFetchTool fetches content from a URL and extracts text.
type WebFetchTool struct {
	// Timeout for HTTP requests.
	Timeout time.Duration
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch content from a URL and extract readable text. Strips HTML tags and returns clean text. Useful for reading articles, documentation, etc."
}

func (t *WebFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "The URL to fetch (must start with http:// or https://)",
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	urlStr, ok := args["url"].(string)
	if !ok {
		return "", fmt.Errorf("url must be a string")
	}

	// Validate URL.
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://")
	}

	// Set timeout.
	timeout := t.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Fetch URL.
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	// Set user agent.
	req.Header.Set("User-Agent", "aidaemon/0.1 (https://github.com/Ask149/aidaemon)")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Extract text from HTML.
	text, err := extractText(resp.Body)
	if err != nil {
		return "", fmt.Errorf("extract text: %w", err)
	}

	// Truncate if too long (Telegram has message limits).
	if len(text) > 10000 {
		text = text[:10000] + "\n\n[... truncated ...]"
	}

	return text, nil
}

// extractText extracts readable text from HTML.
// Strips tags, scripts, styles, etc.
func extractText(r io.Reader) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	var walk func(*html.Node)

	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				buf.WriteString(text)
				buf.WriteString(" ")
			}
		}

		// Skip script and style tags.
		if n.Type == html.ElementNode {
			if n.Data == "script" || n.Data == "style" {
				return
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	walk(doc)

	// Clean up excessive whitespace.
	text := buf.String()
	text = strings.Join(strings.Fields(text), " ")

	return text, nil
}
