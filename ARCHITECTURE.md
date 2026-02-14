# Architecture

Technical deep-dive into AIDaemon's design, data flows, and implementation decisions.

## System Overview

```
┌──────────────────────────────────────────────────────────┐
│                    Telegram Bot                          │
│  Long polling · Edit-message streaming · Command routing │
└──────────────────┬────────────────────────┬──────────────┘
                   │                        │
┌──────────────────▼──────────┐  ┌──────────▼──────────────┐
│        HTTP API             │  │     SQLite Store         │
│  /chat · /tool · /health    │  │  WAL mode · Auto-trim    │
└──────────────────┬──────────┘  └─────────────────────────┘
                   │
┌──────────────────▼──────────────────────────────────────┐
│                   Core Daemon                           │
│  ┌────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │  Provider   │  │ Tool Registry │  │  Permissions   │  │
│  │ (Copilot)   │  │ + Audit Log   │  │  Checker       │  │
│  └──────┬─────┘  └───────┬──────┘  └────────────────┘  │
│         │                │                              │
└─────────┼────────────────┼──────────────────────────────┘
          │                │
          │         ┌──────┴──────────────────┐
          │         │                         │
   ┌──────▼──────┐  │  ┌──────────────────┐   │
   │ Copilot API │  │  │  Built-in Tools  │   │
   │ (13+ models)│  │  │  (5 tools)       │   │
   └─────────────┘  │  └──────────────────┘   │
                    │                         │
              ┌─────▼─────────────────────────▼──┐
              │          MCP Manager              │
              │  ┌────────┐ ┌────────┐ ┌───────┐ │
              │  │Playwrt │ │ Apple  │ │GCal   │ │
              │  │Browser │ │ Apps   │ │Events │ │
              │  └────────┘ └────────┘ └───────┘ │
              │  ┌────────┐ ┌────────┐ ┌───────┐ │
              │  │Memory  │ │Context7│ │Filesys│ │
              │  └────────┘ └────────┘ └───────┘ │
              └──────────────────────────────────┘
```

## Package Reference

### `internal/auth/`

Manages the two-tier authentication flow:

```
GitHub OAuth token (long-lived, stored in auth.json)
         ↓  POST api.github.com/copilot_internal/v2/token
Copilot bearer token (short-lived, ~24h, cached in memory)
         ↓  Used for all API calls
```

**`copilot.go` — TokenManager**
- Lock-free reads via `atomic.Value`
- Deduplicates concurrent refreshes via `singleflight.Group`
- Proactive refresh at 21h (before 24h expiry)
- Auto-retry on 401 responses

**`device_flow.go` — Device Flow**
- GitHub OAuth device authorization (RFC 8628)
- Saves token to `~/.config/aidaemon/auth.json`

**Token source priority:**
1. `GITHUB_TOKEN` environment variable
2. `~/.config/aidaemon/auth.json`
3. `~/.local/share/opencode/auth.json`
4. `~/.config/github-copilot/hosts.json`
5. `~/.config/github-copilot/apps.json`

### `internal/provider/`

Abstraction layer for LLM backends.

```go
type Provider interface {
    Chat(ctx, req) (*ChatResponse, error)           // Blocking
    Stream(ctx, req) (<-chan StreamEvent, error)     // Streaming via SSE
    Models() []ModelInfo                             // Available models
    Name() string                                    // Provider identifier
}
```

**`copilot/copilot.go`** implements this for GitHub Copilot's OpenAI-compatible API:
- SSE (Server-Sent Events) streaming
- 401 retry with automatic token refresh
- Dynamic model discovery via `/models` endpoint
- 1-hour model cache with mutex-protected refresh
- Required headers: `Editor-Version`, `Editor-Plugin-Version`, `Copilot-Integration-Id`

### `internal/tools/`

Tool framework with OpenAI function calling format.

**`tool.go`** — Core interface:
```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}  // JSON Schema
    Execute(ctx, args map[string]interface{}) (string, error)
}
```

**`registry.go`** — Central tool management:
- Register/lookup tools by name
- Execute with permission checking and audit logging
- Generate OpenAI-format tool definitions for LLM requests
- `ExecuteAll` for batch tool call processing

**`mcp_tool.go`** — Adapts MCP server tools to the `Tool` interface.

**`builtin/`** — 5 built-in tools:

| Tool | File | Safety |
|------|------|--------|
| `read_file` | `read_file.go` | Path whitelist (Documents, Projects, Desktop) |
| `write_file` | `write_file.go` | Path whitelist, creates parent dirs |
| `run_command` | `run_command.go` | Blocked commands list, 30s timeout |
| `web_fetch` | `web_fetch.go` | 10s timeout, HTML→text extraction |
| `web_search` | `web_search.go` | Brave API with DuckDuckGo fallback |

### `internal/permissions/`

Configurable per-tool access control.

**Modes:**
- `allow_all` (default) — no restrictions
- `whitelist` — only explicitly allowed values
- `deny` — everything allowed except denied values

**Check methods:**
- `CheckPath(tool, path)` — glob matching with `**` and `~` expansion
- `CheckCommand(tool, cmd)` — extracts base command, matches against rules
- `CheckDomain(tool, url)` — wildcard domain matching (`*.example.com`)

### `internal/mcp/`

MCP client implementing JSON-RPC 2.0 over stdio.

**`transport.go`** — Low-level JSON-RPC 2.0:
- Newline-delimited JSON over stdin/stdout
- 10 MB scanner buffer for large tool responses
- Request/response correlation by ID

**`client.go`** — High-level MCP protocol:
- `Initialize()` — protocol handshake
- `ListTools()` — discover available tools
- `CallTool(name, args)` — execute a tool

**`server.go`** — Process lifecycle:
- Launches MCP servers as subprocesses
- Environment variable injection
- Enable/disable per server

**`types.go`** — MCP protocol types (ToolInfo, CallResult, etc.)

### `internal/telegram/`

Telegram bot with streaming support.

**`bot.go`** (~1050 lines) — Core bot logic:
- Long polling (works behind NAT)
- Edit-message streaming with adaptive debounce
- Tool execution loop (up to 999 iterations)
- Image support (Telegram photos → base64 → vision models)
- Context compaction (summarize old messages with cheap model)
- Per-chat mutex prevents overlapping LLM calls
- Auto-screenshot after Playwright navigation
- Message splitting for responses exceeding 4096 chars

**`markdown.go`** — LLM markdown → Telegram HTML conversion.

**Adaptive debounce strategy:**
| Message length | Edit interval |
|---------------|---------------|
| < 1000 chars | 1 second |
| < 3000 chars | 2 seconds |
| ≥ 3000 chars | 3 seconds |

### `internal/httpapi/`

REST API for programmatic access.

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /health` | No | Health check + model info |
| `POST /chat` | Bearer | Send message, get LLM response with tool loop |
| `POST /tool` | Bearer | Execute a single tool directly |
| `POST /reset` | Bearer | Clear a chat session |
| `GET /sessions` | Bearer | List sessions |

- 30s read timeout, 120s write timeout
- Graceful shutdown on context cancellation

### `internal/store/`

SQLite conversation persistence.

**Schema:**
```sql
CREATE TABLE conversations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    TEXT    NOT NULL,
    role       TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_conv_chat ON conversations(chat_id, created_at);
```

- **WAL mode** — concurrent reads during writes
- **Auto-trim** — deletes oldest messages beyond configured limit
- **Compaction support** — `GetOldestN` + `ReplaceMessages` for summarization

### `internal/config/`

JSON configuration with defaults.

- Loads from `~/.config/aidaemon/config.json`
- Creates data directories on startup
- Loads system prompt from file or inline
- Validates required fields (telegram_token, telegram_user_id)

## Data Flows

### Chat Request (Streaming)

```
 1. User sends Telegram message
 2. Bot validates user ID → reject if unauthorized
 3. Store.AddMessage(chatID, "user", text)
 4. Store.GetHistory(chatID) → last N messages
 5. Build messages: [system_prompt] + history + tool_definitions
 6. Provider.Stream(ctx, request)
 7. TokenManager.GetToken() → refresh if expired
 8. POST api.githubcopilot.com/chat/completions (stream=true)
 9. Read SSE chunks → send deltas to channel
10. Bot accumulates text + edits Telegram message (adaptive debounce)
11. If tool_calls in response:
    a. Execute each tool via Registry.Execute()
    b. Append tool results to messages
    c. Go to step 6 (loop until finish_reason=stop)
12. Store.AddMessage(chatID, "assistant", fullText)
13. Final edit with usage stats
```

### Tool Execution

```
1. Registry.Execute(ctx, toolName, argsJSON)
2. Parse JSON arguments
3. Permission check: CheckPath / CheckCommand / CheckDomain
4. Audit log: CALL entry
5. Tool.Execute(ctx, args)
6. Audit log: OK / ERROR entry with duration
7. Return result string
```

### Context Compaction

```
1. Message count exceeds limit (default 20)
2. Get oldest 10 messages
3. Summarize with cheap model (gpt-4o-mini)
4. Replace 10 messages with single summary message
5. Conversation continues with compressed context
```

## Concurrency Model

| Component | Strategy | Rationale |
|-----------|----------|-----------|
| Token refresh | `singleflight.Group` | Deduplicates concurrent refreshes |
| Token reads | `atomic.Value` | Lock-free hot path |
| Per-chat handling | `sync.Map[chatID]*sync.Mutex` | Serializes per chat, parallel across chats |
| SQLite | WAL mode | Concurrent readers + single writer |
| Tool registry | `sync.RWMutex` | Read-heavy access pattern |
| Audit writes | Best-effort, non-blocking | Never delays tool execution |

## Performance

| Metric | Value |
|--------|-------|
| Cold start | < 500ms (excluding MCP server launch) |
| Token refresh | ~300ms (cached 24h) |
| Model discovery | ~500ms (cached 1h) |
| First token latency | 1–2s |
| SQLite read | < 5ms |
| SQLite write | < 10ms |
| Memory (idle) | ~50 MB |
| Memory (with MCP) | ~150 MB |

## Design Decisions

| Decision | Why |
|----------|-----|
| **Telegram** | Accessible from any device, built-in push notifications, no custom UI needed |
| **SQLite + WAL** | Zero-config, crash-safe, embedded, fast for single-user workloads |
| **Go** | Single binary, fast startup, goroutines for streaming, cross-compilation |
| **Copilot only** | $10/month for 13+ premium models, no API key juggling |
| **MCP over stdio** | Standard protocol, reuse existing server ecosystem, process isolation |
| **Edit-message streaming** | Telegram doesn't support true streaming; edit-in-place is the best UX |

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/go-telegram/bot` | Telegram Bot API client |
| `modernc.org/sqlite` | Pure-Go SQLite (no CGO) |
| `golang.org/x/net` | HTML tokenizer for web_fetch |
| `golang.org/x/sync` | `singleflight` for token dedup |
