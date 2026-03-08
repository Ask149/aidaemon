package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProfile_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user-profile.yaml")
	os.WriteFile(path, []byte(`
name: "TestUser"
timezone: "America/Los_Angeles"
digest:
  morning: "08:00"
  evening: "21:00"
channels:
  urgent: ["telegram"]
  routine: ["telegram"]
goals:
  - id: "exercise"
    description: "Exercise 3x per week"
    frequency: "3/week"
    tracking: "manual"
news:
  topics:
    - label: "AI"
      feeds:
        - "https://hnrss.org/best"
      playwright_sources: []
  max_items_per_topic: 3
`), 0644)

	p, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "TestUser" {
		t.Errorf("name = %q, want TestUser", p.Name)
	}
	if p.Timezone != "America/Los_Angeles" {
		t.Errorf("timezone = %q, want America/Los_Angeles", p.Timezone)
	}
	if p.Digest.Morning != "08:00" {
		t.Errorf("digest.morning = %q, want 08:00", p.Digest.Morning)
	}
	if len(p.Goals) != 1 || p.Goals[0].ID != "exercise" {
		t.Errorf("goals = %+v, want 1 goal with id=exercise", p.Goals)
	}
	if len(p.News.Topics) != 1 || p.News.Topics[0].Label != "AI" {
		t.Errorf("news.topics = %+v, want 1 topic with label=AI", p.News.Topics)
	}
	if p.News.MaxItemsPerTopic != 3 {
		t.Errorf("news.max_items_per_topic = %d, want 3", p.News.MaxItemsPerTopic)
	}
}

func TestLoadProfile_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user-profile.yaml")
	os.WriteFile(path, []byte(`name: "Minimal"`), 0644)

	p, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Digest.Morning != "08:00" {
		t.Errorf("default morning = %q, want 08:00", p.Digest.Morning)
	}
	if p.Digest.Evening != "21:00" {
		t.Errorf("default evening = %q, want 21:00", p.Digest.Evening)
	}
	if p.News.MaxItemsPerTopic != 3 {
		t.Errorf("default max_items = %d, want 3", p.News.MaxItemsPerTopic)
	}
}

func TestLoadProfile_Missing(t *testing.T) {
	_, err := LoadFromPath("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadProfile_DefaultPath(t *testing.T) {
	// Load() uses ~/.config/aidaemon/user-profile.yaml — just test the path resolution.
	path := DefaultPath()
	if path == "" {
		t.Fatal("DefaultPath returned empty")
	}
}
