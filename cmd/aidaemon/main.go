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
	"github.com/Ask149/aidaemon/internal/heartbeat"
	"github.com/Ask149/aidaemon/internal/httpapi"
	"github.com/Ask149/aidaemon/internal/mcp"
	"github.com/Ask149/aidaemon/internal/permissions"
	"github.com/Ask149/aidaemon/internal/provider/copilot"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/telegram"
	"github.com/Ask149/aidaemon/internal/tools"
	"github.com/Ask149/aidaemon/internal/tools/builtin"
	"github.com/Ask149/aidaemon/internal/workspace"
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
	ws := workspace.Load(wsDir)
	initialPrompt := ws.SystemPrompt()
	log.Printf("[daemon] workspace: %s (soul=%d, user=%d, memory=%d, tools=%d chars)",
		wsDir, len(ws.Soul), len(ws.User), len(ws.Memory), len(ws.Tools))

	// 6. Start services.
	log.Println("[daemon] starting...")

	// 6a. HTTP API (optional — requires api_token and port > 0).
	if cfg.APIToken != "" && cfg.Port > 0 {
		api := httpapi.New(httpapi.Config{
			Port:         cfg.Port,
			Token:        cfg.APIToken,
			Store:        st,
			Registry:     registry,
			Provider:     prov,
			Model:        cfg.ChatModel,
			SysPrompt:    initialPrompt,
			WorkspaceDir: wsDir,
		})
		go func() {
			if err := api.Start(ctx); err != nil {
				log.Printf("[httpapi] error: %v", err)
			}
		}()
	} else if cfg.Port > 0 {
		log.Printf("[daemon] HTTP API disabled (set api_token in config to enable)")
	}

	// 6b. Telegram bot (optional).
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
		hb := heartbeat.New(heartbeat.Config{
			Interval:  hbDur,
			SessionID: channel.SessionID("telegram", strconv.FormatInt(cfg.TelegramUserID, 10)),
			SendFn: func(ctx context.Context, text string) error {
				return tbot.Send(ctx, channel.SessionID("telegram", strconv.FormatInt(cfg.TelegramUserID, 10)), text)
			},
			Prompt: "This is a periodic check-in. Review your MEMORY.md, check if there's anything timely to mention, and if nothing urgent, respond with HEARTBEAT_OK.",
		})
		go hb.Run(ctx)
		log.Printf("[daemon] heartbeat started (interval=%s)", hbDur)
	}

	// Block until shutdown signal.
	<-ctx.Done()

	log.Println("[daemon] shutting down...")

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
