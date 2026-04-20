# Verify before dismissing scout — stale priors ≠ hallucination

**Date:** Apr 20, 2026
**Context:** Researching personal-AI-daemon competitive landscape for aidaemon strategy

## Corrected lesson (TL;DR)

**Scout was right. I was wrong.** My plausibility filter — not scout — was the failure mode. Don't dismiss specific data from scout based on heuristics from older mental models. The 2025–2026 AI/agent boom has produced star counts that would've been impossible 2 years ago.

## What happened

1. @scout reported 4 personal-AI-daemon competitors with high star counts (OpenClaw 361K, Hermes Agent 104K, nanobot 40.2K, ZeroClaw 30.4K).
2. I (orchestrator) dismissed these as hallucinations — "361K would be top-5 GitHub ever; 30K in 2 months would be major news."
3. Re-dispatched scout with "verify these are real." Scout returned same numbers with URLs.
4. I STILL didn't believe scout. Wrote this note recording the wrong lesson: "scout hallucinates."
5. Then I personally browsed GitHub via webfetch — **all 4 projects exist with exactly the star counts scout reported.** Scout was 100% correct.

## Why my plausibility check failed

My mental model of "reasonable GitHub star counts" was calibrated to pre-2025 norms. The AI/agent boom of 2025–2026 has compressed timelines dramatically — ZeroClaw getting 30K stars in 2 months is unusual but real in this era.

## Workflow correction

```
scout reports surprising data
  → orchestrator raises skepticism (reasonable)
  → webfetch the URL (takes 30 seconds, authoritative)
  → believe or disbelieve based on EVIDENCE, not priors
```

**Rule:** The resolution to "these numbers seem too high" is **verify**, not **assume hallucination**.

## Verified-real competitors (Apr 2026)

| Project | Repo | Stars | Stack | Notes |
|---------|------|-------|-------|-------|
| OpenClaw | `github.com/openclaw/openclaw` | 361K | TypeScript | Peter Steinberger, 1,688 contributors, 98 releases |
| Hermes Agent | `github.com/NousResearch/hermes-agent` | 104K | Python | Nous Research, has `hermes claw migrate` tool |
| nanobot | `github.com/HKUDS/nanobot` | 40.2K | Python | HKU DS lab |
| ZeroClaw | `github.com/zeroclaw-labs/zeroclaw` | 30.4K | Rust | <5MB RAM, Harvard/MIT/Sundai, 143 releases since Feb 2026 |
| Khoj | `github.com/khoj-ai/khoj` | ~34K | Python/TS | Mature, MCP support |
| Claude Code | Anthropic (closed-source) | — | — | Channels + Routines + Web UI, Nov 2025–Apr 2026 |

## Meta-lesson

Scout is reliable. When it reports specific numbers with URLs, verify before dismissing — don't assume the tool is broken just because the data surprises you.

## Related

- `docs/proposals/voice-and-vector.md` — voice/vector deferral; market-saturation context references this note
- `AGENTS.md` — repo build/test/style guide
