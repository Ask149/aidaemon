package heartbeat

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testRSSFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <item>
      <title>First Article</title>
      <link>https://example.com/1</link>
      <description>First description</description>
      <pubDate>Mon, 04 Mar 2026 10:00:00 +0000</pubDate>
    </item>
    <item>
      <title>Second Article</title>
      <link>https://example.com/2</link>
      <description>Second description</description>
      <pubDate>Mon, 04 Mar 2026 09:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

const testAtomFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Feed</title>
  <entry>
    <title>Atom Article</title>
    <link href="https://example.com/atom/1"/>
    <summary>Atom summary</summary>
    <updated>2026-03-04T10:00:00Z</updated>
  </entry>
</feed>`

func TestFetchRSS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(testRSSFeed))
	}))
	defer srv.Close()

	items, err := FetchRSS(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Title != "First Article" {
		t.Errorf("items[0].Title = %q, want First Article", items[0].Title)
	}
	if items[0].URL != "https://example.com/1" {
		t.Errorf("items[0].URL = %q, want https://example.com/1", items[0].URL)
	}
	if items[0].Description != "First description" {
		t.Errorf("items[0].Description = %q, want First description", items[0].Description)
	}
}

func TestFetchRSS_Atom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write([]byte(testAtomFeed))
	}))
	defer srv.Close()

	items, err := FetchRSS(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Title != "Atom Article" {
		t.Errorf("items[0].Title = %q, want Atom Article", items[0].Title)
	}
}

func TestFetchMultipleFeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testRSSFeed))
	}))
	defer srv.Close()

	items, errors := FetchMultipleFeeds([]string{srv.URL, srv.URL})
	if len(errors) != 0 {
		t.Errorf("unexpected errors: %v", errors)
	}
	if len(items) != 4 {
		t.Errorf("got %d items, want 4", len(items))
	}
}

func TestFetchMultipleFeeds_PartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testRSSFeed))
	}))
	defer srv.Close()

	items, errors := FetchMultipleFeeds([]string{srv.URL, "http://nonexistent.invalid/feed"})
	// Should still get items from the working feed.
	if len(items) != 2 {
		t.Errorf("got %d items, want 2 (partial success)", len(items))
	}
	if len(errors) != 1 {
		t.Errorf("got %d errors, want 1", len(errors))
	}
}
