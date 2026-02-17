# Cron / Scheduled Tasks Design

**Goal:** Allow users to create recurring scheduled tasks via natural language in Telegram. The daemon fires them on schedule, executes them (either as an LLM conversation or a direct tool call), and delivers output back to the originating channel.

**Architecture:** New `internal/cron/` package with three components: a cron expression parser, a scheduler goroutine, and a job executor. Jobs are stored in SQLite alongside a run history table. The agent gets a `manage_cron` tool for CRUD. HTTP API endpoints expose the same operations for the web interface.

**Tech Stack:** Go stdlib only. Pure Go cron parser (5-field, ~200 lines).

---

## Data Model

### `cron_jobs` table

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT (ULID) | Primary key |
| `label` | TEXT | Natural language description |
| `cron_expr` | TEXT | Standard 5-field cron expression |
| `mode` | TEXT | `"message"` or `"tool"` |
| `payload` | TEXT | For message: the prompt. For tool: JSON `{"tool":"name","args":{...}}` |
| `channel_type` | TEXT | `"telegram"`, `"http"`, `"ws"` |
| `channel_meta` | TEXT | JSON, e.g. `{"chat_id":12345}` |
| `enabled` | INTEGER | 1 = active, 0 = paused |
| `last_run_at` | DATETIME | Last fire time (nullable) |
| `next_run_at` | DATETIME | Pre-computed next fire time |
| `created_at` | DATETIME | Creation timestamp |

### `cron_runs` table (execution history)

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT (ULID) | Primary key |
| `job_id` | TEXT | FK to `cron_jobs.id` |
| `started_at` | DATETIME | Execution start |
| `finished_at` | DATETIME | Execution end |
| `status` | TEXT | `"success"` or `"error"` |
| `output` | TEXT | Truncated response or error message |

Auto-prune to last 50 runs per job.

---

## Package Architecture: `internal/cron/`

### `cron.go` — Expression parser

- `Parse(expr string) (Schedule, error)` — validates and parses 5-field cron expressions.
- `Schedule.Next(from time.Time) time.Time` — computes next fire time.
- Supported format: `minute hour day-of-month month day-of-week`.
- Supports `*`, `,`, `-`, `/` operators.
- No seconds field, no `@daily` shortcuts, no per-job timezones (system TZ).

### `scheduler.go` — Tick loop

- `Scheduler` struct holds `Store`, `Engine`, channel registry.
- `Start(ctx context.Context)` — goroutine ticks every 60 seconds.
- Each tick: `SELECT * FROM cron_jobs WHERE enabled=1 AND next_run_at <= now()`.
- Due jobs fire in separate goroutines.
- After firing: update `last_run_at`, compute and set `next_run_at`.
- `Stop()` — context cancellation, wait for in-flight jobs.

### `executor.go` — Job execution

- **Message mode:** construct user message from `payload`, call `engine.Run()` with workspace system prompt. Creates a dedicated session per cron run.
- **Tool mode:** directly invoke the named tool with JSON args.
- Output routed to source channel via `CronSender` interface.

---

## Output Delivery

```go
type CronSender interface {
    SendCronOutput(ctx context.Context, meta json.RawMessage, text string) error
}
```

- **Telegram:** calls `bot.SendMessage(chatID, text)` — appears as a normal message.
- **HTTP/WS:** stores output in `cron_runs`, fetchable via API (pull model).
- Channel registry maps `channel_type` → `CronSender` implementation.

---

## Agent Tool: `manage_cron`

Single tool with an `action` field:

| Action | Parameters | Description |
|--------|-----------|-------------|
| `create` | `label`, `cron_expr`, `mode`, `payload` | Creates job. Channel info auto-captured from conversation context. |
| `list` | _(none)_ | Returns all jobs with status, next run, label. |
| `pause` | `id` | Sets `enabled=0`. |
| `resume` | `id` | Sets `enabled=1`. |
| `delete` | `id` | Removes the job. |

The agent translates natural language to cron expressions. Channel metadata is implicit from the current conversation context.

---

## HTTP API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/cron/jobs` | List all jobs |
| `POST` | `/cron/jobs` | Create a job |
| `PATCH` | `/cron/jobs/{id}` | Update (pause/resume/edit) |
| `DELETE` | `/cron/jobs/{id}` | Delete a job |

Auth follows existing HTTP API token pattern. Thin wrappers around store methods.

---

## Lifecycle in `main.go`

```go
scheduler := cron.NewScheduler(store, engine, channelRegistry)
go scheduler.Start(ctx)
defer scheduler.Stop()
```

Follows the same pattern as heartbeat and daily session rotation goroutines.

---

## Scope Boundaries

### In scope (v1)

- `internal/cron/` package (parser, scheduler, executor)
- `cron_jobs` + `cron_runs` SQLite tables
- `manage_cron` agent tool
- HTTP API CRUD endpoints
- Telegram `CronSender` implementation
- Dedicated session per cron run
- Graceful shutdown

### Not in scope (future)

- Discord/Slack/WhatsApp senders (interface ready, only Telegram impl)
- Web UI (API ready, frontend separate)
- Retry logic for failed jobs
- Job concurrency limits
- Per-job timezone
- `@daily`/`@hourly` shorthand aliases
