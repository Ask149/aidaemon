# Webhook Trigger Design

**Goal:** Allow external services and scripts to trigger the daemon via HTTP POST. The daemon processes the request (LLM reasoning on the prompt + optional payload), persists the run, and delivers output to Telegram. Supports both async (fire-and-forget) and sync (wait-for-response) modes.

**Architecture:** New `POST /hooks/wake` endpoint in `internal/httpapi/`. Execution reuses the same engine pattern as cron (dedicated `engine.Engine` instance, `TelegramSender` for output delivery). Runs are persisted in a `webhook_runs` SQLite table. No new package — handlers live in `httpapi`, store operations in `internal/store/`.

**Tech Stack:** Go stdlib only. No new dependencies.

---

## API Contract

### `POST /hooks/wake`

```
POST /hooks/wake?sync=true   (optional query param)
Authorization: Bearer <api-token>
Content-Type: application/json
```

#### Request Body

```json
{
  "prompt": "Review this deployment alert",
  "payload": { "service": "api", "status": "degraded", "region": "us-east-1" },
  "source": "datadog"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `prompt` | string | yes | Instruction/question for the LLM |
| `payload` | any JSON | no | Structured event data — serialized and appended to prompt as context |
| `source` | string | no | Caller label (e.g., "github", "datadog"). Audit trail only in v1 |

#### Responses

**Async (default):**
```
202 Accepted
{ "run_id": "wh_abc123", "status": "running" }
```

**Sync (`?sync=true`):**
```
200 OK
{ "run_id": "wh_abc123", "status": "completed", "output": "..." }
```

**Errors:** 400 for missing prompt, 401 for bad token, 500 for engine failures.

### `GET /hooks/runs`

List recent webhook runs. Returns array of `WebhookRun` objects, newest first. Accepts `?limit=N` (default 20, max 100).

### `GET /hooks/runs/{id}`

Get a specific webhook run by ID.

---

## Data Model

### `webhook_runs` table

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT | Primary key (`wh_<random>`) |
| `prompt` | TEXT | The user-provided prompt |
| `payload` | TEXT | JSON string of event payload (nullable) |
| `source` | TEXT | Caller label (nullable) |
| `channel_type` | TEXT | `"telegram"` in v1 |
| `channel_meta` | TEXT | JSON, e.g. `{"chat_id":12345}` |
| `status` | TEXT | `"running"`, `"completed"`, `"failed"` |
| `output` | TEXT | LLM response or error message (nullable) |
| `started_at` | TEXT | ISO8601 timestamp |
| `finished_at` | TEXT | ISO8601 timestamp (nullable) |

Indexes: `started_at DESC`, `status`.

### Go Struct

```go
type WebhookRun struct {
    ID          string
    Prompt      string
    Payload     string     // JSON string or empty
    Source      string
    ChannelType string
    ChannelMeta string     // JSON string
    Status      string     // "running", "completed", "failed"
    Output      string
    StartedAt   time.Time
    FinishedAt  *time.Time
}
```

### Store Interface Additions

```go
CreateWebhookRun(ctx context.Context, run *WebhookRun) error
UpdateWebhookRun(ctx context.Context, id, status, output string, finishedAt time.Time) error
GetWebhookRun(ctx context.Context, id string) (*WebhookRun, error)
ListWebhookRuns(ctx context.Context, limit, offset int) ([]WebhookRun, error)
```

---

## Execution Flow

### Async (default)

```
POST /hooks/wake
    │
    ├─ Validate: auth, required fields (prompt)
    ├─ Generate run ID (wh_<random>)
    ├─ Build full prompt: prompt + payload context (if payload present)
    ├─ Insert webhook_run (status: "running", started_at: now)
    ├─ Return 202 { run_id, status: "running" }
    │
    └─ goroutine:
        ├─ Build system prompt via workspace.Load()
        ├─ Call engine.Run(ctx, messages, opts)
        ├─ Update webhook_run (status, output, finished_at)
        └─ Send output to Telegram via TelegramSender
```

### Sync (`?sync=true`)

Same as above but the handler blocks on execution (no goroutine). Returns the result in the HTTP response body. Server's 120s write timeout is the upper bound.

### Prompt Construction

When `payload` is present, the full prompt sent to the LLM is:

```
{prompt}

Event payload:
```json
{payload as formatted JSON}
```​
```

### Engine Instance

Reuse the same dedicated `engine.Engine{Provider, Registry}` instance wired in `main.go` for cron. No session management — webhook calls are stateless one-shots.

### Telegram Delivery

Reuse cron's `CronSender` interface (rename consideration for v2, but fine for now). Message includes a `🔔 Webhook` prefix for visual distinction. For async only — sync callers get the response in the HTTP body and no Telegram message.

---

## Files Changed

### New Files

| File | Purpose |
|------|---------|
| `internal/store/webhook_runs.go` | CRUD operations + migration |
| `internal/store/webhook_runs_test.go` | Store tests (~4 tests) |

### Modified Files

| File | Change |
|------|--------|
| `internal/store/store.go` | Add `WebhookRun` struct, 4 methods to `Conversation` interface, `migrateWebhookRuns()` call |
| `internal/httpapi/httpapi.go` | Add 3 routes + handlers, webhook execution logic (~100 lines) |
| `cmd/aidaemon/main.go` | Wire webhook dependencies into `httpapi.Config` (engine, sender, channel meta) |
| `internal/testutil/testutil.go` | Add 4 webhook method stubs to `MemoryStore` |

---

## Scope Boundaries

### In scope (v1)

- `POST /hooks/wake` — async + sync modes
- `GET /hooks/runs` — list recent runs
- `GET /hooks/runs/{id}` — get run details
- `webhook_runs` SQLite table + migration
- 4 store methods (create, update, get, list)
- Telegram output delivery for async calls
- Prompt + payload construction
- Bearer token auth (same as existing API)

### Not in scope (future — Approach B)

- Registered webhook definitions (`webhook_configs` table)
- Per-webhook prompt templates
- HMAC signature verification
- Callback URL delivery
- Source-specific payload parsers (GitHub, Stripe, etc.)
- `manage_webhook` agent tool
- Retry logic for failed runs
