package heartbeat

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// FeedItem represents a single item from an RSS/Atom feed.
type FeedItem struct {
	Title       string
	URL         string
	Description string
	Source      string // feed URL this came from
	PublishedAt time.Time
}

// rssFeed represents an RSS 2.0 feed.
type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

// atomFeed represents an Atom feed.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string   `xml:"title"`
	Link    atomLink `xml:"link"`
	Summary string   `xml:"summary"`
	Updated string   `xml:"updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
}

// FetchRSS fetches and parses an RSS or Atom feed from a URL.
func FetchRSS(feedURL string) ([]FeedItem, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "aidaemon/0.1 heartbeat")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch feed %s: %w", feedURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("feed %s returned HTTP %d", feedURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1_000_000)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("read feed body: %w", err)
	}

	return parseFeed(body, feedURL)
}

// parseFeed tries RSS first, then Atom.
func parseFeed(data []byte, source string) ([]FeedItem, error) {
	// Try RSS.
	var rss rssFeed
	if err := xml.Unmarshal(data, &rss); err == nil && len(rss.Channel.Items) > 0 {
		var items []FeedItem
		for _, item := range rss.Channel.Items {
			pub, _ := time.Parse(time.RFC1123Z, item.PubDate)
			if pub.IsZero() {
				pub, _ = time.Parse(time.RFC1123, item.PubDate)
			}
			items = append(items, FeedItem{
				Title:       strings.TrimSpace(item.Title),
				URL:         strings.TrimSpace(item.Link),
				Description: strings.TrimSpace(stripHTML(item.Description)),
				Source:      source,
				PublishedAt: pub,
			})
		}
		return items, nil
	}

	// Try Atom.
	var atom atomFeed
	if err := xml.Unmarshal(data, &atom); err == nil && len(atom.Entries) > 0 {
		var items []FeedItem
		for _, entry := range atom.Entries {
			pub, _ := time.Parse(time.RFC3339, entry.Updated)
			items = append(items, FeedItem{
				Title:       strings.TrimSpace(entry.Title),
				URL:         strings.TrimSpace(entry.Link.Href),
				Description: strings.TrimSpace(stripHTML(entry.Summary)),
				Source:      source,
				PublishedAt: pub,
			})
		}
		return items, nil
	}

	return nil, fmt.Errorf("unrecognized feed format from %s", source)
}

// FetchMultipleFeeds fetches multiple RSS feeds concurrently.
// Returns all items (merged) and any errors (partial failure is OK).
func FetchMultipleFeeds(urls []string) ([]FeedItem, []error) {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		items  []FeedItem
		errors []error
	)

	for _, u := range urls {
		wg.Add(1)
		go func(feedURL string) {
			defer wg.Done()
			feedItems, err := FetchRSS(feedURL)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, err)
				return
			}
			items = append(items, feedItems...)
		}(u)
	}

	wg.Wait()
	return items, errors
}

// stripHTML removes HTML tags from a string (simple approach).
func stripHTML(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return strings.TrimSpace(result.String())
}
