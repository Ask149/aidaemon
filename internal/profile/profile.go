package profile

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Profile holds user configuration for the heartbeat system.
type Profile struct {
	Name     string `yaml:"name"`
	Timezone string `yaml:"timezone"`

	Digest    DigestConfig    `yaml:"digest"`
	Channels  ChannelConfig   `yaml:"channels"`
	Sources   SourcesConfig   `yaml:"sources"`
	Goals     []Goal          `yaml:"goals"`
	News      NewsConfig      `yaml:"news"`
	Awareness AwarenessConfig `yaml:"awareness"`
}

// DigestConfig controls morning/evening digest schedule.
type DigestConfig struct {
	Morning string `yaml:"morning"` // "HH:MM" in local time
	Evening string `yaml:"evening"` // "HH:MM" in local time
}

// ChannelConfig maps urgency levels to delivery channels.
type ChannelConfig struct {
	Urgent  []string `yaml:"urgent"`
	Routine []string `yaml:"routine"`
}

// SourcesConfig holds pluggable data source providers.
type SourcesConfig struct {
	Calendar SourceProvider `yaml:"calendar"`
	Email    SourceProvider `yaml:"email"`
	Weather  SourceProvider `yaml:"weather"`
}

// SourceProvider describes how to access a data source.
type SourceProvider struct {
	Provider string                 `yaml:"provider"` // "playwright", "mcp", "api"
	Config   map[string]interface{} `yaml:"config"`
}

// Goal represents a user goal or habit to track.
type Goal struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
	Frequency   string `yaml:"frequency"` // e.g., "3/week", "daily"
	Tracking    string `yaml:"tracking"`  // "manual" or "automatic"
}

// NewsConfig holds news topic configuration.
type NewsConfig struct {
	Topics           []NewsTopic `yaml:"topics"`
	MaxItemsPerTopic int         `yaml:"max_items_per_topic"`
}

// NewsTopic represents a news category with its sources.
type NewsTopic struct {
	Label             string             `yaml:"label"`
	Feeds             []string           `yaml:"feeds"`
	PlaywrightSources []PlaywrightSource `yaml:"playwright_sources"`
}

// PlaywrightSource represents a browser-based news source.
type PlaywrightSource struct {
	URL  string `yaml:"url"`
	Type string `yaml:"type"` // e.g., "twitter"
}

// AwarenessConfig controls what awareness monitors.
type AwarenessConfig struct {
	Calendar bool `yaml:"calendar"`
	Email    bool `yaml:"email"`
	Goals    bool `yaml:"goals"`
}

// DefaultPath returns the default user profile path.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "aidaemon", "user-profile.yaml")
}

// Load reads the profile from the default path.
func Load() (*Profile, error) {
	return LoadFromPath(DefaultPath())
}

// LoadFromPath reads and parses a user profile from the given path.
func LoadFromPath(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %s: %w", path, err)
	}

	p := defaultProfile()
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("parse profile %s: %w", path, err)
	}

	// Apply defaults for zero values.
	if p.Digest.Morning == "" {
		p.Digest.Morning = "08:00"
	}
	if p.Digest.Evening == "" {
		p.Digest.Evening = "21:00"
	}
	if p.News.MaxItemsPerTopic == 0 {
		p.News.MaxItemsPerTopic = 3
	}
	if p.Timezone == "" {
		p.Timezone = "UTC"
	}

	return p, nil
}

func defaultProfile() *Profile {
	return &Profile{
		Digest: DigestConfig{
			Morning: "08:00",
			Evening: "21:00",
		},
		Channels: ChannelConfig{
			Urgent:  []string{"telegram"},
			Routine: []string{"telegram"},
		},
		News: NewsConfig{
			MaxItemsPerTopic: 3,
		},
		Awareness: AwarenessConfig{
			Calendar: true,
			Email:    true,
			Goals:    true,
		},
	}
}
