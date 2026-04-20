# Proposal: Voice Input & Semantic Memory Search

| Field       | Value                                                        |
|-------------|--------------------------------------------------------------|
| **Status**  | **DEFERRED**                                                 |
| **Date**    | 2026-04-20                                                   |
| **Authors** | @founder, @architect, orchestrator                           |

**Rationale:** Inspired by omi.me comparison (audio capture + Pinecone vector memory); @founder verdict was no unmet need today; @architect verified a clean implementation path exists. Parked until evidence-based trigger criteria fire.

---

## Trigger Criteria (Build When)

### Voice Input — build when ANY of these are true

- Ashish documents 5+ instances in a 2-week window where he wanted to talk to aidaemon but couldn't easily type (walking, driving, cooking, etc.)
- OR aidaemon gains a non-Telegram channel where voice would be the primary input modality

### Vector Search — build when ANY of these are true

- A documented page of cases where SQLite FTS5 missed memories Ashish knew existed
- OR memory corpus exceeds ~10k entries (current FTS is still snappy below that threshold)
- OR Ashish is asking the same question multiple ways trying to surface something — a symptom of recall failure

---

## Implementation Sketch — Voice Input

**STT provider:** OpenAI Whisper API (~$0.006/min), pure HTTP, no CGO dependency.

**Pipeline:**

```
Telegram voice msg → bot.GetFile() → POST /v1/audio/transcriptions → inject transcribed text as userText → existing engine path
```

**Files to create:**

| File | Lines | Purpose |
|------|-------|---------|
| `internal/transcribe/whisper.go` | ~60 | `Transcribe(ctx, audioBytes) (string, error)` — HTTP client for Whisper API |

**Files to modify:**

| File | Change | Lines |
|------|--------|-------|
| `internal/telegram/bot.go` | Handle voice/audio messages in `handleMessage`, before the `Text == ""` bail-out | ~40 |
| `internal/config/config.go` | Add `OpenAIAPIKey` field | ~5 |

**Effort:** 4–6 hours.

**Risks:**
- Audio transits OpenAI servers (privacy consideration — see Open Questions).
- Telegram imposes a 20 MB file limit (sufficient for ≤10 min voice clips).
- OGG/Opus format natively supported by Whisper — no transcoding required.

---

## Implementation Sketch — Semantic Search

**Storage:** Pure-Go flat cosine search. NOT `sqlite-vec` — it requires CGO, which breaks the single-binary constraint (`modernc.org/sqlite` cannot load C extensions).

**Justification:** Corpus is under 10k entries. A flat scan of 5k × 1536-dim vectors completes in ~2 ms in Go. Good enough until it isn't.

**Embeddings:** OpenAI `text-embedding-3-small` (1536-dim, $0.02/1M tokens). Backfilling ~5k existing messages costs approximately $0.05.

**Schema addition** (in `store.go` `migrate()`):

```sql
CREATE TABLE IF NOT EXISTS embeddings (
    id          INTEGER PRIMARY KEY,
    source      TEXT NOT NULL,
    source_id   TEXT NOT NULL,
    chunk       TEXT NOT NULL,
    model       TEXT NOT NULL,
    vector      BLOB NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_embeddings_source ON embeddings(source, source_id);
```

**Files to create:**

| File | Lines | Purpose |
|------|-------|---------|
| `internal/embeddings/embeddings.go` | ~80 | `Embed(ctx, text) ([]float32, error)`, `CosineSimilarity`, pack/unpack `[]float32` ↔ `[]byte` |
| `internal/store/embeddings.go` | ~70 | `InsertEmbedding`, `SearchSimilar` |
| `internal/tools/builtin/search_memory_semantic.go` | ~60 | Tool implementation, mirrors `search_history.go` pattern |

**Files to modify:**

| File | Change |
|------|--------|
| `internal/store/store.go` | Add migration for `embeddings` table |
| `cmd/aidaemon/main.go` | Register semantic search tool in `setupTools()` |

**Indexing hook:** In `internal/heartbeat/` post-flush — embed new chunks asynchronously in a goroutine.

**Effort:** 10–15 hours.

**Future migration path:** When corpus exceeds 10k entries OR flat-scan latency becomes noticeable, swap `SearchSimilar` internals to `sqlite-vec` (accept CGO at that point). Schema and tool interface stay identical — no downstream changes.

---

## Sequencing (When Triggered)

Voice first (4–6h, standalone, instant UX win). Vector second (depends on the OpenAI key plumbing that voice already adds).

---

## Open Questions (Resolve Before Building)

1. **OpenAI key availability:** Is `OpenAIAPIKey` already in `~/.config/aidaemon/config.json`? Both features need it.
2. **Privacy posture:** OK with audio + memory text transiting OpenAI? If not → local `whisper.cpp` + Ollama embeddings (introduces CGO or sidecar trade-offs).
3. **Embedding scope:** Conversation messages only, or also daily logs (`YYYY-MM-DD.md`) and `MEMORY.md`?
4. **Backfill strategy:** Embed all existing history on first deploy, or incremental-only going forward?
5. **Confirmation UX:** Show transcribed text back to the user before the LLM processes it?

---

## Decision Log

| Field | Detail |
|-------|--------|
| **Date** | 2026-04-20 |
| **Inspired by** | omi.me feature comparison (omi = always-on audio capture + Pinecone vector memory) |
| **@founder verdict** | SKIP voice (Telegram already handles voice notes natively; zero documented instances of unmet need). DEFER vector (FTS5 works fine under 10k entries; no evidence of search misses). |
| **@architect verdict** | Clean implementation path exists. Both features are a weekend of work total. No architectural risk — the engine path, tool registry, and store migration patterns already support this cleanly. |
| **Orchestrator decision** | Parked. Ashish has no actively-owned IODevz product right now (MailRambo is Piyush's, Subtidy is Param's). Primary focus Apr–Jun 2026 is wedding prep (Jun 27). IODevz strategic direction (likely MCP Gateway in Go) is being re-thought for July+. aidaemon stays a personal tool; voice/vector features deferred until after wedding AND until trigger criteria above fire (e.g., 10K+ FTS entries, or a concrete podcast/voice-note workflow emerges). |
| **Market context** | Personal-AI-daemon market is fully saturated as of Apr 2026 — verified competitors include OpenClaw (361K ⭐), Hermes Agent (104K ⭐), nanobot (40.2K ⭐), ZeroClaw (30.4K ⭐ Rust single-binary, directly owns aidaemon's niche), Khoj (~34K ⭐), and Anthropic's Claude Code Channels. See `notes/2026-04-20-verify-before-dismissing-scout.md` for the full verified list. New entrants need a 10x reason; aidaemon doesn't have one. |

---

## Lightweight Observability (Build Instead)

Cheap data-collection step that informs the trigger decision without building either feature:

- Log every `search_history` tool call with: **query text**, **result count**, **whether the result was used in the final LLM answer**.
- After 2–3 weeks, review the log:
  - Are queries returning empty when they shouldn't? → Build vector search.
  - Are results irrelevant even when returned? → Build vector search.
  - Neither? → Keep deferring.
