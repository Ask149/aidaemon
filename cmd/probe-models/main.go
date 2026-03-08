// probe-models tests which model IDs are accepted by the Copilot API.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Ask149/aidaemon/internal/auth"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/provider/copilot"
)

func main() {
	tm, err := auth.NewTokenManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	p := copilot.New(tm)
	ctx := context.Background()

	models := []string{
		// Base tier (expected unlimited)
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4.1",
		"gpt-4.1-mini",
		"gpt-4.1-nano",
		// Gemini variants
		"gemini-2.0-flash-001",
		"gemini-2.0-flash",
		"gemini-flash-2.0",
		// Premium tier
		"claude-sonnet-4",
		"claude-3.5-sonnet",
		"claude-3.7-sonnet",
		"o3-mini",
		"o4-mini",
		"gemini-2.5-pro",
		// Other possible IDs
		"gpt-4-turbo",
		"gpt-3.5-turbo",
	}

	fmt.Printf("%-30s %s\n", "MODEL", "STATUS")
	fmt.Printf("%-30s %s\n", "-----", "------")

	for _, model := range models {
		req := provider.ChatRequest{
			Model:    model,
			Messages: []provider.Message{{Role: "user", Content: "Hi"}},
		}

		start := time.Now()
		resp, err := p.Chat(ctx, req)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("%-30s ❌ %s (%s)\n", model, truncErr(err), elapsed.Round(time.Millisecond))
		} else {
			fmt.Printf("%-30s ✅ in=%d out=%d (%s) → %s\n",
				model, resp.Usage.InputTokens, resp.Usage.OutputTokens,
				elapsed.Round(time.Millisecond), resp.Model)
		}

		// Small delay to avoid rate limiting.
		time.Sleep(200 * time.Millisecond)
	}
}

func truncErr(err error) string {
	s := err.Error()
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
