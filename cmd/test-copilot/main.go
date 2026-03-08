// test-copilot is a minimal CLI to verify the Copilot connector works.
// It sends a simple prompt and streams the response to stdout.
//
// Usage:
//
//	go run ./cmd/test-copilot/
//	go run -race ./cmd/test-copilot/               # with race detector
//	go run ./cmd/test-copilot/ -model gpt-4o-mini   # specific model
//	go run ./cmd/test-copilot/ -prompt "explain goroutines"
//	go run ./cmd/test-copilot/ -no-stream            # non-streaming mode
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Ask149/aidaemon/internal/auth"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/provider/copilot"
)

func main() {
	model := flag.String("model", "gpt-4o", "model to use")
	prompt := flag.String("prompt", "What is 2+2? Reply in one sentence.", "prompt to send")
	noStream := flag.Bool("no-stream", false, "use non-streaming Chat instead of Stream")
	login := flag.Bool("login", false, "run device code flow to authenticate with GitHub")
	flag.Parse()

	// If --login, run device flow first.
	if *login {
		token, err := auth.RunDeviceFlow()
		if err != nil {
			log.Fatalf("❌ Device flow: %v", err)
		}
		fmt.Fprintf(os.Stderr, "🔑 Token saved. First 10 chars: %s...\n\n", token[:10])
	}

	// 1. Create token manager.
	tm, err := auth.NewTokenManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n\n", err)
		fmt.Fprintf(os.Stderr, "💡 Run with --login to authenticate:\n")
		fmt.Fprintf(os.Stderr, "   go run ./cmd/test-copilot/ --login\n")
		os.Exit(1)
	}

	// 2. Validate GitHub token.
	fmt.Fprintf(os.Stderr, "🔑 Validating GitHub token... ")
	if err := tm.ValidateGitHub(); err != nil {
		log.Fatalf("❌ %v", err)
	}
	fmt.Fprintln(os.Stderr, "✅")

	// 3. Get initial Copilot token (validates Copilot subscription).
	fmt.Fprintf(os.Stderr, "🎫 Getting Copilot token... ")
	tok, err := tm.GetToken()
	if err != nil {
		log.Fatalf("❌ %v", err)
	}
	fmt.Fprintf(os.Stderr, "✅ (expires in %s)\n", tok.ExpiresIn().Round(time.Second))

	// 4. Create Copilot provider.
	p := copilot.New(tm)
	fmt.Fprintf(os.Stderr, "📡 Provider: %s\n", p.Name())
	fmt.Fprintf(os.Stderr, "🤖 Model: %s\n", *model)
	fmt.Fprintf(os.Stderr, "💬 Prompt: %s\n\n", *prompt)

	req := provider.ChatRequest{
		Model: *model,
		Messages: []provider.Message{
			{Role: "user", Content: *prompt},
		},
	}

	ctx := context.Background()
	start := time.Now()

	if *noStream {
		chatMode(ctx, p, req, start)
	} else {
		streamMode(ctx, p, req, start)
	}
}

func chatMode(ctx context.Context, p *copilot.Provider, req provider.ChatRequest, start time.Time) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		log.Fatalf("❌ Chat error: %v", err)
	}

	fmt.Println(resp.Content)
	elapsed := time.Since(start)
	fmt.Fprintf(os.Stderr, "\n---\n")
	fmt.Fprintf(os.Stderr, "⏱  %s | 📊 in=%d out=%d cached=%d | 🤖 %s\n",
		elapsed.Round(time.Millisecond),
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CachedTokens,
		resp.Model,
	)
}

func streamMode(ctx context.Context, p *copilot.Provider, req provider.ChatRequest, start time.Time) {
	stream, err := p.Stream(ctx, req)
	if err != nil {
		log.Fatalf("❌ Stream error: %v", err)
	}

	var totalChunks int
	for event := range stream {
		if event.Error != nil {
			log.Fatalf("\n❌ Stream error: %v", event.Error)
		}

		if event.Delta != "" {
			fmt.Print(event.Delta)
			totalChunks++
		}

		if event.Done {
			elapsed := time.Since(start)
			fmt.Fprintf(os.Stderr, "\n---\n")
			fmt.Fprintf(os.Stderr, "⏱  %s | 📊 ", elapsed.Round(time.Millisecond))
			if event.Usage != nil {
				fmt.Fprintf(os.Stderr, "in=%d out=%d cached=%d",
					event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.CachedTokens)
			} else {
				fmt.Fprintf(os.Stderr, "(no usage data)")
			}
			fmt.Fprintf(os.Stderr, " | 📦 %d chunks\n", totalChunks)
		}
	}
}
