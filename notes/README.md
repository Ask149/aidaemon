# notes/

Short-form session learnings — workflow gotchas, corrected mental models, verified facts, agent-routing lessons. These are NOT design docs (those live in `docs/proposals/`). Each note captures a single mistake or discovery so future sessions don't repeat it. Filename convention: `YYYY-MM-DD-short-slug.md`. Pattern inside each note: title (one-line lesson), date, context, what happened, lesson, workflow correction.

## When to add a note

- A major routing or dispatch error was caught and corrected
- A tool or MCP server behaved unexpectedly (timeout, wrong output, silent failure)
- A "stale prior" was identified — something you believed that turned out wrong
- A verified fact that contradicts common assumptions
- A workflow shortcut worth memorizing

## Index

| Date | Slug | Lesson |
|------|------|--------|
| 2026-04-20 | `verify-before-dismissing-scout` | Verify scout findings via webfetch before dismissing as hallucinations; stale priors ≠ hallucination |
| 2026-04-20 | `scout-hallucination-pattern` | *(redirect stub)* Original conclusion was wrong — see corrected note above |

## Cross-references

- **Design docs** — `docs/proposals/`
- **Build / test / style guide** — `AGENTS.md` (project root)
