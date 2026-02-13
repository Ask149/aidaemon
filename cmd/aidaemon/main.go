// AIDaemon — a personal AI assistant accessible via Telegram.
//
// Usage:
//
//	aidaemon              # run the daemon (reads ~/.config/aidaemon/config.json)
//	aidaemon --login      # authenticate with GitHub Copilot via device flow
//
// The daemon runs until SIGTERM or SIGINT.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ask149/aidaemon/internal/auth"
	"github.com/Ask149/aidaemon/internal/config"
	"github.com/Ask149/aidaemon/internal/provider/copilot"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/telegram"
)

func main() {
	loginFlag := flag.Bool("login", false, "authenticate with GitHub Copilot via device flow")
	flag.Parse()

	if *loginFlag {
		doLogin()
		return
	}

	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func doLogin() {
	token, err := auth.RunDeviceFlow()
	if err != nil {
		log.Fatalf("login failed: %v", err)
	}
	fmt.Printf("✅ Authenticated. Token: %s...%s\n", token[:4], token[len(token)-4:])
}

func run() error {
	// 1. Load config.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log.Printf("[daemon] config loaded (model=%s, conv_limit=%d)", cfg.ChatModel, cfg.MaxConversationMessages)

	// 2. Auth — create token manager and validate.
	tm, err := auth.NewTokenManager()
	if err != nil {
		return fmt.Errorf("token manager: %w\nRun 'aidaemon --login' to authenticate", err)
	}
	if err := tm.ValidateGitHub(); err != nil {
		return fmt.Errorf("github auth: %w\nRun 'aidaemon --login' to authenticate", err)
	}
	tok, err := tm.GetToken()
	if err != nil {
		return fmt.Errorf("copilot token: %w", err)
	}
	log.Printf("[daemon] copilot auth OK (expires in %s)", tok.ExpiresIn().Round(time.Minute))

	// 3. Provider.
	prov := copilot.New(tm)
	log.Printf("[daemon] provider: %s (%d models)", prov.Name(), len(prov.Models()))

	// 4. Store.
	st, err := store.New(cfg.DBPath, cfg.MaxConversationMessages)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()
	log.Printf("[daemon] store: %s", cfg.DBPath)

	// 5. Telegram bot.
	tbot, err := telegram.New(telegram.Config{
		Token:        cfg.TelegramToken,
		UserID:       cfg.TelegramUserID,
		Provider:     prov,
		Store:        st,
		Model:        cfg.ChatModel,
		SystemPrompt: cfg.SystemPrompt,
		ConvLimit:    cfg.MaxConversationMessages,
	})
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}

	// 6. Context with signal handling.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	log.Println("[daemon] starting — send a Telegram message to chat")

	// Bot.Start blocks until ctx is cancelled.
	tbot.Start(ctx)

	log.Println("[daemon] shutting down...")

	// Give in-flight operations time to finish.
	<-time.After(2 * time.Second)
	log.Println("[daemon] stopped")

	return nil
}
