# Changelog

All notable changes to AIDaemon are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- `/tools` command — lists all tools grouped by source (built-in vs MCP server)
- `/context` command — detailed context window breakdown (tokens, roles, capacity bar)
- Rich stats footer on every response: token counts (with K/M formatting), timing, tool usage, LLM call count
- Proactive token budget management — trims messages before LLM calls to stay within model limits
- Auto-recovery from token limit errors — emergency summarization with cheap model (up to 3 retries)
- Tool result truncation — caps individual tool outputs at 30K chars to prevent context blowout
- Per-tool execution timing and error tracking in `Result`
- `ModelTokenLimit()` — static token limit map for all supported models
- Watchdog script (`scripts/watchdog.sh`) — keeps daemon alive via macOS launchd (every 30 min)

### Changed
- `/status` now shows model tier (premium/unlimited), context health bar, token limit, and tool count
- `/help` reorganized into Chat / Monitoring / Tips sections
- Stats footer upgraded from simple `tokens | model` to full `tokens | timing | tools | LLM calls | model`
- Max-iteration summary now handles token limit errors with emergency fallback

## [0.1.0] — 2026-02-13

First complete release with all core features.

### Added

**Phase 1 — Tool Framework**
- Tool interface and registry with OpenAI function calling format
- 5 built-in tools: `read_file`, `write_file`, `run_command`, `web_fetch`, `web_search`
- Tool execution loop in Telegram bot (up to 999 iterations)
- Path whitelisting for file tools, command blocking for shell tool

**Phase 2 — Intelligence Layer**
- LLM markdown → Telegram HTML formatting
- Configurable system prompt (loaded from file or inline)
- Image analysis support (Telegram photos → vision models)
- Smart context compaction (summarize old messages with cheap model)
- Media and log persistence to disk

**Phase 3 — Web Access**
- Brave Search API integration with DuckDuckGo fallback
- HTML → text extraction for web fetching

**Phase 4 — MCP Integration**
- Custom MCP client (JSON-RPC 2.0 over stdio)
- MCP server process manager with lifecycle management
- Dynamic tool registration from MCP servers
- 6 pre-configured servers: Playwright, Apple apps, Google Calendar, Memory, Context7, Filesystem
- 70+ MCP tools available to the LLM
- Auto-screenshot after Playwright browser navigation

**Phase 5 — Safety & Polish**
- Per-tool permission system (whitelist/deny modes, glob paths, wildcard domains)
- Structured audit logging for all tool executions
- HTTP REST API (`/chat`, `/tool`, `/reset`, `/health`) with Bearer token auth
- `tool_permissions` and `api_token` config fields

**Infrastructure**
- GitHub OAuth device flow authentication
- Copilot token management with lock-free reads and singleflight refresh
- Dynamic model discovery (13+ models, hourly refresh)
- SQLite WAL-mode conversation store with auto-trim
- Streaming responses with adaptive debounce (1s → 2s → 3s)
- Per-chat concurrency control
- Message splitting for Telegram's 4096 char limit
- Pre-commit hook to block accidental credential commits
- Dual logging (stderr + file)

[Unreleased]: https://github.com/Ask149/aidaemon/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Ask149/aidaemon/releases/tag/v0.1.0
