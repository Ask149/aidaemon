# AIDaemon Architecture

## Overview

AIDaemon follows a layered architecture with clear separation of concerns:

```
┌─────────────────────────────────────────────┐
│         Telegram Bot (Frontend)             │
│  - Long polling                             │
│  - Edit-message streaming                   │
│  - Command handling                         │
└─────────────────┬───────────────────────────┘
                  │
┌─────────────────▼───────────────────────────┐
│           Core Daemon                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐ │
│  │ Provider │  │  Store   │  │  Config  │ │
│  │Interface │  │ (SQLite) │  │  Loader  │ │
│  └──────────┘  └──────────┘  └──────────┘ │
└─────────────────┬───────────────────────────┘
                  │
┌─────────────────▼───────────────────────────┐
│        Copilot Provider                     │
│  ┌──────────────┐  ┌──────────────┐        │
│  │ TokenManager │  │ Model        │        │
│  │ (Auth)       │  │ Discovery    │        │
│  └──────────────┘  └──────────────┘        │
└─────────────────┬───────────────────────────┘
                  │
┌─────────────────▼───────────────────────────┐
│      Copilot API (OpenAI-compatible)        │
│  - /chat/completions                        │
│  - /models                                  │
└─────────────────────────────────────────────┘
```

## Component Details

### 1. Authentication Layer (`internal/auth/`)

#### TokenManager (`copilot.go`)

**Responsibilities:**
- Manage GitHub OAuth token lifecycle
- Exchange GitHub token for Copilot bearer token
- Auto-refresh tokens before expiry
- Thread-safe token access via `atomic.Value`
- Deduplicate concurrent refresh via `singleflight.Group`

**Token Flow:**
```
GitHub OAuth token (long-lived, ~1 year)
         ↓
POST api.github.com/copilot_internal/v2/token
         ↓
Copilot bearer token (short-lived, 24h)
         ↓
Used for all /chat/completions calls
```

**Token Sources (priority order):**
1. `GITHUB_TOKEN` environment variable
2. `~/.config/aidaemon/auth.json` (saved by device flow)
3. `~/.local/share/opencode/auth.json` (OpenCode integration)
4. `~/.config/github-copilot/hosts.json` (VS Code extension)
5. `~/.config/github-copilot/apps.json` (older installations)

#### Device Flow (`device_flow.go`)

**Purpose:** Authenticate users without browser automation.

**Flow:**
1. Request device code from GitHub
2. Show URL + code to user
3. Poll for completion
4. Save token to `~/.config/aidaemon/auth.json`

**Implementation:** Uses GitHub's OAuth device flow (RFC 8628).

### 2. Provider Layer (`internal/provider/`)

#### Provider Interface (`provider.go`)

```go
type Provider interface {
    Chat(ctx, req) (*ChatResponse, error)     // Blocking call
    Stream(ctx, req) (<-chan StreamEvent, error) // Streaming
    Models() []ModelInfo                      // Available models
    Name() string                             // "copilot"
}
```

**Design rationale:** Abstraction allows future multi-provider support (OpenRouter, Anthropic direct, etc.).

#### Copilot Provider (`copilot/copilot.go`)

**Key features:**
- OpenAI-compatible API calls
- SSE (Server-Sent Events) streaming
- 401 retry with token refresh
- Dynamic model discovery via `/models` API
- 1-hour model cache with mutex-protected refresh

**Required headers:**
```go
"Editor-Version": "vscode/1.105.1"
"Editor-Plugin-Version": "copilot-chat/0.32.4"
"Copilot-Integration-Id": "vscode-chat"
"Openai-Intent": "conversation-panel"
```

**Model Discovery:**
- Calls `GET https://api.githubcopilot.com/models` with GitHub token
- Filters: `model_picker_enabled && type=="chat" && policy.state=="enabled"`
- Falls back to hardcoded list if API fails
- Refreshes every 1 hour

### 3. Storage Layer (`internal/store/`)

#### Store (`store.go`)

**Database:** SQLite with WAL (Write-Ahead Logging) mode.

**Schema:**
```sql
CREATE TABLE conversations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    TEXT    NOT NULL,
    role       TEXT    NOT NULL,  -- "user" | "assistant" | "system"
    content    TEXT    NOT NULL,
    created_at INTEGER NOT NULL   -- Unix timestamp
);
CREATE INDEX idx_conv_chat ON conversations(chat_id, created_at);
```

**Operations:**
- `GetHistory(chatID)` — Returns last N messages, oldest→newest
- `AddMessage(chatID, role, content)` — Appends and auto-trims
- `ClearChat(chatID)` — Deletes all messages for a chat
- `MessageCount(chatID)` — Returns current message count

**Auto-trim logic:** After each insert, deletes messages beyond the configured limit (default 20).

**Concurrency:** WAL mode allows concurrent reads during writes.

### 4. Telegram Layer (`internal/telegram/`)

#### Bot (`bot.go`)

**Library:** `github.com/go-telegram/bot` v1.18

**Polling strategy:** Long polling (works behind NAT, no public URL needed).

**Streaming pattern:**
1. Send placeholder: "🤔 Thinking..."
2. Stream LLM response via SSE
3. Accumulate text in buffer
4. Edit message periodically (adaptive debounce: 1s → 2s → 3s as message grows)
5. Final edit includes usage stats

**Adaptive debounce:**
```go
< 1000 chars: 1 second between edits
< 3000 chars: 2 seconds
≥ 3000 chars: 3 seconds
```

**Rationale:** Telegram rate-limits edits to ~1/sec. Larger messages need less frequent updates.

**Concurrency control:** Per-chat mutex (`sync.Map`) prevents overlapping LLM calls for the same chat.

**Security:** Only configured `telegram_user_id` can interact. Other users silently dropped.

### 5. Configuration Layer (`internal/config/`)

#### Config (`config.go`)

**Path:** `~/.config/aidaemon/config.json`

**Schema:**
```go
type Config struct {
    TelegramToken           string
    TelegramUserID          int64
    ChatModel               string  // default: "claude-opus-4.6"
    MaxConversationMessages int     // default: 20
    SystemPrompt            string
    Port                    int     // default: 8420 (not wired yet)
    DBPath                  string  // default: ~/.config/aidaemon/aidaemon.db
    LogLevel                string  // default: "info"
}
```

**Validation:**
- `telegram_token` and `telegram_user_id` are required
- `max_conversation_messages` minimum 2
- Falls back to defaults for optional fields

## Data Flow

### Chat Request (Streaming)

```
1. User sends Telegram message
   ↓
2. Bot validates user ID
   ↓
3. Store.AddMessage(chatID, "user", text)
   ↓
4. Store.GetHistory(chatID) → last 20 messages
   ↓
5. Build messages: [system] + history
   ↓
6. Provider.Stream(ctx, req)
   ↓
7. TokenManager.GetToken() (refresh if needed)
   ↓
8. POST api.githubcopilot.com/chat/completions (stream=true)
   ↓
9. Read SSE stream, send deltas to channel
   ↓
10. Bot accumulates + edits Telegram message periodically
    ↓
11. On stream complete, Store.AddMessage(chatID, "assistant", fullText)
    ↓
12. Final edit with usage stats
```

### Model Discovery

```
1. Provider.Models() called (startup or hourly)
   ↓
2. Check cache: fresh? → return cached
   ↓
3. TokenManager.FetchModels()
   ↓
4. GET api.githubcopilot.com/models (with GitHub token)
   ↓
5. Parse response, filter chat-capable models
   ↓
6. Map to ModelInfo, cache for 1 hour
   ↓
7. Return to caller
```

## Concurrency Design

### Token Manager

- **Read path:** Lock-free via `atomic.Value.Load()`
- **Refresh path:** `singleflight.Group` deduplicates concurrent refreshes
- **Result:** High read throughput, single refresh per expiry

### Telegram Bot

- **Per-chat mutex:** `sync.Map[int64]*sync.Mutex`
- **Prevents:** Race conditions from user sending multiple messages quickly
- **Trade-off:** Serializes requests per chat, but allows parallel chats

### SQLite Store

- **WAL mode:** Multiple readers + single writer concurrency
- **Trade-off:** Slight complexity increase, but critical for multi-chat support

## Error Handling

### Token Refresh (401 Retry)

```go
resp := sendRequest(ctx, token, body)
if resp.StatusCode == 401 {
    resp.Body.Close()
    token, err := tokenManager.ForceRefresh()  // Invalidate cache
    if err != nil { return err }
    resp = sendRequest(ctx, token, body)  // Retry once
}
```

**Rationale:** Token may expire mid-request. Single retry is sufficient.

### Stream Errors

Telegram bot sends errors inline in the message:
```
❌ Stream error: context deadline exceeded
```

User sees the error immediately rather than silent failure.

### Tool Call Errors (Future)

Will follow same pattern: inline error message in Telegram.

## Security Considerations

### Current

1. **Single-user authentication:** Only configured Telegram user ID can access
2. **Local token storage:** Tokens never leave the machine
3. **HTTPS only:** All API calls over TLS
4. **No logging of tokens:** Logs redact sensitive data

### Future (with tool use)

1. **File access permissions:** Whitelist paths, deny sensitive dirs
2. **Command allowlist:** Start with read-only commands
3. **Tool audit log:** Every tool call logged with timestamp, args, result
4. **Rate limiting:** Prevent runaway tool calls

## Performance Characteristics

### Latency

- **Token refresh:** ~300ms (cached for 24h, proactive refresh at 21h)
- **Model discovery:** ~500ms (cached for 1h)
- **Chat (streaming):** First token in 1-2s, full response 5-30s depending on length
- **SQLite reads:** <5ms (indexed queries)
- **SQLite writes:** <10ms (WAL mode)

### Throughput

- **Concurrent chats:** No limit (per-chat mutex prevents chat-level races)
- **Messages/second:** Limited by Telegram's edit rate (~1/sec) and LLM speed
- **Token refresh:** Deduplicated via singleflight

### Resource Usage

- **Memory:** ~50MB resident (mostly Go runtime + SQLite)
- **Disk:** DB size ~100KB per 1000 messages
- **CPU:** <1% when idle, 5-10% during streaming (parsing SSE)

## Design Decisions

### Why Telegram?

- **Accessible anywhere:** Phone, desktop, web
- **No custom UI needed:** Telegram handles all presentation
- **Push notifications:** Built-in
- **Works behind NAT:** No port forwarding needed
- **Multi-device:** Same conversation on all devices

### Why SQLite?

- **Zero-config:** Embedded, no server to manage
- **Reliable:** ACID transactions, crash-safe (WAL)
- **Fast:** Local filesystem access
- **Portable:** Single file, easy to backup

### Why Go?

- **Single binary:** No Node.js dependencies
- **Fast startup:** <100ms cold start
- **Concurrency:** Goroutines for streaming, mutex for safety
- **Cross-compile:** Easy macOS + Linux support
- **Type safety:** Catch errors at compile time

### Why Copilot Only?

- **Cost:** $10/mo vs $20/mo Anthropic + $20/mo OpenAI
- **Model variety:** 13+ models including latest GPT-5, Claude, Gemini
- **Already paid for:** If you use VS Code, you probably have Copilot
- **Simple auth:** Single OAuth flow, no API key juggling

## Future Architecture (with Tools)

```
┌─────────────────────────────────────────────┐
│         Telegram Bot (Frontend)             │
└─────────────────┬───────────────────────────┘
                  │
┌─────────────────▼───────────────────────────┐
│           Core Daemon                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐ │
│  │ Provider │  │  Tool    │  │  MCP     │ │
│  │Interface │→ │ Registry │→ │ Client   │ │
│  └──────────┘  └──────────┘  └──────────┘ │
└─────────────────┬───────────────────────────┘
                  │
    ┌─────────────┼─────────────┐
    │             │             │
┌───▼────┐  ┌────▼─────┐  ┌───▼────────┐
│ Built  │  │ Browser  │  │ MCP Servers│
│ Tools  │  │ (Rod)    │  │ (spawned)  │
└────────┘  └──────────┘  └────────────┘
    │             │             │
┌───▼─────────────▼─────────────▼─────┐
│  Local Machine (Files, Shell, Web)  │
└─────────────────────────────────────┘
```

**Key additions:**
- **Tool Registry:** Manages tool lifecycle and dispatch
- **MCP Client:** Communicates with external MCP servers via stdio
- **Browser Automation:** Rod (Puppeteer for Go)

## Monitoring & Observability

### Current Logging

```
[daemon] config loaded (model=claude-opus-4.6, conv_limit=20)
[daemon] copilot auth OK (expires in 24h0m0s)
[copilot] discovered 13 models from API
[daemon] provider: copilot (13 models)
[telegram] bot starting (user_id=8564687989, model=claude-opus-4.6)
```

### Future Metrics (Post-Tool Implementation)

- Tool execution time histogram
- Tool success/failure rates
- LLM token usage per chat
- Conversation length distribution
- Model usage breakdown

## Testing Strategy

### Current

- Manual testing via Telegram
- Model probing script (`cmd/probe-models/`)

### Needed (Phase 1+)

- Unit tests for tool registry
- Integration tests for tool execution
- Mock LLM responses for deterministic tests
- Load testing (concurrent chat simulation)

## Deployment

### Current (Development)

```bash
/opt/homebrew/bin/go run ./cmd/aidaemon/
```

### Recommended (Production)

```bash
# Build binary
/opt/homebrew/bin/go build -o aidaemon ./cmd/aidaemon/

# Run as launchd service (macOS)
cp aidaemon /usr/local/bin/
cp com.aidaemon.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.aidaemon.plist
```

### Future: Docker

```dockerfile
FROM golang:1.25-alpine
WORKDIR /app
COPY . .
RUN go build -o aidaemon ./cmd/aidaemon/
CMD ["./aidaemon"]
```

## References

- **OpenCode architecture:** `/Users/ashishkshirsagar/Projects/active/opencode`
- **Copilot API:** Reverse-engineered from OpenCode + Zed editor
- **MCP protocol:** https://modelcontextprotocol.io/
- **Telegram Bot API:** https://core.telegram.org/bots/api
