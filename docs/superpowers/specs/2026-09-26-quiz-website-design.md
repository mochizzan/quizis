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

**UI language:** full **international English**.
**UI style:** minimalist, custom **monochrome palette** (no default Bootstrap colors), light/dark mode, modern corner radius, responsive, 0 native CSS.

---

## 2. Tech Stack (verified against local Docker)

| Component | Version | Source |
|---|---|---|
| Go | 1.26.4 | local + `golang:1.26.4-alpine` image |
| Echo | **v5.3.1** (requires Go ≥1.25) | latest stable; v4 is security-fix-only until 2026-12-31 |
| MariaDB | **`mariadb:12`** (12.3 LTS) | already present locally; 13.0.2 exists but not local |
| Bootstrap | **5.3.8** | vendored locally at `web/vendor/bootstrap/` — **no CDN** |
| Realtime | **SSE (server→client) + POST JSON (client→server)** | native `EventSource`, no WebSocket library |
| QR | `skip2/go-qrcode` | generates join-URL PNG |
| Password hash | `golang.org/x/crypto/bcrypt` | |
| DB driver | `github.com/go-sql-driver/mysql` | |
| Docker | 29.4.3 / Compose v5.1.3 | multi-stage: `golang:1.26.4-alpine` → `alpine:3.24` |

**Dependency list is closed** — adding any dependency requires asking first (§10).

---

## 3. Commands

```bash
# Build
go build ./cmd/server

# Test (unit + integration, with race detector)
go test ./... -race -count=1

# Lint
go vet ./...

# Dev / run (local == production)
docker compose up --build          # → http://localhost:8080

# Migrations run automatically at boot (embedded, numbered .sql)
```

---

## 4. Project Structure

```
quiz/
├── cmd/server/main.go          # entrypoint: config → db → migrate → routes → listen
├── internal/
│   ├── config/                 # .env loader (GURU_USER, GURU_PASS, DSN, PORT)
│   ├── db/                     # MariaDB connection + embedded numbered migrations
│   ├── models/                 # structs (User, Quiz, Question, Participant, Answer, ...)
│   ├── handlers/               # auth/, teacher/, student/ — SSR render + JSON endpoints
│   ├── middleware/             # authTeacher, authStudent, forceChangePW, session
│   ├── realtime/               # SSE hub: rooms per quiz, snapshot-on-reconnect
│   ├── cache/                  # in-memory read-through mirror (per-entity TTL, write-through invalidation)
│   └── quizengine/             # scoring, timers (clock-injectable), shuffle, ranking, anti-cheat
├── views/                      # html/template: layout/, teacher/, student/, partials/
├── web/
│   ├── js/                     # native JS only: sse.js, anti-cheat.js, monitor.js, theme.js
│   └── vendor/
│       ├── bootstrap/          # Bootstrap 5.3.8 (css+js, local)
│       └── theme.css           # monochrome palette via --bs-* overrides ONLY
├── migrations/                 # 0001_*.sql, 0002_*.sql (numbered, embedded)
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
             nama_lengkap VARCHAR(100) NOT NULL,      -- display name (English UI: "Full name")
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
             timer_type ENUM('global','per_soal') NOT NULL,
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
             correct JSON NULL,               -- "B" | [0,2] | essay answer key
             created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
             INDEX idx_q_type (type)

quiz_questions id INT UNSIGNED AUTO_INCREMENT PK,
             quiz_id INT UNSIGNED NOT NULL,
             question_id INT UNSIGNED NOT NULL,
             seq SMALLINT UNSIGNED NOT NULL,                -- manual order
             UNIQUE KEY uq_qq (quiz_id, question_id),
             INDEX idx_qq_order (quiz_id, seq)

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
             answer TEXT NULL,                -- selected option IDs / JSON multi / essay text
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
- No WebSocket library; no third-party realtime dependency.

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
- **Disconnect pause:** freeze `ends_at` at the **last heartbeat second** (not detection time), store `remaining_seconds`; on reconnect `ends_at = now + remaining_seconds`. Applies to both timer types (fairness per participant; others unaffected).
- Downtime during restart counts against wall-clock `ends_at` (acceptable for local deployment).

### 6.7 Answer flow → preview → ranking
1. Student submits → `POST /answer` → idempotent upsert (`uq_ans`) → compute `is_correct` → **SSE push to teacher monitor** (current question, dwell time, selected option in real time).
2. **Correct/wrong preview** (`show_correct_wrong`):
   - **Linear mode (global):** `/answer` response includes preview → shown 2–3 s → auto-next. OFF → no preview, immediate advance.
   - **Review mode (per-question):** `/answer` never leaks correctness; preview appears on `POST /next` (Next/Finish press). OFF → no preview.
   - Final results page always shows preview if setting ON.
3. **Ranking live** (`ranking_live=ON`): computed **in-memory** from progress points (correct relative to total), pushed to student leaderboard **and** teacher monitor on every answer. Zero DB queries per event; rebuildable from `answers` on demand.
4. Finish (submit/timeout/STOP/removal/close): `score_auto = correct/total × 100`, **unanswered = wrong**. If quiz has essays → `final_score` stays NULL until teacher grades, then `score_auto + essay_score`.

### 6.8 Shuffle / order
- `qorder` snapshot (per participant, written at start): question order + option order.
- Option shuffle is **per-student** (option "B" may differ between students → answers store **option IDs**, letters rendered server-side from snapshot).
- If sequential question order chosen → `shuffle_questions=0` forced **server-side** (not just UI).
- `max_attempts`: global timer always forced to 1. Per-question honors setting; ranking uses MAX(`final_score`); all attempts shown in student history and teacher data.

### 6.9 Anti-cheat
- `visibilitychange`/`blur`/gap > 30 s → `POST /visibility {kind}` → insert `anti_cheat_events` → **SSE push to that student's card in teacher monitor**.
- **Both buttons appear only after a violation exists:** `Cheat` toggle (idempotent, sets `participants.cheating`) and `Remove` (always behind a confirmation modal).
- Student keeps working until teacher acts.
- **Event flood control:** duplicate kind within 10 s collapses to one row; events after quiz end are ignored.

### 6.10 Removal semantics
- Removed **in waiting room** (never started): status `dikeluarkan`, `final_score=NULL`, excluded from ranking, shown in history as removed.
- Removed **while working**: snapshot `score_auto` first (unanswered = wrong) → status `dikeluarkan` → counted in results, history, **and ranking** with the removed flag.

### 6.11 Results & history
- **Teacher results page:** participant list (name, final score, status: finished/removed/cheating, awaiting-grading), per-question analysis (% correct/wrong/unanswered, most-chosen option), CSV export (injection-guarded: values starting `=`/`+`/`-`/`@` get `'` prefix; empty results → header-only file).
- **Essay grading:** teacher opens student's essay answers → inputs scores → `final_score` finalized. Before grading: "awaiting grading".
- **Student history:** all attempts listed; respects per-quiz settings: show/hide score, show/hide ranking, question review `none` / `text` (question + own answers + right/wrong marks, no key) / `full` (with answer key).
- Ranking across attempts uses highest score.

### 6.12 Join rules
- Open to all students regardless of class/major (class/major = profile labeling only).
- Join modes: **Open** (immediate registration/waiting room) or **Approve** (request appears on teacher dashboard → approve/reject).
- After START pressed (global): new joiners rejected with exact message **"Quiz is in progress, you cannot join."**
- Pending approvals that arrive **after** START are auto-rejected.
- Registration ≠ start for per-question quizzes; student starts anytime while quiz is active and they are registered.
- Teacher can manually add/remove students from a quiz.

---

## 7. Routing

**Path language:** all URL paths are **English** (UI = international English). Schema column/ENUM literals stay as approved in §5 — they are internal identifiers, never user-facing.

**Method contract:** plain HTML form posts (auth forms, CRUD forms) are SSR navigations — `application/x-www-form-urlencoded` + redirect. Anything sent by JavaScript (`fetch`) is **JSON only**.

### Auth
```
GET  /login               login page (teacher + student in one form)
POST /login               username OR email + password (form post → redirect)
POST /logout
GET  /register            student registration (name, username, email, password,
                          class dropdown, major dropdown)
GET  /forgot-password     username/email + full name
POST /forgot-password     validate match → create reset request
GET  /change-password     accessible only when must_change_pw=1
POST /change-password     new + confirm → clear flag, revoke old sessions
```

### Teacher (`/teacher/*`, authTeacher middleware)
```
GET  /teacher                     dashboard (quiz list + reset-request badge)
GET|POST /teacher/classes, /teacher/majors CRUD dropdowns

GET  /teacher/questions           question bank + length filter (short/medium/long)
POST /teacher/questions           add (pg/multi/essay)
POST /teacher/questions/:id/edit, /delete

GET  /teacher/quiz                quiz list
GET  /teacher/quiz/new            create form (title, timer type, all settings)
POST /teacher/quiz/new
GET  /teacher/quiz/:id            tabs [Questions | Settings | Participants | Results]
POST /teacher/quiz/:id/edit       → 409 unless nonaktif AND zero attempts
POST /teacher/quiz/:id/questions  compose: filter bank by length → pick → order (seq)
POST /teacher/quiz/:id/status     activate/close (close while running → modal with
                                    "N students working" → confirm → lock all)
POST /teacher/quiz/:id/start      global: begin countdown (→ berjalan)
POST /teacher/quiz/:id/stop       confirm → end all (unanswered = wrong)

GET  /teacher/quiz/:id/monitor    live monitor (SSE): waiting room + student cards
                                    (current question, dwell time, selected answer,
                                    violation badge, [Cheat] toggle + [Remove] buttons —
                                    only visible after violation; Remove = modal)
GET  /teacher/quiz/:id/monitor/stream SSE stream (JSON events)
GET  /teacher/quiz/:id/qr         PNG QR code of the join URL (join via QR)
POST /teacher/quiz/:id/participants/:pid/action
                                    JSON: remove | cheat_toggle | approve | reject

GET  /teacher/quiz/:id/results    list + per-question analysis + CSV export
GET  /teacher/quiz/:id/grading    essay grading view
POST /teacher/quiz/:id/grading/:aid save essay score → finalize final_score
GET  /teacher/quiz/:id/results/export streaming CSV

GET  /teacher/password-resets     pending reset requests
POST /teacher/password-resets/:id/approve   → must_change_pw=1
POST /teacher/password-resets/:id/reject
```

### Student (authStudent middleware)
```
GET  /                    home: join-code input + active quizzes
POST /join                JSON {code} → validation chain (exists? attempts? started?)
GET  /quiz/:code          workspace (linear or review mode per quiz type)
GET  /quiz/:code/stream   SSE: start broadcast, teacher events, live ranking,
                          correct preview, anti-cheat alerts, force-stop, time left
POST /quiz/:code/start    per-question: start personal timer
POST /quiz/:code/answer   JSON {question_id, answer} → upsert + score
POST /quiz/:code/next     review mode: advance + preview payload (if setting ON)
POST /quiz/:code/finish   end attempt → compute score_auto
POST /quiz/:code/visibility JSON {kind: blur|minimize|switch|sleep}
GET  /history             all attempts, respect settings (score/ranking/review)
GET  /history/:id         attempt detail per question_review level
GET  /profile, POST /profile/edit   name, class, major
```

**SSE endpoints:** `/teacher/quiz/:id/monitor/stream`, `/quiz/:code/stream` — all payloads JSON.

**Contract:** every non-SSR endpoint returns `{"ok":true,"data":...}` or `{"ok":false,"error":"CODE","message":"..."}`.
Roles enforced by middleware: student → `/teacher/*` = redirect home; teacher cannot enter student workspaces.

---

## 8. Error Handling & Edge Cases

HTTP: `400` validation · `401` unauthenticated · `403` role/locked · `404` missing · `409` state conflict · `410` quiz ended · `500` server.

**Auth & session**
- Login failure returns a generic message (never reveals whether the account exists); duplicate username/email on register → `409`.
- Forgot-password: name+account mismatch → same generic message for both failure kinds; duplicate requests → only one `pending` active; double-click approve → idempotent `WHERE status='pending'`.
- `must_change_pw` enforced by middleware redirect; password < 8 chars or confirm mismatch → `400`; on change → delete all sessions + invalidate cache.
- Tampered/expired/unknown-session cookie → treated as logged-out + cookie cleared; double logout = safe no-op.

**CRUD**
- Race: two tabs editing → guard in handler + `WHERE status='nonaktif'` → `409`.
- Delete quiz with attempts → `409`. Delete class/major in use → `409`.
- Join-code UNIQUE collision → regenerate with bounded retry, else `500`.
- Activate with zero selected questions → `400`. Question validation: PG needs exactly 1 correct; multi needs ≥2 options and ≥1 correct; <2 options → `400`.
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
- CSV injection guard; empty dataset → header-only file.

---

## 9. Testing Strategy

**Framework:** Go built-in `testing` + `net/http/httptest`. No third-party test framework.

**Levels:**
- `tests/unit/` — pure logic, **no DB/HTTP**; injectable fake clock; seeded RNG for shuffle. Fast, deterministic, parallel-safe.
- `tests/integration/` — real handlers over HTTP against MariaDB test database (`quiz_test`), truncated per test.

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

**Integration coverage (run with `-race`):**

| File | Covers |
|---|---|
| `auth_test.go` | register→login→forgot→approve→force-change-password→old sessions dead; tampered/expired cookie; double-approve race |
| `quiz_crud_test.go` | `409` edit-active race (two tabs), delete-attempted blocked, validation, activate-without-questions, code collision |
| `join_matrix_test.go` | full matrix: every `status × join_mode` → expected response (in-progress message, attempt exhausted, pending lapsed after START) |
| `flow_global_test.go` | waiting room→approve/open→START→linear+preview→STOP/timeout/confirm race→results; removed in waiting room vs while working |
| `flow_persoal_test.go` | registered→start→jump→back, timer expired at submit, close with modal (N working), registered-not-started, re-attempt |
| `realtime_test.go` | SSE event order, **snapshot rehydrate = new hub built from DB** (simulated restart), heartbeat, full buffer→disconnect, concurrent submits→single row |
| `anticheat_test.go` | visibility POST → log + card + buttons visibility, flood collapse |
| `riwayat_test.go` | settings matrix honored (score/ranking/review none-text-full) per attempt |
| `export_test.go` | CSV content + injection guard |

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
- Numbered migrations in `migrations/`; keep schema in sync with §5 (living spec).
- Stay within the closed dependency list: Echo v5, `go-sql-driver/mysql`, `skip2/go-qrcode`, `x/crypto`.

**Ask first:**
- Database schema changes (columns, tables, indexes, ENUM values).
- Adding/upgrade any dependency in `go.mod` or image versions.
- New auth paths/roles or changes to `/teacher/*` access rules.
- SSE contract changes (event names, payloads) or removing schema columns.
- Dockerfile/compose/port/`.env` changes affecting local==production.
- Git commits (repo initialized with this spec as first commit).

**Never do:**
- Commit `data/` contents or `.env` (only `.env.example`).
- Native CSS beyond Bootstrap; CDN Bootstrap (must be vendored).
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
8. **Live monitor:** current question, dwell time, selected answer in real time (MC on click, essay on Next); all quiz types have a monitor.
9. **Anti-cheat:** blur/minimize/switch/sleep → log + teacher notification → `Cheat` toggle + `Remove` (modal) appear **only** after a violation; both functional (toggle idempotent, remove modal-mandatory).
10. **Student history** honors all setting combinations; every attempt shown; ranking = highest score; removed & cheating clearly flagged.
11. **Essay grading:** teacher inputs scores → `final_score` finalized; before grading shows "awaiting grading".
12. **Teacher results:** scores + flags + per-question analysis (% correct/wrong/unanswered, most-chosen option) + CSV export (injection-safe).
13. **Live ranking ON** → shown on student screens **and** teacher monitor, updates on every answer.

**Robustness:**
14. `go test ./... -race -count=1` fully green — unit + integration, nothing disabled.
15. **Rehydrate after restart:** new hub from DB → running status, remaining time (`ends_at`), ranking, cheat flags, approval queue restored identically; expired-while-down → auto-finished.
16. Disconnect → timer frozen at last heartbeat → reconnect resumes with no time lost.
17. DB write failure → no broadcast (state never newer than DB).
18. Join after START → exact message: **"Quiz is in progress, you cannot join."**

**UI & deployment:**
19. `docker compose up --build` works from a clean machine; identical images (Go 1.26.4-alpine, mariadb:12, alpine:3.24, Bootstrap 5.3.8 vendored).
20. 0 native CSS; responsive; light/dark toggle; modern radius; **custom monochrome palette** (no default Bootstrap colors) with controlled semantic accents (muted red `#B42318` for error/delete/cheat, muted green `#067647` for correct) — theming via `--bs-*` overrides in `web/vendor/theme.css` only.
21. All UI text in **international English**; no CDN assets; no quiz state lost on restart; no dependencies outside the closed list.

---

## 12. Open Questions

None — all 24 clarification decisions plus session strategy, persistence/mirror policy, UI language (English), palette (monochrome + controlled semantic), and testing scope are resolved in this document.
