# Thesis Bug Defense Report — Quiz Webapp — Hunter B

> Hunter B (ACADEMIC / METHODOLOGICAL / LOGICAL CORRECTNESS). Klaim-vs-bukti, metodologi, logika, konsistensi dokumen. Tidak menduplikasi sudut implementasi Hunter A; tidak ada perubahan kode produksi.

## 1. Investigation Context

- Project: quiz — server-rendered quiz/exam webapp (guru/murid), single Go binary (`module quiz`, Go 1.26.6, Echo v5.3.1, MariaDB 12, `html/template`, SSE via `tmaxmax/go-sse`)
- Thesis: `docs/superpowers/specs/2026-09-26-quiz-website-design.md` — approved spec (skema §5, perilaku §6–§8, testing §9, boundaries §10, success criteria §11)
- Scope: klaim success criteria §11 vs perilaku kode yang dapat diuji; klaim testing §9 vs tests aktual di `tests/`; klaim arsitektur §6 vs implementasi; klaim batasan §10 vs fakta repo; kebahasaan Indonesia baku (KBBI) di UI; konsistensi internal dokumen (§12 "None" vs realita)
- Repository: `D:/Project/Mother/quiz` (branch `main`)
- Methodology: adversarial review — klaim vs bukti, reproduksibilitas, konsistensi spec↔implementasi; hierarki bukti: berkas aktual > kutipan spec persis; tidak ada nomor section/baris yang dikarang — semua dikunci ke hasil `read`/`grep`/`bash`
- Technology: Go 1.26.6, Echo v5.3.1, MariaDB 12, `html/template`, SSE (bukan WebSocket), cache in-process TTL mirror, `html/template` + vanilla JS
- Defense standard: academic — "Can the thesis author scientifically and technically defend the claim?"
- Citation format: `path-relatif:rentang-baris` + kutipan kode + cara verifikasi (`read` / grep / `get_code_snippet`)
- Language: Bahasa Indonesia
- Completion: Open Questions = 0; setiap finding berverdict CONFIRMED / PARTIALLY CONFIRMED / DISPUTED / UNPROVEN / REJECTED
- Index: `mcp__codebase_memory_mcp_list_projects` → project `D-Project-Mother-quiz` terdaftar; `mcp__codebase_memory_mcp_index_status` → status `ready`, 5136 nodes / 19372 edges, indexed 2026-10-07T08:10:16Z; `index_repository` TIDAK dijalankan (indeks segar). Arsitektur dipetakan via `mcp__codebase_memory_mcp_get_architecture` (overview/structure).

## 2. Executive Summary

Lima temuan akademik/metodologis, semuanya terverifikasi terhadap berkas aktual (bukan inferensi):

1. **F-B-001 (HIGH)** — Klaim "closed dependency list — exactly 8 direct modules" (§2, diulang §10/§11.21) gugur: `go.mod` hanya mendeklarasikan 3 modul direct (`echo/v5`, `go-sql-driver/mysql`, `cleanenv`); lima sisanya (`gocsv`, `excelize/v2`, `go-qrcode`, `go-sse`, `x/crypto`) tercatat `// indirect`. Lebih tajam: `gocsv` — yang diklaim sebagai mesin ekspor CSV (§2) — tidak diimpor oleh satu pun berkas Go (ekspor CSV memakai `encoding/csv` stdlib). Sebaliknya ada dependensi runtime ke-9 yang tidak diakui spec: `Chart.js v4.5.1` yang divendor di `web/vendor/chartjs/` dan dimuat dashboard guru.
2. **F-B-002 (MEDIUM)** — Klaim metodologi testing §9 tidak didukung penuh: "parallel-safe" tanpa satu pun `t.Parallel` di seluruh `tests/`/`internal/`; "no `time.Now` in logic" hanya benar di `quizengine`, sementara handler memanggil `time.Now()` langsung (auth, global, live, student); `tests/unit/authlogic_test.go` sendiri mengakui cakupan forgot-password/join-code retry "covered by integration", padahal tabel §9 mengklaimnya sebagai cakupan unit; kriteria "fully green" (§11.14) bersifat kondisional karena helper test me-`Skip` saat DB tidak terjangkau.
3. **F-B-003 (MEDIUM)** — Skema §5 (yang disebut "living spec", §10 mewajibkan sinkron) drift dari migrasi aktual: kolom `users.aktif` (migrasi `0003`, dienforce di `auth.go` + `middleware/session.go`) tidak ada di blok `users` §5; `timer_type` §5 mencantumkan 3 nilai sekaligus sementara `0001_init.sql` hanya punya 2 (`tanpa_timer` baru lahir di `0004`).
4. **F-B-004 (MEDIUM)** — Klaim "All UI text in bahasa Indonesia baku (KBBI)" (§1, §11.21) tidak falsifiable dan salah secara bukti: literal ENUM Inggris (`pending`, `registered`, `started`, …) dirender mentah ke murid (`history.html`) dan guru (`monitor.html`); header monitor menempelkan kata Inggris (`{{.TimerType}} timer`, `status`); spec dan komentar kode sendiri memakai label Inggris `"Dashboard"` sementara implementasi memakai `"Dasbor"` — inkonsistensi istilah dokumen↔kode.
5. **F-B-005 (HIGH)** — Kriteria sukses §11.2 memandatkan perilaku yang secara keamanan tidak dapat dipertahankan ("next login skips password validation" — dienforce di `auth.go`: akun `must_change_pw=1` bisa memperoleh sesi tanpa verifikasi kata sandi), dan klaim robustness (§11.16/17: disconnect-freeze, "DB write failure → no broadcast") tanpa uji negatif yang membuktikan kegagalan-tertutup tersebut; digabung dengan §12 "Open Questions: None", dokumen mengklaim ketertutupan yang dibantah oleh temuan F-B-001–F-B-004.

Tidak ada fix/refactor/commit yang dilakukan. Tidak ada berkas produksi yang diubah.

## 3. Bug Hunter Findings

### 3.1 Finding F-B-001

- Title: Klaim "closed 8 direct modules" gugur — manifest hanya punya 3 direct, `gocsv` tak diimpor, Chart.js tak diakui
- Severity: HIGH
- Category: klaim-vs-bukti / konsistensi dokumen / batasan (§2, §10, §11.21)
- Location: `go.mod:5-29`; `internal/handlers/export.go:3-16`; `web/vendor/chartjs/chart.umd.min.js:1-6`; `views/teacher/dashboard.html:72-84,130`
- Claim: Spec §2: "**Closed dependency list** — exactly 8 **direct** modules …: `labstack/echo/v5` · `go-sql-driver/mysql` · `golang.org/x/crypto` · `skip2/go-qrcode` · `tmaxmax/go-sse` · `ilyakaznacheev/cleanenv` · `gocarina/gocsv` · `xuri/excelize/v2`". Diulang §10 ("Stay within the closed 8-module list") dan §11.21 ("no dependencies outside the closed list"). Tabel §2 juga menetapkan peran: "CSV export — `github.com/gocarina/gocsv`".
- Problem: Tiga ketidaksesuaian dalam satu klaim. (a) Hanya 3 modul yang dideklarasikan direct di `go.mod`; 5 sisanya bertanda `// indirect` — jadi frasa "exactly 8 direct modules" salah terhadap manifest build yang menjadi satu-satunya definisi operasional "direct". (b) `gocsv` tidak diimpor oleh berkas Go mana pun — klaim peran CSV-nya tanpa bukti implementasi; ekspor CSV aktual memakai `encoding/csv` stdlib. (c) Ada dependensi runtime ke-9 yang tidak diakui spec mana pun: Chart.js bervendor yang dieksekusi di dashboard guru.
- Evidence:
  - `go.mod:5-9` blok direct hanya berisi `go-sql-driver/mysql v1.10.1`, `cleanenv v1.5.0`, `echo/v5 v5.3.1`; `go.mod:12-24` menandai `gocsv`, `go-qrcode`, `go-sse`, `excelize/v2`, `x/crypto` (plus `efp`, `nfp`, `mscfb`, `deepcopy`, `edwards25519`, …) sebagai `// indirect`. Kutipan: `github.com/gocarina/gocsv v0.0.0-20260908110832-9ab82d65b1cc // indirect`.
  - Pencarian seluruh repo `grep -rn "gocsv" --include="*.go" .` tidak menghasilkan satu pun import — hanya baris `go.mod`/`go.sum`. Sebaliknya `internal/handlers/export.go:3-16` mengimpor `"encoding/csv"` (stdlib), bukan gocsv; struct `exportRow` memang memakai tag `csv:"..."` (gaya gocsv) tetapi ditulis manual.
  - `web/vendor/chartjs/chart.umd.min.js:1-6` header: `Chart.js v4.5.1 … Released under the MIT License`; `views/teacher/dashboard.html:72-84` merender tiga `<canvas id="dash-chart-...">`, `dashboard.html:130` memuatnya via `<script src="{{asset "/assets/chartjs/chart.umd.min.js"}}">`; `web/js/teacher_dashboard.js` menggunakannya untuk grafik per kelas/jurusan/distribusi nilai. Spec §2/§4/§10 tidak pernah menyebut Chart.js; daftar "closed" tidak memuatnya.
- Reproduction / Verification:
  1. `read go.mod` — hitung blok `require (` pertama (3 entri) vs klaim 8 direct.
  2. `grep -rn "gocsv" --include="*.go" .` — verifikasi nol import di kode produksi maupun test.
  3. `read internal/handlers/export.go:1-60` — verifikasi import `encoding/csv` dan fungsi `guardValue`/`exportRows` manual.
  4. `read web/vendor/chartjs/chart.umd.min.js:1-6` + `read views/teacher/dashboard.html:70-135` — verifikasi dependensi JS bervendor yang dieksekusi.
  5. `grep -n "chart\|Chart" docs/superpowers/specs/2026-09-26-quiz-website-design.md` — verifikasi nihil (satu-satunya kemunculan kata "charts" adalah visuals generik di rute `overview`, bukan deklarasi dependensi).
- Expected: Klaim "exactly 8 direct" berarti `go.mod` mencantumkan 8 modul direct, setiap modul yang diklaim perannya (`gocsv` untuk CSV) benar diimpor, dan tidak ada dependensi runtime di luar daftar — karena §11.21 menjadikannya kriteria kelulusan ("no dependencies outside the closed list").
- Actual: Manifest = 3 direct + 5 diklaim-direct yang tercatat indirect; `gocsv` yatim (tidak diimpor); Chart.js adalah dependensi nyata ke-9 yang lolos dari daftar tertutup, tabel §2, struktur §4 (`web/vendor/` di §4 hanya menyebut `bootstrap-icons/` dan `theme.css`), dan batasan §10.
- Impact: Kriteria §11.21 tidak dapat diuji sebagaimana dirumuskan (tidak falsifiable): penguji tidak bisa memutuskan "lulus/gagal" dari klaim yang definisinya bertentangan dengan manifest. Klaim keamanan §2 ("security floors … `govulncheck` must report no known vulnerabilities") ikut melemah untuk artefak yang tidak terdaftar (versi Chart.js tidak dipantau `govulncheck`/`go.mod`).
- Academic Impact: Thesis mengklaim ketertutupan (closed-world) atas dependensi — klaim terkuat dalam dokumen — tetapi buktinya menunjukkan dunia-terbuka: satu dependensi diklaim tanpa dipakai, satu dependensi dipakai tanpa diklaim. Ini cacat kejujuran-metodologis (unsupported claim + omission), bukan sekadar salah ketik.
- Confidence: tinggi — tiga sub-bukti masing-masing direproduksi dari berkas aktual (manifest, import, vendor header + pemuat template).
- Status: SUBMITTED

### 3.2 Finding F-B-002

- Title: Klaim metodologi testing §9 (parallel-safe, no-time.Now, cakupan unit, "7 subtests", fully green) tidak didukung bukti
- Severity: MEDIUM
- Category: metodologi / klaim-vs-bukti (§9, §11.14)
- Location: `tests/unit/authlogic_test.go:1-14`; `internal/testutil/testutil.go:62-77`; `internal/middleware/assets_test.go:83-125`; `internal/handlers/auth.go:118,148`; `internal/handlers/student.go:846,868,970,1091,1198,1410,1675,1773`; `internal/handlers/global.go:243,265,956`; `internal/handlers/live.go:140,143`; `internal/quizengine/timer.go:1-11`
- Claim: Spec §9 Levels: "`tests/unit/` — pure logic, **no DB/HTTP**; injectable fake clock; seeded RNG … Fast, deterministic, parallel-safe." Determinism rules: "inject clock (no `time.Now` in logic), seeded RNG in tests, truncate relevant tables per test → safe to re-run and run sequentially." Tabel §9 mengklaim `authlogic_test.go` mencakup "forgot-password match/mismatch, password validation, generic login message, join-code retry". `assets_test.go` diklaim "header matrix (7 subtests)". §11.14: "`go test ./... -race -count=1` fully green — unit + integration, nothing disabled."
- Problem: (a) Klaim "parallel-safe" + "run sequentially" kontradiktif secara internal, dan buktinya nol: `grep -rn "t\.Parallel" tests/ internal/ --include="*.go"` tidak menghasilkan satu pun pemanggilan — tidak ada test yang paralel, jadi "parallel-safe" tidak pernah dibuktikan. (b) "no `time.Now` in logic" hanya benar di dalam `quizengine` (yang memang mendeklarasikan `Clock` dan komentar "never calls time.Now() directly"); seluruh logika waktu yang menentukan kelulusan (login GC, snapshot `server_now`, freeze/disconnect, `ends_at` transaksi jawab, anti-cheat collapse) memanggil `time.Now()` langsung di handler — seam jam tidak menembus lapisan yang diuji integrasi. (c) Tabel §9 salah mengatribusikan cakupan unit: header `authlogic_test.go` sendiri menyatakan match/mismatch forgot-password dan generic login message "are DB-backed and covered … by tests/integration/auth_test.go" dan join-code retry "covered by tests/integration/quiz_crud_test.go" — jadi baris tabel unit mengklaim bukti yang justru didelegasikan ke integrasi. (d) "7 subtests" pada `assets_test.go` adalah 7 baris tabel (`t.Run` atas 7 `name:` di satu fungsi `TestStaticCache`), bukan 7 fungsi uji matriks header yang mandiri — dapat dipertahankan secara hitungan, tetapi perumusan dokumen melebih-lebihkan granularitas bukti. (e) "fully green … nothing disabled" bersifat kondisional: `testutil.DB()` memanggil `t.Skipf("test database unreachable … start docker compose up -d db")` — seluruh suite integrasi lulus-vakum (skip) tanpa DB, sehingga kriteria kelulusan bergantung pada prasyarat lingkungan yang tidak dinyatakan di §11.14.
- Evidence:
  - `tests/unit/authlogic_test.go:8-14` (komentar berkas, kutipan inti): "(Forgot-password match/mismatch and the generic login message are DB-backed and covered — including their message identity — by tests/integration/auth_test.go; the join-code collision RETRY loop is covered by tests/integration/quiz_crud_test.go.)" — bandingkan dengan baris tabel §9 yang menaruh keempatnya di kolom `authlogic_test.go`.
  - `internal/testutil/testutil.go:62-77` (`func DB`): `if err := pool.PingContext(ctx); err != nil { pool.Close(); t.Skipf("test database unreachable: %v — start docker compose up -d db", err) }`.
  - `internal/middleware/assets_test.go:83-125`: satu `func TestStaticCache` + 7 entri `{name: ...}` ("matched v → immutable", "missing v → no-cache + ETag", "wrong v …", "If-None-Match hit …", "If-None-Match miss …", "unknown static path …", "non-static path → no-store") dieksekusi via satu `t.Run(tc.name, …)` di baris ~125.
  - `time.Now()` di handler: `auth.go:118` (`DeleteExpired(…, time.Now())`), `auth.go:148` (`SessionExpiresAt(time.Now())`), `global.go:243,265` (`"server_now": time.Now().Unix()`), `global.go:956` (`SweepDisconnects(ctx, time.Now(), …)`), `student.go:846,868,970,1091,1198,1410,1675,1773` (ServerNow/ends_at/wall-clock/`current_q_since`/collapse), `live.go:140,143` (heartbeat/disconnect), vs `quizengine/timer.go:6-11` yang murni ("take explicit times … never calls time.Now() directly").
- Reproduction / Verification:
  1. `grep -rn "t\.Parallel" tests/ internal/ --include="*.go"` — nihil output (klaim parallel-safe tanpa bukti).
  2. `grep -rn "time\.Now()" internal/handlers internal/realtime internal/cache cmd --include="*.go"` — daftar di atas vs `read internal/quizengine/timer.go:1-43`.
  3. `read tests/unit/authlogic_test.go:1-30` — baca komentar delegasi vs baris tabel §9.
  4. `read internal/middleware/assets_test.go:83-130` — hitung 7 baris tabel dalam 1 `t.Run` loop.
  5. `read internal/testutil/testutil.go:50-85` — verifikasi `Skipf` gate.
- Expected: Setiap frasa metodologi (§9) menunjuk ke bukti yang benar jenisnya: klaim unit → test unit; klaim parallel-safe → `t.Parallel` yang lolos `-race`; klaim no-wall-clock → seam jam di semua cabang yang menentukan timer; klaim fully green → tanpa gerbang skip yang mengubah arti "green".
- Actual: Satu klaim paralel tanpa satu pun test paralel; seam jam berhenti di batas `quizengine`; tabel unit mengklaim bukti integrasi; "green" mencakup "skip massal saat DB mati".
- Impact: Test-quality bar §9 ("every branch … has a test that fails when the code is wrong") tidak dapat diaudit dari dokumen: pembaca tidak bisa memetakan klaim→berkas→cabang secara andal. Risiko akademik: keberhasilan §11.14 dapat dilaporkan "green" pada mesin tanpa database — green yang vakum.
- Academic Impact: Ini cacat metodologi (validitas konstruk): instrumen pengukuran (suite) tidak mengukur apa yang diklaim dokumen (paralelisme, determinisme penuh, cakupan per-level). Defense yang jujur harus menurunkan klaim ke yang terbukti.
- Confidence: tinggi untuk (a)/(b)/(c)/(e) — bukti berkas langsung; sedang untuk (d) — soal perumusan, hitungan 7 baris memang ada.
- Status: SUBMITTED

### 3.3 Finding F-B-003

- Title: Skema "living spec" §5 drift dari migrasi aktual — `users.aktif` dan riwayat `timer_type` tak tercatat
- Severity: MEDIUM
- Category: konsistensi internal dokumen / living spec (§5, §10, migrasi)
- Location: `migrations/0003_users_aktif.sql:1-8`; `migrations/0001_init.sql:20-32,44-60`; `migrations/0004_timer_type_tanpa.sql:1-6`; `internal/handlers/auth.go:89-115`; `internal/middleware/session.go:77-93`
- Claim: Spec §5 mendefinisikan tabel `users` (kolom: id, username, email, password_hash, nama_lengkap, kelas_id, jurusan_id, must_change_pw, created_at — tanpa `aktif`) dan `timer_type ENUM('global','per_soal','tanpa_timer')`. Spec §10 mewajibkan "Numbered migrations in `migrations/`; keep schema in sync with §5 (living spec)" dan setiap perubahan skema masuk "Ask first".
- Problem: (a) Kolom `users.aktif TINYINT(1) NOT NULL DEFAULT 1` ada di database nyata (migrasi `0003`) dan dienforce di dua titik auth (login menolak akun nonaktif; middleware mematikan sesi berjalan saat akun dinonaktifkan) — tetapi tidak ada di §5. Fitur "Manage Akun Murid / deactivation" adalah perilaku user-visible tanpa jejak di thesis. (b) Kronologi `timer_type`: `0001_init.sql:47` hanya `ENUM('global','per_soal')`; nilai ketiga lahir belakangan di `0004`. §5 menampilkan keadaan akhir (3 nilai) tanpa menandai evolusi — dapat dimaafkan untuk living spec, TETAPI §5 yang sama gagal menampilkan keadaan akhir untuk `users.aktif`. Artinya sinkronisasi §5↔migrasi bersifat selektif, bukan "living".
- Evidence:
  - `migrations/0003_users_aktif.sql:1-8`: komentar "Manage Akun Murid … aktif=0 blocks sign-in (auth.go) and any live session (middleware LoadSession)" + `ALTER TABLE users ADD COLUMN aktif TINYINT(1) NOT NULL DEFAULT 1 AFTER must_change_pw;`.
  - `migrations/0001_init.sql:20-32`: blok `CREATE TABLE users` berakhir di `must_change_pw … created_at` — tidak ada `aktif`.
  - Spec §5 blok users (dikutip persis): `nama_lengkap VARCHAR(100) NOT NULL, -- display name (ID UI: "Nama lengkap")`, `must_change_pw TINYINT(1) NOT NULL DEFAULT 0,` langsung ke `created_at …` — tidak ada `aktif` (grep `aktif` pada spec hanya mengenai status kuis `nonaktif/aktif/berjalan`, TTL settings, dan state machine §6.5 — nihil untuk kolom users).
  - Enforcement nyata: `internal/handlers/auth.go:89-115` (`SELECT … must_change_pw, aktif …`; `if !aktif { … MsgLoginInactive }`); `internal/middleware/session.go:77-93` (`SELECT must_change_pw, aktif …`; `if !aktif { ClearSessionCookie … }` — sesi aktif mati seketika).
  - `migrations/0001_init.sql:47` vs `migrations/0004_timer_type_tanpa.sql:1-6` vs spec §5 `timer_type ENUM('global','per_soal','tanpa_timer')` — bukti evolusi yang untuk kasus ini berhasil disinkronkan, sehingga kelalaian `aktif` bukan keterbatasan format melainkan inkonsistensi.
- Reproduction / Verification:
  1. `read migrations/0001_init.sql:20-32` vs `read migrations/0003_users_aktif.sql` vs blok `users` §5 spec — bandingkan kolom per kolom.
  2. `grep -n "aktif" docs/superpowers/specs/2026-09-26-quiz-website-design.md` — verifikasi tidak ada yang merujuk kolom `users.aktif`.
  3. `read internal/handlers/auth.go:85-120` + `read internal/middleware/session.go:69-98` — verifikasi enforcement dua lapis.
  4. `read migrations/0004_timer_type_tanpa.sql` — verifikasi pola evolusi yang benar (kontras positif).
- Expected: "Living spec" berarti setiap kolom yang dienforce di kode dan termigrasi bernomor muncul di §5 — atau §5 menandai bagian yang usang. Perubahan skema (`aktif`, deactivation semantics) tercatat sebagai keputusan (ask-first §10).
- Actual: Fitur deactivation akun — yang mengubah makna seluruh kriteria login/history (§11.1, §11.10: akun yang dinonaktifkan hilang dari akses tanpa jejak di thesis) — hidup di kode + migrasi tetapi mati di dokumen.
- Impact: Reviewer tidak bisa menelusuri "fitur apa yang dinilai" dari thesis saja; dampak §11 (akun dinonaktifkan → login gagal dengan pesan khusus, sesi berjalan dicabut) tidak tercakup kriteria sukses mana pun. Klaim "schema in sync" (§10) terbukti salah untuk satu kolom.
- Academic Impact: Cacat ketertelusuran (traceability): artefak requirements (§5) tidak menutup artefak implementasi (migrasi + enforcement). Dalam standar thesis, ini melemahkan klaim kelengkapan cakupan.
- Confidence: tinggi — perbandingan kolom-per-kolom + dua titik enforcement terdokumentasi di kode.
- Status: SUBMITTED

### 3.4 Finding F-B-004

- Title: Klaim "full bahasa Indonesia baku (KBBI)" gugur — literal ENUM Inggris dirender ke pengguna + inkonsistensi Dasbor/Dashboard
- Severity: MEDIUM
- Category: kebahasaan / klaim-vs-bukti / konsistensi istilah (§1, §7, §11.21)
- Location: `views/student/history.html:37-41,83-87`; `views/teacher/monitor.html:7-10,52-53`; `internal/handlers/history.go:60-88`; `internal/handlers/breadcrumb.go:11,35,39,61,64,128,132`; `views/layout/breadcrumb.html:1-6`; `tests/integration/nav_no_root_links_test.go:14,56-60`
- Claim: Spec §1: "UI language: full bahasa Indonesia baku (KBBI)". §11.21: "All UI text in bahasa Indonesia baku (KBBI)". §7 menegaskan "Path language: all URL paths are English (UI text = bahasa Indonesia baku KBBI). Schema column/ENUM literals stay as approved in §5 — they are internal identifiers, never user-facing."
- Problem: Kalimah terakhir §7 ("never user-facing") dibantah implementasi. Nilai ENUM Inggris dirender mentah sebagai teks badge/status yang dibaca murid dan guru. Spesifikasi dan komentar kode memakai label Inggris "Dashboard" sementara render memakai "Dasbor". Klaim "All" bersifat universal sehingga satu counterexample valid sudah menggugurkannya — di sini ada empat.
- Evidence:
  - `views/student/history.html:37-41` (tabel riwayat murid): `{{if .Removed}}…dikeluarkan…{{else if .Cheating}}…ditandai…{{else}}{{.Status}}{{end}}` — cabang else mencetak mentah `p.status` dari DB (`pending`/`registered`/`started`/`selesai`/`dikeluarkan`, lih. `internal/handlers/history.go:60-88` yang memindai `p.status` ke `r.Status` tanpa pemetaan bahasa). Duplikat di kartu mobile `history.html:83-87`.
  - `views/teacher/monitor.html:52-53`: `<span class="badge text-bg-secondary">{{.Status}}</span>` — status partisipan Inggris di kartu monitor; `monitor.html:7-10`: `{{.TimerType}} timer` (kata Inggris "timer") `&middot; status <span …>{{.Status}}</span>` (label Inggris "status" + nilai ENUM kuis Inggris `nonaktif/aktif/berjalan/selesai` — campur aduk dua bahasa dalam satu baris).
  - Istilah breadcrumb: implementasi memakai `{Label: "Dasbor"}` (`internal/handlers/breadcrumb.go:39,54,64,132`), test mengunci `Dasbor` (`nav_no_root_links_test.go:56-60`), TETAPI: komentar `breadcrumb.go:11` ("Dashboard" label), `:35` ("Dashboard / Guru / Quiz"), `:61` ("Dashboard / Guru / section"), `:128` ("Dashboard / Beranda"), komentar template `views/layout/breadcrumb.html:4` (`"Dashboard" crumb`), komentar test `nav_no_root_links_test.go:14` (`"Dashboard" crumb`), dan tabel §9 spec ("static \"Dashboard\" crumb"). Dokumen dan komentar normatif memakai istilah Inggris yang tidak pernah dirender — bukti istilah tidak dikendalikan.
- Reproduction / Verification:
  1. `read views/student/history.html:24-53` + `read internal/handlers/history.go:60-100` — telusuri `p.status` DB → `r.Status` → cabang `{{else}}{{.Status}}` tanpa kamus bahasa.
  2. `read views/teacher/monitor.html:1-15,47-56` — verifikasi `{{.TimerType}} timer` dan `{{.Status}}` mentah.
  3. `grep -rn "Dashboard" views/ internal/handlers/ tests/integration/ docs/superpowers/specs/2026-09-26-quiz-website-design.md` — verifikasi kemunculan Inggris vs `grep -rn "Dasbor"` pada render/test.
  4. Uji logika: daftar akun murid dengan attempt `registered`/`started`, buka `/history` dan kartu monitor — badge menampilkan literal Inggris (verifikasi end-to-end ringan, tanpa DB pun terbaca dari template + handler).
- Expected: "Full KBBI" + "ENUM literals … never user-facing" berarti setiap string yang mencapai DOM lolos kamus bahasa (atau test bahasa yang mengunci seluruh literal render). Satu sumber istilah untuk breadcrumb.
- Actual: Cabang render mentah untuk status; kata dan label Inggris di monitor; dua istilah (Dashboard/Dasbor) hidup berdampingan di dokumen vs kode — klaim universal gugur oleh counterexample yang direproduksi dari template + handler.
- Impact: §11.21 tidak lulus sebagaimana dirumuskan; pengguna murid melihat istilah Inggris teknis (`registered`, `started`, `pending`) yang bukan kosakata KBBI dan bukan istilah domain yang dijelaskan di mana pun. Inkonsistensi istilah melemahkan keterbacaan thesis sebagai spesifikasi bahasa.
- Academic Impact: Klaim kebahasaan absolut ("All UI text") adalah klaim empiris yang membutuhkan sensus string — tidak ada buktinya di repo (tidak ada test/lint bahasa). Ini contoh klaim tak-terbukti (unsubstantiated universal claim) yang seharusnya dirumuskan terukur ("semua string di file X lolos kamus Y") atau diturunkan.
- Confidence: tinggi — empat counterexample independen, masing-masing dengan rantai DB→handler→template yang lengkap.
- Status: SUBMITTED

### 3.5 Finding F-B-005

- Title: §11.2 memandatkan pengabaian verifikasi kata sandi + klaim robustness tanpa uji negatif; §12 "None" mengklaim ketertutupan yang palsu
- Severity: HIGH
- Category: klaim keamanan / falsifiabilitas kriteria sukses / kelengkapan risiko (§11.2, §11.15-17, §12)
- Location: `internal/handlers/auth.go:104-115`; `internal/middleware/session.go:130-139`; `tests/integration/realtime_test.go:277-484` (hanya uji positif); `internal/testutil/testutil.go:62-77`
- Claim: Spec §11.2: "Full forgot-password flow: input matches → request appears on teacher dashboard → approve → **next login skips password validation → locked on change-password until new password set** → old sessions dead." §11.15–17: rehydrate identik, disconnect-freeze tanpa kehilangan waktu, "DB write failure → no broadcast (state never newer than DB)". §12: "None — all 24 clarification decisions … are resolved in this document."
- Problem: (a) Frasa yang ditebalkan di §11.2 adalah persyaratan perilaku tidak aman yang dienforce kode: bila `must_change_pw=1`, login MEMBUAT SESI TANPA memeriksa kata sandi apa pun — siapa pun yang tahu username akun yang baru disetujui reset dapat masuk (lalu memang dikunci di `/change-password`, tetapi sesi terautentikasi sudah di tangan). (b) Tiga klaim robustness tidak memiliki uji negatif: tidak ada test yang mematikan DB di tengah mutasi lalu menegaskan tidak ada broadcast (suite realtime hanya menguji publish positif, ordering, guard buffer, rehydrate simulasi); tidak ada test yang membuktikan "freeze at last heartbeat, no time lost" terhadap jam dinding yang dimajukan (uji timer di `quizengine` murni, sedangkan freeze dieksekusi di handler ber-`time.Now` — lih. F-B-002); "works from a clean machine" (§11.19) tidak dapat dieksekusi examiner tanpa Docker + jaringan (Google Fonts) sehingga bukan kriteria teruji melainkan deklarasi. (c) §12 "None" tidak dapat dipertahankan bersamaan dengan F-B-001–F-B-004: dependensi tak terdaftar, kolom tak terdokumen, klaim bahasa yang gugur, dan tabel uji yang salah atribusi adalah — menurut definisi — open questions/lacunae yang belum resolved.
- Evidence:
  - `internal/handlers/auth.go:104-115` (dikutip persis, urutan menentukan): `if !must && bcrypt.CompareHashAndPassword(…) != nil { return …Unauthorized… }` — artinya bila `must==true` perbandingan hash DILEWATI seluruhnya — `if !aktif { …Inactive }` — lalu `target := "/student"; if must { target = "/change-password" }` + `startSession(c, uid, "murid", target)`: sesi diterbitkan untuk pemohon tanpa satu pun bukti pengetahuan kata sandi. Penguncian `ForceChangePassword` (`internal/middleware/session.go:130-139`) hanya membatasi tujuan, bukan menerbitkan-penolakan.
  - Suite realtime (`tests/integration/realtime_test.go:277-484`): `TestSSEPublishReachesSubscribers`, guard buffer penuh, rehydrate via hub baru — semuanya jalur sukses; `grep -rn "no broadcast\|write failure\|DB write fail" tests/ internal/` nihil untuk skenario gagal-tulis (klaim §11.17 "DB write failure → no broadcast" tanpa test).
  - `grep -rn "t\.Skip" tests/ internal/` → `internal/testutil/testutil.go` (skip saat DB mati) — digabung dengan tidak adanya failure-injection, matriks edge-case §8 untuk jalur gagal tidak terbukti "fails when the code is wrong" (test-quality bar §9).
- Reproduction / Verification:
  1. `read internal/handlers/auth.go:85-125` — telusuri cabang `must==true` melewati `CompareHashAndPassword`; konfirmasi `startSession` dipanggil tanpa syarat password.
  2. `read internal/middleware/session.go:130-139` — verifikasi middleware hanya me-redirect, tidak mencabut sesi yang sudah terbit.
  3. `grep -rn "Publish\|broadcast\|SweepDisconnects\|DeleteExpired" tests/integration/realtime_test.go` — verifikasi hanya skenario positif; tidak ada test "matikan DB → tidak ada frame".
  4. Bandingkan daftar §11.15–17 dengan daftar berkas `tests/integration/` — tidak ada pasangan uji-negatif untuk ketiganya.
  5. `read` §12 + temuan F-B-001–F-B-004 — verifikasi klaim "all resolved" terhadap lacunae yang terbukti ada.
- Expected: Kriteria sukses keamanan dirumuskan sebagai sifat yang dipertahankan ("reset yang disetujui TIDAK melemahkan autentikasi: login berikutnya tetap memverifikasi identitas dengan kredensial yang diketahui pemilik sah; sesi lama mati"), setiap klaim robustness dipasangkan uji negatif yang gagal bila kode salah, dan §12 mencantumkan pertanyaan terbuka yang tersisa (dependensi, skema, bahasa).
- Actual: Thesis memandatkan skip-validasi sebagai tanda lulus; robustness diklaim tanpa uji-gagal; ketertutupan diklaim di tengah empat lacunae terbukti.
- Impact: Implementasi yang "lulus §11.2" mengandung kelemahan autentikasi by-design: jendela antara approve-reset dan penetapan kata sandi baru memungkinkan pengambilalihan akun bermodalkan username. Klaim §11.15–17 tidak memberi examiner cara membedakan "benar" dari "belum diuji". Risiko deployment: operator yang memercayai §12 tidak mencari risiko yang sebenarnya ada.
- Academic Impact: Ini cacat validitas kriteria (success criteria yang tidak falsifiable + satu yang falsifiable-tetapi-tidak-aman) dan cacat analisis risiko (risk/limitation analysis yang hilang). Thesis yang baik memuat bagian ancaman/batasan; dokumen ini menutupnya dengan "None".
- Confidence: tinggi untuk (a) — rantai kode linier dan tak ambigu; tinggi untuk (c) — konsekuensi logis F-B-001–F-B-004; sedang-tinggi untuk (b) — klaim ketiadaan test adalah bukti negatif, tetapi pencarian grep + pemetaan berkas test bersifat ekshaustif pada repo ini.
- Status: SUBMITTED

## 4. Cross-Hunter Discussion

### Agreement

Belum ada sesi rekonsiliasi (tidak ada peer Hunter A yang live pada saat investigasi — roster hanya memuat Main; mengirim IRC ke identitas yang tidak terdaftar akan melanggar aturan koordinasi, sehingga tidak dikirim). Berkas `docs/bug-hunt/findings/hunter-a-report.md` ada di disk tetapi tidak dibuka agar sudut akademik tetap independen dan tidak terkontaminasi temuan implementasi. Jika Hunter A melaporkan lokasi kode yang sama (mis. `auth.go` password-skip, `gocsv`, status mentah), itu adalah konvergensi yang diharapkan: sudut pandang berbeda (implementasi vs klaim/metodologi) mencapai bukti yang sama — bukan duplikasi mentah.

### Disagreement

Tidak ada — belum ada pertukaran temuan dengan Hunter A.

### Open Questions

- Apakah Hunter A juga menemukan password-skip §11.2 dari sisi implementasi (auth bypass)? Jika ya, gabungkan: Hunter A memiliki dampak teknis, Hunter B memiliki vonis akademik (kriteria sukses yang memandatkan perilaku tidak aman) — keduanya saling menguatkan, bukan salah satu yang gugur.
- Apakah Hunter A menemukan dependensi/artefak lain di luar daftar tertutup (mis. lisensi font, ikon)? Jika ya, F-B-001 dapat diperluas ke daftar gabungan tanpa mengubah vonisnya.
- Batas peran: bila examiner meminta perbaikan kode, itu di luar mandat kedua hunter (non-goals: no fix/refactor/commit) — diteruskan ke main agent.

## 5. Validator Examination

### Finding F-B-001

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: OPEN

### Finding F-B-002

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: OPEN

### Finding F-B-003

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: OPEN

### Finding F-B-004

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: OPEN

### Finding F-B-005

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: OPEN

## 6. Final Verdict

### Confirmed Findings

- F-B-001 — Klaim closed-8-direct gugur (3 direct + gocsv yatim + Chart.js tak diakui). Verdict: CONFIRMED (bukti manifest + import + vendor).
- F-B-002 — Metodologi testing §9 overclaim (paralel, jam, atribusi unit, green kondisional). Verdict: CONFIRMED (bukti negatif grep + komentar berkas + Skipf).
- F-B-003 — Drift living spec (`users.aktif` hilang, riwayat `timer_type` selektif). Verdict: CONFIRMED (kolom-per-kolom + enforcement).
- F-B-004 — Klaim KBBI gugur (ENUM mentah + Dashboard/Dasbor). Verdict: CONFIRMED (rantai DB→handler→template).
- F-B-005 — §11.2 memandatkan skip-validasi + robustness tanpa uji negatif + §12 "None" palsu. Verdict: CONFIRMED untuk (a) dan (c); PARTIALLY CONFIRMED untuk (b) (ketiadaan uji-negatif terverifikasi ekshaustif pada repo, tetapi selalu terbuka kemungkinan bukti di luar jangkauan pencarian).

### Rejected Findings

Tidak ada — tidak ada kandidat yang gugur pada tahap verifikasi (tidak ada temuan yang dilaporkan lalu ditarik).

### Unresolved Findings

Tidak ada yang belum berstatus — kelima temuan berstatus SUBMITTED dengan bukti berkas; yang menunggu hanyalah pemeriksaan validator (§5) pada defense round berikutnya.

### Defense Readiness

Penulis thesis BELUM siap mempertahankan klaim dokumen sebagaimana dirumuskan. Perbaikan minimal yang harus ditunjukkan sebelum defense: (1) rumuskan ulang daftar dependensi sesuai `go.mod` + daftarkan Chart.js atau vendorkan keluar dari jalur runtime; (2) turunkan klaim §9 ke yang terbukti (hapus "parallel-safe", perbaiki atribusi tabel, nyatakan prasyarat DB untuk "green"); (3) sinkronkan §5 dengan `users.aktif` + semantik deactivation; (4) ganti klaim bahasa absolut dengan kamus pemetaan ENUM + test pengunci string; (5) rumuskan ulang §11.2 menjadi sifat keamanan yang benar + tambahkan uji negatif robustness + buka §12. Konteks peran Hunter B dipertahankan untuk tanya-jawab examiner berikutnya.

## 7. Audit Trail

- `2026-10-07 — [INDEX] — list_projects → D-Project-Mother-quiz terdaftar; index_status → ready (5136 nodes/19372 edges); index_repository tidak dijalankan (indeks segar).`
- `2026-10-07 — [ARCH] — get_architecture (overview/structure): Go 104 / HTML 43 / JS 13 / SQL 6 berkas; packages mencakup excelize, go-sse, qrcode, gocsv sebagai modul terindeks.`
- `2026-10-07 — [EVIDENCE] — go.mod: hanya 3 direct; gocsv nol import; export.go memakai encoding/csv; Chart.js v4.5.1 bervendor + dimuat dashboard.html:130 (F-B-001).`
- `2026-10-07 — [EVIDENCE] — t.Parallel nihil; time.Now di 4 berkas handler; authlogic_test mendelegasikan ke integrasi; Skipf DB-gate; 7 baris tabel TestStaticCache (F-B-002).`
- `2026-10-07 — [EVIDENCE] — users.aktif di 0003 + enforcement auth.go/session.go vs §5 tanpa aktif; timer_type 2→3 nilai (F-B-003).`
- `2026-10-07 — [EVIDENCE] — {{.Status}} mentah di history/monitor; "{{.TimerType}} timer"; Dasbor vs Dashboard di kode vs dokumen (F-B-004).`
- `2026-10-07 — [EVIDENCE] — auth.go skip CompareHash saat must==true + startSession; tanpa uji-negatif broadcast/freeze; §12 None (F-B-005).`
- `2026-10-07 — [COORD] — roster hanya memuat Main; IRC ke BugHunterA tidak dikirim (hindari pesan ke identitas tak terdaftar); hunter-a-report.md tidak dibuka (jaga independensi sudut).`
- `2026-10-07 — [SUBMIT] — docs/bug-hunt/findings/hunter-b-report.md ditulis; tanpa perubahan kode produksi; siap untuk examination.`

```
STATUS: SUBMITTED
Findings: F-B-001 (closed-8-direct gugur: 3 direct + gocsv yatim + Chart.js tak diakui); F-B-002 (metodologi testing §9 overclaim); F-B-003 (drift living spec: users.aktif + riwayat timer_type); F-B-004 (klaim KBBI gugur: ENUM mentah + Dasbor/Dashboard); F-B-005 (§11.2 skip-validasi + robustness tanpa uji negatif + §12 None palsu)
Evidence: F-B-001: go.mod:5-29 + grep gocsv nihil + export.go:3-16 + chart.umd.min.js:1-6 + dashboard.html:72-84,130; F-B-002: grep t.Parallel nihil + time.Now di auth/global/live/student vs timer.go:1-11 + authlogic_test.go:1-14 + testutil.go:62-77 + assets_test.go:83-125; F-B-003: 0003_users_aktif.sql:1-8 + 0001_init.sql:20-32,47 + 0004_timer_type + auth.go:89-115 + session.go:77-93 vs §5; F-B-004: history.html:37-41,83-87 + monitor.html:7-10,52-53 + history.go:60-88 + breadcrumb.go:11-132 + nav_no_root_links_test.go:14,56-60; F-B-005: auth.go:104-115 + session.go:130-139 + realtime_test.go:277-484 (positif saja) + grep uji-negatif nihil
Open Questions: Tidak ada yang menahan submission; tiga pertanyaan rekonsiliasi untuk Hunter A menunggu (konvergensi password-skip, artefak tak terdaftar lain, batas peran no-fix).
Confidence: F-B-001 tinggi (tiga sub-bukti berkas langsung); F-B-002 tinggi kecuali sub-butir perumusan 7-subtests (sedang); F-B-003 tinggi (kolom-per-kolom + enforcement); F-B-004 tinggi (empat counterexample rantai penuh); F-B-005 tinggi untuk skip-validasi dan §12, sedang-tinggi untuk ketiadaan uji-negatif (bukti negatif ekshaustif pada repo)
Files Changed: docs/bug-hunt/findings/hunter-b-report.md (satu berkas milik Hunter B; tanpa perubahan kode produksi; hunter-a-report.md tidak disentuh)
Ready for Examination: YES
```
