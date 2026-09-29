# Implementation Plan — Quizizz-Style Quiz Website

## Context

Build the full application described by the approved spec
`D:\Project\Mother\quiz\docs\superpowers\specs\2026-09-26-quiz-website-design.md`
(573 lines, read in full this session). The repo currently contains **only** that spec,
`.gitignore`, and `.git` (branch `main`, 2 commits). Everything else is greenfield.

End state: `docker compose up --build` serves a two-role quiz app (teacher/student) at
`http://localhost:8090`, with `go test ./... -race -count=1` fully green.

Three spec-vs-reality conflicts (found by reading library source) plus seven
plan-vs-spec cross-check findings — missing QR and password-reset routes, two-topic SSE
wording, option-index encoding, handlers layout, manual add-student, START pending
settlement — are resolved by user decision or source verification. They change the
spec, so **Step 1 syncs the spec before any code is written** — every later step must be
consistent with the amended spec.

---

## Verified facts (discovered this session — do not re-derive)

### Environment
| Fact | Value |
|---|---|
| Go / Docker / Compose | 1.26.4 / 29.4.3 / v5.1.3 |
| Local images present | `mariadb:12`, `golang:1.26.4-alpine`, `alpine:3.24` |
| Port **8090** | **FREE** (chosen host port) |
| Port **3306** | **FREE** (MariaDB host port) |
| Port 8080 | OCCUPIED by `kidversa-edutourism-backend-1` — never bind it |
| Port 3307 | OCCUPIED by `kidversa-edutourism-mariadb-1` — never bind it |
| Repo contents | `.gitignore` (has `data/`, `.env`), `docs/`, `.git` |
| `.env` | does not exist yet — created from `.env.example` |

### Echo v5.3.1 (verified at tag `v5.3.1`, NOT v4)
```go
type Context struct                                    // struct, NOT interface (v5 break)
type HandlerFunc func(c *Context) error
type MiddlewareFunc func(next HandlerFunc) HandlerFunc
func WrapHandler(h http.Handler) HandlerFunc           // package-level, echo.go:849
func Recover() MiddlewareFunc                          // middleware/recover.go:43
func (e *Echo) GET(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo
func (e *Echo) POST(...) RouteInfo
func (e *Echo) Use(m ...MiddlewareFunc)
func (e *Echo) Group(prefix string, m ...MiddlewareFunc) (g *Group)
func (e *Echo) StaticFS(pathPrefix string, fs fs.FS, m ...MiddlewareFunc) RouteInfo
func (e *Echo) Start(address string) error
func (c *Context) JSON(code int, i any) error
func (c *Context) Render(code int, name string, data any) error
func (c *Context) Stream(code int, contentType string, r io.Reader) error
func (c *Context) Redirect(code int, url string) error
func (c *Context) Blob(code int, contentType string, b []byte) error
func (c *Context) Param(name string) string
func (c *Context) QueryParam(name string) string
func (c *Context) Cookie(name string) (*http.Cookie, error)
func (c *Context) SetCookie(*http.Cookie)
func (c *Context) Get(key string) any ; func (c *Context) Set(key string, val any)
type Renderer interface{ Render(c *Context, w io.Writer, name string, data any) error }
type TemplateRenderer struct{ Template interface{ ExecuteTemplate(w io.Writer, name string, data any) error } }
```
### github.com/tmaxmax/go-sse
```go
type Server struct{ OnSession func(w http.ResponseWriter, r *http.Request) (topics []string, allowed bool) }
func (s *Server) Publish(e *Message, topics ...string) error
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)   // implements http.Handler
func Upgrade(w http.ResponseWriter, r *http.Request) (*Session, error)
type Subscription struct{ Client MessageWriter; LastEventID EventID; Topics []string }
type MessageWriter interface{ Send(*Message) error; Flush() error }   // session.go:16
type Session struct                                                        // session.go:29
type Message struct{ ID EventID; Type EventType }                         // message.go:56
type Joe struct{}
func (j *Joe) Start(ctx context.Context) (context.Context, error)
var DefaultTopic Topic   // topic "": all subscribers receive it
```
**CRITICAL:** `Joe.start()` fan-out does `sub.Client.Send(msg)` **synchronously on one
goroutine** (joe.go:242-250) → a slow client blocks the entire hub. Spec §8 requires
"slow client → close that connection, never block the hub". `sse.Server` therefore
**cannot** be used as-is; Step 9 uses `Upgrade` + `Joe` + a guarded writer (see below).

**Browser constraint:** `EventSource` cannot set custom headers, so multi-topic
subscription (which requires the `Subscribe` header) is impossible. **Every connection is
single-topic**, with the topic resolved from the URL path inside `OnSession`.

### github.com/ilyakaznacheev/cleanenv
```go
func ReadConfig(path string, cfg interface{}) error   // parseFile → readEnvVars(cfg, false)
func ReadEnv(cfg interface{}) error                   // OS env only
```
`parseENV` (cleanenv.go:182-195) does **unconditional `os.Setenv` for every key present in
the file** → file wins over OS env. Keys **absent** from the file are never touched → OS
env reaches `readEnvVars`.

`readEnvVars` order (verified): required-check (`rawValue==nil && required && field is zero`
→ error) → **then** `defValue` fallback → then `continue` if still nil.
⇒ Consequence: a field with `env-default` must NOT also carry `env-required`.

Tags: `env`, `env-required`, `env-default`, `env-separator` (default `,`),
`env-prefix`, `env-layout`, `env-description`, `env-upd`.
Transitive deps pulled automatically: `godotenv`, `BurntSushi/toml`, `yaml.v3`,
`olympos.io/edn` (these are **not** direct dependencies — the spec's "8 modules" counts direct only).

### github.com/gocarina/gocsv
```go
func Marshal(in interface{}, out io.Writer) error     // csv.go:153 — streams to HTTP writer
var TagName = "csv"                                   // struct tag: `csv:"Header Name"`
```

### github.com/xuri/excelize/v2 (≥ v2.11.0, security floor)
```go
func NewFile(opts ...Options) *File                                    // file.go:31
func (f *File) NewSheet(sheet string) (int, error)                     // sheet.go:51
func (f *File) SetCellStr(sheet, cell, value string) error             // cell.go:459 — STRING cell, never a formula
func (f *File) Write(w io.Writer, opts ...Options) error               // file.go:111
func (f *File) SetColWidth(min, max int, width float64) error
func (f *File) NewStreamWriter(sheet string) (*StreamWriter, error)    // stream.go:114
func (sw *StreamWriter) SetRow(cell string, values []interface{}, opts ...RowOpts) error  // stream.go:388
func (sw *StreamWriter) Flush() error
```

---

## User decisions (load-bearing, locked)

1. **Host port 8090** — compose publishes `8090:8090`; the app listens on `:8090` both
   locally and inside the container (same value everywhere, so `PORT` needs no override).
2. **The `.env` file wins** over OS environment for any key it contains, and `.env` is
   `COPY`'d into the Docker image.
3. **`DB_HOST` is deliberately ABSENT from `.env`.** Local resolves to
   `env-default:"127.0.0.1"`; Docker supplies it via compose `environment: DB_HOST=db`.
   Because `parseENV` only writes keys present in the file, compose's value survives.

---

## Approach — ordered steps

Steps are ordered so the tree builds and the test suite passes after each one. Steps 2-6
are strictly sequential; 7-16 each depend on the previous but are independent of each other
beyond that.

**Mandatory protocol for every step** (from the task rules):
> **read** the actual target files before editing → **execute** → **re-read** what was
> written → **self-review** against spec + completion criteria → record
> `step N: PASS/FAIL + note` → only then start the next step.
If a step's re-read or validation reveals a spec ambiguity or conflict: **stop and ask**,
do not improvise.

---

### Step 1 — Sync the spec (3 locked decisions + 7 cross-check findings)

**Why first:** the implementer reads the spec before editing; leaving it contradictory
guarantees wrong code.

**Modify** `docs/superpowers/specs/2026-09-26-quiz-website-design.md`:

| § | Current | Replace with |
|---|---|---|
| §2 last paragraph | "OS env (Docker) wins over file" | "For any key **present** in `.env`, the file value wins (cleanenv `parseENV` writes it into OS env unconditionally). Keys **absent** from `.env` — specifically `DB_HOST` — resolve from OS env or `env-default`." |
| §2 dependency list | "exactly 8 modules" | "exactly 8 **direct** modules (cleanenv additionally pulls `godotenv`, `toml`, `yaml.v3`, `edn` transitively — not counted)" |
| §3 command block | `# → http://localhost:8080` | `# → http://localhost:8090` |
| §4 `.env` comment | `# .env loader (GURU_USER, GURU_PASS, DSN, PORT)` | `# cleanenv: .env → struct (GURU_USER, GURU_PASS, DB_USER, DB_PASS, DB_NAME, DB_PORT, PORT); DB_HOST intentionally absent (local default 127.0.0.1, Docker sets db)` |
| §5 `questions.correct` comment | `correct JSON NULL, -- "B" \| [0,2] \| essay answer key` | `correct JSON NULL, -- pg: 1 (original option index); multi: [0,2]; essay: free-text key (never auto-graded)` |
| §5 note after schema | (absent) | add: "Migration bookkeeping table `schema_migrations(name VARCHAR(191) PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)` is created by the migrator; it is infrastructure, not domain data." |
| §11.19 | `docker compose up --build` works from a clean machine; … | add "`http://localhost:8090`" after the command |
| §7 (top, after "Path language") | (absent) | add: "`DB_HOST` is not an application config key — it is supplied only by Docker compose `environment:` and by the `env-default` fallback." |
| §2 SSE server cell | "broker `Publish` per topic = per-quiz room, cross-topic dedup, server-only" | "broker `Publish` per topic = per-quiz room (two topics per quiz: `quiz:<id>` + `quiz:<id>:teacher`); hub built on `Upgrade` + `Joe` with a slow-client guard (§6.1), server-only" |
| §4 handlers comment | `# auth/, teacher/, student/ — SSR render + JSON endpoints` | `# flat files — auth.go, password_resets.go, teacher_*.go, student.go, respond.go: SSR render + JSON endpoints` |
| §5 `answers.answer` comment | `answer TEXT NULL, -- selected option IDs / JSON multi / essay text` | `answer TEXT NULL, -- pg/multi: JSON array of ORIGINAL option indexes e.g. [0,2]; essay: free text` |
| §6.1 SSE bullet | "one topic per quiz room (teacher monitor + that quiz's students subscribe to the room topic; history/dashboard pages get their own topic) … cross-topic dedup keeps a subscriber from receiving the same event twice" | "two topics per quiz — `quiz:<id>` (students) and `quiz:<id>:teacher` (monitor) — role filtering at subscribe time; each connection is single-topic (EventSource cannot send the `Subscribe` header), topic resolved from the URL path; hub built on `Upgrade` + `Joe` with a guarded queue writer because `sse.Server.Publish` blocks on slow clients; no dashboard/history topics (those pages are plain SSR)" |
| §6.8 shuffle note | "answers store **option IDs**" | "answers store **original option indexes** from the pre-shuffle options array" |
| §6.12 pending sentence | "Pending approvals that arrive **after** START are auto-rejected." | "Pending approvals are settled at START: any row still `pending` when START fires becomes `dikeluarkan` (`final_score=NULL`) in the same transaction; approve attempts after START return `409`." |
| §7 teacher routes (after `participants/:pid/action`) | (absent) | add: `POST /teacher/quiz/:id/participants/add   JSON {username} → manually add a student (404 unknown user, 409 already a participant)` — restores §6.12 manual-add capability (user-approved) |

**Done when:** no remaining occurrence of `localhost:8080`, no `DSN` as an `.env` key,
§2 no longer claims OS-env precedence for file-present keys, and none of
`auth/, teacher/, student/`, `cross-topic dedup`, `option IDs` survive anywhere in the file.

**Protocol:** read full file → apply the 15 edits → re-read all 15 regions →
`grep -nE "localhost:8080|DSN, PORT|OS env \(Docker\) wins|auth/, teacher/, student/|cross-topic dedup|option IDs" <spec>`
must return **nothing** → record result.

---

### Step 2 — Module scaffold, config, Docker

**Create:**

1. `go.mod`
   ```
   module quiz

   go 1.26.4
   ```
   Then `go get github.com/labstack/echo/v5@v5.3.1 github.com/go-sql-driver/mysql@v1.10.0 golang.org/x/crypto@v0.55.0 github.com/skip2/go-qrcode github.com/tmaxmax/go-sse github.com/ilyakaznacheev/cleanenv@latest github.com/gocarina/gocsv github.com/xuri/excelize/v2@v2.11.0`
   Verify `go list -m all | wc -l` and confirm `golang.org/x/crypto` ≥ v0.55.0 and
   `excelize/v2` ≥ v2.11.0 (`go list -m golang.org/x/crypto github.com/xuri/excelize/v2`).

2. `internal/config/config.go`
   ```go
   package config

   type Config struct {
       Port      int    `env:"PORT" env-default:"8090"`
       GuruUser  string `env:"GURU_USER" env-required:"true"`
       GuruPass  string `env:"GURU_PASS" env-required:"true"`
       DBUser    string `env:"DB_USER"  env-required:"true"`
       DBPass    string `env:"DB_PASS"  env-required:"true"`
       DBName    string `env:"DB_NAME"  env-required:"true"`
       DBPort    int    `env:"DB_PORT"  env-default:"3306"`
       DBHost    string `env:"DB_HOST"  env-default:"127.0.0.1"` // NOT in .env
       TestDBName string `env:"TEST_DB_NAME" env-default:"quiz_test"`
   }

   func Load(path string) (*Config, error)   // cleanenv.ReadConfig(path, &c); then
                                             // nil/error → wrap with "read config %s: %w"
   func (c *Config) DSN(dbName string) string
   // fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&loc=UTC",
   //     c.DBUser, c.DBPass, c.DBHost, c.DBPort, dbName)
   ```
   `parseTime=true` is mandatory: `answers.answered_at`, `participants.*_at` are `DATETIME`
   and will be scanned into `time.Time`/`sql.NullTime`.
   **No `env-required` on `DB_HOST`** — required-check runs before default fallback and
   would error out.

   Config path resolution in `main`: `os.Getenv("CONFIG_PATH")`, default `".env"`.

3. `tests/config/config_test.go` — table-driven:
   - file containing `GURU_USER=a …` + no `DB_HOST` → `cfg.DBHost == "127.0.0.1"`
   - same file + `os.Setenv("DB_HOST","db")` before `Load` → `cfg.DBHost == "db"`
     (proves compose can override) — reset with `t.Setenv`
   - missing required key → error
   - nonexistent path → error

4. `.env.example` (committed)
   ```
   # Quiz app config — copy to .env and edit.
   # DB_HOST is intentionally NOT set here:
   #   local (go run / go test) -> 127.0.0.1 (built-in default)
   #   docker compose           -> db (supplied by compose environment)
   GURU_USER=guru
   GURU_PASS=changeme
   DB_USER=quiz
   DB_PASS=quizpass
   DB_NAME=quiz
   DB_PORT=3306
   PORT=8090
   ```

5. `cmd/server/main.go` — minimal for now: `config.Load`, `e := echo.New()`,
   `e.Use(middleware.Recover())`, `e.GET("/", …)` returning plain `OK`,
   `e.Start(fmt.Sprintf(":%d", cfg.Port))`. Wired fully in Step 4.

6. `Dockerfile`
   ```dockerfile
   FROM golang:1.26.4-alpine AS build
   WORKDIR /src
   COPY go.mod go.sum ./
   RUN go mod download
   COPY . .
   RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

   FROM alpine:3.24
   WORKDIR /app
   COPY --from=build /out/server /app/server
   COPY . /app/                      # brings .env if present, plus .env.example
   # clean machine has no .env: fall back to the template so boot succeeds (§11.19)
   RUN [ -f /app/.env ] || cp /app/.env.example /app/.env
   EXPOSE 8090
   CMD ["/app/server"]
   ```

7. `.dockerignore` — `.git`, `data`, `docs`

8. `docker-compose.yml`
   ```yaml
   services:
     app:
       build: .
       ports: ["8090:8090"]
       environment:
         DB_HOST: db          # absent from .env, so this survives cleanenv
       depends_on:
         db:
           condition: service_healthy
       restart: unless-stopped

     db:
       image: mariadb:12
       environment:
         MARIADB_DATABASE:      ${DB_NAME:-quiz}
         MARIADB_USER:          ${DB_USER:-quiz}
         MARIADB_PASSWORD:      ${DB_PASS:-quizpass}
         MARIADB_ROOT_PASSWORD: ${DB_ROOT_PASS:-rootpass}
       ports: ["3306:3306"]
       volumes:
         - ./data/mariadb:/var/lib/mysql
         - ./docker/mysql-init:/docker-entrypoint-initdb.d:ro
       healthcheck:
         test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
         interval: 5s
         timeout: 5s
         retries: 20
       restart: unless-stopped
   ```
   Compose interpolates `${DB_*}` from the project `.env`; defaults match `.env.example`
   so a clean clone works.

9. `docker/mysql-init/001-testdb.sql` (runs only on first boot of an empty data dir)
   ```sql
   CREATE DATABASE IF NOT EXISTS quiz_test
     CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
   GRANT ALL PRIVILEGES ON quiz_test.* TO 'quiz'@'%';
   FLUSH PRIVILEGES;
   ```

**Done when:** `go build ./...` succeeds; `go vet ./...` clean;
`go test ./tests/config/... -count=1` green; `docker compose up -d --build` → `curl -f http://localhost:8090/`
returns `OK`; `docker compose ps` shows `db` healthy.

**Protocol:** read each file after writing; `docker compose logs app` must be free of
config errors; record `step 2: PASS/FAIL`.

---

### Step 3 — DB layer, migrations, full schema

**Create:**

1. `migrations/embed.go`
   ```go
   package migrations
   import "embed"
   //go:embed *.sql
   var FS embed.FS
   ```
   (Root `migrations/` cannot be embedded from `internal/db` — `go:embed` forbids `..`.)

2. `migrations/0001_init.sql` — **exactly** the DDL from spec §5, plus the
   `schema_migrations` bookkeeping table created by the migrator, not the file.
   Preserve verbatim: every column, type, `ENUM(...)`, `UNIQUE KEY`, `INDEX`, and the
   comment that there are **no foreign keys**. Engine InnoDB, charset utf8mb4.

3. `internal/db/db.go`
   ```go
   func Open(dsn string) (*sql.DB, error)   // sql.Open("mysql", dsn); SetMaxOpenConns(25);
                                             // SetMaxIdleConns(10); SetConnMaxLifetime(5*time.Minute);
                                             // ping with 10s context timeout
   ```

4. `internal/db/migrate.go`
   ```go
   func Migrate(db *sql.DB) error
   ```
   - ensure `schema_migrations` exists (idempotent `CREATE TABLE IF NOT EXISTS`)
   - list `migrations/*.sql` from `migrations.FS`, sort ascending
   - skip names already in `schema_migrations`
   - each file runs in its own transaction: `BEGIN` → exec → insert name → `COMMIT`
   - on error: `ROLLBACK`, return wrapped with the file name
   - write the applied list to stdout (`fmt.Printf("migrated %s\n", name)`)

5. `tests/db/db_test.go` — integration:
   - skip with `t.Skip` if `cfg.DSN(cfg.TestDBName)` unreachable (message: start `docker compose up -d db`)
   - `Migrate` twice → second run applies nothing (assert count of applied files unchanged)
   - assert the tables/columns from §5 exist by querying `information_schema.columns`
     for at least: `quizzes.code`, `participants.qorder`, `participants.ends_at`,
     `answers.uq_ans`, `anti_cheat_events.kind`
   - assert **zero** rows in `information_schema.referential_constraints` for schema
     `quiz_test` → proves "no FK" (spec §5)

**Done when:** `docker compose up -d` running, `go test ./tests/db/... -count=1` green.

**Protocol:** re-read `0001_init.sql` against spec §5 line-by-line (column names, types,
ENUM literals, indexes) → record `step 3: PASS/FAIL + column-mismatch list (must be empty)`.

---

### Step 4 — Models, JSON envelope, templates, base layout

**Create:**

1. `internal/models/models.go` — structs mirroring §5 tables exactly:
   `User`, `Session`, `Quiz`, `Question`, `QuizQuestion`, `Participant`, `Answer`,
   `AntiCheatEvent`, `PasswordReset`, `RefKelas`, `RefJurusan`.
   Use `sql.NullX` variants where the column is nullable (`sql.NullTime` for
   `started_at`, `ends_at`, `finished_at`, `current_q_since`; `sql.NullString` for
   `questions.correct`, `participants.qorder`; `sql.NullFloat64` for scores).
   Go field names CamelCase; no DB tags needed beyond `db:"..."` if a helper requires it.

2. `internal/handlers/respond.go`
   ```go
   func ok(c *echo.Context, data any) error            // c.JSON(200, map[string]any{"ok": true, "data": data})
   func fail(c *echo.Context, status int, code, msg string) error
   // body: map[string]any{"ok": false, "error": code, "message": msg}
   ```
   Error code constants (exact literals, reused by frontend and tests):
   ```go
   const (
       ErrValidation      = "VALIDATION"
       ErrUnauthenticated = "UNAUTHENTICATED"
       ErrForbidden       = "FORBIDDEN"
       ErrNotFound        = "NOT_FOUND"
       ErrConflict        = "CONFLICT"
       ErrQuizEnded       = "QUIZ_ENDED"
       ErrQuizInProgress  = "QUIZ_IN_PROGRESS"
       ErrAttemptLimit    = "ATTEMPT_LIMIT"
       ErrAlreadyAttempted = "ALREADY_ATTEMPTED"
       ErrInvalidCode     = "INVALID_CODE"
       ErrServer          = "SERVER_ERROR"
   )
   ```
   Human messages are **English** (spec: UI language is international English).
   `ErrQuizInProgress` message must be exactly:
   `"Quiz is in progress, you cannot join."`

3. `internal/handlers/renderer.go` — custom `echo.Renderer`:
   ```go
   type Renderer struct{ t *template.Template }
   func NewRenderer(dir string) (*Renderer, error)   // filepath.WalkDir collects *.html, ParseFiles once
   func (r *Renderer) Render(c *echo.Context, w io.Writer, name string, data any) error
   ```
   Func map: `fmtScore` (formats a `float64` to 2 decimals), `join` (strings.Join),
   `add`/`sub` (int), `dict` (map building). No other helpers.
   **Parsing:** `template.ParseGlob` cannot cross `/` (no `**` glob support) — walk
   `views/`, collect every `*.html`, call `t.ParseFiles(files...)` exactly once at boot;
   any parse error crashes the boot (fail fast, not at first request).
   **Layout convention (avoids duplicate-define collisions):** one template set; shared
   partials `views/layout/head.html` (`{{define "head"}}` → `<!doctype html>`, `<head>`
   with vendored Bootstrap `<link>`, `theme.css`, `theme.js`, `<body>` + nav slot) and
   `views/layout/foot.html` (`{{define "foot"}}` → Bootstrap `<script>`, closes
   `</body></html>`). Each page file defines ONE uniquely-named template bracketing its
   content: `{{define "page-login"}}{{template "head" .}} … {{template "foot" .}}{{end}}`.
   `Render` executes that define by name. **No shared `content` block** — it would collide
   across pages inside a single template set.

4. `views/layout/head.html`, `views/layout/foot.html`, `views/auth/login.html` (stub content), `views/home.html`
   — English labels: "Sign in", "Register", "Forgot password", "Join quiz", "Dashboard",
   "Question bank", "Results", "History", "Profile".

5. root `bootstrap/` package (`bootstrap/embed.go` — `//go:embed css js`): offline
   **Bootstrap 5.3.8** `bootstrap/css/bootstrap.min.css`,
   `bootstrap/js/bootstrap.bundle.min.js` (with Popper), served at `/bootstrap` via
   `e.StaticFS("/bootstrap", bootstrap.FS)`; `bootstrap-icons` optional (skip icons — use
   text/emoji-free ASCII labels to stay dependency-light).
   **No CDN references for Bootstrap** — Google Fonts is the sole external request (spec §2).

6. `web/vendor/theme.css` — blue-palette overrides ONLY, no hand-written layout CSS:
   ```css
   :root, [data-bs-theme="light"] {
     --bs-body-bg:#F4F8FD; --bs-body-color:#0F2240;
     --bs-tertiary-bg:#FFFFFF; --bs-border-color:#C4E2F5;
     --bs-primary:#2C5EAD; --bs-primary-rgb:44,94,173;
     --bs-body-font-weight:400;
     --bs-border-radius:0.75rem; --bs-border-radius-sm:0.5rem;
     --bs-border-radius-lg:0.75rem; --bs-border-radius-xl:1rem;
     --bs-link-color:#2C5EAD; --bs-link-hover-color:#1C4C93;
     --bs-body-font-family:"Noto Sans Cypro Minoan", sans-serif;
   }
   [data-bs-theme="dark"] {
     --bs-body-bg:#0B1A30; --bs-body-color:#D7E8F8;
     --bs-tertiary-bg:#10233F; --bs-border-color:rgba(196,226,245,0.16);
     --bs-primary:#1591DC; --bs-primary-rgb:21,145,220;
     --bs-link-color:#4BB8FA; --bs-link-hover-color:#C4E2F5;
   }
   /* unified semantic accents — same hues in both themes */
   .text-ok   { color: var(--bs-success-text) !important; }  /* correct answer */
   .text-bad  { color: var(--bs-danger-text) !important; }   /* error / delete / cheat */
   .bg-ok     { background-color: rgb(var(--bs-success-rgb)) !important; }
   .bg-bad    { background-color: rgb(var(--bs-danger-rgb)) !important; }
   ```
   (full token set — success `#0E7A46` / danger `#C93128` / warning `#A15C0B` in both
   themes, plus the var-driven component bridges — lives in `web/vendor/theme.css`)
   Embed `web/` via `//go:embed` in `web/embed.go` (binary self-contained) and mount TWO
   roots: `e.StaticFS("/assets", echo.MustSubFS(webFS, "vendor"))` (serves
   `/assets/theme.css`, `/assets/bootstrap-icons/…`) and
   `e.StaticFS("/js", echo.MustSubFS(webFS, "js"))` — the `js/` dir lives **outside**
   `vendor/` and needs its own mount (`/js/theme.js`, `/js/workspace.js`,
   `/js/monitor.js`, `/js/anti-cheat.js`). Bootstrap is NOT under `vendor/`: the root
   `bootstrap/` package embeds `css/` + `js/` and is mounted separately as
   `e.StaticFS("/bootstrap", bootstrap.FS)` (`/bootstrap/css/bootstrap.min.css`,
   `/bootstrap/js/bootstrap.bundle.min.js`). Templates are read from `views/` on disk at
   boot (Dockerfile `COPY . /app/`), not embedded.

7. `web/js/theme.js` — reads `localStorage.getItem("theme")`, sets
   `document.documentElement.setAttribute("data-bs-theme", …)`, toggles on click.
   No framework, no build step.

**Done when:** `go build ./...` green; starting the server and opening
`http://localhost:8090/login` renders the English login page with the blue palette
applied (brand `#2C5EAD`, not Bootstrap's stock primary) and the dark toggle works after refresh.

**Protocol:** re-read each written file; `grep -rn "cdn\|jsdelivr\|unpkg" web/ views/`
returns nothing; record `step 4: PASS/FAIL`.

---

### Step 5 — Sessions: DB store + in-memory mirror + auth middleware

**Create:**

1. `internal/cache/mirror.go`
   ```go
   package cache
   type Store struct { /* sync.RWMutex; items map[string]entry; now func() time.Time */ }
   func New() *Store
   func NewWithClock(now func() time.Time) *Store      // tests inject a fake clock
   func (s *Store) Get(key string) (any, bool)         // expired → delete + miss
   func (s *Store) Set(key string, val any, ttl time.Duration)
   func (s *Store) Delete(key string)
   func (s *Store) DeletePrefix(prefix string)
   ```
   `tests/cache/mirror_test.go`: TTL expiry with fake clock; `Delete` makes `Get`
   miss immediately (proves revocation does not wait for TTL); concurrent `Get`/`Set` under
   `-race`.

   **Mirror registry — every row of the spec §6.2 TTL table** (`internal/cache/keys.go`):

   | Key | TTL | Populated by | Invalidated by |
   |---|---|---|---|
   | `session:<id>` | 5 min | LoadSession (this step) | logout, password change, `DeleteUserSessions` |
   | `quizstate:<quizID>` | 3 s | join / answer / workspace reads (Step 10) | every quiz+participant+answer mutation: start, answer, finish, close, status, approve, remove |
   | `quizlist:all` | 10 s | dashboard & home lists (Step 8) | quiz CRUD |
   | `bank:<length>` | 10 s | question-bank reads (Step 8) | question CRUD |
   | `history:<userID>` | 10 s | history list (Step 15) | attempt finish, essay grading |
   | `quizset:<quizID>` | 30 s | settings reads while `nonaktif` (Step 8) | quiz edit |
   | `refs:kelas`, `refs:jurusan` | 60 s | register + dropdown reads (Steps 6/8) | ref CRUD |
   | app config | process lifetime | `config.Load` at boot | redeploy |

   Invalidation runs **after COMMIT, before broadcast** (spec §6.2 order). Serving a value
   up to TTL old is explicitly allowed — golden rule is "faster, **never newer** than DB,
   never something DB doesn't have".

2. `internal/db/sessions.go`
   ```go
   func NewSessionID() (string, error)   // crypto/rand 32 bytes → base64.RawURLEncoding (43 chars)
   func InsertSession(ctx, db, id string, userID uint64, role string, expiresAt time.Time) error
   func GetSession(ctx, db, id string) (*models.Session, error)   // nil,nil when absent/expired
   func DeleteSession(ctx, db, id string) error
   func DeleteUserSessions(ctx, db, userID uint64) error          // password change
   func DeleteExpired(ctx, db, now time.Time) error               // lazy GC at boot + on login
   ```

3. `internal/middleware/session.go`
   ```go
   type Session struct{ ID string; UserID uint64; Role string }
   const CookieName = "session_id"   // spec §6.4 — 43-char crypto/rand id, HttpOnly cookie
   func LoadSession(db *sql.DB, store *cache.Store) echo.MiddlewareFunc
   func AuthTeacher(next echo.HandlerFunc) echo.HandlerFunc
   func AuthStudent(next echo.HandlerFunc) echo.HandlerFunc
   func ForceChangePassword(next echo.HandlerFunc) echo.HandlerFunc
   ```
   Behaviour (spec §6.4, §8):
   - `LoadSession` reads cookie → mirror `session:<id>` → miss → DB → `Set` with **5 min TTL**.
     Missing/tampered/expired/unknown → clear cookie, treat as anonymous.
   - `AuthTeacher`: no session → redirect `/login`; `Role != "guru"` → redirect `/`.
   - `AuthStudent`: no session → redirect `/login`; `Role != "murid"` → redirect `/login`.
   - `ForceChangePassword`: if session user has `must_change_pw=1` → redirect
     `/change-password`, except when the request path is `/change-password` or
     `/logout`.
   - Cookie: `HttpOnly=true`, `SameSite=Lax`, `Path=/`, `MaxAge=7*24*3600`,
     `Secure` only when `r.TLS != nil` (local is plain HTTP).
   - **Every mutation deletes the mirror entry first**, then the DB row.

4. `tests/integration/session_test.go`
   - login → cookie works → logout → **same cookie rejected immediately** (revocation not
     waiting 5 min)
   - password change deletes all sessions: two logins, change password on one, both
     cookies dead
   - tampered cookie (flip a char) → treated as anonymous, cookie cleared
   - student hitting `/teacher/...` → redirect `/`; teacher hitting student workspace → redirect

**Done when:** `go test ./tests/cache/... ./tests/integration/... -race -count=1` green.

**Protocol:** re-read `session.go` middleware after writing; confirm every session
mutation path calls `store.Delete` before the DB write → record `step 5: PASS/FAIL`.

---

### Step 6 — Auth handlers + views (first end-to-end slice)

**Create** `internal/handlers/auth.go`, `internal/handlers/password_resets.go` + templates `views/auth/*.html`, `views/teacher/password_resets.html`:

| Route | Behaviour |
|---|---|
| `GET /login` | one form for both roles (username **or** email + password) |
| `POST /login` | lookup by `username` (indexed), fallback `email` (indexed); bcrypt compare; **generic failure message "Invalid username/email or password."** (never reveals which); on success create session, `DeleteExpired` GC, redirect: `guru` → `/teacher`, `murid` → `/student` (a valid pending-join cookie reroutes that murid to `/join?code=...`). If user has `must_change_pw=1` → skip password verification entirely and redirect `/change-password` |
| `POST /logout` | delete mirror + DB row, clear cookie, redirect `/login`; double logout is a no-op |
| `GET /register` | fields: full name, username, email, password, `ref_kelas` dropdown, `ref_jurusan` dropdown |
| `POST /register` | validate: username/email unique → `409` `CONFLICT`; password ≥ 8 chars → `400`; insert user with bcrypt hash; `302` → `/login?registered=1` — NO session (manual sign-in; success flash `MsgRegistered`; `pending_join` cookie survives) |
| `GET /forgot-password` | username/email + full name |
| `POST /forgot-password` | both must match the same user, else **one generic message**; if a `pending` row exists → no-op (only one active request); else insert `pending` |
| `GET /change-password` | reachable only under `ForceChangePassword` |
| `POST /change-password` | new ≥ 8, confirm matches → `400` otherwise; set hash, `must_change_pw=0`, `DeleteUserSessions` + mark this user's `disetujui` reset rows `selesai`, create fresh session, redirect `/student` |
| `GET /teacher/password-resets` | list `pending` requests (badge count also shown on the dashboard); under the `/teacher` group + `AuthTeacher` |
| `POST /teacher/password-resets/:id/approve` | idempotent guard: `UPDATE ... SET status='disetujui' WHERE id=? AND status='pending'` → 0 rows → `409 CONFLICT` (double-click safe); sets the user's `must_change_pw=1` in the same transaction |
| `POST /teacher/password-resets/:id/reject` | `WHERE id=? AND status='pending'` → `status='ditolak'`; 0 rows → `409` |

Seed `ref_kelas`/`ref_jurusan` rows in `migrations/0002_seed_refs.sql` so the register
dropdowns are non-empty: classes `Grade 10`…`Grade 12`, majors `Science`, `Social`,
`Language`, `Vocational` (English labels).

**Tests** `tests/integration/auth_test.go` (spec §9):
- register → login → logout → login again
- login with email instead of username
- duplicate username → `409`; duplicate email → `409`
- wrong password → generic message (assert message identical to unknown-user case)
- forgot-password match → request visible to teacher; mismatch → same generic message
- approve → next login skips password → lands on `/change-password` → setting a new
  password revokes the old session and unblocks the app
- double approve is idempotent (second call returns `409`, row unchanged)
- reject → next login still validates the password normally; a `ditolak` row can no
  longer be approved (`409`) — status transitions are one-way and guarded

**Done when:** `go test ./tests/... -race -count=1` green; manual: open
`http://localhost:8090/register`, complete registration → land on
`/login?registered=1` with the success flash, then sign in manually.

**Protocol:** re-read `auth.go`; confirm no handler returns a distinct message for
"unknown user" vs "wrong password" → record `step 6: PASS/FAIL`.

---

### Step 7 — `quizengine`: pure logic + unit tests

No DB, no HTTP. Injectable clock and seeded RNG. This step locks the rules that every
later step depends on.

**Create `internal/quizengine/` with these files:**

1. `score.go`
   ```go
   // CentiPercent returns correct/total*100 rounded half-up to 2 decimals using
   // integer math only: (correct*10000*2 + total) / (2*total), then /100.
   func CentiPercent(correct, total int) float64
   ```
   - `total == 0` → `0` (guard; a quiz cannot activate with 0 questions but be safe)
   - `5/10 → 50.00`, `5/7 → 71.43`, `1/3 → 33.33`, `2/3 → 66.67`, `0/10 → 0.00`
   - **Unanswered counts as wrong** — callers pass only the correct count over the
     *full* question count.

2. `answer.go`
   ```go
   func IsCorrect(qType string, correct, given any) bool
   func FinalScore(scoreAuto, essayScore *float64, hasEssay bool) *float64
   ```
   - `qType == "essay"` → `IsCorrect` always returns false and `answers.is_correct`
     stays NULL (essay is never auto-graded).
   - `FinalScore`: no essay → `scoreAuto`; essay pending → `nil` (spec §5: `final_score`
     NULL until graded); essay graded → `scoreAuto + essayScore`.

3. `order.go`
   ```go
   type Order struct {
       Questions []uint64       `json:"questions"`
       Options   map[uint64][]int `json:"options"` // questionID → permutation of ORIGINAL option indexes
   }
   func BuildOrder(seed int64, questionIDs []uint64, optionCounts map[uint64]int,
                   shuffleQuestions, shuffleOptions bool) Order
   func LetterOf(perm []int, chosenOriginalIndex int) int  // position in shuffled display
   ```
   - Uses `rand.New(rand.NewPCG(uint64(seed), uint64(seed)>>32))` — deterministic per seed.
   - Seed passed in by callers = `int64(participantID)*1_000_003 + int64(quizID)*97 + int64(attemptNo)`.
   - `shuffleQuestions=false` → identity order; `shuffleOptions=false` → identity
     permutation for every question.
   - Sequential order **forces** `shuffleQuestions=false`; caller must also force
     `shuffleOptions=false` (spec §6.7 note: "sequential chosen → shuffle options off").

4. `timer.go`
   ```go
   type Clock interface{ Now() time.Time }
   func GlobalEndsAt(startedAt time.Time, totalSeconds int) time.Time
   func Remaining(endsAt, now time.Time) time.Duration   // clamped at 0
   func OnDisconnect(endsAt, lastBeat, now time.Time) (newEndsAt time.Time, remaining int)
   // freeze at the LAST-HEARTBEAT second, not the detection time (spec §6.6)
   func OnReconnect(remainingSeconds int, now time.Time) time.Time  // now + remaining
   func PersonalTotal(questionCount, secondsPerQuestion int) time.Duration
   ```
   Boundary: `now.Equal(endsAt)` → time is **up** (returns 0 remaining and expired=true).

5. `ranking.go`
   ```go
   type Entry struct {
       ParticipantID uint64; Name string
       Score float64; Finished, Removed, Cheating bool; Rank int
   }
   type Ranker struct{ /* sync.RWMutex; byID map[uint64]*Entry */ }
   func NewRanker() *Ranker
   func (r *Ranker) Upsert(e Entry)
   func (r *Ranker) Remove(id uint64)
   func (r *Ranker) Snapshot() []Entry   // sorted: Score desc → Finished first → ParticipantID asc
   ```
   - `Removed=true` entries **stay** in the ranking (spec §6.10 removal while working).
   - `registered`-but-never-started participants are never inserted (spec §8).

6. `review.go`
   ```go
   type ReviewLevel string
   const (ReviewNone ReviewLevel = "none"; ReviewText ReviewLevel = "text"; ReviewFull ReviewLevel = "full")
   type HistoryView struct {
       ShowScore, ShowRanking bool
       Questions []QuestionView   // empty when ReviewNone
   }
   func BuildHistory(level ReviewLevel, showScore, showRanking bool,
                     qs []QuestionView) HistoryView
   ```
   - `text` → question + student's answer + right/wrong mark, **no answer key**
   - `full` → same plus `CorrectAnswer`
   - `none` → `Questions` empty

7. `anticheat.go`
   ```go
   const CollapseWindow = 10 * time.Second
   func ShouldRecord(lastAt, now time.Time, kind, lastKind string) bool
   ```
   - same `kind` within 10 s → false; different kind → true; `now` after quiz end → caller
     decides (pass `quizEnded bool` and return false).

**Unit tests** `tests/unit/*_test.go` — table-driven, one file per concern as named in
spec §9 (`scoring_test.go`, `timer_test.go`, `shuffle_test.go`, `ranking_test.go`,
`attempt_test.go`, `review_matrix_test.go`, `authlogic_test.go`, `anticheat_test.go`).
`attempt_test.go` covers `MaxAttempts`: global timer always 1 regardless of setting;
per-question honours the setting; two `started` attempts simultaneously are rejected
(the guard lives in Step 10/11 but the pure rule — `attemptNo ≤ maxAttempts` — is tested here).
`timer_test.go` MUST include the spec §9 pause/reconnect cases: `OnDisconnect` freezes at
the **last-heartbeat second** (a detection-time freeze fails this test), `OnReconnect` =
`now + remaining`, `now.Equal(endsAt)` boundary = expired, `PersonalTotal = n × seconds`,
`timer_on=0` → no deadline.

**Done when:** `go test ./tests/unit/... ./internal/quizengine/... -race -count=1` green.

**Protocol:** re-read each `quizengine/*.go`; confirm no `time.Now()` outside `Clock`,
no `math/rand` global (seeded only) → record `step 7: PASS/FAIL`.

---

### Step 8 — Teacher CRUD: references, question bank, quizzes

**Create** `internal/handlers/teacher_refs.go`, `teacher_questions.go`, `teacher_quiz.go`
and `views/teacher/*.html`. All routes under `g := e.Group("/teacher", middleware.AuthTeacher)`.

| Route | Behaviour / guards |
|---|---|
| `GET /teacher` | dashboard: quiz list + badge count of `pending` password resets |
| `GET|POST /teacher/classes`, `/teacher/majors` | create; `POST .../:id/delete` → `409` if any `users.kelas_id` references it |
| `GET /teacher/questions` | bank with `?length=short|medium|long` filter, `CHAR_LENGTH(teks)`: short `<80`, medium `80–200`, long `>200` |
| `POST /teacher/questions` | validate by type: `pg` needs exactly 1 correct index; `multi` needs ≥2 options and ≥1 correct; `essay` needs key text or may be empty. `<2 options` → `400` |
| `POST /teacher/questions/:id/edit`, `/delete` | delete → `409` if referenced by any `quiz_questions` row |
| `GET /teacher/quiz` | list with status chips |
| `GET|POST /teacher/quiz/new` | create → status `nonaktif`; generate `code` = 6 chars from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (crypto/rand); on `UNIQUE` collision retry up to 5 times, then `500` |
| `GET /teacher/quiz/:id` | tabs `Questions \| Settings \| Participants \| Results` |
| `POST /teacher/quiz/:id/edit` | **guard:** `UPDATE … WHERE status='nonaktif'` **and** zero `participants` rows; if 0 rows affected → `409 CONFLICT` with `"Quiz has participants or is active — editing is locked."` |
| `POST /teacher/quiz/:id/questions` | compose from bank: accepts `question_ids[]` + per-item `seq`; length filter is applied **server-side** when the client sends `length=`; upsert into `quiz_questions`; duplicates → `409` (UNIQUE) |
| `GET /teacher/quiz/:id/qr` | PNG of the join URL `http://<request-host>/quiz/<code>` via `skip2/go-qrcode`; `404` unknown id; `Content-Type: image/png` (spec §7 + §11.5 "join via code/URL/QR") |
| `POST /teacher/quiz/:id/participants/add` | JSON `{username}` → manually add a student (spec §6.12, user-approved): unknown user → `404`, already a participant → `409`, else insert `registered` (open mode) / `pending` (approve mode) |
| `POST /teacher/quiz/:id/status` | the **only** activation/close route (spec §7 — no separate `/activate`): `{status:"aktif"}` → reject zero questions with `400 VALIDATION` `"Add at least one question."`, force `shuffle_options=0` when `shuffle_questions==0` (server-side), ignore `total_seconds` when `timer_on=0`, set `status='aktif'`; `{status:"nonaktif"}` allowed only with zero participants (else `409`); `aktif→selesai` (per-question close, Step 12) / `berjalan→selesai` (Step 11) |

Settings form fields (all from §6.7): `timer_type`, `timer_on`, `total_seconds`,
`per_question_seconds`, `join_mode`, `shuffle_options`, `shuffle_questions`,
`show_correct_wrong`, `show_final_score`, `ranking_live`, `max_attempts`,
`question_review`.

**Tests** `tests/integration/quiz_crud_test.go` (spec §9):
- two-tab edit race: activate quiz, then `POST /edit` → `409`
- delete a quiz that has participants → `409`
- activate with 0 questions → `400`
- sequential order chosen → stored `shuffle_options` becomes `0`
- code collision path (inject a forced duplicate via a direct DB insert) → retries then succeeds
- bank length filter returns only rows in the requested bucket
- `GET /teacher/quiz/:id/qr` → `200`, `Content-Type: image/png`, PNG magic bytes (`\x89PNG`)
- `participants/add`: unknown username → `404`; duplicate → `409`; success → row present
  with the right initial `status` per join mode

**Done when:** `go test ./tests/... -race -count=1` green.

**Protocol:** re-read `teacher_quiz.go`; confirm every mutating handler writes DB inside a
transaction and only then returns `ok` → record `step 8: PASS/FAIL`.

---

### Step 9 — Realtime hub on go-sse with slow-client guard

**Create** `internal/realtime/hub.go`.

Because `sse.Server`'s `Publish` blocks on a slow client, build the handler from
`Upgrade` + `Joe` directly:

```go
package realtime

const TopicPrefix = "quiz:"
const heartbeatEvery = 15 * time.Second
const writeDeadline  = 30 * time.Second
const queueSize      = 64

type Event struct { Type string `json:"type"`; Data any `json:"data"` }

type Hub struct {
    provider *sse.Joe
    stop     context.CancelFunc
    // rooms tracking not required: Joe owns subscriptions by topic
}

func New() (*Hub, error)                 // j := &sse.Joe{}; ctx,cancel := context.WithCancel(context.Background()); j.Start(ctx); store cancel
func (h *Hub) Close()                    // stop()
func (h *Hub) Publish(topic string, ev Event) error   // m := &sse.Message{Type: sse.EventType(ev.Type)}; m.AppendData(jsonBytes); h.provider.Publish(m, sse.Topic(topic))
func (h *Hub) Handler(topic string, auth func(*http.Request) bool) echo.HandlerFunc
```

`Handler` body (this is the slow-client guard, spec §8):
1. `if !auth(r) { return fail(401) }`
2. `sess, err := sse.Upgrade(w, r)`; on error return (Upgrade already wrote a response).
3. `rc := http.NewResponseController(w)`; `rc.SetWriteDeadline(time.Now().Add(writeDeadline))`.
4. `ctx, cancel := context.WithCancel(r.Context())`; `defer cancel()`.
5. `q := make(chan *sse.Message, queueSize)`; wrap `sess` in `guardedWriter`:
   ```go
   type guardedWriter struct{ s *sse.Session; q chan *sse.Message; cancel context.CancelFunc }
   func (g *guardedWriter) Send(m *sse.Message) error {
       select { case g.q <- m: return nil
                default: g.cancel(); return errors.New("sse: client too slow") }
   }
   func (g *guardedWriter) Flush() error { return nil }   // real flush happens in the drain goroutine
   ```
   Joe sees the error and unsubscribes — the hub never blocks.
6. drain goroutine: `for { select { case <-ctx.Done(): return; case m := <-g.q: sess.Send(m); sess.Flush(); rc.SetWriteDeadline(now+writeDeadline) } }`.
   A write error or an expired deadline cancels `ctx` → `provider.Subscribe` returns →
   handler returns → Echo closes the socket.
7. `return h.provider.Subscribe(ctx, sse.Subscription{Client: gw, Topics: []sse.Topic{sse.Topic(topic)}})`

`internal/realtime/heartbeat.go`: one goroutine per Hub; every `heartbeatEvery` it calls
`Publish(topic, Event{Type:"ping"})` for each **active** topic (track active topics in a
`map[string]int` guarded by a mutex, incremented in `Handler` and decremented on return).
The browser `EventSource` ignores unknown event types, so `ping` is invisible to users.

`internal/realtime/room.go` — topic derivation used by both streams:
```go
// Teacher stream: /teacher/quiz/:id/monitor/stream  -> TopicPrefix + id
// Student stream: /quiz/:code/stream                -> TopicPrefix + <quiz id resolved from code>
func TopicForQuizID(id uint64) string   // "quiz:" + strconv.FormatUint(id, 10)
```
**Two topics per quiz (final):** `quiz:<id>` for the student stream, `quiz:<id>:teacher`
for the monitor — role filtering happens at subscribe time, so the fan-out needs no
per-role checks (spec §6.1 synced in Step 1). `room.go` exposes both helpers:
`TopicForQuizID(id)` and `TeacherTopicForQuizID(id)`.

**Wiring** in `cmd/server/main.go`: build `realtime.New()` before `e.Start`, pass it into
handlers via closure; `e.GET("/quiz/:code/stream", h)` and
`e.GET("/teacher/quiz/:id/monitor/stream", h, middleware.AuthTeacher)`.

**Tests** `tests/integration/realtime_test.go`:
- connect `httptest.NewServer` client, publish, assert event arrives with the right
  `event:`/`data:` lines and `data` decodes to the JSON `Event`
- **slow-client test:** open a subscription whose body reader is never drained, publish
  `queueSize+10` events, assert (a) `Publish` returns within 2 s (hub not blocked),
  (b) the slow connection is closed
- heartbeat: advance a fake ticker (inject the ticker interval as a field defaulting to
  15 s, settable from tests) → a `ping` event is received
- **rehydrate test:** build hub #1, deliver events, close it; build hub #2 from the same
  DB rows; a new subscriber receives a full snapshot event → proves restart recovery

**Done when:** `go test ./internal/realtime/... ./tests/... -race -count=1` green.

**Protocol:** re-read `hub.go`; confirm no code path calls `sess.Send` from
`guardedWriter.Send` (only channel enqueue) → record `step 9: PASS/FAIL`.

---

### Step 10 — Student join, workspace, answer flow

**Create** `internal/handlers/student.go` + `views/student/*.html`.

| Route | Behaviour |
|---|---|
| `GET /` | home: join-code input + list of `aktif`/`berjalan` quizzes |
| `POST /join` | JSON `{code}`; validation chain, one distinct code per branch (spec §9 join matrix): malformed/unknown code → `404 INVALID_CODE`; **existing participant row** → `dikeluarkan` → `403 FORBIDDEN` (removed), attempts exhausted → `409 ALREADY_ATTEMPTED`, otherwise **reuse the row — rejoining after a disconnect is always allowed, never 409**; **no row** → quiz `nonaktif` → `404 NOT_FOUND`, quiz `selesai` → `410 QUIZ_ENDED`, global timer `berjalan` → `409 QUIZ_IN_PROGRESS` with the exact message **"Quiz is in progress, you cannot join."**, else create: `join_mode='open'` → `registered` (global: waiting room; per-question: ready), `approve` → `pending`. Success → redirect `/quiz/:code` |
| `GET /quiz/:code` | **Invite-URL / QR auto-join (user decision):** no participant row → run the same join chain server-side — open → `registered` (global: waiting-room view; per-question: ready + Start button), approve → `pending` (waiting-for-approval view), `berjalan` → render the exact in-progress message, attempts exhausted → `ALREADY_ATTEMPTED` page. With a row: global+`berjalan` → linear layout; per-question → free-navigation layout |
| `POST /quiz/:code/start` | per-question only: idempotent — if `started_at` already set, return current state unchanged; else set `started_at=now`, `ends_at=now+personalTotal`, `status='started'`, write `qorder` |
| `POST /quiz/:code/answer` | body `{question_id, answer}`; reject when quiz `selesai` or `ends_at` passed → `410 QUIZ_ENDED` (existing row untouched); idempotent upsert on `uq_ans`. `answer` encoding: `pg`/`multi` → JSON array of **original** option indexes (`[1]`, `[0,2]`); `essay` → JSON-encoded string. Compute `is_correct` via `quizengine.IsCorrect`. **Transaction:** `BEGIN` → upsert → `COMMIT` → publish SSE → update `Ranker` → respond. Response shape: `{"ok":true,"data":{"preview":<nullable>}}` — `preview` present **only** when linear mode AND `show_correct_wrong=1`: `{"correct":bool,"correct":<raw key>}` |
| `POST /quiz/:code/next` | review mode only; returns `{"preview": …}` when `show_correct_wrong=1`, else `{"preview":null}`; also advances `participants.current_q` and `current_q_since=now` |
| `POST /quiz/:code/finish` | sets `finished_at`, `score_auto = CentiPercent(correct, totalQuestions)` (unanswered = wrong), `final_score` per `quizengine.FinalScore`, `status='selesai'`; idempotent (already `selesai` → no-op success); publish finish event + final ranking |
| `POST /quiz/:code/visibility` | body `{kind}`; `quizengine.ShouldRecord` gate → insert `anti_cheat_events` → publish to the **teacher** topic only |
| `GET /quiz/:code/stream` | `realtime.Handler` with topic = student topic; `auth` callback validates session + membership |

`qorder` is written **once** at start (global: at START in Step 11; per-question: at
`/start`) and never regenerated.

**Tests** `tests/integration/join_matrix_test.go` — a table over every
`quiz.status × join_mode × prior-attempt` combination asserting the exact response code
and message (incl. `nonaktif` → `404 NOT_FOUND`, `selesai` → `410 QUIZ_ENDED`,
removed → `403`, rejoin of a `registered`/`started` row → `200`), plus invite-URL
auto-join cases (`GET /quiz/:code` unjoined: open creates `registered`, approve creates
`pending`, `berjalan` renders the exact in-progress message); plus
`flow_persoal_test.go` basics (start idempotency, answer upsert idempotency,
answer-after-close → `410`).

**Done when:** `go test ./tests/... -race -count=1` green.

**Protocol:** re-read `student.go`; confirm every mutation is `BEGIN → write → COMMIT →
publish` and that `publish` is strictly after `COMMIT` → record `step 10: PASS/FAIL`.

---

### Step 11 — Global timer flow (waiting room, START, linear mode, STOP/timeout)

**Create** `internal/handlers/global.go`.

| Route | Behaviour |
|---|---|
| `GET /teacher/quiz/:id/monitor` | live monitor page: waiting room list + student cards |
| `POST /teacher/quiz/:id/participants/:pid/action` | `{action: "approve"\|"reject"\|"remove"\|"cheat_toggle"}`. Approve requires `participants.status='pending'` **and** `quizzes.status='aktif'` in the same `UPDATE`; 0 rows → `409` (already handled / START happened). Reject sets `status='dikeluarkan'` with `final_score=NULL`. Remove while `started` → snapshot `score_auto` first (unanswered = wrong) then `status='dikeluarkan'`. `cheat_toggle` flips `participants.cheating` idempotently |
| `POST /teacher/quiz/:id/start` | requires `status='aktif'`; single transition `UPDATE quizzes SET status='berjalan', started_at=NOW() WHERE id=? AND status='aktif'` → 0 rows = someone else started, return `409`. For every `registered` participant: write `qorder`, set `ends_at = started_at + total_seconds`, `status='started'`. **Pending approvals arriving after this point are rejected** (the approve guard above checks `quizzes.status='aktif'`). If `timer_on=0` → `ends_at=NULL`. Rows still `pending` at START → auto-rejected in the same transaction (`status='dikeluarkan'`, `final_score=NULL`; spec §6.12 sync — settles pre-START stragglers; approve attempts after START already return `409`). **Zero participants allowed** (spec §8: START with 0 proceeds after the confirmation modal shows 0). Publish `start` event with `{started_at, total_seconds, ends_at}` to both topics |
| `POST /teacher/quiz/:id/stop` | body `{confirm:true}` required → `400` otherwise. Runs the shared close routine (below) |
| timeout watcher | one goroutine in `main`, tick every 1 s; for each quiz with `status='berjalan'` and `timer_on=1` and `NOW() >= started_at + total_seconds` → run the shared close routine |
| disconnect watcher | same 1 s goroutine: any `started` participant with no SSE heartbeat for 15 s → `OnDisconnect` freeze (`ends_at` = **last heartbeat second**, persist `remaining_seconds`) + monitor badge `connection lost`; on SSE resubscribe → `ends_at = now + remaining_seconds`. **Applies to BOTH timer types** (spec §6.6) |

**Shared close routine** `closeQuiz(ctx, db, quizID uint64, reason string) (closed bool, err error)`:
```
UPDATE participants SET status='selesai', finished_at=NOW(), score_auto=<computed>,
       final_score=<computed> WHERE quiz_id=? AND status='started'
UPDATE quizzes      SET status='selesai' WHERE id=? AND status='berjalan'
```
Both inside **one transaction**; `rowsAffected==0` on the second statement → another
caller won → return `closed=false` without publishing. On success publish `force_stop`
with `{reason}` (`"stop"` / `"timeout"` / `"all_finished"`) to both topics.
`reason` is included in the event so the client can word the closing notice correctly.
**Timeout does not require confirmation** (spec §6.6); STOP and `all_finished` do.

`all_finished` detection: in the same 1 s watcher, if every `started` participant has
`finished_at IS NOT NULL` → publish `all_finished_pending` **once** (dedupe with an
in-memory `map[uint64]bool` cleared on close), and the teacher page shows the modal
**"All students have finished — end the quiz?"** → `POST /stop {confirm:true}`.

**Boot rehydrate** `rehydrateOnBoot(ctx, db, hub, store)` called from `main` **before**
`e.Start` (spec §6.3):
1. `SELECT id FROM quizzes WHERE status='berjalan'` → for each: participants with
   `ends_at <= NOW()` → auto-finish in one transaction (score over the FULL question
   count, unanswered = wrong); warm `quizstate:<id>` mirrors; rebuild the `Ranker` from
   `answers` so the first monitor render is already correct.
2. Per-question participants (`status='started'`) with past `ends_at` → auto-finish too
   (spec §6.3.4 "expired-while-down").
3. Pending approvals, cheat flags and scores live in DB — read on first monitor render,
   no extra warm state needed.
4. Clients auto-reconnect via `EventSource` retry → the first event on each connection is
   a `snapshot` for that topic (current question, `ends_at`, ranking, cheat badges,
   pending count) — timers do **not** reset.

**Linear mode client** `web/js/workspace.js`:
- on `/answer` response with `preview != null` → render ✓/✗ + key for 2.5 s → advance
  automatically; `preview == null` → advance immediately
- on `force_stop` → stop timers, POST nothing, navigate to the result/summary view
- countdown derived from `ends_at` received at start, **re-synced on every heartbeat**

**Tests** `tests/integration/flow_global_test.go`:
- open + approve → waiting room → START → students get identical `ends_at`
- approve arriving **after** START → `409`
- join after START → `409` with the exact in-progress message
- STOP twice concurrently (two goroutines) → exactly one `closed==true`, one `409`
- STOP vs timeout race → one close, one no-op, quiz ends `selesai` exactly once
- timeout with 3/10 answered → `score_auto` computed over **10** questions
- `all_finished` → modal payload → confirm → `selesai`
- START with **zero** participants → allowed (200) → quiz `berjalan`
- a row still `pending` at START → auto-rejected (`dikeluarkan`, `final_score=NULL`);
  approve attempted after START → `409`; that student rejoining → `403`
- global disconnect: freeze at the **last heartbeat** (not detection time) → reconnect
  resumes with no time lost (spec §11.16)
- rehydrate: seed an `berjalan` quiz with an expired participant + a live one →
  `rehydrateOnBoot` auto-finishes the expired one (unanswered = wrong) and preserves the
  live one's `ends_at`/ranking (spec §6.3, §11.15)

**Done when:** `go test ./tests/... -race -count=1` green.

**Protocol:** re-read `global.go`; confirm all three end-triggers funnel through
`closeQuiz` and that the `WHERE status='berjalan'` guard is inside the transaction →
record `step 11: PASS/FAIL`.

---

### Step 12 — Per-question flow + close-with-modal

**Create** `internal/handlers/personal.go`.

- Student may Start **any time** while `quizzes.status='aktif'` (not `berjalan` — this
  timer type never enters `berjalan`). Quiz stays `aktif` until the teacher closes it.
- Per-question close = `POST /teacher/quiz/:id/status {status:"selesai"}` — the quiz
  moves `aktif → selesai` (spec §6.5 diagram; **never** back to `nonaktif`, which is the
  pre-activation state). When participants are `started`: respond `409` with
  `{"working": N}` so the client can show the modal **"N students are still working —
  close anyway?"**; the confirm sends `{status:"selesai", confirm:true}` → runs the same
  close routine with `reason="teacher_close"`; registered-but-never-started rows →
  `status='selesai'`, `final_score=NULL` ("not attempted", excluded from ranking).
- Guard: edit still requires `status='nonaktif'` **and** zero participants (Step 8) —
  closing does not make an attempted quiz editable.
- Attempt guard: starting when a previous attempt row is still `started` → `409`;
  `attempt_no > max_attempts` → `409 ATTEMPT_LIMIT`.
- Timer expiry exactly at submit: `ends_at` checked **inside** the answer transaction →
  auto-finish that participant and return `410`.
- Disconnected participant: the **shared disconnect watcher (Step 11 — both timer types,
  spec §6.6)** freezes `ends_at` at the last heartbeat second and persists
  `remaining_seconds`; on SSE resubscribe `OnReconnect` sets `ends_at = now + remaining`.
  Monitor shows a `connection lost` badge meanwhile.

**Tests** `tests/integration/flow_persoal_test.go` — start idempotency; jump/back
navigation persists different `current_q`; timer-expired-at-submit → `410` + auto-finish;
close with 2 working + 1 registered → modal payload `{working:2}` → confirm → both
working scored, registered one `final_score=NULL`; attempt-limit enforcement; disconnect
→ freeze at last heartbeat → reconnect resumes with no time lost (spec §11.16); close
lands the quiz on `selesai` exactly once (never `nonaktif`).

**Done when:** `go test ./tests/... -race -count=1` green.

**Protocol:** re-read `personal.go`; confirm `registered` never-started participants are
excluded from the `Ranker` → record `step 12: PASS/FAIL`.

---

### Step 13 — Live monitor + anti-cheat UI

**Create** `web/js/monitor.js` + monitor template fragments.

Student card fields (spec §7 monitor route): current question number, dwell time on the
current question (`current_q_since`), selected answer (PG updates **on click**; essay on
Next), connection state, violation badge, timer remaining.

Buttons — **hidden until a violation exists** (spec §6.9):
```html
<button data-act="cheat_toggle" class="btn btn-outline-danger">Mark as cheating</button>  <!-- toggle -->
<button data-act="remove"      class="btn btn-danger" data-bs-toggle="modal"
        data-bs-target="#confirmRemove">Remove</button>
```
- `Remove` **always** opens `#confirmRemove` — a Bootstrap modal requiring an explicit
  Confirm click (spec: modal mandatory to avoid accidental presses). The modal body shows
  the student's name.
- `Cheat toggle` posts directly (no modal) and reflects `participants.cheating`.
- Both buttons render only after the first `cheat_event` for that card arrives over SSE.

Anti-cheat capture `web/js/anti-cheat.js`:
```js
document.addEventListener('visibilitychange', () => { if (document.hidden) report('minimize'); });
window.addEventListener('blur', () => report('blur'));
// sleep: setInterval 5000; if Date.now() - last > 30000 → report('sleep')
```
`report(kind)` POSTs `/quiz/:code/visibility` with a **client-side 10 s debounce per
kind** (server still enforces its own 10 s collapse). The client emits `minimize`
(visibilitychange), `blur` (window blur), `sleep` (gap > 30 s); the server validates
`kind` against the full §5 ENUM `blur|minimize|switch|sleep` and integration tests POST
every kind directly — `switch` is accepted and logged like the rest.

Server push: insert into `anti_cheat_events` → publish `Event{Type:"cheat", Data:{participant_id, kind}}`
to the **teacher** topic only.

**Tests** `tests/integration/anticheat_test.go`: POST visibility → row exists → teacher
SSE receives `cheat` → buttons' visibility predicate (`violation count > 0`) flips;
second identical event within 10 s creates no new row; post-`selesai` event is ignored;
`cheat_toggle` twice leaves `cheating=1` (idempotent per spec "toggle" is re-appliable —
assert exactly two transitions, not three).

**Done when:** `go test ./tests/... -race -count=1` green.

**Protocol:** re-read `monitor.js`; confirm both buttons are gated on violation state in
the DOM, not merely styled invisible → record `step 13: PASS/FAIL`.

---

### Step 14 — Results, essay grading, CSV + XLSX export

**Create** `internal/handlers/results.go`, `internal/handlers/export.go`,
`views/teacher/results.html`, `views/teacher/grading.html`.

`GET /teacher/quiz/:id/results` — participant list (name, `final_score` or "Awaiting
grading", status flags finished/removed/cheating) + per-question analysis: % correct /
wrong / unanswered and the most-chosen option per question (SQL over `answers` joined to
`participants` on `quiz_id`, using `idx_part_monitor` + `uq_ans`).

`GET /teacher/quiz/:id/grading` — list essay answers with `is_correct IS NULL`;
`POST /teacher/quiz/:id/grading/:answerId` body `{score}` → set `answers.score`,
`answers.is_correct = NULL` (essay stays ungraded-by-rule), recompute
`participants.essay_score` (mean of graded essay scores ×100/possible) and
`participants.final_score = score_auto + essay_score`; idempotent.

`GET /teacher/quiz/:id/results/export?format=csv|xlsx`:
- unknown/missing-but-invalid `format` → `400 VALIDATION`
  (default when the param is **absent** is `csv`; any other value is an error)
- row type shared by both writers:
  ```go
  type exportRow struct {
      Rank int     `csv:"Rank" json:"rank"`
      Name string  `csv:"Name" json:"name"`
      Username string `csv:"Username" json:"username"`
      Class string `csv:"Class" json:"class"`
      Major string `csv:"Major" json:"major"`
      Score float64 `csv:"Score" json:"score"`
      Status string `csv:"Status" json:"status"`   // finished / removed / not-attempted
      Cheating string `csv:"Cheating" json:"cheating"` // yes / no
      Attempts int `csv:"Attempts" json:"attempts"`
  }
  ```
- CSV: `gocsv.Marshal(rows, c.Response())` with `Content-Type: text/csv; charset=utf-8`
  and `Content-Disposition: attachment; filename="quiz-<code>.csv"`.
  **Injection guard:** prefix `'` to any string value starting with `=`, `+`, `-`, `@`.
- XLSX: `f := excelize.NewFile()` → user text via `f.SetCellStr(sheet, cell, v)` after the
  `guardValue` prefix so nothing user-supplied is ever parsed as a formula; numbers via
  `SetCellValue`; computed cells (Rekapitulasi metrics + Pertanyaan TOTAL, cross-sheet `Murid!`/`Pertanyaan!`) via `SetCellFormula`;
  header row via `SetCellStr`, then `f.Write(c.Response())` with
  `Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`.
  Also apply the `=`/`+`/`-`/`@` prefix for parity. Empty result set → header-only file
  in both formats.
- Streaming: set headers, `c.Response().WriteHeader(200)`, then write — never buffer the
  whole workbook unless excelize requires it (`f.Write` to the response writer is fine at
  this scale).

**Tests** `tests/integration/export_test.go`:
- CSV: parse with `encoding/csv`, assert headers and one row; assert an injection value
  `=SUM(A1)` appears as `'=SUM(A1)`
- XLSX: write to a buffer, reopen with `excelize.OpenFile`-equivalent on a temp file,
  read cells back with `GetCellValue`, assert string cells (not formulas) and identical
  values to the CSV rows
- `?format=bogus` → `400`
- empty participants → CSV has header only; XLSX has header row only

**Done when:** `go test ./tests/... -race -count=1` green; manual export opens cleanly in
a spreadsheet app.

**Protocol:** re-read `export.go`; confirm `SetCellValue`/`SetCellFormula` are absent from
the file → record `step 14: PASS/FAIL`.

---

### Step 15 — History, profile, riwayat settings matrix

**Create** `internal/handlers/history.go`, `views/student/history.html`,
`views/student/profile.html`.

| Route | Behaviour |
|---|---|
| `GET /history` | every attempt row for the user, ordered newest first; per-quiz settings decide displayed fields: `show_final_score` → score column; `ranking_live` → rank column; quiz title always shown |
| `GET /history/:id` | `quizengine.BuildHistory(question_review, …)` — `none` → no questions; `text` → question + own answer + ✓/✗; `full` → plus correct key. Row ownership checked (`participants.user_id == session user`) else `403` |
| `GET /profile`, `POST /profile/edit` | edit full name, class (`kelas_id`), major (`jurusan_id`); username/email immutable (spec lists only those three) |

**Tests** `tests/integration/riwayat_test.go` — the full matrix from spec §9:
`{none,text,full} × {show_final_score on/off} × {ranking_live on/off}` = 12 cases,
each asserting the rendered data (score present/absent, rank present/absent, question
count 0 / N-without-key / N-with-key). Multi-attempt: all attempts listed, ranking uses
the highest score.

**Done when:** `go test ./tests/... -race -count=1` green.

**Protocol:** re-read `history.go`; confirm the ownership check exists on
`/history/:id` → record `step 15: PASS/FAIL`.

---

### Step 16 — Final verification

Run, in order, and record each:

1. `go vet ./...` → clean.
2. `go test ./... -race -count=1` → all green, no skipped tests except the documented
   DB-unreachable skip (and that must not trigger: `docker compose up -d` first).
3. `golang.org/x/vuln/cmd/govulncheck ./...` → **no known vulnerabilities**; assert
   `go list -m golang.org/x/crypto` ≥ v0.55.0 and `github.com/xuri/excelize/v2` ≥ v2.11.0.
4. `docker compose down -v && docker compose up -d --build` from clean state →
   `curl -f http://localhost:8090/login` returns HTML.
5. **Manual end-to-end on `http://localhost:8090`:**
   - register a student → sign in as teacher (`GURU_USER`/`GURU_PASS` from `.env`)
   - create 3 questions (1 pg, 1 multi, 1 essay) → compose into a quiz with
     `length=short` filter → activate
   - start a **global** quiz; second browser profile joins → waiting room → approve →
     START → student answers → preview appears → ranking updates live
   - teacher marks cheating on a card → confirm the two buttons appear only after the
     violation → Remove opens a confirmation modal
   - STOP → results → essay grading → export CSV **and** XLSX → open both
   - create a **per-question** quiz → student Starts → jump between questions → teacher
     closes while 1 student works → modal shows the count → confirm
   - forgot-password: student submits → teacher opens `/teacher/password-resets` →
     approve → student's next login lands on change-password → new password works
   - QR + invite URL: teacher opens `/teacher/quiz/:id/qr` → second profile opens the
     encoded `/quiz/:code` URL while unjoined → auto-join lands in the waiting room
   - teacher manually adds a student via `participants/add` → that student sees the quiz
   - toggle dark mode; confirm the blue palette with unified semantic accents in
     both themes; confirm no stock Bootstrap primary anywhere
6. Restart proof: `docker compose restart app` **during** a running quiz → reload →
   status, remaining time, ranking, cheat flags and pending approvals all restored.
7. `git status` → no `.env`, no `data/` staged; `.gitignore` covers both.

---

## Critical files & anchors

| File | Why it's non-obvious |
|---|---|
| `internal/realtime/hub.go` | `sse.Server.Publish` blocks on slow clients (verified joe.go:242-250); must use `Upgrade` + `Joe` + `guardedWriter`. Also: `EventSource` can't send the `Subscribe` header, so topics come from the URL path, and teacher/student need **separate** topics per quiz. |
| `internal/config/config.go` | `env-default` + `env-required` cannot be combined on one field (required-check runs first and would error). `DB_HOST` must be absent from `.env` for compose's `environment:` to survive cleanenv's unconditional `os.Setenv`. |
| `migrations/0001_init.sql` | Must match spec §5 verbatim — no FKs, exact ENUM literals, `UNIQUE(participant_id, question_id)`, `participants.qorder` JSON. A drift here silently breaks Steps 10-15. |
| `internal/handlers/global.go` | Three end-triggers (STOP / timeout / all-finished) must race safely through one `WHERE status='berjalan'` update inside a transaction; unanswered = wrong over the **full** question count. |
| `internal/quizengine/score.go` | Integer half-up rounding `(correct*10000*2 + total) / (2*total)`; float accumulation would drift on `5/7 → 71.43`. |
| `internal/handlers/renderer.go` | `ParseGlob` cannot cross `/`; all pages share ONE template set, so page defines must be unique and content is bracketed by `head`/`foot` partials — a shared `content` define would silently collide (last parse wins). |

---

## Verification

Prerequisites for every command: Docker running, `docker compose up -d` (app on
`http://localhost:8090`, MariaDB on `127.0.0.1:3306`), repo root as working directory.

```bash
go vet ./...                                   # lint, no output
go test ./... -race -count=1                   # unit + integration, all green
govulncheck ./...                              # no known vulnerabilities
docker compose up -d --build                   # clean build
curl -f http://localhost:8090/login            # HTML login page
```

**New-behavior proof (not just a green suite):**
1. `POST /join` with a quiz whose global timer already started returns
   `{"ok":false,"error":"QUIZ_IN_PROGRESS","message":"Quiz is in progress, you cannot join."}` —
   assert the exact string, not just a 409.
2. A quiz of **10** questions, STOP after 7 answered (5 correct, 2 wrong) yields
   `score_auto = 50.00` — proves unanswered-is-wrong over the full count.
3. Slow-SSE client test: `Publish` returns in <2 s while a non-draining subscriber is
   attached, and that subscriber's connection is closed.
4. Export: reopen the produced `.xlsx` with excelize and read cells back — values match
   the CSV rows, every cell is a string (no formula).
5. `docker compose restart app` mid-quiz → reconnect restores `ends_at`, ranking, cheat
   flags and pending approvals.

---

## Assumptions & contingencies

- **`.env` absent on a clean clone:** the Dockerfile falls back to `.env.example`
  (values `guru` / `changeme`), so §11.19 "works from a clean machine" holds. For local
  `go run`, the developer runs `cp .env.example .env` — `config.Load` fails with
  `read config .env: no such file` if they don't. **If instead the team wants the app to
  boot without `.env` at all, add a `LoadOrDefault()` path — but that silently accepts
  empty credentials, so the fallback here is a hard error by design.**
- **Two topics per quiz** (`quiz:<id>` for students, `quiz:<id>:teacher` for the monitor)
  is the chosen form of role separation; if `sse.Topic` types get awkward, the fallback is
  a single topic with the handler dropping events whose `Type` starts with `teacher.` for
  student subscribers. Prefer two topics.
- **MariaDB healthcheck** relies on `healthcheck.sh` shipping in `mariadb:12`. If it is
  absent, replace with `test: ["CMD", "mariadb-admin", "ping", "-h", "localhost",
  "-uroot", "-p$$MARIADB_ROOT_PASSWORD"]`.
- **Test DB reachability:** integration tests skip with a clear message if
  `127.0.0.1:3306/quiz_test` is unreachable. If 3306 ever becomes occupied, set
  `DB_PORT` in `.env` (compose `ports` must be updated to match) — do not silently point
  tests at another database.
- **`go:embed` cannot use `..`**, hence `migrations/embed.go` lives at the repo root
  beside `migrations/*.sql`; `internal/db` imports `quiz/migrations`. Do not move it.
- **`quiz_test` creation:** `docker/mysql-init/001-testdb.sql` runs only on the FIRST
  boot of an empty `./data/mariadb`. If a pre-existing data dir lacks `quiz_test`, stop
  db, delete `./data/mariadb`, `docker compose up -d db` again — the bind mount means
  `down -v` does NOT clear it.
- **Commits:** none during execution — the spec (§10) makes commits ask-first. After
  Step 16, ask before committing; when approved use
  `-c user.name="Zan" -c user.email="zan@local"` and never stage `.env` or `data/`.
- **Cache scope:** every row of the spec §6.2 table is implemented via the Step 5
  registry (session 5 min, quizstate 3 s, lists/history 10 s, settings 30 s, refs 60 s,
  config process-lifetime); invalidation always after COMMIT, before broadcast.

---

## Design decision 2026-09-27 — teacher quiz management UX rework
- **Structure: keep the single manage route `GET /teacher/quiz/:id` with a tabbar — tabs `Questions | Settings | Participants`.** Rejected the per-route split: spec §7 pins this route, tabs are zero-cost client-side switching, and a split would multiply SSR routes/breadcrumbs without UX gain. Cross-page content moved out instead: Results → its own page (single surface, reachable from the header button; the duplicate inline tab was removed), Share → header action opening a modal (QR + join URL + join code in one place; QR no longer buried in the Questions tab).
- **Questions management = table + selector modal.** Composed questions render as table rows (# / question / type / remove) with server-side search (`?qq` SSR param + live-search region swap; client-side filter deleted); adding happens via the "Add from question bank" modal (live search, client-side length filter, "In quiz" state, one JSON POST per pick, no checkboxes, no "Add selected"). Removal uses `POST /teacher/quiz/:id/questions/:qid/delete` (nonaktif only).
- **Create flow:** successful creation redirects to `/teacher/quiz/{id}` (`data-next="/teacher/quiz/"` + `data-next-id="id"` against runFetch's existing branch).
- **POST 400 root cause:** `net/http`'s `ParseForm` never reads `multipart/form-data` bodies, so the browser's FormData submit always hit "Choose at least one question." Fix: content-type-aware decode (JSON / multipart / urlencoded) in `composeForm`, with a multipart integration regression test.
- **Error/edge handling:** compose 404s on a missing quiz (schema has no FKs), duplicates return 409 and the modal reconciles to "In quiz", network failures surface toasts (runFetch/runPost try/catch), the active tab survives save/reload via `location.hash`.
- **Bootstrap:** offline 5.3.8 dist at root `bootstrap/`, served at `/bootstrap` (spec §2 updated).

---

## Design decision 2026-09-27 — client-side asset caching
- **Strategy: per-file content-hash `?v=` + immutable, with a `no-cache`+`ETag` fallback and `no-store` for SSR.** Boot-time `Fingerprints` maps every file under the four mounts to the first 12 hex chars of its SHA-256; templates emit `{{asset "/…"}}` → `path?v=<hash12>`, so the URL changes exactly when content changes. Matching `?v=` → `Cache-Control: public, max-age=31536000, immutable`; missing/wrong `v` on a known asset → `no-cache` + `ETag: "<hash12>"` with RFC 9110 `If-None-Match` (list / `*` / `W/`) short-circuiting a body-less `304` — this row also serves CSS-internal relative font URLs; path under a mount but unregistered → `no-cache` without ETag (static handler 404s); everything else → `no-store`, so SSR HTML/JSON/SSE/QR is always fresh. Rejected: service worker (extra invalidation surface for an app with no build step), build-step hashing (no Node/npm by design), new dependency (stdlib `crypto/sha256` suffices).
- **Key files:** `cmd/server/main.go` — single-source `staticMounts` (`/assets`, `/css`, `/js`, `/bootstrap`) feeding mounts + fingerprint registry + middleware, chain Recover → StaticCache → LoadSession; `internal/middleware/assets.go` (`StaticMount`, `Fingerprints`, `AssetURL`, `StaticCache`); `internal/handlers/renderer.go` — `asset` template func (unknown path passthrough); `views/**` — all 34 static href/src refs via `{{asset …}}`; `web/js` has no hardcoded mount URLs (if one appears later, the `no-cache`+`ETag` row covers it).
- **Verification hooks:** `internal/middleware/assets_test.go` (`TestFingerprints`, `TestAssetURL`, `TestStaticCache` — 7 subtests: matched-v immutable / missing-v no-cache+ETag / wrong-v / 304 hit / 304 miss / unknown-asset no-ETag / non-static `no-store`); `tests/integration/auth_test.go` `testAssets` helper keeps test-rendered pages on real `?v=` fingerprints (parity with production, mirrors the `staticMounts` list).

---

## Design decision 2026-09-28 — blue palette retheme + Google Font
- **Blue palette replaces the monochrome scheme, dark derived from light.** Mandated light hexes: body `#F4F8FD`, ink `#0F2240`, brand primary/link `#2C5EAD` (rgb 44,94,173), secondary `#47608C`; derived dark tokens: body `#0B1A30`, ink `#D7E8F8`, primary `#1591DC`, links `#4BB8FA`, secondary-bg `#16304F`, tertiary/sidebar (always dark) `#10233F`; supporting neutrals: structural border `#C4E2F5`, interactive control borders `#6B8BB7`, info `#4BB8FA`.
- **Semantics unified across both themes:** success `#0E7A46`, danger `#C93128`, warning `#A15C0B` (dark on-surface tints via `--bs-success-text` etc.); contrast checked with computed ratios — body ink 14.88:1 on the light bg, links 5.95:1, buttons ≥5.08:1 in dark.
- **Bootstrap-only mechanism:** 100% `--bs-*` property overrides in `web/vendor/theme.css` — no Sass build, no edits to the vendored dist files, no standalone component CSS (spec §11.20 rule unchanged, only values). Component bridges re-declared var-driven: buttons (primary/outline/secondary/danger/success/warning), form focus, pagination, list-group, nav pills/tabs, `.text-ok/.text-bad/.bg-ok/.bg-bad`, `.text-bg-warning` white label, dark `.text-bg-secondary` label, focus halos at alpha .8.
- **Google Font:** Noto Sans Cypro Minoan 400 from Google Fonts (preconnect ×2 + `css2?…&display=swap` stylesheet in the three head partials) — the sanctioned CDN exception and the site's only external request; applied through `--bs-body-font-family` / `--bs-font-sans-serif` in `:root` so both themes inherit it. The family covers the Linear A syllabary — Latin text falls back to `sans-serif` (documented as-is, not a defect).
- **Cache interplay:** the Google Fonts stylesheet keeps Google's default CDN caching (outside the app's fingerprint system); local static assets keep the SHA-256 `?v=` fingerprints + `StaticCache` policy unchanged (spec §6.13).
