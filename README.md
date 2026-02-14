# AIDaemon — Personal AI Agent via Telegram

**A 24/7 AI assistant accessible from anywhere via Telegram, powered by your GitHub Copilot subscription.**

## What It Is

AIDaemon is a personal AI agent daemon that runs on your Mac and gives you access to premium LLM models (GPT-5, Claude Opus 4.6, Gemini 3 Pro) through Telegram — all using your **$10/month GitHub Copilot subscription**. No separate API keys needed.

**Message your bot from anywhere** (phone, work computer, tablet) and get:
- Streaming AI responses with live typing indicators
- Access to 13+ premium models
- Persistent conversations across sessions
- Tool use capabilities (file access, shell commands, web search, MCP integration)

## Quick Start

### 1. Prerequisites

- macOS (ARM64 or Intel)
- Go 1.25+ (`/opt/homebrew/bin/go`)
- GitHub Copilot subscription ($10/mo)
- Telegram account

### 2. Authentication

```bash
cd aidaemon
go run ./cmd/aidaemon/ --login
```

Follow the device code flow to authenticate with GitHub.

### 3. Configuration

Create `~/.config/aidaemon/config.json`:

```json
{
  "telegram_token": "YOUR_BOT_TOKEN_FROM_BOTFATHER",
  "telegram_user_id": YOUR_TELEGRAM_USER_ID,
  "chat_model": "claude-opus-4.6",
  "system_prompt": "You are a helpful personal assistant. Be concise and direct.",
  "max_conversation_messages": 20
}
```

**Get your bot token:**
1. Message [@BotFather](https://t.me/botfather) on Telegram
2. Send `/newbot` and follow prompts
3. Copy the token

**Get your user ID:**
1. Message [@userinfobot](https://t.me/userinfobot) on Telegram
2. Copy your numeric ID

### 4. Run

```bash
/opt/homebrew/bin/go run ./cmd/aidaemon/
```

The daemon will start and listen for Telegram messages.

## Commands

| Command | Action |
|---------|--------|
| *any text* | Chat with the AI (streamed response) |
| `/model` | List all available models |
| `/model <id>` | Switch to a different model |
| `/status` | Show current model and context size |
| `/reset` | Clear conversation history |
| `/help` | Show help message |

## Available Models (as of Feb 2026)

**Base tier (unlimited):**
- gpt-4o, gpt-4.1, gpt-4o-mini

**Premium tier (~300 req/month on Copilot Individual):**
- GPT-5, GPT-5-mini, GPT-5.1, GPT-5.2
- Claude Opus 4.6, Claude Opus 4.6 Fast, Claude Opus 4.5
- Claude Sonnet 4.5, Claude Sonnet 4
- Claude Haiku 4.5
- Gemini 2.5 Pro, Gemini 3 Pro Preview, Gemini 3 Flash Preview

Models are auto-discovered from the Copilot API and refreshed hourly.

## Architecture

```
GitHub OAuth → Copilot Token → OpenAI-compatible API
                              ↓
                        13+ LLM models
                              ↓
                        Telegram Bot
                              ↓
                    SQLite conversation store
```

### Project Structure

```
cmd/
  aidaemon/           # Main daemon entry point
  test-copilot/       # Auth testing tool
  probe-models/       # Model discovery testing
internal/
  auth/               # GitHub OAuth + Copilot token management
  config/             # Configuration loading
  provider/           # LLM provider abstraction
    copilot/          # Copilot-specific implementation
  store/              # SQLite conversation persistence
  telegram/           # Telegram bot implementation
```

## Features

✅ **Currently working:**
- Chat with 13+ premium models via Telegram
- Streaming responses with live typing indicator
- Dynamic model switching mid-conversation
- Auto-discovery of available models
- Persistent conversations (SQLite)
- Multi-chat support (each Telegram chat is separate)
- Secure (only your Telegram user ID can access)

🚧 **In development (Phase 1):**
- Tool/function calling framework
- Local file access (read/write)
- Shell command execution
- Web search and fetching

📋 **Planned (Phase 2+):**
- MCP server integration (Google Calendar, Tasks, Apple Notes)
- Browser automation
- Image analysis
- Markdown formatting in Telegram
- Smart context compaction
- Rich system prompt with personal context
- HTTP API

## Development

**Build:**
```bash
/opt/homebrew/bin/go build ./...
```

**Run tests:**
```bash
/opt/homebrew/bin/go test ./...
```

**Vet:**
```bash
/opt/homebrew/bin/go vet ./...
```

## Configuration Options

Full config schema:

```json
{
  "telegram_token": "string (required)",
  "telegram_user_id": 123456789 (required),
  "chat_model": "string (default: claude-opus-4.6)",
  "system_prompt": "string (default: generic assistant prompt)",
  "max_conversation_messages": 20,
  "port": 8420,
  "db_path": "~/.config/aidaemon/aidaemon.db",
  "log_level": "info"
}
```

## Troubleshooting

**Authentication failed:**
```bash
# Re-run device flow
/opt/homebrew/bin/go run ./cmd/aidaemon/ --login
```

**Daemon not starting:**
- Check logs for errors
- Verify config.json is valid JSON
- Ensure Telegram token and user ID are correct

**No models available:**
- Check GitHub Copilot subscription is active
- Run `/opt/homebrew/bin/go run ./cmd/test-copilot/` to test auth

**Bot not responding:**
- Verify your user ID matches config
- Check daemon logs
- Restart daemon

## Security

- Only the configured Telegram user ID can interact with the bot
- Messages from other users are silently dropped
- All conversations stored locally in SQLite
- No data leaves your machine except LLM API calls

## Comparison to OpenCode

AIDaemon is inspired by [OpenCode](https://github.com/sst/opencode) but optimized for Telegram access:

| Feature | OpenCode | AIDaemon |
|---------|----------|----------|
| Interface | TUI + Desktop + Web | Telegram |
| Tool use | 17+ built-in tools | In development |
| Providers | 22+ | Copilot only (13+ models) |
| MCP support | Full | Planned |
| Use case | AI coding assistant | Personal assistant |
| Cost | Various API keys | $10/mo Copilot only |

## License

MIT

## Author

Built for personal use. Inspired by OpenCode's architecture.
