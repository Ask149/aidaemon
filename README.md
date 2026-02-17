# AIDaemon

A personal AI agent daemon that runs on your machine and gives you access to premium LLM models through Telegram — powered by your GitHub Copilot subscription.

Chat with GPT-5, Claude Opus 4.6, Gemini 3 Pro, and 10+ other models from your phone, tablet, or any device with Telegram. Execute tools, browse the web, control your Mac, and integrate with 70+ MCP-powered capabilities — all for $10/month.

## Features

- **13+ premium models** — GPT-5, Claude Opus 4.6, Gemini 3 Pro, and more via GitHub Copilot API
- **Session management** — persistent session IDs with titles, browse/switch via web UI or API
- **Auto-rotation** — daily 4AM rotation with memory flush and summary
- **Smart context** — load recent daily memory logs (last 3 days) into system prompt
- **Streaming responses** — live typing indicators with adaptive debounce
- **Tool execution** — read/write files, run shell commands, search the web
- **MCP integration** — 6 servers, 70+ tools (Playwright, Apple apps, Google Calendar, memory, filesystem)
- **Browser automation** — navigate, click, type, screenshot web pages via Playwright
- **Image analysis** — send photos to Telegram, get AI-powered descriptions
- **Persistent conversations** — SQLite-backed history with smart context compaction
- **HTTP API** — REST endpoints for programmatic access alongside the Telegram interface
- **Permission system** — configurable per-tool access control with path/command/domain rules
- **Audit logging** — structured log of every tool execution with timing data
- **Token management** — proactive context trimming, auto-summarize on token limit errors, emergency compaction
- **Rich stats footer** — every response shows token usage, timing, tool calls, and model info
- **Single-user security** — only your Telegram user ID can interact with the bot

## Quick Start

### Prerequisites

- **macOS** (ARM64 or Intel) or Linux
- **Go 1.25+**
- **GitHub Copilot** subscription ($10/month)
- **Telegram** account

### Install

```bash
git clone https://github.com/Ask149/aidaemon.git
cd aidaemon
go build -o aidaemon ./cmd/aidaemon/
```

### Authenticate

```bash
./aidaemon --login
```

Follow the GitHub device code flow — open the URL, enter the code, authorize.

### Configure

Create `~/.config/aidaemon/config.json`:

```jsonc
{
  "telegram_token": "YOUR_BOT_TOKEN",       // from @BotFather on Telegram
  "telegram_user_id": 123456789,            // from @userinfobot on Telegram
  "chat_model": "claude-sonnet-4.5",        // default model
  "max_conversation_messages": 20,          // context window size
  "token_limit": 128000,                    // token limit for rotation threshold
  "system_prompt": "You are a helpful personal assistant."
}
```

You can also use a markdown file for richer system prompts:

```bash
# Create a system prompt file (takes priority over config field)
vim ~/.config/aidaemon/system_prompt.md
```

When `~/.config/aidaemon/system_prompt.md` exists, it is loaded automatically and takes priority over the `system_prompt` field in config.json.

<details>
<summary><strong>Getting your Telegram credentials</strong></summary>

**Bot token:** Message [@BotFather](https://t.me/botfather) → `/newbot` → follow prompts → copy the token.

**User ID:** Message [@userinfobot](https://t.me/userinfobot) → copy your numeric ID.

</details>

### Run

```bash
./aidaemon
```

The daemon starts, connects to Telegram, and waits for your messages.

## Usage

### Telegram Commands

| Command | Description |
|---------|-------------|
| _any text_ | Chat with the AI (streamed response) |
| `/model` | List available models |
| `/model <id>` | Switch model (e.g., `/model gpt-5`) |
| `/status` | Model, context health, tool count |
| `/context` | Detailed context window breakdown (tokens, roles, capacity) |
| `/tools` | List all available tools grouped by source |
| `/new` | Start new session (archives current conversation) |
| `/title <text>` | Rename current session |
| `/reset` | Clear conversation history |
| `/help` | Show help |

### Web Interface

AIDaemon includes a web UI at `http://localhost:8420` (configurable via `http_port` in config):

- **Session sidebar** — Browse all sessions, click to switch and view history
- **Live chat** — Real-time WebSocket messaging with the AI
- **Command support** — Use `/new` and `/title` commands in the web UI
- **Session rotation** — Automatic notification when session rotates

#### Web Commands

| Command | Description |
|---------|-------------|
| `/new` | Start new session via WebSocket |
| `/title <text>` | Rename current session |

### HTTP API

When `api_token` is set in config, a REST API is available on port 8420:

REST endpoints for programmatic access:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/sessions` | GET | List all sessions |
| `/sessions/{id}` | GET | Get session details |
| `/sessions/{id}/messages` | GET | Get message history for session |
| `/sessions/{id}/title` | POST | Rename session (body: `{"title": "..."}`) |
| `/chat` | POST | Send message (existing endpoint) |
| `/tool` | POST | Execute a tool directly |
| `/health` | GET | Health check (no auth required) |

All endpoints require `Authorization: Bearer <token>` header (token from config).

```bash
# Chat
curl -X POST http://localhost:8420/chat \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello!", "session_id": "my-session"}'

# Execute a tool directly
curl -X POST http://localhost:8420/tool \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "read_file", "args": {"path": "/tmp/test.txt"}}'

# Health check (no auth required)
curl http://localhost:8420/health
```

| Tool | Description |
|------|-------------|
| `read_file` | Read file contents from allowed directories |
| `write_file` | Create or overwrite files |
| `run_command` | Execute shell commands (destructive commands blocked) |
| `web_fetch` | Fetch and extract text from any URL |
| `web_search` | Search the web via Brave Search API or DuckDuckGo fallback |

## MCP Servers

AIDaemon connects to [MCP](https://modelcontextprotocol.io/) servers at startup, dynamically registering their tools alongside the built-in ones.

| Server | Description |
|--------|-------------|
| [Playwright](https://github.com/playwright-community/mcp) | Browser automation — navigate, click, type, screenshot |
| [Apple](https://github.com/nicholasyager/apple-mcp) | Calendar, Contacts, Mail, Notes, Reminders, Maps, Messages |
| [Google Calendar](https://github.com/cocal-ai/google-calendar-mcp) | Google Calendar event management |
| [Memory](https://github.com/modelcontextprotocol/servers) | Persistent key-value memory across conversations |
| [Context7](https://github.com/upstash/context7-mcp) | Look up API documentation for any library |
| [Filesystem](https://github.com/modelcontextprotocol/servers) | Read, write, search, move files in allowed directories |

Configure in `config.json`:

```jsonc
{
  "mcp_servers": {
    "playwright": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest", "--browser", "chrome"]
    },
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"]
    }
  }
}
```

## Configuration

<details>
<summary><strong>Full config.json reference</strong></summary>

```jsonc
{
  // Required
  "telegram_token": "string",          // Telegram bot token from BotFather
  "telegram_user_id": 123456789,       // Your Telegram numeric user ID

  // Model
  "chat_model": "claude-sonnet-4.5",   // Default LLM model
  "max_conversation_messages": 20,     // Messages before context compaction
  "token_limit": 128000,               // Token limit for rotation threshold

  // System prompt
  "system_prompt": "string",           // Inline prompt (overridden by system_prompt.md file)

  // Or use a file: ~/.config/aidaemon/system_prompt.md  (takes priority)

  // Storage
  "db_path": "string",                 // SQLite path (default: ~/.config/aidaemon/aidaemon.db)
  "data_dir": "string",               // Media/logs dir (default: ~/.config/aidaemon/data)

  // Web search
  "brave_api_key": "string",          // Brave Search API key (optional, falls back to DuckDuckGo)

  // HTTP API
  "port": 8420,                        // API port (0 to disable)
  "api_token": "string",              // Bearer token for API auth

  // Permissions (optional)
  "tool_permissions": {
    "run_command": {
      "mode": "deny",                 // "allow_all" | "whitelist" | "deny"
      "denied_commands": ["rm", "sudo", "shutdown"]
    },
    "read_file": {
      "mode": "whitelist",
      "allowed_paths": ["~/Documents/**", "~/Projects/**"]
    }
  },

  // MCP servers
  "mcp_servers": {
    "server-name": {
      "command": "npx",
      "args": ["-y", "package-name"],
      "env": {"KEY": "value"},
      "enabled": true
    }
  },

  "log_level": "info"                 // "debug" | "info" | "warn" | "error"
}
```

</details>

## Available Models

Models are auto-discovered from the Copilot API and refreshed hourly.

**Base tier** (unlimited):
`gpt-4o` · `gpt-4.1` · `gpt-4o-mini`

**Premium tier** (~300 req/month on Copilot Individual):
`gpt-5` · `gpt-5-mini` · `gpt-5.1` · `gpt-5.2` · `claude-opus-4.6` · `claude-sonnet-4.5` · `claude-sonnet-4` · `claude-haiku-4.5` · `gemini-2.5-pro` · `gemini-3-pro-preview` · `gemini-3-flash-preview`

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed technical documentation.

```
You (Telegram) ──→ Telegram Bot ──→ Copilot API (13+ models)
                        │                    │
                        │              Tool calls
                        │                    │
                   SQLite Store    ┌─────────┴─────────┐
                                   │                   │
                             Built-in Tools      MCP Servers
                             (5 tools)           (70+ tools)
                                   │                   │
                             Files, Shell,      Browser, Calendar,
                             Web Search         Notes, Memory, ...
```

## Project Structure

```
cmd/
  aidaemon/              Main daemon entry point
  test-copilot/          Auth testing utility
  probe-models/          Model discovery testing
  test-tools/            Tool execution testing

scripts/
  watchdog.sh            Watchdog script (keeps daemon alive)
  com.ask149.*.plist     macOS launchd agent for 30-min checks

internal/
  auth/                  GitHub OAuth + Copilot token management
  config/                Configuration loading and validation
  httpapi/               REST API server
  mcp/                   MCP client (JSON-RPC 2.0 over stdio)
  permissions/           Per-tool permission enforcement
  provider/
    copilot/             GitHub Copilot API implementation
  store/                 SQLite conversation persistence (WAL mode)
  telegram/              Telegram bot (streaming, commands, images)
  tools/
    builtin/             Built-in tools (5 tools)
    registry.go          Tool registry + execution engine
    mcp_tool.go          MCP tool adapter
```

## Running as a Service

AIDaemon includes a watchdog script that keeps the daemon alive. It checks every 30 minutes (via macOS `launchd`) and restarts the daemon if it crashed or was stopped.

### Quick setup

```bash
# Install the launchd agent (runs every 30 min + at login)
make watchdog-install
```

That's it. The watchdog will:
- Start aidaemon immediately if it's not running
- Auto-rebuild the binary if Go source files changed
- Rotate daemon logs when they exceed 50 MB
- Log all health checks to `~/.config/aidaemon/data/logs/watchdog.log`

### Manual control

```bash
make watchdog              # Run the watchdog once manually
./scripts/watchdog.sh --force   # Force kill + restart
make watchdog-uninstall    # Remove the launchd agent
```

### How it works

| File | Purpose |
|------|----------|
| `scripts/watchdog.sh` | Bash script — checks PID, starts/restarts daemon |
| `scripts/com.ask149.aidaemon.watchdog.plist` | macOS launchd agent definition |
| `~/.config/aidaemon/data/logs/watchdog.log` | Watchdog check history |
| `~/.config/aidaemon/data/logs/aidaemon-daemon.log` | Daemon stdout/stderr (when started by watchdog) |
| `~/.config/aidaemon/data/logs/aidaemon.pid` | PID file for fast alive-checks |

<details>
<summary><strong>Setting up a similar watchdog for your own project</strong></summary>

The pattern is generic — you can adapt it for any long-running process on macOS:

1. **Create a watchdog script** (`scripts/watchdog.sh`):
   - Check if the process is running (`pgrep` or PID file)
   - If running → log "OK" and exit
   - If not → start the process via `nohup ... &`, save PID
   - Optional: auto-build if source is newer than binary

2. **Create a launchd plist** (`~/Library/LaunchAgents/com.yourname.yourapp.watchdog.plist`):
   ```xml
   <?xml version="1.0" encoding="UTF-8"?>
   <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
     "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
   <plist version="1.0">
   <dict>
       <key>Label</key>
       <string>com.yourname.yourapp.watchdog</string>
       <key>ProgramArguments</key>
       <array>
           <string>/path/to/your/watchdog.sh</string>
       </array>
       <key>StartInterval</key>
       <integer>1800</integer>  <!-- 30 minutes -->
       <key>RunAtLoad</key>
       <true/>
       <key>EnvironmentVariables</key>
       <dict>
           <key>PATH</key>
           <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
       </dict>
       <key>StandardOutPath</key>
       <string>/path/to/watchdog-launchd.log</string>
       <key>StandardErrorPath</key>
       <string>/path/to/watchdog-launchd.log</string>
   </dict>
   </plist>
   ```

3. **Load the agent**:
   ```bash
   cp your.plist ~/Library/LaunchAgents/
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/your.plist
   ```

4. **Unload when needed**:
   ```bash
   launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/your.plist
   ```

**On Linux**, replace launchd with a systemd user service:
```ini
# ~/.config/systemd/user/yourapp-watchdog.timer
[Unit]
Description=Watchdog for yourapp

[Timer]
OnBootSec=1min
OnUnitActiveSec=30min

[Install]
WantedBy=timers.target
```

```bash
systemctl --user enable --now yourapp-watchdog.timer
```

</details>

## Development

```bash
go build ./...           # Build all packages
go vet ./...             # Static analysis
go test ./...            # Run tests
go run -race ./cmd/aidaemon/  # Run with race detector
go install ./cmd/aidaemon/    # Install to $GOBIN

# Watchdog (keeps daemon alive)
make watchdog-install         # Install launchd agent (every 30 min)
make watchdog-uninstall       # Remove launchd agent
make watchdog                 # Run watchdog once manually
```

## Security

- **Single-user only** — messages from unauthorized Telegram users are silently dropped
- **Local execution** — all tools run on your machine; nothing leaves except LLM API calls
- **Permission system** — per-tool whitelist/deny rules for paths, commands, and domains
- **Audit trail** — every tool execution logged with timestamps, duration, and status
- **Token safety** — pre-commit hook blocks accidental credential commits
- **No telemetry** — zero data collection, zero phone-home

See [SECURITY.md](SECURITY.md) for the security policy.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[MIT](LICENSE)
