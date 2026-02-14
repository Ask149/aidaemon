# AIDaemon

A personal AI agent daemon that runs on your machine and gives you access to premium LLM models through Telegram — powered by your GitHub Copilot subscription.

Chat with GPT-5, Claude Opus 4.6, Gemini 3 Pro, and 10+ other models from your phone, tablet, or any device with Telegram. Execute tools, browse the web, control your Mac, and integrate with 70+ MCP-powered capabilities — all for $10/month.

## Features

- **13+ premium models** — GPT-5, Claude Opus 4.6, Gemini 3 Pro, and more via GitHub Copilot API
- **Streaming responses** — live typing indicators with adaptive debounce
- **Tool execution** — read/write files, run shell commands, search the web
- **MCP integration** — 6 servers, 70+ tools (Playwright, Apple apps, Google Calendar, memory, filesystem)
- **Browser automation** — navigate, click, type, screenshot web pages via Playwright
- **Image analysis** — send photos to Telegram, get AI-powered descriptions
- **Persistent conversations** — SQLite-backed history with smart context compaction
- **HTTP API** — REST endpoints for programmatic access alongside the Telegram interface
- **Permission system** — configurable per-tool access control with path/command/domain rules
- **Audit logging** — structured log of every tool execution with timing data
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
  "system_prompt": "You are a helpful personal assistant."
}
```

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
| `/status` | Show current model and context info |
| `/reset` | Clear conversation history |
| `/help` | Show help |

### HTTP API

When `api_token` is set in config, a REST API is available on port 8420:

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

## Built-in Tools

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

  // System prompt
  "system_prompt": "string",           // Prepended to every conversation

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

## Development

```bash
go build ./...           # Build all packages
go vet ./...             # Static analysis
go test ./...            # Run tests
go run -race ./cmd/aidaemon/  # Run with race detector
go install ./cmd/aidaemon/    # Install to $GOBIN
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
