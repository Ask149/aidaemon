# Changelog

All notable changes to AIDaemon are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [2.1.0] — 2026-02-17

### Added
- **Scheduled tasks (cron)** — create recurring jobs via natural language in Telegram
  - Pure Go 5-field cron expression parser (supports `*`, ranges, lists, steps)
  - Two execution modes: `message` (LLM reasoning) and `tool` (direct tool call)
  - Scheduler goroutine checks for due jobs every 60 seconds
  - Output delivered back to the source channel (Telegram)
  - Full CRUD: create, list, pause, resume, delete via agent conversation
  - `manage_cron` built-in tool for agent-driven job management
  - HTTP API endpoints: `GET/POST /cron/jobs`, `PATCH/DELETE /cron/jobs/{id}`
  - SQLite persistence (`cron_jobs` + `cron_runs` tables) — survives daemon restarts
  - Run history with automatic pruning (keeps last 50 runs per job)
- **Skill files** — drop `.md` files into `~/.config/aidaemon/skills/` for custom agent instructions
  - All `*.md` files automatically loaded into system prompt as `## Active Skills` section
  - Sorted alphabetically, rendered with `### filename` subheaders
  - Changes take effect immediately (no restart required)
  - Counts toward token budget; empty files silently skipped
  - Agent sees skills (read-only awareness) but cannot modify them

## [2.0.0] — 2026-02-16

Major release with session lifecycle management and Windows support.

### Added
- **Session lifecycle management** — persistent session IDs with auto-generated titles
- `/new` command (Telegram + WebSocket) — start new session, archives current conversation
- `/title <text>` command (Telegram + WebSocket) — rename current session
- Daily 4AM rotation — automatically rotates all active sessions
- Memory flush before rotation — saves context to `workspace/MEMORY.md`
- Daily memory logs — last 3 days of `workspace/memory/YYYY-MM-DD.md` loaded into system prompt
- Session manager — orchestrates session lifecycle, token threshold checking (80%), rotation flow
- HTTP API session endpoints — GET/POST for sessions, messages, titles
- Web UI session sidebar — browse sessions, click to switch, view history
- WebSocket command messages — `/new` and `/title` as command type
- `session_rotated` WebSocket event — notifies clients when session rotates
- Token budget includes daily logs — workspace token calculation now accounts for memory logs
- Race-safe test utilities — MemoryStore now uses mutex protection
- **Windows support** — full cross-platform compatibility (Windows, macOS, Linux)
- Windows service setup documentation — Task Scheduler instructions

### Changed
- Conversations are now session-based instead of infinite per-channel
- WebSocket OnMessage routed through SessionManager.HandleMessage
- Telegram messages routed through SessionManager
- HTTP API enhanced with SessionManager integration
- Token limit now configurable via `token_limit` field in config (default: 128000)

### Fixed
- Off-by-one error in daily log cutoff (was loading 4 days instead of 3)
- Race conditions in title generation goroutine
- Date comparison bug in daily rotation (now uses full date, not day-of-month)
- Context leak in async title generation (now propagates parent context)

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

[Unreleased]: https://github.com/Ask149/aidaemon/compare/v2.1.0...HEAD
[2.1.0]: https://github.com/Ask149/aidaemon/compare/v2.0.1...v2.1.0
[2.0.0]: https://github.com/Ask149/aidaemon/compare/v0.1.0...v2.0.0
[0.1.0]: https://github.com/Ask149/aidaemon/releases/tag/v0.1.0
