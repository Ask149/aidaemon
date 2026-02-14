// Package builtin provides built-in tools for AIDaemon.
package builtin

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// WebSearchTool searches the web using DuckDuckGo HTML scraping.
// No API key required — works out of the box.
type WebSearchTool struct {
	// BraveAPIKey enables Brave Search API if set (higher quality, 2K free/month).
	// When empty, falls back to DuckDuckGo HTML scraping.
	BraveAPIKey string

	// Timeout for HTTP requests (default: 10s).
	Timeout time.Duration
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Search the web for information. Returns search results with titles, snippets, and URLs. Use this to find current information, research topics, look up documentation, etc."
}

func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "The search query (e.g. 'Go programming language tutorial')",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 5, max: 10)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query must be a non-empty string")
	}

	maxResults := 5
	if n, ok := args["max_results"].(float64); ok && n > 0 {
		maxResults = int(n)
		if maxResults > 10 {
			maxResults = 10
		}
	}

	timeout := t.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if t.BraveAPIKey != "" {
		return t.searchBrave(ctx, query, maxResults)
	}
	return t.searchDDG(ctx, query, maxResults)
}

// searchResult represents a single search result.
type searchResult struct {
	Title   string
	Snippet string
	URL     string
}

// searchBrave uses the Brave Search API.
func (t *WebSearchTool) searchBrave(ctx context.Context, query string, maxResults int) (string, error) {
	u := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", t.BraveAPIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("brave search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("brave search HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response — extract web results.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 100_000))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return parseBraveJSON(string(body), maxResults), nil
}

// parseBraveJSON extracts results from Brave Search JSON.
// Uses simple string parsing to avoid importing encoding/json just for this.
func parseBraveJSON(body string, maxResults int) string {
	// The Brave API returns structured JSON with web.results[].
	// We do lightweight extraction since the format is stable.
	var results []searchResult

	// Find "results" array entries by looking for "title", "url", "description" keys.
	idx := 0
	for i := 0; i < maxResults; i++ {
		titleIdx := strings.Index(body[idx:], `"title"`)
		if titleIdx == -1 {
			break
		}
		idx += titleIdx

		title := extractJSONString(body, idx, "title")
		urlStr := extractJSONString(body, idx, "url")
		desc := extractJSONString(body, idx, "description")

		if title != "" && urlStr != "" {
			results = append(results, searchResult{
				Title:   title,
				Snippet: desc,
				URL:     urlStr,
			})
		}
		idx += 10 // Move past this result.
	}

	return formatResults(results, "Brave Search")
}

// extractJSONString extracts a simple string value from JSON near a position.
func extractJSONString(body string, startPos int, key string) string {
	search := fmt.Sprintf(`"%s"`, key)
	idx := strings.Index(body[startPos:], search)
	if idx == -1 {
		return ""
	}
	abs := startPos + idx + len(search)

	// Skip `: "`
	colonIdx := strings.Index(body[abs:], `"`)
	if colonIdx == -1 || colonIdx > 5 {
		return ""
	}
	abs += colonIdx + 1

	// Read until closing quote (handle escaped quotes).
	end := abs
	for end < len(body) {
		if body[end] == '"' && (end == 0 || body[end-1] != '\\') {
			break
		}
		end++
	}
	if end >= len(body) {
		return ""
	}

	return strings.ReplaceAll(body[abs:end], `\"`, `"`)
}

// searchDDG searches using DuckDuckGo HTML interface.
func (t *WebSearchTool) searchDDG(ctx context.Context, query string, maxResults int) (string, error) {
	u := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	// DuckDuckGo requires a browser-like user agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ddg search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ddg search HTTP %d", resp.StatusCode)
	}

	results, err := parseDDGHTML(resp.Body, maxResults)
	if err != nil {
		return "", fmt.Errorf("parse ddg results: %w", err)
	}

	if len(results) == 0 {
		return "No results found for: " + query, nil
	}

	return formatResults(results, "DuckDuckGo"), nil
}

// parseDDGHTML parses DuckDuckGo HTML search results page.
func parseDDGHTML(r io.Reader, maxResults int) ([]searchResult, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}

	var results []searchResult

	// DuckDuckGo HTML results are in <div class="result"> elements.
	// Each contains:
	//   <a class="result__a" href="...">Title</a>
	//   <a class="result__snippet">Snippet text</a>
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}

		if n.Type == html.ElementNode && n.Data == "div" {
			if hasClass(n, "result") && !hasClass(n, "result--ad") {
				r := extractDDGResult(n)
				if r.Title != "" && r.URL != "" {
					results = append(results, r)
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	walk(doc)
	return results, nil
}

// extractDDGResult extracts title, URL, and snippet from a DDG result div.
func extractDDGResult(n *html.Node) searchResult {
	var result searchResult

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			if hasClass(n, "result__a") {
				result.Title = textContent(n)
				result.URL = getAttr(n, "href")

				// DDG wraps URLs in redirect — extract actual URL.
				if strings.Contains(result.URL, "uddg=") {
					if u, err := url.Parse(result.URL); err == nil {
						if actual := u.Query().Get("uddg"); actual != "" {
							result.URL = actual
						}
					}
				}
			}
			if hasClass(n, "result__snippet") {
				result.Snippet = textContent(n)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)

	return result
}

// hasClass checks if an HTML node has a specific CSS class.
func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, c := range strings.Fields(attr.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// textContent extracts all text from an HTML node tree.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return strings.TrimSpace(n.Data)
	}

	var parts []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := textContent(c); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// getAttr returns the value of an HTML attribute.
func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// formatResults formats search results for display.
func formatResults(results []searchResult, source string) string {
	if len(results) == 0 {
		return "No results found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results (%s, %d results):\n\n", source, len(results)))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
		}
		sb.WriteString(fmt.Sprintf("   URL: %s\n\n", r.URL))
	}

	log.Printf("[web_search] returned %d results from %s", len(results), source)
	return sb.String()
}
