# AIDaemon Refactoring Tracker

> Temporary document tracking the modularity refactoring. Delete when all phases are complete.

## Overview

Applying SOLID/OOP principles to improve extensibility, reusability, and testability — without changing existing functionality.

---

## Phase 0 — Foundation: Test Infrastructure *(Complete)*

| # | Task | Status | Tests Added | Files Changed |
|---|------|--------|-------------|---------------|
| 0.1 | Create REFACTOR.md tracking doc | ✅ | — | `REFACTOR.md` |
| 0.2 | Update Makefile test/cover targets | ✅ | — | `Makefile` |
| 0.3 | Create testutil helpers (TempStore, MockProvider, DummyTool, MemoryStore) | ✅ | — | `internal/testutil/testutil.go` |
| 0.4 | Add permissions_test.go (CheckPath, CheckCommand, CheckDomain) | ✅ | ~20 cases | `internal/permissions/permissions_test.go` |
| 0.5 | Add markdown_test.go (FormatHTML edge cases) | ✅ | ~14 cases | `internal/telegram/markdown_test.go` |
| 0.6 | Verify `make check` passes | ✅ | — | — |

**Manual verification:** ✅ `make check` passes, `make cover` generates coverage report.

---

## Phase 1 — Extract Store Interface *(Complete)*

| # | Task | Status | Tests Added | Files Changed |
|---|------|--------|-------------|---------------|
| 1.1 | Define `Conversation` interface, rename struct → `SQLiteStore` | ✅ | — | `internal/store/store.go` |
| 1.2 | Add type alias `Store = SQLiteStore` for zero-breakage | ✅ | — | `internal/store/store.go` |
| 1.3 | Add store_test.go (CRUD, trim, compaction, round-trips) | ✅ | 13 cases | `internal/store/store_test.go` |
| 1.4 | Verify build + manual test | ✅ | — | — |

**Manual verification:** ✅ `make check` passes, all 13 store tests green.

---

## Phase 2 — Extract ChatEngine *(Complete)*

| # | Task | Status | Tests Added | Files Changed |
|---|------|--------|-------------|---------------|
| 2.1 | Create `internal/engine/engine.go` with tool-call loop | ✅ | — | `internal/engine/engine.go` |
| 2.2 | Define `ToolExecutor` interface + `DefaultExecutor` | ✅ | — | `internal/engine/engine.go` |
| 2.3 | Refactor `telegram.Bot.handleMessage` to delegate to engine | ✅ | — | `internal/telegram/bot.go` |
| 2.4 | Refactor `telegram.Bot.handlePhotoMessage` to delegate to engine | ✅ | — | `internal/telegram/bot.go` |
| 2.5 | Define `telegramToolExecutor` (MCP screenshots, Playwright) | ✅ | — | `internal/telegram/bot.go` |
| 2.6 | Remove unused `toolDefinitions()` + `executeTools()` methods | ✅ | — | `internal/telegram/bot.go` |
| 2.7 | Refactor `httpapi.API` `/chat` to delegate to engine | ✅ | — | `internal/httpapi/httpapi.go` |
| 2.8 | Add engine_test.go (text, tools, multi-iter, max-iter, custom executor) | ✅ | 10 cases | `internal/engine/engine_test.go` |
| 2.9 | Add registry_test.go (Register, Get, List, Execute, permissions, audit) | ✅ | 14 cases | `internal/tools/registry_test.go` |
| 2.10 | Verify build + all tests pass | ✅ | — | — |

**Manual verification:** `make check` passes. All 5 test suites green (permissions, telegram, store, engine, tools).

---

## Phase 3 — DRY Built-in Tools + Permission Interface

| # | Task | Status | Tests Added | Files Changed |
|---|------|--------|-------------|---------------|
| 3.1 | Extract `pathutil.go` (ExpandHome, IsAllowedPath, IsBinary) | ⬜ | — | `internal/tools/builtin/pathutil.go` |
| 3.2 | DRY read_file.go + write_file.go using pathutil | ⬜ | — | `read_file.go`, `write_file.go` |
| 3.3 | Extract `PermissionChecker` interface in registry | ⬜ | — | `internal/tools/registry.go` |
| 3.4 | Add pathutil_test.go + builtin tools_test.go | ⬜ | ~12 cases | `pathutil_test.go`, `tools_test.go` |
| 3.5 | Verify build + manual test | ⬜ | — | — |

**Manual verification:** `make check`, bot file read/write operations.

---

## Phase 4 — Move Misplaced Concerns + Final Cleanup

| # | Task | Status | Tests Added | Files Changed |
|---|------|--------|-------------|---------------|
| 4.1 | Move `FetchModels` from auth → provider/copilot | ⬜ | — | `auth/copilot.go`, `provider/copilot/copilot.go` |
| 4.2 | Extract `setupTools` from main.go → `internal/app/setup.go` | ⬜ | — | `cmd/aidaemon/main.go`, `internal/app/setup.go` |
| 4.3 | Remove Store alias, use interface everywhere | ⬜ | — | `telegram/bot.go`, `httpapi/httpapi.go`, `engine/engine.go` |
| 4.4 | Add auth_test.go + httpapi_test.go | ⬜ | ~8 cases | `auth_test.go`, `httpapi_test.go` |
| 4.5 | Delete REFACTOR.md, update CHANGELOG.md (v0.2.0) | ⬜ | — | `REFACTOR.md`, `CHANGELOG.md` |

**Manual verification:** Full E2E: build → install → run → Telegram + HTTP API → `make cover` ≥ 40%.

---

## Test Coverage Targets

| Phase | Cumulative Coverage | Packages Tested |
|-------|-------------------|-----------------|
| 0 | ~10% | permissions, telegram (markdown) |
| 1 | ~20% | + store |
| 2 | ~35% | + engine, tools/registry |
| 3 | ~45% | + tools/builtin |
| 4 | ~50%+ | + auth, httpapi |
