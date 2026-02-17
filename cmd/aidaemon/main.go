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
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Ask149/aidaemon/internal/auth"
	"github.com/Ask149/aidaemon/internal/channel"
	"github.com/Ask149/aidaemon/internal/config"
	"github.com/Ask149/aidaemon/internal/cron"
	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/heartbeat"
	"github.com/Ask149/aidaemon/internal/httpapi"
	"github.com/Ask149/aidaemon/internal/imageutil"
	"github.com/Ask149/aidaemon/internal/mcp"
	"github.com/Ask149/aidaemon/internal/permissions"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/provider/copilot"
	openaiProvider "github.com/Ask149/aidaemon/internal/provider/openai"
	"github.com/Ask149/aidaemon/internal/session"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/telegram"
	"github.com/Ask149/aidaemon/internal/tools"
	"github.com/Ask149/aidaemon/internal/tools/builtin"
	"github.com/Ask149/aidaemon/internal/workspace"
	"github.com/Ask149/aidaemon/internal/wschannel"
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

// createProvider creates the configured LLM provider.
func createProvider(cfg *config.Config) (provider.Provider, error) {
	switch cfg.Provider {
	case "copilot", "":
		tm, err := auth.NewTokenManager()
		if err != nil {
			return nil, fmt.Errorf("token manager: %w\nRun 'aidaemon --login' to authenticate", err)
		}
		if err := tm.ValidateGitHub(); err != nil {
			return nil, fmt.Errorf("github auth: %w\nRun 'aidaemon --login' to authenticate", err)
		}
		tok, err := tm.GetToken()
		if err != nil {
			return nil, fmt.Errorf("copilot token: %w", err)
		}
		log.Printf("[daemon] copilot auth OK (expires in %s)", tok.ExpiresIn().Round(time.Minute))
		return copilot.New(tm), nil

	case "openai":
		return openaiProvider.New(openaiProvider.Config{
			BaseURL:         cfg.ProviderConfig.BaseURL,
			APIKey:          cfg.ProviderConfig.APIKey,
			AzureAPIVersion: cfg.ProviderConfig.AzureAPIVersion,
			Model:           cfg.ChatModel,
		}), nil

	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
}

func run() error {
	// 1. Load config.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log.Printf("[daemon] config loaded (model=%s, conv_limit=%d)", cfg.ChatModel, cfg.MaxConversationMessages)

	// 1b. Set up log file persistence (write to both stderr and file).
	logPath := filepath.Join(cfg.DataDir, "logs", "aidaemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[daemon] warning: could not open log file %s: %v", logPath, err)
	} else {
		defer logFile.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		log.Printf("[daemon] logging to %s", logPath)
	}

	// 2-3. Provider.
	prov, err := createProvider(cfg)
	if err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	log.Printf("[daemon] provider: %s (%d models)", prov.Name(), len(prov.Models()))

	// 4. Tool registry (built-in tools).
	registry := setupTools(cfg)
	log.Printf("[daemon] built-in tools: %d registered", len(registry.List()))

	// 4a. Audit logger for tool calls.
	auditPath := filepath.Join(cfg.DataDir, "logs", "audit.log")
	auditFile, err := os.OpenFile(auditPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Printf("[daemon] warning: could not open audit log %s: %v", auditPath, err)
	} else {
		defer auditFile.Close()
		registry.SetAuditWriter(auditFile)
		log.Printf("[daemon] audit logging to %s", auditPath)
	}

	// 4b. MCP servers — launch subprocesses and register their tools.
	// Create context early so MCP servers can use it for lifecycle.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	var mcpMgr *mcp.Manager
	if len(cfg.MCPServers) > 0 {
		mcpMgr = mcp.NewManager()
		mcpConfigs := make(map[string]mcp.ServerConfig)
		for name, sc := range cfg.MCPServers {
			mcpConfigs[name] = mcp.ServerConfig{
				Command: sc.Command,
				Args:    sc.Args,
				Env:     sc.Env,
				Enabled: sc.Enabled,
			}
		}
		mcpMgr.StartAll(ctx, mcpConfigs)

		// Register MCP tools into the same registry.
		for _, srv := range mcpMgr.Servers() {
			client := srv.Client()
			if client == nil {
				continue
			}
			mcpTools, err := client.ListTools()
			if err != nil {
				log.Printf("[daemon] ⚠️  %s: list tools failed: %v", srv.Name(), err)
				continue
			}
			for _, ti := range mcpTools {
				tool := tools.NewMCPTool(client, ti, srv.Name())
				registry.Register(tool)
			}
		}
		log.Printf("[daemon] total tools (built-in + MCP): %d", len(registry.List()))
	}

	// 5. Store.
	st, err := store.New(cfg.DBPath, cfg.MaxConversationMessages)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()
	log.Printf("[daemon] store: %s", cfg.DBPath)

	// 5b. Migrate system_prompt.md → workspace SOUL.md (one-time, idempotent).
	workspace.MigrateSystemPrompt(filepath.Dir(cfg.DBPath), cfg.ResolvedWorkspaceDir())

	// 5c. Ensure default workspace files exist.
	if err := workspace.EnsureDefaults(cfg.ResolvedWorkspaceDir()); err != nil {
		log.Printf("[daemon] warning: workspace defaults: %v", err)
	}

	// 5d. Load workspace (re-read per message, but load once here for initial prompt).
	wsDir := cfg.ResolvedWorkspaceDir()
	skillsDir := cfg.ResolvedSkillsDir()
	ws := workspace.Load(wsDir, skillsDir)
	initialPrompt := ws.SystemPrompt()
	log.Printf("[daemon] workspace: %s (soul=%d, user=%d, memory=%d, tools=%d, skills=%d chars)",
		wsDir, len(ws.Soul), len(ws.User), len(ws.Memory), len(ws.Tools), len(ws.Skills))

	// 5e. Create session manager.
	mgr := session.NewManager(session.ManagerConfig{
		Store:        st,
		Engine:       &engine.Engine{Provider: prov, Registry: registry},
		Model:        cfg.ChatModel,
		TokenLimit:   cfg.TokenLimit,
		Threshold:    0.8,
		WorkspaceDir: wsDir,
		SystemPromptFunc: func() string {
			return workspace.Load(wsDir, skillsDir).SystemPrompt()
		},
	})
	log.Printf("[daemon] session manager created")

	// 5f. Migrate existing sessions to use daily rotation.
	st.MigrateExistingSessions()
	log.Printf("[daemon] existing sessions migrated")

	// 5g. Register cron management tool (needs store, registered after setupTools).
	registry.Register(&builtin.ManageCronTool{
		Store:       st,
		ChannelType: "telegram",
		ChannelMeta: fmt.Sprintf(`{"chat_id":%d}`, cfg.TelegramUserID),
	})

	// 6. Start services.
	log.Println("[daemon] starting...")

	// 6a. Start daily session rotation.
	stopDaily := mgr.StartDailyRotation(ctx)
	defer stopDaily()
	log.Println("[daemon] daily session rotation started")

	// 6b. WebSocket channel — delegates to session manager with image support.
	// Declare wsCh first so the OnImage closure can reference it.
	var wsCh *wschannel.Channel
	wsCh = wschannel.New(wschannel.Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			result, err := mgr.HandleMessage(ctx, sessionID, text, session.HandleOptions{
				ToolExecutor: &engine.ImageAwareExecutor{
					Registry: registry,
					OnImage: func(ctx context.Context, toolName string, images []imageutil.Image) {
						for _, img := range images {
							if err := wsCh.SendImage(ctx, sessionID, imageutil.DataURL(img)); err != nil {
								log.Printf("[wschannel] send image error: %v", err)
							}
						}
					},
				},
			})
			if err != nil {
				return "", err
			}
			return result.Content, nil
		},
		OnNewSession: func(ctx context.Context, sessionID string) (string, error) {
			return mgr.RotateSession(ctx, sessionID)
		},
	})
	log.Println("[daemon] websocket channel ready")

	// 6c. Telegram bot (optional).
	var tbot *telegram.Bot
	if cfg.TelegramEnabled() {
		st.MigrateChatIDs("telegram")
		tbot, err = telegram.New(telegram.Config{
			Token:        cfg.TelegramToken,
			UserID:       cfg.TelegramUserID,
			Provider:     prov,
			Store:        st,
			Model:        cfg.ChatModel,
			SystemPrompt: initialPrompt,
			ConvLimit:    cfg.MaxConversationMessages,
			ToolRegistry: registry,
			DataDir:      cfg.DataDir,
			WorkspaceDir: wsDir,
			SkillsDir:    skillsDir,
			SessionMgr:   mgr,
		})
		if err != nil {
			return fmt.Errorf("telegram: %w", err)
		}
		go func() {
			if err := tbot.Start(ctx); err != nil {
				log.Printf("[telegram] error: %v", err)
			}
		}()
		log.Println("[daemon] telegram bot started")
	} else {
		log.Println("[daemon] telegram disabled (no token configured)")
	}

	// 7. Heartbeat (optional).
	if hbDur := cfg.HeartbeatDuration(); hbDur > 0 && cfg.TelegramEnabled() {
		sid := channel.SessionID("telegram", strconv.FormatInt(cfg.TelegramUserID, 10))
		hb := heartbeat.New(heartbeat.Config{
			Interval:  hbDur,
			SessionID: sid,
			SendFn: func(ctx context.Context, text string) error {
				return tbot.Send(ctx, sid, text)
			},
			Prompt: "This is a periodic check-in. Review your MEMORY.md, check if there's anything timely to mention, and if nothing urgent, respond with HEARTBEAT_OK.",
		})
		go hb.Run(ctx)
		log.Printf("[daemon] heartbeat started (interval=%s)", hbDur)
	}

	// 8. Cron scheduler.
	var cronSender cron.CronSender
	if tbot != nil {
		cronSender = &cron.TelegramSender{
			SendFn: func(ctx context.Context, chatID int64, text string) error {
				sid := channel.SessionID("telegram", strconv.FormatInt(chatID, 10))
				return tbot.Send(ctx, sid, text)
			},
		}
	}

	// 8b. HTTP API (optional — requires api_token and port > 0).
	// Placed after cronSender so webhook async delivery can reuse it.
	if cfg.APIToken != "" && cfg.Port > 0 {
		api := httpapi.New(httpapi.Config{
			Port:               cfg.Port,
			Token:              cfg.APIToken,
			Store:              st,
			Registry:           registry,
			Provider:           prov,
			Model:              cfg.ChatModel,
			SysPrompt:          initialPrompt,
			WorkspaceDir:       wsDir,
			SkillsDir:          skillsDir,
			WSHandler:          wsCh.Handler(),
			SessionManager:     mgr,
			WebhookSender:      cronSender,
			WebhookChannelType: "telegram",
			WebhookChannelMeta: fmt.Sprintf(`{"chat_id":%d}`, cfg.TelegramUserID),
		})
		go func() {
			if err := api.Start(ctx); err != nil {
				log.Printf("[httpapi] error: %v", err)
			}
		}()
	} else if cfg.Port > 0 {
		log.Printf("[daemon] HTTP API disabled (set api_token in config to enable)")
	}

	cronEngine := &engine.Engine{
		Provider: prov,
		Registry: registry,
	}

	cronExec := &cron.Executor{
		Sender: cronSender,
		RunMessage: func(ctx context.Context, prompt string) (string, error) {
			sysPrompt := workspace.Load(wsDir, skillsDir).SystemPrompt()
			messages := []provider.Message{
				{Role: "system", Content: sysPrompt},
				{Role: "user", Content: prompt},
			}
			result, err := cronEngine.Run(ctx, messages, engine.RunOptions{
				Model:         cfg.ChatModel,
				MaxIterations: 25,
			})
			if err != nil {
				if result != nil && result.Content != "" {
					return result.Content, nil
				}
				return "", err
			}
			return result.Content, nil
		},
		RunTool: func(ctx context.Context, toolName, argsJSON string) (string, error) {
			return registry.Execute(ctx, toolName, argsJSON)
		},
	}

	cronScheduler := cron.NewScheduler(cron.SchedulerConfig{
		Store:    st,
		Executor: cronExec,
	})
	cronScheduler.Start(ctx)
	log.Printf("[daemon] cron scheduler started")

	// Block until shutdown signal.
	<-ctx.Done()

	log.Println("[daemon] shutting down...")

	// Wait for in-flight cron jobs to finish.
	cronScheduler.Wait()

	// Stop MCP servers.
	if mcpMgr != nil {
		mcpMgr.StopAll()
	}

	// Give in-flight operations time to finish.
	<-time.After(2 * time.Second)
	log.Println("[daemon] stopped")

	return nil
}

// setupTools creates and configures the tool registry.
func setupTools(cfg *config.Config) *tools.Registry {
	// Build permission checker from config.
	var perms *permissions.Checker
	if len(cfg.ToolPermissions) > 0 {
		rules := make(map[string]permissions.Rule)
		for name, rule := range cfg.ToolPermissions {
			rules[name] = permissions.Rule{
				Mode:            permissions.Mode(rule.Mode),
				AllowedPaths:    rule.AllowedPaths,
				DeniedPaths:     rule.DeniedPaths,
				AllowedCommands: rule.AllowedCommands,
				DeniedCommands:  rule.DeniedCommands,
				AllowedDomains:  rule.AllowedDomains,
				DeniedDomains:   rule.DeniedDomains,
			}
		}
		perms = permissions.NewChecker(rules)
		log.Printf("[daemon] permissions: %d tool rules loaded", len(rules))
	}

	registry := tools.NewRegistry(perms)

	home, _ := os.UserHomeDir()

	// Safe default paths for file operations.
	allowedFilePaths := []string{
		filepath.Join(home, "Documents"),
		filepath.Join(home, "Projects"),
		filepath.Join(home, "Desktop"),
	}

	// Block only destructive commands — everything else is allowed.
	blockedCommands := []string{
		"rm", "rmdir", "shred", "unlink",
		"mkfs", "dd",
		"shutdown", "reboot", "halt", "poweroff",
		"kill", "killall", "pkill",
		"sudo",
	}

	// Register built-in tools.
	registry.Register(&builtin.ReadFileTool{
		AllowedPaths: allowedFilePaths,
	})

	registry.Register(&builtin.WriteFileTool{
		AllowedPaths: allowedFilePaths,
	})

	registry.Register(&builtin.RunCommandTool{
		BlockedCommands: blockedCommands,
	})

	registry.Register(&builtin.WebFetchTool{})

	registry.Register(&builtin.WebSearchTool{
		BraveAPIKey: cfg.BraveAPIKey,
	})

	registry.Register(&builtin.WriteWorkspaceTool{
		WorkspaceDir: cfg.ResolvedWorkspaceDir(),
	})

	return registry
}
