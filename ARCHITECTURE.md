# Architecture

Technical deep-dive into AIDaemon's design, data flows, and implementation decisions.

## System Overview

```
┌──────────────────────────────────────────────────────────┐
│                 Telegram Bot + WebSocket                 │
│  Long polling · Progress · Commands · Session sidebar   │
└──────────────────┬───────────────────────┬───────────────┘
                   │                       │
┌──────────────────▼──────────┐  ┌─────────▼─────────────┐
│        HTTP API             │  │   SQLite Store        │
│  /chat · /sessions · /tool  │  │  sessions table       │
└──────────────────┬──────────┘  │  WAL mode · Migrations│
                   │              └───────────────────────┘
┌──────────────────▼──────────────────────────────────────┐
│           Session Manager (551 lines)                    │
│  Session lifecycle · Token threshold · Auto-rotation     │
│  Title generation · Memory flush · Daily logs            │
│  ┌────────────────┐  ┌──────────────┐  ┌─────────────┐ │
│  │  HandleMessage │  │ RotateSession │  │ DailyRotate │ │
│  │  (orchestrate) │  │ (5-step flow) │  │ (4AM cron)  │ │
│  └────────┬───────┘  └──────┬───────┘  └─────────────┘ │
└───────────┼──────────────────┼──────────────────────────┘
            │                  │
┌───────────▼──────────────────▼──────────────────────────┐
│              Engine (747 lines)                          │
│  LLM↔tool loop · Token budget · Progress · Summarize    │
│  ┌────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │  Provider   │  │ Tool Registry │  │  Permissions   │  │
│  │ (Copilot)   │  │ + Audit Log   │  │  Checker       │  │
│  └──────┬─────┘  └───────┬──────┘  └────────────────┘  │
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

### `internal/session/`

Session lifecycle management with persistent IDs, titles, and automatic rotation.

**`manager.go` — Session Manager (551 lines)**
- Orchestrates session creation, lookup, rotation
- HandleMessage: routes messages through Engine with per-session history
- Token threshold checking (80% of limit triggers rotation)
- RotateSession: 5-step flow (flush → summarize → log → close → create)
- Auto-title generation: async call to gpt-4o-mini after first exchange
- Daily rotation goroutine: runs at 4AM, rotates all active sessions
- Helper methods: getOrCreateSession, estimateTokens, generateSessionID

**Session Flow:**
```
User message → HandleMessage
              ↓
       getOrCreateSession (SQLite lookup)
              ↓
       Build history + system prompt
              ↓
       Token check (80% threshold?)
       ├─ Yes → RotateSession (5 steps)
       └─ No → Continue
              ↓
       Engine.Run (LLM + tools)
              ↓
       Update session metadata
              ↓
       Generate title (async, first message only)
```

**Rotation Flow (5 steps):**
1. **Memory flush** — Silent Engine.Run to save context to MEMORY.md
2. **Summarization** — Call gpt-4o-mini for 2-3 paragraph summary
3. **Daily log** — Append to `workspace/memory/YYYY-MM-DD.md`
4. **Close session** — Update status to "closed", store summary
5. **Create new session** — New ID, carry forward summary as first message

**`sessions.go` (store layer)**
- SQLite `sessions` table: ID, channel, title, status, summary, token_estimate, timestamps
- CRUD methods: CreateSession, GetSession, UpdateSession, ListAllSessions, ActiveSession
- Migration function: MigrateExistingSessions (converts old chat_id values to session IDs)

### `internal/workspace/`

Workspace management with soul, user files, memory, tools, and daily logs.

**`workspace.go` — Workspace Loading (176 lines)**
- Load(): reads all workspace files from disk
- SystemPrompt(): assembles full system prompt with sections
- DailyLogs: loads last 3 days of `memory/YYYY-MM-DD.md` files
- Token budget: calculates total prompt size including daily logs
- Cropping: when over budget, crops soul to 50% of budget
- File structure:
  ```
  workspace/
  ├── SOUL.md          (main persona/instructions)
  ├── USER.md          (user context/preferences)
  ├── MEMORY.md        (persistent memories)
  ├── TOOLS.md         (tool-specific guidance)
  └── memory/
      ├── 2026-02-16.md (daily activity log)
      ├── 2026-02-15.md
      └── 2026-02-14.md
  ```

**loadDailyLogs():**
- Reads `memory/*.md` files matching YYYY-MM-DD.md format
- Filters to last N days (default: 3)
- String comparison for date filtering (ISO 8601 is lexicographically sortable)
- Returns []DailyLog with date and content

### `internal/wschannel/`

WebSocket channel implementation with command message support.

**`wschannel.go` — WebSocket Channel (241 lines)**
- Full-duplex WebSocket communication
- Command message type: `{"type": "command", "command": "new"}` or `"title"`
- Message message type: `{"type": "message", "text": "...", "image": "..."}`
- session_rotated event: `{"type": "session_rotated", "session_id": "...", "title": "..."}`
- Connection management: concurrent map of connections per channel
- Callbacks: OnMessage, OnNewSession, OnRenameSession

### `internal/httpapi/`

HTTP API with session management endpoints.

**`httpapi.go` — HTTP API (410 lines)**
- SessionManager interface (optional, graceful degradation)
- New endpoints:
  - `GET /sessions` — list all sessions (uses SessionManager if available)
  - `GET /sessions/{id}` — get session details
  - `GET /sessions/{id}/messages` — get message history
  - `POST /sessions/{id}/title` — rename session (JSON body: `{"title": "..."}`)
- Auth: requireAuth middleware checks Bearer token
- Error handling: 404 for not found, 400 for bad request, 500 for internal errors

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

**`bot.go`** (~1,407 lines) — Core bot logic:
- Long polling (works behind NAT)
- Edit-message streaming with adaptive debounce
- Tool execution loop (up to 999 iterations)
- Image support (Telegram photos → base64 → vision models)
- Context compaction (summarize old messages with cheap model)
- Per-chat mutex prevents overlapping LLM calls
- Auto-screenshot after Playwright navigation
- Message splitting for responses exceeding 4096 chars
- Progress updates via `engine.OnProgress` callback
- Rich stats footer (tokens, timing, tool calls, LLM calls, model)
- MESSAGE_TOO_LONG error handling with chunked sending
- Concurrent editText chunking for large responses

**`markdown.go`** — LLM markdown → Telegram HTML conversion.

**Adaptive debounce strategy:**
| Message length | Edit interval |
|---------------|---------------|
| < 1000 chars | 1 second |
| < 3000 chars | 2 seconds |
| ≥ 3000 chars | 3 seconds |

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
- Loads system prompt from file or inline (`loadSystemPrompt()`)
- Validates required fields (telegram_token, telegram_user_id)
- `HeartbeatDuration()` placeholder (returns 30m, currently unused)

## Data Flows

### Chat Request (Streaming)

```
 1. User sends Telegram message
 2. Bot validates user ID → reject if unauthorized
 3. Store.AddMessage(chatID, "user", text)
 4. Store.GetHistory(chatID) → last N messages
 5. Build messages: [system_prompt] + history + tool_definitions
 6. Engine.Run(ctx, messages, opts) with OnProgress callback
 7. Engine trims messages to fit ModelTokenLimit()
 8. Provider.Chat(ctx, request) → TokenManager.GetToken() → refresh if expired
 9. POST api.githubcopilot.com/chat/completions
10. On token-limit error: emergency summarize with gpt-4o-mini, retry (up to 3x)
11. Bot receives ProgressUpdate → edits Telegram placeholder message
12. If tool_calls in response:
    a. Execute each tool via Registry.Execute() (with timing)
    b. Truncate tool results >30K chars
    c. Append tool results to messages
    d. Go to step 7 (loop until finish_reason=stop)
13. Store.AddMessage(chatID, "assistant", fullText)
14. Final edit with rich stats footer (tokens, timing, tools, LLM calls, model)
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
