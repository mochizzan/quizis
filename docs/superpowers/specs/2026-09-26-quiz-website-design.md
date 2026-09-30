# Spec: Quizizz-Style Quiz Website (Golang + Echo + SSR + MariaDB + Bootstrap 5.3)

**Date:** 2026-09-26
**Status:** Approved (design reviewed section-by-section)
**Scope:** Single-capability product (no capability map needed — one coherent application)

---

## 1. Objective

Build a Quizizz-like quiz web application for schools with two roles:

- **Teacher (Guru)** — single admin account from `.env`. Creates quizzes and a question bank, runs live sessions, monitors students in real time, detects cheating, grades essays, reviews results.
- **Student (Murid)** — self-registration (name, class/level, major). Joins quizzes via code / invite URL / QR code, attempts quizzes, views history.

**Success** = all criteria in §11 pass with `go test ./... -race -count=1` green and `docker compose up --build` running an identical local/production environment.

**UI language:** full **bahasa Indonesia baku (KBBI)** — URL paths, code identifiers, schema literals, and third-party/tool names stay in their original form.
**UI style:** minimalist, custom **blue palette** (no default Bootstrap colors), light/dark mode, modern corner radius, responsive; hand-written CSS limited to the layout chrome in `web/css/app.css`; body font **Noto Sans Cypro Minoan** 400 via Google Fonts — the sanctioned CDN exception (§2); Latin text falls back to `sans-serif`.

---

## 2. Tech Stack (verified against local Docker)

| Component | Version | Source |
|---|---|---|
| Go | 1.26.6 | local + `golang:1.26.6-alpine` image (1.26.4 had 8 stdlib vulns per govulncheck) |
| Echo | **v5.3.1** (requires Go ≥1.25) | latest stable; v4 is security-fix-only until 2026-12-31 |
| MariaDB | **`mariadb:12`** (12.3 LTS) | already present locally; 13.0.2 exists but not local |
| Bootstrap | **5.3.8** | offline dist at repo-root `bootstrap/` (`bootstrap/embed.go`, `//go:embed css js`) served at `/bootstrap` — **no CDN** |
| **Static caching** | SHA-256[:12] `?v=` fingerprint | single `staticMounts` list (`cmd/server/main.go`) feeds mounts + boot-time fingerprint registry + `StaticCache` middleware (`internal/middleware/assets.go`) — header matrix §6.13 |
| **Google Fonts** | Noto Sans Cypro Minoan 400 | preconnect ×2 + `fonts.googleapis.com/css2?family=Noto+Sans+Cypro+Minoan&display=swap` in all three head partials — the **sanctioned CDN exception** (the only external request; Bootstrap/app assets stay offline); family covers the Linear A syllabary, Latin falls back to `sans-serif` |
| Realtime (client) | native `EventSource` | no WebSocket library |
| **SSE server** | **`github.com/tmaxmax/go-sse`** (latest, active) | broker `Publish` per topic = per-quiz room (two topics per quiz: `quiz:<id>` + `quiz:<id>:teacher`); hub built on `Upgrade` + `Joe` with a slow-client guard (§6.1), server-only. Rejected: `r3labs/sse` (unmaintained since Jan 2023), `joshuafuller/sse/v3` (new fork, 0 importers — too risky) |
| **.env loader** | **`github.com/ilyakaznacheev/cleanenv`** v0.5.0+ | parse `.env` + struct-tag mapping + **fail-fast required-field validation at boot**; for any key **present** in `.env`, the file value wins (cleanenv `parseENV` writes it into OS env unconditionally). Keys **absent** from `.env` — specifically `DB_HOST` — resolve from OS env or `env-default`. **The file is optional:** the image never contains `.env` (`.dockerignore`), so containers resolve purely from OS env injected at run time by compose `env_file` (host `.env.example` first, then `.env` — later wins; the `environment:` section outranks both). Each host's own `.env` is applied at run time and never baked into the pushed image; a clean machine without a `.env` boots on the injected `.env.example` (§11.19). Rejected: `godotenv` (OS-set only, needs hand-written mapping), Viper (heavyweight) |
| **CSV export** | **`github.com/gocarina/gocsv`** (latest, active Sep 2026) | struct → CSV via `csv:"..."` tags |
| **XLSX export** | **`github.com/xuri/excelize/v2` ≥ v2.11.0 (MANDATORY)** | ⚠️ CVE-2026-59162 / GO-2026-6452 fixed in **v2.11.0**; **≤v2.10.1 is vulnerable** (panic on negative shared-string index). Pure Go, streaming writer for large sheets |
| QR | `skip2/go-qrcode` (latest) | generates join-URL PNG |
| Password hash | `golang.org/x/crypto/bcrypt` **≥ v0.55.0** | v0.55.0 (Aug 2026) includes CVE-2026-39833 fix |
| DB driver | `github.com/go-sql-driver/mysql` **v1.10.0** (Aug 2026) | pure Go, no cgo |
| Docker | 29.4.3 / Compose v5.1.3 | multi-stage: `golang:1.26.6-alpine` → `alpine:3.24` |

**Closed dependency list** — exactly 8 **direct** modules (cleanenv additionally pulls `godotenv`, `toml`, `yaml.v3`, `edn` transitively — not counted):
`labstack/echo/v5` · `go-sql-driver/mysql` · `golang.org/x/crypto` · `skip2/go-qrcode` · `tmaxmax/go-sse` · `ilyakaznacheev/cleanenv` · `gocarina/gocsv` · `xuri/excelize/v2`

**Dependency list is closed** — adding any dependency requires asking first (§10). Minimum versions above are security floors: `go.mod` must pin ≥ those versions and `govulncheck ./...` must report no known vulnerabilities before release.

---

## 3. Commands

```bash
# Build
go build ./cmd/server

# Test (unit + integration, with race detector)
go test ./... -race -count=1

# Lint
go vet ./...

# Vulnerability scan (required before release; enforces §2 security floors)
go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...

# Dev / run (local == production)
docker compose up --build          # → http://localhost:8090

# Migrations run automatically at boot (embedded, numbered .sql)
```

---

## 4. Project Structure

```
quiz/
├── cmd/server/main.go          # entrypoint: config → db → migrate → routes → listen
├── bootstrap/                  # bootstrap/embed.go: //go:embed css js → /bootstrap
│                               #   offline Bootstrap 5.3.8 css+js, no CDN
├── internal/
│   ├── config/                 # cleanenv: .env → struct (GURU_USER, GURU_PASS, DB_USER, DB_PASS, DB_NAME, DB_PORT, PORT); DB_HOST intentionally absent (local default 127.0.0.1, Docker sets db); fail-fast at boot
│   ├── db/                     # MariaDB connection + embedded numbered migrations
│   ├── models/                 # structs (User, Quiz, Question, Participant, Answer, ...)
│   ├── handlers/               # flat files — auth.go, password_resets.go, teacher_*.go, student.go, respond.go: SSR render + JSON endpoints
│   ├── middleware/             # authTeacher, authStudent, forceChangePW, session
│   ├── realtime/               # SSE hub on tmaxmax/go-sse broker: two topics per quiz
│   │                           #   (student + teacher), snapshot-on-reconnect, slow-client drop
│   ├── cache/                  # in-memory read-through mirror (per-entity TTL, write-through invalidation)
│   ├── uploads/                # chunked image upload store: session dirs under data/uploads,
│   │                           #   magic-byte sniff, sha256 verify, 24h orphan sweep (linked-safe)
│   └── quizengine/             # scoring, timers (clock-injectable), shuffle, ranking, anti-cheat
├── views/                      # html/template: layout/, teacher/, student/, auth/,
│                               #   landing.html, join.html, about.html
├── web/
│   ├── css/                    # app.css: landing page + sidebar dashboard shell layout
│   │                           #   (Bootstrap tokens/palette still come from theme.css)
│   ├── js/                     # native JS only: ui.js (Toast/Modal/flash), teacher.js,
│   │                           #   workspace.js, monitor.js, anti-cheat.js, theme.js
│   └── vendor/                 # served at /assets (bootstrap-icons/, theme.css)
│       ├── bootstrap-icons/    # Bootstrap Icons (offline)
│       └── theme.css           # blue palette via --bs-* overrides ONLY
├── migrations/                 # 0001_*.sql … 0005_*.sql (numbered, embedded)
├── tests/
│   ├── unit/                   # pure logic, no DB/HTTP, fake clock, seeded RNG
│   └── integration/            # HTTP handlers + MariaDB (test DB: quiz_test)
├── data/mariadb/               # bind mount for MariaDB data (git-ignored)
├── docker-compose.yml          # app + mariadb:12 (./data/mariadb:/var/lib/mysql)
├── Dockerfile                  # multi-stage build
├── .env                        # secrets (git-ignored)
├── .env.example                # committed template
└── .gitignore                  # data/, .env
```

---

## 5. Database Schema (MariaDB 12, InnoDB, utf8mb4)

Flat schema: **no foreign keys, no strict normalization, no deduplication** — integrity enforced in Go. Source of truth for ALL quiz execution state; in-memory structures are rebuildable mirrors only.

```sql
-- References (teacher-managed dropdowns; cache TTL 60s)
ref_kelas    id SMALLINT UNSIGNED AUTO_INCREMENT PK, nama VARCHAR(50) NOT NULL
ref_jurusan  id SMALLINT UNSIGNED AUTO_INCREMENT PK, nama VARCHAR(50) NOT NULL

-- Students
users        id INT UNSIGNED AUTO_INCREMENT PK,
             username VARCHAR(50) NOT NULL UNIQUE,
             email VARCHAR(100) NOT NULL UNIQUE,
             password_hash VARCHAR(255) NOT NULL,
             nama_lengkap VARCHAR(100) NOT NULL,      -- display name (ID UI: "Nama lengkap")
             kelas_id SMALLINT UNSIGNED NOT NULL,
             jurusan_id SMALLINT UNSIGNED NOT NULL,
             must_change_pw TINYINT(1) NOT NULL DEFAULT 0,
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP

-- Sessions (source of truth; in-memory mirror TTL 5 min)
sessions     id CHAR(43) PK,                 -- crypto/rand base64url, HttpOnly cookie
             user_id INT UNSIGNED NOT NULL,
             role ENUM('guru','murid') NOT NULL,
             expires_at DATETIME NOT NULL,
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             INDEX idx_sessions_user (user_id),
             INDEX idx_sessions_exp (expires_at)

-- Quiz
quizzes      id INT UNSIGNED AUTO_INCREMENT PK,
             code CHAR(6) NOT NULL UNIQUE,
             judul VARCHAR(150) NOT NULL,
             deskripsi TEXT NULL,
             timer_type ENUM('global','per_soal','tanpa_timer') NOT NULL,
             timer_on TINYINT(1) NOT NULL DEFAULT 1,         -- 0 = no time limit
             status ENUM('nonaktif','aktif','berjalan','selesai') NOT NULL DEFAULT 'nonaktif',
             join_mode ENUM('open','approve') NOT NULL DEFAULT 'open',
             total_seconds INT UNSIGNED NOT NULL DEFAULT 0,          -- global timeout = started_at + total
             per_question_seconds SMALLINT UNSIGNED NOT NULL DEFAULT 300,
             started_at DATETIME NULL,
             shuffle_options TINYINT(1) NOT NULL DEFAULT 0,
             shuffle_questions TINYINT(1) NOT NULL DEFAULT 0,
             show_correct_wrong TINYINT(1) NOT NULL DEFAULT 1,
             show_final_score TINYINT(1) NOT NULL DEFAULT 1,
             ranking_live TINYINT(1) NOT NULL DEFAULT 0,
             max_attempts TINYINT UNSIGNED NOT NULL DEFAULT 1,      -- global forced to 1 in code
             question_review ENUM('none','text','full') NOT NULL DEFAULT 'none',
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             INDEX idx_quiz_status (status)

-- Question bank (separate from quizzes → length filter + reuse)
questions    id INT UNSIGNED AUTO_INCREMENT PK,
             teks TEXT NOT NULL,
             type ENUM('pg','multi','essay') NOT NULL,
             options JSON NULL,               -- ["option text", ...]; NULL for essay
             correct JSON NULL,               -- pg: 1 (original option index); multi: [0,2]; essay: free-text key (never auto-graded)
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             INDEX idx_q_type (type)

quiz_questions id INT UNSIGNED AUTO_INCREMENT PK,
             quiz_id INT UNSIGNED NOT NULL,
             question_id INT UNSIGNED NOT NULL,
             seq SMALLINT UNSIGNED NOT NULL,                -- manual order
             UNIQUE KEY uq_qq (quiz_id, question_id),
             INDEX idx_qq_order (quiz_id, seq)

-- One optional image per bank question. Separate table so bank list queries
-- stay light (no blob/column in questions); questions without a row stay
-- fully valid. The file lives on disk under
-- data/uploads/questions/<yyyy>/<mm>/<uuid>/original.<ext> (never embedded,
-- never served from a source mount) — bytes are only exposed through
-- GET /media/question/:imageId. question_id is a LOGICAL reference: no
-- FOREIGN KEY (zero-FK rule above); UNIQUE enforces 1:1 and is the lookup
-- index; attach/verify/delete-both-rows integrity lives in Go.
question_images id INT UNSIGNED AUTO_INCREMENT PK,
             question_id INT UNSIGNED NOT NULL,        -- logical ref → questions.id (FK in Go)
             filename VARCHAR(255) NOT NULL,           -- stored name: original.<ext>
             path VARCHAR(500) NOT NULL,               -- relative to the uploads root (questions/<yyyy>/<mm>/<uuid>/…)
             byte_size INT UNSIGNED NOT NULL,          -- bytes on disk
             mime_type VARCHAR(100) NOT NULL,          -- magic-sniffed: image/jpeg|png|gif|webp
             sha256 CHAR(64) NOT NULL,                 -- lowercase hex digest; doubles as the media ETag
             active TINYINT(1) NOT NULL DEFAULT 1,     -- 0 = hidden, file kept; never indexed
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             UNIQUE KEY uq_qi_question (question_id)   -- one image per question (1:1)

-- Participant per attempt (write-first → commit → broadcast)
participants id INT UNSIGNED AUTO_INCREMENT PK,
             quiz_id INT UNSIGNED NOT NULL,
             user_id INT UNSIGNED NOT NULL,
             attempt_no TINYINT UNSIGNED NOT NULL DEFAULT 1,
             status ENUM('pending','registered','started','selesai','dikeluarkan')
                        NOT NULL DEFAULT 'registered',
             qorder JSON NULL,               -- snapshot: question order + option order per student
             started_at DATETIME NULL,
             ends_at DATETIME NULL,          -- absolute per-participant clock (pause = freeze)
             remaining_seconds INT UNSIGNED NOT NULL DEFAULT 0,
             finished_at DATETIME NULL,
             score_auto DECIMAL(5,2) NULL,   -- set at finish/timeout/stop/removal
             essay_score DECIMAL(5,2) NULL,  -- NULL = awaiting teacher grading
             final_score DECIMAL(5,2) NULL,  -- = score_auto; + essay after grading
             cheating TINYINT(1) NOT NULL DEFAULT 0,        -- teacher toggle
             current_q SMALLINT UNSIGNED NULL,              -- live monitor
             current_q_since DATETIME NULL,                 -- dwell time
             UNIQUE KEY uq_part (quiz_id, user_id, attempt_no),
             INDEX idx_part_monitor (quiz_id, status),
             INDEX idx_part_user (user_id)

-- Answers (write-through per submit; idempotent upsert)
answers      id INT UNSIGNED AUTO_INCREMENT PK,
             participant_id INT UNSIGNED NOT NULL,
             question_id INT UNSIGNED NOT NULL,
             answer TEXT NULL,                -- pg/multi: JSON array of ORIGINAL option indexes e.g. [0,2]; essay: free text
             is_correct TINYINT(1) NULL,      -- NULL = essay / not yet graded
             score DECIMAL(5,2) NULL,         -- per-question (essay)
             answered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             UNIQUE KEY uq_ans (participant_id, question_id),
             INDEX idx_ans_part (participant_id)

-- Anti-cheat log (always write DB → then push SSE)
anti_cheat_events id INT UNSIGNED AUTO_INCREMENT PK,
             participant_id INT UNSIGNED NOT NULL,
             kind ENUM('blur','minimize','switch','sleep') NOT NULL,
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             INDEX idx_ac_part (participant_id)

-- Student password reset (teacher-approved)
password_resets id INT UNSIGNED AUTO_INCREMENT PK,
             user_id INT UNSIGNED NOT NULL,
             input_username VARCHAR(50) NOT NULL,
             input_nama VARCHAR(100) NOT NULL,
             status ENUM('pending','disetujui','ditolak','selesai') NOT NULL DEFAULT 'pending',
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             INDEX idx_pr_user (user_id, status)
```

> **Migration bookkeeping:** table `schema_migrations(name VARCHAR(191) PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)` is created by the migrator; it is infrastructure, not domain data.

### Type-selection rules applied
- `ENUM` for statuses (1–2 bytes); adding a value = one `ALTER` (tables are small).
- `TINYINT(1)` for booleans, **never indexed** (low cardinality).
- `SMALLINT`/`TINYINT` for reference IDs and counters; `INT` for main entities.
- `DECIMAL(5,2)` for scores (exact ranking, ~4 bytes) — no floats.
- `CHAR(6)` for join code (fixed-length index), `CHAR(43)` for session ID.
- `TEXT` only for free text (question, essay, description); `VARCHAR` for bounded strings.
- `DATETIME DEFAULT CURRENT_TIMESTAMP` (no TIMESTAMP — no 2038/TZ ambiguity).
- Indexes only for real queries: login (username/email unique), join (`quizzes.code`), upsert (`uq_ans`), live monitor (`idx_part_monitor`), rehydrate (`idx_quiz_status`).

---

## 6. Architecture & Key Decisions

### 6.1 Realtime: SSE + POST JSON (approved approach A)
- Server→client: SSE (`EventSource`, native auto-reconnect) — live monitor, ranking, anti-cheat alerts, waiting-room updates, start/stop broadcasts, correct-answer previews, force-stop.
- Client→server: POST JSON for every non-SSR communication (answers, start, visibility events, actions).
- **Server side runs on `tmaxmax/go-sse`**: two topics per quiz — `quiz:<id>` (students) and `quiz:<id>:teacher` (monitor) — role filtering at subscribe time; each connection is single-topic (EventSource cannot send the `Subscribe` header), topic resolved from the URL path; hub built on `Upgrade` + `Joe` with a guarded queue writer because `sse.Server.Publish` blocks on slow clients; no dashboard/history topics (those pages are plain SSR). The `realtime/` package owns what the library doesn't: snapshot-on-reconnect, heartbeat, and the slow-client drop policy (§8).
- **Wire shape (amendment):** every frame's `data` field carries the full `{"type":…,"data":…}` envelope (pinned by tests) — client handlers must unwrap `d.data` before reading payload fields; parsing the raw `ev.data` as the payload makes the monitor's greeting snapshot normalize to an empty card list and wipe the cards right after each page load.
- **Stream-lifecycle presence (amendment):** opening or closing a student stream (refresh, leave-and-return) republishes that participant's `page` event — name, status, page, clocks, `connected` — to the teacher topic (`Streams.announcePresence`), because workspace renders only announce *changed* pages; a reconnect that keeps the same page must still show the murid leave and come back. A submitted attempt is never re-announced (its `finished` event already removed the card).
- No WebSocket library.

### 6.2 Persistence + mirror cache (approved)
**Write order (inviolable):** `BEGIN → write DB → COMMIT → invalidate/update mirror → broadcast SSE`. If the DB write fails, **nothing is broadcast**.

- DB is the **only source of truth** for quiz execution state (status, `ends_at`, `qorder`, answers, cheating, pending approvals).
- In-memory is allowed **only** for: (1) SSE connections (transport, not state), (2) session cache, (3) derivative caches (ranking, monitor snapshots) rebuildable from DB.
- **Read-through cache** with short TTLs — cache never answers newer than DB:

| Mirror | TTL | Invalidation |
|---|---|---|
| Running-quiz state | 2–3 s | write-through on every mutation |
| Sessions | 5 min | explicit delete on logout / password change (instant revocation) |
| Quiz lists, bank, history | 10 s | CRUD invalidates immediately |
| Quiz settings (while nonaktif) | 30 s | edit invalidates |
| ref_kelas / ref_jurusan | 60 s | CRUD invalidates |
| App config (.env) | process lifetime | redeploy |

**Golden rule:** cache may answer *faster*, never *newer* than DB, never something DB doesn't have.

### 6.3 Rehydrate after container restart
1. Clients auto-reconnect (`EventSource` built-in retry).
2. On boot: `SELECT quizzes WHERE status='berjalan'` → rebuild hub from participants/answers → warm cache.
3. Each client receives a **snapshot** (current question, time left = `ends_at - now`, ranking from answers, cheat badges, pending approvals) → session continues, timers do not reset.
4. Participants whose `ends_at` already passed while down → auto-finished with stored answers.
5. Sessions survive restart (DB-backed).

### 6.4 Sessions: DB + in-memory mirror
- Cookie `session_id` (43-char crypto/rand, `HttpOnly`, `SameSite=Lax`, `Path=/`, Max-Age 7 days).
- Read path: in-memory map → miss → DB → cache 5 min.
- Mutations (login/logout/password change): write DB first, then **explicitly delete cache entry** → revocation is instant; TTL is only a safety net.
- Logout-all on password change: `DELETE FROM sessions WHERE user_id=?`.
- `must_change_pw=1` → middleware redirects to `/change-password` from every page (except logout).
- No localStorage for credentials (XSS risk).

### 6.5 Quiz state machine

```
GLOBAL TIMER (waiting room + synchronized)
nonaktif ──activate──▶ aktif ──START──▶ berjalan ──stop/timeout/confirm──▶ selesai
(editable)          (waiting room,     (countdown, linear mode)          (results,
                     join open/approve)                                  essay grading,
                                                                         export)

PER-QUESTION TIMER (no waiting room, per-student clock)
nonaktif ──activate──▶ aktif ──close (modal if N working)──▶ selesai
(editable)            (registered ≠ started;               (results)
                       each student starts independently,
                       review/free-navigation mode)
```

**Edit guard (stricter than stated spec):** edit allowed **only** when `status='nonaktif'` **AND zero attempts exist** — a quiz that was ever attempted is permanently locked against editing (protects result integrity under no-normalization).

### 6.6 Timer model — per-participant absolute clock
- **Global:** at START every participant gets `ends_at = start + total_seconds` (if `timer_on=0`: no countdown/timeout; ends via STOP or "all finished" confirmation).
  - End triggers (race-safe): **timeout auto-finishes** (no confirmation — wall-clock decides); **manual STOP** and **"all students finished"** both require a teacher confirmation modal. All three funnel through `UPDATE ... SET status='selesai' WHERE status='berjalan'` — first commit wins, others no-op.
- **Per-question:** timer starts when student clicks Start: total = `per_question_seconds × question_count`. `timer_on=0` → no countdown.
- **No timer (`tanpa_timer`):** the per-question flow (student starts own attempt, free navigation, teacher closes) with no countdown at all — `timer_on` derives to 0, `ends_at` stays NULL, timer seconds are stored as 0.
- **Disconnect pause:** freeze `ends_at` at the **last heartbeat second** (not detection time), store `remaining_seconds`; on reconnect `ends_at = now + remaining_seconds`. Applies to both timer types (fairness per participant; others unaffected).
- Downtime during restart counts against wall-clock `ends_at` (acceptable for local deployment).

### 6.7 Answer flow → preview → ranking
1. Student submits → `POST /answer` → idempotent upsert (`uq_ans`) → compute `is_correct` → **SSE push to teacher monitor** (current question, dwell time, selected option in real time).
2. **Correct/wrong preview** (`show_correct_wrong`):
   - **Linear mode (global):** `/answer` response includes preview → shown 2–3 s → auto-next. OFF → no preview, immediate advance.
   - **Review mode (per-question):** `/answer` never leaks correctness; preview appears on `POST /next` (Next press) — there is no Finish button anymore. OFF → no preview.
   - Final results page always shows preview if setting ON.
2b. **Navigation & pre-submit review:** exactly two pager buttons per question (`Previous`, `Next`). **Essay answers autosave** on every character change (`input` → debounced save; the Save button is gone) through one serialized save chain per card. `Next` on the final question — and the first moment every question is answered — opens the **preview page**: one box per question (green = answered, red = unanswered) with `Back to questions` + `Submit quiz`; manual submit now happens only there. The workspace reports the preview open/close via `POST /quiz/:code/page {page: "question"|"preview"}` → SSE `page` event, so the live monitor shows the murid on **Preview** in every timer mode. `page` events fire on start/navigation too (name + current page live); a submitted attempt **leaves the monitor cards**.
3. **Ranking live** (`ranking_live=ON`): computed **in-memory** from progress points (correct relative to total), pushed to student leaderboard **and** teacher monitor on every answer. Zero DB queries per event; rebuildable from `answers` on demand.
4. Finish (submit/timeout/STOP/removal/close): `score_auto = correct/total × 100`, **unanswered = wrong**. If quiz has essays → `final_score` stays NULL until teacher grades, then `score_auto + essay_score`.
5. **Per-question time ledger (mirror):** every free-navigation move banks the elapsed seconds of the question being left (`Live.AddSpent`, in-memory, restart-safe-by-rebuild = display only, never a schema change) — the monitor shows `Q1 0:12 · Q2 0:45 · Q3 …` (running) per participant in `per_soal` mode.

### 6.8 Shuffle / order
- `qorder` snapshot (per participant, written at start): question order + option order.
- Option shuffle is **per-student** (option "B" may differ between students → answers store **original option indexes** from the pre-shuffle options array, letters rendered server-side from snapshot).
- If sequential question order chosen → `shuffle_questions=0` forced **server-side** (not just UI).
- **Manual order:** the teacher reorders the composed list (`POST /teacher/quiz/:id/questions/reorder`) — the payload must be EXACTLY the composed set (empty / duplicate / mismatched set → 400 VALIDATION, unknown quiz → 404) and `seq` is renumbered 1..N in one transaction, only while `nonaktif` (same guard as remove → otherwise 409 locked). `loadQuestions` reads `ORDER BY seq`, so the teacher's order is what every FUTURE attempt snapshots; an attempt's existing `qorder` is never rewritten, and the shuffle overrides the teacher order only when `shuffle_questions=1`. The manage page exposes it as per-row up/down buttons (disabled at the first/last row, hidden while the table is filtered by `?qq=` — a slice cannot post a complete order), applied optimistically with rollback on failure.
- `max_attempts`: global timer always forced to 1. Per-question honors setting; ranking uses MAX(`final_score`); all attempts shown in student history and teacher data.

### 6.9 Anti-cheat
- `visibilitychange`/`blur`/gap > 30 s → `POST /visibility {kind}` → insert `anti_cheat_events` → **SSE push to that student's card in teacher monitor**.
- **Both buttons appear only after a violation exists OR the student is flagged (`participants.cheating`):** `Cheat` toggle (idempotent, sets `participants.cheating`) and `Remove` (always behind a confirmation modal). A detected/flagged card turns yellow (`bg-warning-subtle`) with its badge (`flagged` when flagged, `N violation(s)` when only detected).
- Student keeps working until teacher acts.
- **Event flood control:** duplicate kind within 10 s collapses to one row; events after quiz end are ignored.

### 6.10 Removal semantics
- Removed **in waiting room** (never started): status `dikeluarkan`, `final_score=NULL`, excluded from ranking, shown in history as removed.
- Removed **while working**: snapshot `score_auto` first (unanswered = wrong) → status `dikeluarkan` → counted in results, history, **and ranking** with the removed flag.

### 6.11 Results & history
- **Teacher results — two views sharing one tab bar (separate routes):**
  - **`/results` — students:** participant list (name, final score, **correct count** `x/y`, status: finished/removed/cheating, awaiting-grading) where every row expands into an answer panel with one line per question: the student's answer beside the answer key plus the state (correct / wrong / unanswered / graded / awaiting grading). Letters are ORIGINAL option indexes, so answer and key always line up regardless of option shuffle. **Export in both CSV and XLSX** via `?format=csv|xlsx` (default `csv`): gocsv for CSV, excelize ≥v2.11.0 for XLSX (streaming writer). Both **injection-guarded**: values starting `=`/`+`/`-`/`@` get `'` prefix (CSV) / written as literal text cells, never formulas (XLSX); empty results → header-only file.
  - **`/results/analysis` — per-question analysis:** % correct/wrong/unanswered and the most-chosen option, on its own route so that (per-question + most-chosen) query is only computed when this view is opened.
- **Essay grading:** teacher opens student's essay answers → inputs scores → `final_score` finalized. Before grading: "awaiting grading".
- **Student history:** all attempts listed; respects per-quiz settings: show/hide score, show/hide ranking, question review `none` / `text` (question + own answers + right/wrong marks, no key) / `full` (with answer key).
- Ranking across attempts uses highest score.

### 6.12 Join rules
- Open to all students regardless of class/major (class/major = profile labeling only).
- Join modes: **Open** (immediate registration/waiting room) or **Approve** (request appears on teacher dashboard → approve/reject).
- After START pressed (global): new joiners rejected with exact message **"Kuis sedang berlangsung, Anda tidak dapat bergabung."**
- Pending approvals are settled at START: any row still `pending` when START fires becomes `dikeluarkan` (`final_score=NULL`) in the same transaction; approve attempts after START return `409`.
- Registration ≠ start for per-question quizzes; student starts anytime while quiz is active and they are registered.
- Teacher can manually add/remove students from a quiz.

### 6.13 Client-side static-asset caching

One `staticMounts` list in `cmd/server/main.go` is the single source for the four mounts (`/assets`, `/css`, `/js`, `/bootstrap`): the mount calls, the fingerprint registry and the cache middleware all derive from it, so the prefix sets cannot drift. At boot `middleware.Fingerprints` walks each mounted FS once and maps final URL path → first 12 hex chars of the file's SHA-256.

Templates build static URLs through the `asset` func (`{{asset "/css/app.css"}}` → `/css/app.css?v=<hash12>`); unknown paths pass through unchanged. Every static `href`/`src` in `views/**` uses it — `web/js` contains no hardcoded mount URLs (if one ever appears, the fallback rows below still apply).

`StaticCache` middleware (chain: Recover → StaticCache → LoadSession) sets exactly one policy per request:

| Request | Cache-Control | ETag |
|---|---|---|
| registered asset with matching `?v=` | `public, max-age=31536000, immutable` | — |
| registered asset, `?v=` missing or wrong (covers CSS-internal relative font URLs) | `no-cache` | `"<hash12>"` — `If-None-Match` matching (RFC 9110 list / `*` / `W/`) short-circuits a body-less `304` |
| path under a mount but not registered | `no-cache` | none (the static handler 404s) |
| everything else — SSR HTML / JSON / SSE / QR | `no-store` | — |
| question image `/media/question/:imageId` | `no-cache` (handler overwrites the `no-store` pre-set — `Set`, never `Add`) | `"<sha256>"` full digest of the stored file — `If-None-Match` → body-less `304`; response also carries `X-Content-Type-Options: nosniff` + `Content-Disposition: inline` |

---

## 7. Routing

**Path language:** all URL paths are **English** (UI text = bahasa Indonesia baku KBBI). Schema column/ENUM literals stay as approved in §5 — they are internal identifiers, never user-facing.

**Config note:** `DB_HOST` is not an application config key — it is supplied only by Docker compose `environment:` and by the `env-default` fallback.

**Method contract:** plain HTML form posts (auth forms, CRUD forms) are SSR navigations — `application/x-www-form-urlencoded` + redirect. Anything sent by JavaScript (`fetch`) is **JSON only**.

### Auth
```
GET  /login               login page (teacher + student in one form)
POST /login               username OR email + password (form post → redirect)
POST /logout
GET  /register            student registration (name, username, email, password,
                          class dropdown, major dropdown)
POST /register            validate → 302 /login?registered=1, NO session until
                          manual login (success flash; pending_join survives)
GET  /forgot-password     username/email + full name
POST /forgot-password     validate match → create reset request
GET  /change-password     accessible only when must_change_pw=1
POST /change-password     new + confirm → clear flag, revoke old sessions
```

### Teacher (`/teacher/*`, authTeacher middleware)
```
GET  /teacher                     dashboard (quiz list + reset-request badge)
GET  /teacher/api/overview        JSON analytics overview for dashboard cards/charts/table — filters (?kelas/?jurusan/?status, 400 VALIDATION on bad values), summary, per-kelas, per-jurusan, quizzes, rekap_nilai; no-store
GET|POST /teacher/classes, /teacher/majors CRUD dropdowns
GET  /teacher/classes/new       add form (own page, posts to /teacher/classes)
GET  /teacher/majors/new        add form (own page, posts to /teacher/majors)
GET  /teacher/students/:id/edit edit form (own page, prefilled, posts to :id/edit)

GET  /teacher/questions           question bank + length filter (short/medium/long)
GET  /teacher/questions/new       add form (own page, posts to /teacher/questions)
GET  /teacher/questions/:id/edit  edit form (own page, prefilled, posts to :id/edit)
POST /teacher/questions           add (pg/multi/essay); optional multipart field `image` = the path
                                    returned by /teacher/uploads/:id/complete (empty/absent = no image),
                                    re-verified server-side (regex + meta.json + size + sha256 re-hash,
                                    once-only attach) inside the insert transaction
POST /teacher/questions/:id/edit, /delete   delete also removes the question_images row (same tx)
                                    and its file dir (post-commit; TTL sweep is the safety net)

POST /teacher/uploads             chunked image upload — initiate {total,size,sha256} with
                                    1 ≤ size ≤ 25 MB, total == ceil(size/1 MiB) ≤ 25 →
                                    {upload_id, chunk_size, total, expires_at}; runs the orphan
                                    sweep first; 400 VALIDATION on size/total/sha mismatch
POST /teacher/uploads/:id/chunks/:index   one raw chunk (application/octet-stream), STRICTLY
                                    sequential — accepted only as the next index or an idempotent
                                    retransmit of the last one; exact expected length enforced;
                                    400 on wrong index/size/order ("Chunks must be uploaded in order.")
POST /teacher/uploads/:id/complete assemble in order + verify cumulative size ≤ 25 MB +
                                    sha256 equality + magic-byte MIME (jpeg/png/gif/webp) →
                                    {path, mime, size, sha256}; incomplete / corrupted / oversize /
                                    unsupported type → 400 (chunks kept unless assembled); idempotent
POST /teacher/uploads/:id/delete   cancel a session → 200; unknown id → 404; once the image is
                                    attached to a question → 409 CONFLICT (file never deleted)

GET  /teacher/quiz                quiz list
GET  /teacher/quiz/new            create form (title, timer type, all settings)
POST /teacher/quiz/new
GET  /teacher/quiz/:id            tabs [Questions | Settings | Participants]
                                    header actions: Share → modal (QR + join URL + join
                                    code), Results → /teacher/quiz/:id/results (the
                                    inline Results tab was removed), Live monitor,
                                    Activate/Deactivate, Delete
POST /teacher/quiz/:id/edit       → 409 unless nonaktif AND zero attempts
POST /teacher/quiz/:id/questions  compose: add bank questions to the quiz — accepts JSON
                                    {"question_ids":[…],"length":""} (bank-selector
                                    modal via fetch) or form question_ids[]/length/
                                    seq_<id> as x-www-form-urlencoded or multipart
                                    (absent seq → append after MAX(seq) in arrival
                                    order): 404 NOT_FOUND quiz missing, 409 CONFLICT
                                    duplicates, same VALIDATION messages otherwise
POST /teacher/quiz/:id/questions/:qid/delete   remove one composed question (only while nonaktif → otherwise 409 locked; 404 quiz missing / not in quiz)
POST /teacher/quiz/:id/questions/reorder   rewrite the manual order: JSON {"question_ids":[…]}
                                    or form question_ids[] must be EXACTLY the composed
                                    set → seq renumbered 1..N in one transaction, only
                                    while nonaktif (else 409 locked); 400 VALIDATION on
                                    empty / duplicate / mismatched set, 404 quiz missing;
                                    data: {question_ids:[…]} echoes the stored order
POST /teacher/quiz/:id/status     activate/close (close while running → modal with
                                    "N students working" → confirm → lock all)
POST /teacher/quiz/:id/start      global: begin countdown (→ berjalan)
POST /teacher/quiz/:id/stop       confirm → end all (unanswered = wrong)

GET  /teacher/quiz/:id/monitor    live monitor (SSE): waiting room + student cards
                                    (current page — Question N or Preview — dwell time,
                                    per-question time spent in per-question mode,
                                    selected answer, yellow cheating card with badge,
                                    [Cheat] toggle + [Remove] buttons after a violation
                                    OR once flagged; Remove = modal; a submitted student
                                    leaves the cards)
GET  /teacher/quiz/:id/monitor/stream SSE stream (JSON events)
GET  /teacher/quiz/:id/qr         PNG QR code of the join URL (join via QR)
POST /teacher/quiz/:id/participants/:pid/action
                                    JSON: remove | cheat_toggle | approve | reject
POST /teacher/quiz/:id/participants/add
                                    JSON: {username} → manually add a student
                                    (404 unknown user, 409 already a participant)

GET  /teacher/quiz/:id/results    student list (rank / score / correct count / flags) +
                                    per-student expandable answer panel (answer beside
                                    the key, right/wrong per question) + export buttons
GET  /teacher/quiz/:id/results/analysis per-question analysis (% correct/wrong/unanswered,
                                    most-chosen option) — its own route so the analysis
                                    query only runs when this view is opened
GET  /teacher/quiz/:id/grading    essay grading view
POST /teacher/quiz/:id/grading/:aid save essay score → finalize final_score
GET  /teacher/quiz/:id/results/export?format=csv|xlsx   streaming download (default csv)

GET  /teacher/password-resets     pending reset requests
POST /teacher/password-resets/:id/approve   → must_change_pw=1
POST /teacher/password-resets/:id/reject
```

### Student

Public pages (no session; the navbar has exactly three menus — Home, Join quiz,
About — plus a profile icon that routes by session role: anonymous → `/login`,
murid → `/student`, guru → `/teacher`):
```
GET  /                    landing page (hero + promo banner + capability strip)
GET  /join                join-code input + active quizzes
                          (join form renders only for a murid session)
GET  /about               about page (feature overview, both roles)
```

Behind authStudent middleware:
```
GET  /student             murid dashboard: join-code input + active quizzes
                          (sidebar shell; role-isolated menu); renders the
                          ongoing-quiz notice modal (data-ssr-modal, auto-shown
                          by ui.js) whenever a 'started' attempt is still open
                          in a running quiz — 'aktif' OR 'berjalan', since only
                          the global START flips a quiz to 'berjalan'; sign-in
                          lands here, so a returning murid is told immediately
POST /join                JSON {code} → validation chain (exists? attempts? started?)
GET  /quiz/:code          workspace (linear or review mode per quiz type); while
                          an attempt is open, leaving asks first — in-page link
                          clicks get the quizConfirm modal, tab close / refresh /
                          back get beforeunload; programmatic navigations
                          (submit-finish, SSE repaints) never prompt (all timer
                          modes share the 'started' state)
GET  /quiz/:code/stream   SSE: start broadcast, teacher events, live ranking,
                          correct preview, anti-cheat alerts, force-stop, time left
POST /quiz/:code/start    per-question: start personal timer
POST /quiz/:code/answer   JSON {question_id, answer} → upsert + score
POST /quiz/:code/next     review mode: advance + preview payload (if setting ON)
POST /quiz/:code/page     workspace beacon {page: question|preview} → SSE `page`
                          event: live monitor follows the murid's page in every
                          timer mode (mirror-only — no DB write)
POST /quiz/:code/finish   end attempt → compute score_auto
POST /quiz/:code/visibility JSON {kind: blur|minimize|switch|sleep}
GET  /history             all attempts, respect settings (score/ranking/review)
GET  /history/:id         attempt detail per question_review level
GET  /profile, POST /profile/edit   name, class, major
```

**Image endpoint (both roles):**
```
GET  /media/question/:imageId     question image bytes: in-handler session gate (guru OR murid →
                                  200, anonymous → 401 JSON); stored MIME + Cache-Control: no-cache
                                  + strong ETag (sha256) → body-less 304 on If-None-Match, plus
                                  nosniff + inline disposition; unknown/inactive id, invalid stored
                                  path or missing file → 404
```

**SSE endpoints:** `/teacher/quiz/:id/monitor/stream`, `/quiz/:code/stream` — all payloads JSON.

**Contract:** every non-SSR endpoint returns `{"ok":true,"data":...}` or `{"ok":false,"error":"CODE","message":"..."}`.
Roles enforced by middleware: student → `/teacher/*` = redirect home; teacher cannot enter student workspaces.

---

## 8. Error Handling & Edge Cases

HTTP: `400` validation · `401` unauthenticated · `403` role/locked · `404` missing · `409` state conflict · `410` quiz ended · `500` server.

**Auth & session**
- Login failure returns a generic message (never reveals whether the account exists); duplicate username/email on register → `409`.
- Register success → success flash on `/login?registered=1` (`MsgRegistered`); no session is created — the student signs in manually, and the `pending_join` cookie survives the redirect to be consumed at that sign-in.
- Forgot-password: name+account mismatch → same generic message for both failure kinds; duplicate requests → only one `pending` active; double-click approve → idempotent `WHERE status='pending'`.
- `must_change_pw` enforced by middleware redirect; password < 8 chars or confirm mismatch → `400`; on change → delete all sessions + invalidate cache.
- Tampered/expired/unknown-session cookie → treated as logged-out + cookie cleared; double logout = safe no-op.

**CRUD**
- Race: two tabs editing → guard in handler + `WHERE status='nonaktif'` → `409`.
- Delete quiz with attempts → `409`. Delete class/major in use → `409`.
- Join-code UNIQUE collision → regenerate with bounded retry, else `500`.
- Activate with zero selected questions → `400`. Question validation: PG needs exactly 1 correct; multi needs ≥2 options and ≥1 correct; <2 options → `400`.
- Question image — every check server-side (client logic is UI-only): >25 MB → `400` at initiate; chunk out-of-order / wrong length / bad index → `400` (strictly sequential, last-chunk retransmit allowed); finalize with missing chunks → `400`, assembled size >25 MB → `400`, sha256 mismatch → `400` (chunks kept for resend), magic bytes not jpeg/png/gif/webp → `400` (extension and `Content-Type` are never trusted); path traversal impossible (ids are crypto/rand base64url re-validated by regex, extension derives only from sniffed MIME, the form echoes a server-issued path re-checked against `meta.json` + full re-hash + stat size before the row insert); already-attached upload → `409`; disk-full/IO → `500 SERVER_ERROR`; canceled sessions are removed at once, stalled/unlinked ones by the 24 h sweep — which never touches a dir referenced by `question_images`.
- Sequential order chosen → `shuffle_questions=0` forced server-side; `timer_on=0` → `total_seconds` ignored.
- Approve arriving after START → auto-rejected (`WHERE status='pending' AND quiz.status='aktif'`).
- Double-click submit → idempotent upsert; answer after `ends_at` → `410`, existing row untouched.

**Global flow**
- START with zero participants allowed (after confirmation modal showing 0).
- STOP vs timeout vs all-finished race → single transition `WHERE status='berjalan'`; losers no-op.
- Simultaneous answer + timeout → **server wall-clock wins** (checked inside the transaction).
- Removed in waiting room → `final_score=NULL`, out of ranking; removed while working → snapshot, in ranking, flagged.

**Per-question flow**
- Double Start → idempotent (only first sets `ends_at`). Start after close → `409`/`410`.
- Timer expires exactly at submit → `ends_at` checked in transaction → auto-finish, `410`.
- New attempt while previous not finished → `409`. `attempt_no > max_attempts` → `409 ATTEMPT_LIMIT`.
- Close with registered-but-not-started students → they get `final_score=NULL` ("not attempted"), outside ranking.

**Connection & realtime**
- Disconnect → freeze `ends_at` at last heartbeat; reconnect → resume.
- Slow SSE client (full buffer) → close that connection (never block the hub); heartbeat every 15 s; dead connections reaped (no goroutine leaks).
- Container restart → rehydrate (§6.3); past-`ends_at` participants auto-finish.
- Two tabs same student → allowed, last upsert wins; teacher monitor reconnect → full snapshot.
- DB write failure → no broadcast (clients keep last known state).

**Anti-cheat & export**
- Flood → 10 s collapse per kind; post-finish events ignored; toggle idempotent.
- **CSV and XLSX** injection guard (CSV: `'` prefix; XLSX: string cells only, never formula cells — guard against `=`/`+`/`-`/`@` leading values); empty dataset → header-only file.
- Unknown `?format=` → `400 VALIDATION` (never silently fall back).

---

## 9. Testing Strategy

**Framework:** Go built-in `testing` + `net/http/httptest`. No third-party test framework.

**Levels:**
- `tests/unit/` — pure logic, **no DB/HTTP**; injectable fake clock; seeded RNG for shuffle. Fast, deterministic, parallel-safe.
- `tests/integration/` — real handlers over HTTP against MariaDB test database (`quiz_test`), truncated per test.
- `tests/{cache,config,db}/` — per-package tests for `internal` mirrors/config/pool (packages `cachetest`, `configtest`, `dbtest`); no HTTP; DB-dependent ones use the shared `quiz_test` gate.
- `internal/middleware/assets_test.go` (package `middleware`, in-package): `Fingerprints` = sha256[:12], `asset` `?v=`/passthrough, `StaticCache` header matrix (7 subtests).

**Unit coverage (table-driven, every decision branch):**

| File | Covers |
|---|---|
| `scoring_test.go` | unanswered=wrong, all-correct, partial, essay NULL→merge, removal snapshot, MAX(multi-attempt), `final_score` vs `score_auto` |
| `timer_test.go` | pause/reconnect math (freeze at last second), `now == ends_at` boundary, total = n×sec, `timer_on=0`, boot auto-finish of expired |
| `shuffle_test.go` | `qorder` deterministic, option order differs across students, sequential forces shuffle off |
| `ranking_test.go` | progress points, ordering, removed included, registered-not-started excluded, updates per answer |
| `attempt_test.go` | attempt limits, global forced to 1, two started attempts prevented |
| `review_matrix_test.go` | 3 review levels × show_final_score × ranking_live → correct history data |
| `authlogic_test.go` | forgot-password match/mismatch, password validation, generic login message, join-code retry |
| `anticheat_test.go` | 10 s collapse, post-finish ignored, toggle idempotent |
| `uploads_test.go` | initiate validation (size/total/sha bounds), chunk order gate (first / sequential / retransmit-last / gap / replay-too-old / out-of-range), magic-byte allowlist (jpeg/png/gif/webp + rejects), ref-path regex, sweep planner (old+unlinked swept; linked & fresh kept), chunk assembly from t.TempDir (concat+sha, missing chunk, sha mismatch keeps chunks) |

**Integration coverage (run with `-race`):**

| File | Covers |
|---|---|
| `auth_test.go` | register→login→forgot→approve→force-change-password→old sessions dead; tampered/expired cookie; double-approve race |
| `register_redirect_test.go` | register → `302 /login?registered=1` with zero `Set-Cookie`, success flash on the login page, authed route still bounces until manual login |
| `quiz_crud_test.go` | `409` edit-active race (two tabs), delete-attempted blocked, validation, activate-without-questions, code collision |
| `join_matrix_test.go` | full matrix: every `status × join_mode` → expected response (in-progress message, attempt exhausted, pending lapsed after START) |
| `flow_global_test.go` | waiting room→approve/open→START→linear+preview→STOP/timeout/confirm race→results; removed in waiting room vs while working |
| `flow_persoal_test.go` | registered→start→jump→back, timer expired at submit, close with modal (N working), registered-not-started, re-attempt |
| `realtime_test.go` | SSE event order, **snapshot rehydrate = new hub built from DB** (simulated restart), heartbeat, full buffer→disconnect, concurrent submits→single row |
| `anticheat_test.go` | visibility POST → log + card + buttons visibility, flood collapse |
| `monitor_tracking_test.go` | workspace `page` beacon → teacher SSE `page` event + monitor blob (`page`/`spent`, `selesai` leaves cards); yellow cheating card + badge + both action buttons; start announces name+page in all three timer modes; started workspace has exactly 2 nav buttons + preview panel, no finish/save button; student stream open/close republishes presence (`connected` true → false) so a refresh repaints the monitor |
| `dashboard_notice_test.go` | ongoing-quiz notice modal on `GET /student`: absent before the attempt starts, present (with `data-ssr-modal` + return link) while a per-question attempt is open on an `aktif` quiz |
| `riwayat_test.go` | settings matrix honored (score/ranking/review none-text-full) per attempt |
| `export_test.go` | CSV content + injection guard; XLSX content readable via excelize + string-cell guard; `?format=xlsx` valid, `?format=bogus` → 400; empty dataset → header-only both formats |
| `export_sheets_test.go` | 3-sheet workbook contract [Murid, Pertanyaan, Rekapitulasi] in order; frozen `ySplit=1` + `#2C5EAD` bold header on every sheet; Rekapitulasi `SetCellFormula` cells (`CellTypeFormula`, cross-sheet `Murid!`/`Pertanyaan!`); Pertanyaan TOTAL `SUM` formula; CSV 9 legacy headers + Q<i> Answer/Score pairs + `Question text` row |
| `search_ssr_test.go` | SSR search params `qq` (composed) / `bq` (bank) / `q` (roster) isolated per region, empty+absent param = full list, cross-contamination blocked, hostile input escaped (200, no raw tag) |
| `nav_no_root_links_test.go` | no exact `href="/"` on any of 13 app-shell pages; static "Dashboard" crumb; role-mapped brands (guru → /teacher, murid → /student) |
| `reorder_test.go` | reorder payload ladder (empty/zero/non-numeric/duplicate/mismatched-set/foreign id → 400, unknown quiz → 404, active quiz → 409 locked) with seq untouched by rejections, JSON + urlencoded acceptance renumbering seq 1..N, mirror invalidation; student `qorder` = teacher order with shuffle off, `BuildOrder` over the teacher order with shuffle on |
| `results_split_test.go` | `/results` renders the expandable answer panel (answer + key side by side, correct/wrong/unanswered badges, correct count) and no analysis table; `/results/analysis` renders the analysis alone with its own active tab; both 404 on unknown ids |
| `upload_test.go` | initiate→chunks→complete happy path + idempotent re-complete, out-of-order chunk → 400 "in order" then ordered retry succeeds, oversize/fake-MIME/sha-mismatch/incomplete → 400, cancel → session dir gone, attached upload → 409, expiry sweep I/O (linked dirs survive), media endpoint: 200 header matrix (Content-Type/Cache-Control/ETag/nosniff/inline), 304 on If-None-Match, 401 anonymous, 404 unknown/inactive/invalid-path |

**Determinism rules:** inject clock (no `time.Now` in logic), seeded RNG in tests, truncate relevant tables per test → safe to re-run and run sequentially.

**Test-quality bar:** every branch of the edge-case matrix has a test that **fails when the code is wrong** — not merely "does not panic". Never delete or disable a failing test.

---

## 10. Boundaries

**Always do:**
- Write DB (transaction) → commit → update/invalidate mirror → broadcast SSE, for every mutation.
- Non-SSR endpoints return the JSON contract `{ok, data|error, code}`.
- Guard quiz state **server-side** (`WHERE status='...'` inside transactions); never trust client state.
- Validate all handler input (length, type, enum, numeric bounds) before querying.
- Keep `go vet` + `go test ./... -race -count=1` green before every commit.
- Route user feedback through Bootstrap: Toasts for notifications (SSR messages arrive as the `Flash` payload rendered by `layout/feedback.html`, JS failures via `quizToast`), Bootstrap Modal for every confirmation (`quizConfirm`); messages that survive a reload travel through `sessionStorage` (`quizStoreFlash`).
- Numbered migrations in `migrations/`; keep schema in sync with §5 (living spec).
- Stay within the closed 8-module list (§2): `echo/v5`, `go-sql-driver/mysql`, `x/crypto`, `skip2/go-qrcode`, `tmaxmax/go-sse`, `cleanenv`, `gocsv`, `excelize/v2` — and never below the security-floor versions in §2 (excelize ≥v2.11.0, x/crypto ≥v0.55.0).

**Ask first:**
- Database schema changes (columns, tables, indexes, ENUM values).
- Adding/upgrade any dependency in `go.mod` or image versions.
- New auth paths/roles or changes to `/teacher/*` access rules.
- SSE contract changes (event names, payloads) or removing schema columns.
- Dockerfile/compose/port/`.env` changes affecting local==production.
- Git commits (repo initialized with this spec as first commit).

**Never do:**
- Commit `data/` contents or `.env` (only `.env.example`).
- Native CSS outside `web/css/app.css` (landing + sidebar shell layout) and the `--bs-*` overrides in `web/vendor/theme.css`; CDN Bootstrap (must be vendored).
- Browser `alert()`/`confirm()` — notifications are Bootstrap Toasts, confirmations are Bootstrap Modals (`web/js/ui.js`).
- Calculate scores/ranking/timers in JavaScript — all in Go; JS only renders SSE events, POSTs JSON, detects `visibilitychange`.
- Add foreign keys or extra normalization in MariaDB.
- Ship shims, aliases, deprecated endpoints, or `TODO` as a "solution".
- Delete or disable failing tests to get green.
- Keep quiz execution state in memory without a DB trace.

---

## 11. Success Criteria (specific, testable)

**Functional (each proven by integration tests):**
1. Teacher logs in from `.env`; student registers → logs in (username/email) → edits profile.
2. Full forgot-password flow: input matches → request appears on teacher dashboard → approve → **next login skips password validation → locked on change-password until new password set** → old sessions dead.
3. Question bank CRUD (3 types) + length filter during compose + manual `seq` ordering.
4. **Edit active quiz → `409`** (two-tab race test); edit only when `nonaktif` + zero attempts.
5. **Global quiz:** join (code/URL/QR) → waiting room (open/approve) → START countdown → linear mode (auto-next, preview on/off) → STOP/timeout/confirm-all-finished → results, unanswered = wrong.
6. **Per-question quiz:** registered ≠ start; personal timer = n × seconds; jump/back navigation; close → modal "N working" → lock all.
7. Every quiz setting has real effect: option/question shuffle (sequential forces options off), review none/text/full, show score, show ranking — proven by `review_matrix_test`.
8. **Live monitor:** current page (Question N **or Preview**), dwell time, selected answer in real time (MC on click, essay on autosave) in **every timer mode** — no-timer students appear with name + page the moment they start; per-question mode also shows time spent per question; submitted students leave the cards; all quiz types have a monitor. The monitor never goes blank on refresh (the greeting snapshot restores every card) and a murid who closes/reopens their quiz page flips `connected` live via the stream-lifecycle presence push.
9. **Anti-cheat:** blur/minimize/switch/sleep → log + teacher notification → yellow card + badge with `Cheat` toggle + `Remove` (modal) appearing **only** after a violation **or a flag**; both functional (toggle idempotent, remove modal-mandatory).
10. **Student history** honors all setting combinations; every attempt shown; ranking = highest score; removed & cheating clearly flagged.
11. **Essay grading:** teacher inputs scores → `final_score` finalized; before grading shows "awaiting grading".
12. **Teacher results:** scores + flags + per-question analysis (% correct/wrong/unanswered, most-chosen option) + export in **both CSV and XLSX** (injection-safe in both; `?format=xlsx` produces a file that excelize re-opens with correct cell values).
13. **Live ranking ON** → shown on student screens **and** teacher monitor, updates on every answer.

**Robustness:**
14. `go test ./... -race -count=1` fully green — unit + integration, nothing disabled.
15. **Rehydrate after restart:** new hub from DB → running status, remaining time (`ends_at`), ranking, cheat flags, approval queue restored identically; expired-while-down → auto-finished.
16. Disconnect → timer frozen at last heartbeat → reconnect resumes with no time lost.
17. DB write failure → no broadcast (state never newer than DB).
18. Join after START → exact message: **"Kuis sedang berlangsung, Anda tidak dapat bergabung."**

**UI & deployment:**
19. `docker compose up --build` works from a clean machine (`http://localhost:8090`); identical images (Go 1.26.6-alpine, mariadb:12, alpine:3.24, Bootstrap 5.3.8 vendored).
20. Layout CSS limited to `web/css/app.css` (Bootstrap utilities first); responsive; light/dark toggle; modern radius; **custom blue palette** (no default Bootstrap colors) with controlled semantic accents — danger `#C93128` (error/delete/cheat), success `#0E7A46` (correct), warning `#A15C0B`, **unified across both themes**: light body `#F4F8FD` / ink `#0F2240` with primary + links `#2C5EAD`, dark derived from the same blue family (body `#0B1A30` / ink `#D7E8F8`, primary `#1591DC`, links `#4BB8FA`, sidebar + tertiary `#10233F`) — palette theming via `--bs-*` overrides in `web/vendor/theme.css` only.
21. All UI text in **bahasa Indonesia baku (KBBI)**; the only external request is the sanctioned Google Fonts stylesheet (§2) — no other CDN assets; no quiz state lost on restart; no dependencies outside the closed list.

---

## 12. Open Questions

None — all 24 clarification decisions plus session strategy, persistence/mirror policy, UI language (bahasa Indonesia baku KBBI), palette (blue + controlled semantic), testing scope, and the library selection (SSE broker / .env loader / CSV+XLSX export, §2) are resolved in this document.
