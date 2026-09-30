# Repository Guidelines

## Project Overview

A server-rendered quiz/exam webapp (Indonesian school context: *guru* = teacher, *murid* = student). Single Go binary (`module quiz`, Go 1.26.6) using **Echo v5.3.1**, **MariaDB 12**, `html/template`, and **SSE** (not WebSockets) for realtime. Local environment == production (`docker compose up --build` → http://localhost:8090).

Authoritative documents (read before big changes; do not duplicate their content here):
- `docs/superpowers/specs/2026-09-26-quiz-website-design.md` — approved spec (schema §5, behavior §6, routes §7, error matrices §8, testing §9, boundaries §10). Code comments cite it as "spec §N".
- `.omp/plans/QUIZ_WEBSITE_IMPLEMENTATION_PLAN.md` — 16-step plan with critical-file anchors, verified API gotchas, locked decisions. Its Environment section is **stale** (says Go 1.26.4); trust the code/Dockerfile.
- `design_landingpage.md`, `design_dashboard.md` — **stale reference material** (foreign projects); superseded by the spec's palette rule.

There is **no README, Makefile, CI, or scripts/ directory** — plain `go` + Docker Compose only. Git: single `main` branch, conventional-commit prefixes (`docs:`, `feat:` …), commits are ask-first.

## Architecture & Data Flow

Boot is one function, no DI framework: `cmd/server/main.go` `run()`:

```
CONFIG_PATH(.env) → config.Load → db.Open → db.Migrate (embedded SQL, MySQL named lock)
→ handlers.NewRenderer("views", assets) → cache.New → handlers constructed
→ echo + Recover/StaticCache/LoadSession middleware → 4 static mounts (fingerprinted `?v=`) → ALL routes declared inline
→ globals.Rehydrate → go globals.WatchLoop (1s) → e.Start(":PORT")
```

Request flow: **handler → raw SQL (`*sql.DB`/`*sql.Tx`) → COMMIT → `Store.Delete(cache key)` → `Hub.Publish` to SSE topic** → render template or `ok(c, data)` JSON.

Read flow: **`cache.Store` read-through mirror → on miss, DB query → `store.Set` with TTL from `internal/cache/keys.go`**.

Key layers:
- `internal/handlers` — Echo handlers + renderer + JSON envelope (`ok`/`fail`/`failData` in `respond.go`).
- `internal/quizengine` — **pure logic, no DB/HTTP, never calls `time.Now()`** (times are params; `Clock` interface is the test seam): scoring, shuffle/order, timers, ranking, join codes, attempt rules, anti-cheat collapse, review matrix, password policy.
- `internal/realtime` — SSE hub over `tmaxmax/go-sse`; bounded `QueueSize=64` (`guardedWriter` cancels slow clients); 15s heartbeat carries `server_now`.
- `internal/cache` — in-process TTL mirror (`Store`, `sync.RWMutex`, `NewWithClock`/`Has` test seams); all key builders/TTLs centralized in `keys.go` (`SessionKey`, `QuizStateKey`, `QuizListKey`, `BankKey`, `HistoryKey`, `QuizSetKey`, `RefsKey`).
- `internal/db` — pool (MaxOpen 25/Idle 10/lifetime 5m), embedded migrations under `GET_LOCK('quiz.migrations',30)` with one tx per file, session CRUD.
- `internal/middleware` (import alias `mw`) — `LoadSession` (cookie → 5m mirror → DB) + role guards (`AuthTeacher`/`AuthStudent`/`ForceChangePassword`); `RevokeSession` deletes mirror **first**, then DB row; `StaticCache` client asset caching (fingerprinted `?v=` → immutable, SSR → `no-store`).
- `internal/handlers/live.go` — `Live`: rebuildable in-memory rankers + heartbeat registry backing disconnect freeze.

Two SSE topics per quiz: `quiz:<id>` (students) and `quiz:<id>:teacher` (monitor); every connection opens with a per-connection greeting snapshot (never fanned out).

Background work: one 1s `Global.WatchLoop` goroutine (timeout closes, disconnect sweeps, all-finished probes); `Rehydrate` runs once at boot before traffic. **DB is source of truth; `Live` and `cache.Store` are rebuildable/evictable mirrors.**

### Load-bearing invariants (restated in code — preserve them)

1. Mirrors may answer faster, never newer: invalidate **AFTER COMMIT, BEFORE broadcast**.
2. `RevokeSession`: delete the cache mirror entry **first**, then the DB row.
3. Score over the FULL question count — unanswered = wrong.
4. `participants.qorder` is written **once** at attempt start, never regenerated; answers store ORIGINAL option indexes.
5. Disconnect freeze pins `ends_at` to the **last heartbeat** instant, not detection time.
6. Every quiz end (STOP, timeout, all-finished, per-question close) funnels through `Global.closeQuiz`.
7. JSON envelope `{ok,data}` / `{ok,error,message}` and error-code literals (`VALIDATION`, `UNAUTHENTICATED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `QUIZ_ENDED`, `QUIZ_IN_PROGRESS`, `ATTEMPT_LIMIT`, `INVALID_CODE`, `SERVER_ERROR`) are a **contract shared by frontend JS and tests — never reword them**.
8. Schema has **no foreign keys** (integrity enforced in Go; `tests/db/db_test.go` pins this against spec §5). Teacher account lives in `.env`, not DB.

## Key Directories

| Path | Purpose |
|---|---|
| `cmd/server/` | Sole entry point; all wiring + route table inline |
| `internal/handlers/` | HTTP handlers (`auth`, `teacher_*`, `student`, `global`, `personal`, `streams`, `results`, `export`, `history`, `live`, `respond`, `renderer`, `flash`, `breadcrumb`) |
| `internal/quizengine/` | Pure domain logic (safe to unit-test in isolation) |
| `internal/realtime/` | SSE hub (`hub.go`, `room.go` topics) |
|`internal/cache/`, `internal/db/`, `internal/uploads/`, `internal/middleware/`, `internal/config/`, `internal/models/`, `internal/testutil/`|Mirror store, pool/migrations/sessions, chunked image-upload store (sessions + magic-byte sniff + sha256 verify + 24 h orphan sweep under `data/uploads/questions/<yyyy>/<mm>/<uuid>/`), guards, env config, schema structs, test helpers|
| `views/` | `html/template` pages via `{{define}}` blocks (`layout/`, `auth/`, `student/`, `teacher/`, `landing.html`, `join.html`, `about.html`); read from **disk at boot**, NOT embedded |
| `web/` | `embed.go` → static mounts: `/assets` = `web/vendor` (bootstrap-icons/, theme.css), `/css` = `web/css` (app.css), `/js` = `web/js` (`ui.js`, `teacher.js`, `workspace.js`, `monitor.js`, `anti-cheat.js`, `theme.js`); `/bootstrap` = root `bootstrap/` package (Bootstrap 5.3 css/js, offline, no CDN); templates reference them via `{{asset}}` → `?v=<hash12>` |
|`migrations/`|Numbered `NNNN_snake_name.sql` (0001–0005) + `embed.go` (`go:embed *.sql` — cannot move; `go:embed` forbids `..`)|
| `tests/unit/`, `tests/integration/`, `tests/cache/`, `tests/config/`, `tests/db/` | Test suites (packages `unit`, `integration`, `cachetest`, `configtest`, `dbtest`) |
| `docker/`, `docker-compose.yml`, `Dockerfile` | MariaDB init (`docker/mysql-init/001-testdb.sql` creates `quiz_test`), `app` + `db` services, multi-stage build |

## Development Commands

```sh
cp .env.example .env            # required once for local run (DB_HOST intentionally absent)
go build ./cmd/server           # build
go run ./cmd/server             # run locally (needs DB on 127.0.0.1:3306)
docker compose up -d db         # MariaDB only (integration tests need it; creates quiz_test on first boot of empty ./data/mariadb)
docker compose up --build       # full app on http://localhost:8090

go test ./... -count=1          # tests
go vet ./...                    # lint
govulncheck ./...               # vulnerability scan
```

Migrations apply automatically at boot — never run them by hand.

## Code Conventions & Common Patterns

- **Closed dependency list** — deps are spec-frozen (security floors: excelize ≥ v2.11.0, x/crypto ≥ v0.55.0). Direct deps: `echo/v5`, `go-sql-driver/mysql`, `cleanenv`. Adding any module requires asking first.
- **Error handling**: wrap with `fmt.Errorf("...: %w", err)`; handlers respond via `ok(c, data)` / `fail(c, status, CODE, msg)` — error-code constants in `internal/handlers/respond.go`.
- **DI**: constructor structs with exported fields (`Auth{DB,Store,Cfg}`, `Teacher{DB,Store}`, `Student{DB,Hub,Store,Live}`, `Streams{DB,Hub,Live}`), built manually in `main.go`. No interfaces except real seams (`Clock`, `qRow interface{QueryRowContext}`, `migrator`).
- **SQL**: hand-written queries, manual `Scan()`; `models` structs document the schema (`db:` tags are documentation only). MySQL duplicate key detected via `isDuplicateKey` (1062).
- **Concurrency**: `sync.Mutex`/`RWMutex` on shared registries (`Live`, `Ranker`, `Store`); atomics for flags; goroutines only for `WatchLoop` and SSE pumps.
- **Cache discipline**: every mutation → commit → `Store.Delete(<Key>...)` → publish. Use key builders/TTLs from `internal/cache/keys.go` — never inline strings.
- **Templates**: parsed once from disk (`renderer.go` injects `Role`/`Nav`/`Crumbs` into every payload; funcs `fmtScore/join/add/sub/dict`); SSR state via `data-*` attributes and inline `<script type="application/json">` (template.JS).
- **Frontend**: vanilla JS IIFEs (`"use strict"`), no framework/build step. `teacher.js` auto-submits `form[data-fetch]` / `button[data-post]` expecting the JSON envelope. Feedback: Bootstrap **Toast** for notifications, **Modal** for confirmations (`web/js/ui.js`: `quizToast`/`quizConfirm`/`quizStoreFlash`; SSR messages ride the `Flash` payload via `views/layout/feedback.html`); browser `alert()`/`confirm()` are banned. Layout CSS only in `web/css/app.css` (palette stays in `theme.css`). Exports: user-text cells via `SetCellStr` + `guardValue` formula-injection prefix; Rekapitulasi metrics and the Pertanyaan TOTAL row are computed cells via `SetCellFormula` (cross-sheet `Murid!`/`Pertanyaan!` refs), never user input.
- **Naming**: standard Go package conventions; user-facing strings/templates in Indonesian (`guru`, `murid`, `aktif`, `selesai`, riwayat).

## Important Files

- `cmd/server/main.go` — entry point, middleware chain, entire route table
- `internal/config/config.go` — cleanenv tags = full env surface; `CONFIG_PATH` overrides `.env`; precedence file → OS env → defaults — but the file is **optional**: containers carry no `.env` (`.dockerignore`), compose `env_file` injects the host's `.env.example` then `.env` at run time and `environment:` outranks both; `DB_HOST` deliberately env-default only so compose's `DB_HOST=db` wins; `DSN()` uses `parseTime=true&charset=utf8mb4&loc=UTC`
- `internal/handlers/respond.go` — JSON envelope + error-code contract
- `internal/handlers/global.go` — `closeQuiz` (single end-trigger), `WatchLoop`/`WatchOnce`, `Rehydrate`
- `internal/handlers/renderer.go` — template rendering + injected nav/crumb payload
- `internal/realtime/hub.go` — SSE hub; bounded-queue semantics (see plan's verified API notes)
- `internal/cache/keys.go` — all TTLs and cache keys
- `migrations/0001_init.sql` — canonical schema (no FKs); `0005_question_images.sql` adds the one-image-per-question table (logical `question_id`, UNIQUE 1:1; bytes served only via `GET /media/question/:imageId`)
- `.env.example` — env template; never commit `.env` or `data/` (the only two `.gitignore` entries)

## Runtime/Tooling Preferences

- **Go 1.26.6** only (Dockerfile: `golang:1.26.6-alpine` build → `alpine:3.24` runtime, `CGO_ENABLED=0`); no Node/npm — JS is vendored, unminified vanilla.
- Ports: app **8090**, MariaDB **3306**. Compose injects `DB_HOST=db` at OS level; `TEST_DB_NAME` defaults to `quiz_test`.
- Windows 10 + Git Bash (MSYS2) host shell.

## Testing & QA

- **Stdlib `testing` only** — spec §9 bans third-party assertion frameworks (no testify). Table-driven tests with `t.Run`, direct `t.Errorf`/`t.Fatalf`; **no `t.Parallel()`, no build tags** anywhere (verified by grep).
- `tests/unit/` (package `unit`): pure `quizengine` logic — fixed clocks, seeded RNG, exact constant pinning (e.g. `CollapseWindow == 10s`).
- `tests/integration/` (package `integration`): real Echo routes via `httptest.Server` against DB `quiz_test`; in-package fixtures (`authFixture`/`quizFixture`/`globalFixture`) mirror `cmd/server` wiring. Skip policy is centralized: `testutil.DB` pings 3s → `t.Skipf("start `docker compose up -d db`")` when unreachable.
- Per-package tests (moved out of `internal/`): `tests/cache` (package `cachetest`, fake clock via `NewWithClock`/`Has` seam), `tests/config` (package `configtest`, table-driven + `t.Setenv`/`t.TempDir`), `tests/db` (package `dbtest`, `TestMigrateTwiceIsIdempotent`, `TestSchemaMatchesSpecSection5`).
- Coverage expectations are qualitative branch tables in spec §9 — not `-cover` percentages.
- Run `go test ./... -count=1` and `go vet ./...`; both must pass before considering work done.
- Never delete or weaken a failing test (spec §10); commits are ask-first and must never stage `.env` or `data/`.
