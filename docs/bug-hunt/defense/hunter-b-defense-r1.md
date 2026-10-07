# Hunter B — Defense Round 1 (N-006…N-010)

> File baru untuk putaran defense; laporan awal (`docs/bug-hunt/findings/hunter-b-report.md`) TIDAK ditimpa. Tanpa perubahan kode produksi. Format respons mengikuti skill `thesis-bug-hunter` §14 (DEFENSE RESPONSE). Tidak ada kutipan yang dikarang — semua dikunci ke hasil `read`/`grep`/`bash`/`go` pada putaran ini.

## Status akomodasi per validator (ringkas)

- N-006 (F-B-001): sub-butir (a) DIPERSEMPIT — `// indirect` adalah komentar basi `go mod tidy`, bukan bukti tidak-dipakai (4 dari 5 modul benar diimpor; `go mod why` + `go mod tidy -diff` membuktikan). Vonis bertahan pada (b) `gocsv` yatim + (c) Chart.js sebagai **undocumented runtime dependency** (diturunkan dari "di luar closed list" menjadi "tak terdokumentasi namun material").
- N-007 (F-B-002): kaki (d) "7 subtests" DICABUT (quibble — klaim spec akurat menurut konvensi Go). Kaki (b) dipersempit menjadi "seam jam berhenti di batas quizengine; spec tidak pernah mewajibkan seam di handler". Vonis overclaim metodologi bertahan pada (a) paralel, (c) atribusi unit, (e) green kondisional + sebagian (b).
- N-008 (F-B-003): CONFIRMED bulat — tidak ada keberatan substantif; melengkapi semantik deactivation + usulan redaksi §5 agar amendemen (b) lengkap.
- N-009 (F-B-004): DIPERSEMPIT — `status` (serapan baku) dan render `Dasbor` (patuh) diakui BUKAN pelanggaran; inkonsistensi dokumen-vs-render dipisah sebagai isu istilah. Vonis bertahan pada kaki ENUM-mentah + `{{.TimerType}} timer`.
- N-010 (F-B-005): kaki (b) DIPERSEMPIT menjadi PARTIALLY — freeze/rehydrate DIAKUI sudah diuji (dengan kutipan assert); sisa yang benar-benar tanpa pasangan ditegaskan = §11.17 "DB write failure → no broadcast" + failure-injection generik. Kaki (a) skip-validasi DIPERTAHANKAN PENUH (tak disengketakan validator mana pun; dikunci test). Kaki (c): posisi "misleading-by-omission, bukan fabrikasi".

---

## DEFENSE RESPONSE — N-006 (F-B-001: closed list / gocsv / Chart.js)

**Finding:** F-B-001

**Validator Question:**
(1) Apakah `// indirect` hanya artefak `go mod tidy` (komentar informatif) sementara definisi operasional direct = "diimpor paket main module"? Empat dari lima modul benar diimpor langsung. Bersediakah mempersempit (a) dengan `go help modules` / `go list -m all` / `go mod why`?
(2) Kutip kalimat §2/§10/§11.21 yang memasukkan/mengeluarkan aset JS bervendor dari closed list — bila tak ada, turunkan (c) ke "undocumented runtime dependency" + tunjukkan pemakaian Chart.
(3) Adakah artefak runtime tak-terdaftar lain selain Chart.js? Daftar web/vendor yang dieksekusi template.

**Position:** partially concede — sub-butir (a) dipersempit; vonis keseluruhan bertahan pada (b)+(c).

**Evidence:**

1. `go mod why` per modul (verbatim, dieksekusi putaran ini):
   - `github.com/gocarina/gocsv` → `(main module does not need package github.com/gocarina/gocsv)` — yatim, tidak dibutuhkan siapa pun.
   - `github.com/tmaxmax/go-sse` → `quiz/internal/realtime` → modul (dipakai langsung).
   - `golang.org/x/crypto/bcrypt` → `quiz/internal/handlers` → modul (dipakai langsung; catatan: `go mod why golang.org/x/crypto` tanpa subpath melaporkan tidak-butuh, tetapi `go mod why golang.org/x/crypto/bcrypt` melaporkan butuh — ini pola normal modul multi-paket).
   - `github.com/skip2/go-qrcode` → `quiz/internal/handlers` → modul (dipakai langsung).
2. Import langsung di kode (grep putaran ini): `internal/config/config.go:12` (`cleanenv`), `internal/handlers/auth.go:16` (`golang.org/x/crypto/bcrypt`), `internal/handlers/export_sheets.go:6` (`xuri/excelize/v2`), `internal/handlers/teacher_quiz.go:16` (`skip2/go-qrcode`), `internal/realtime/hub.go:16` (`tmaxmax/go-sse`), `internal/db/db.go:10` + `internal/handlers/auth.go:14` (`go-sql-driver/mysql`).
3. `go mod tidy -diff` (tanpa menulis, verbatim):
   ```
   +  github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
   +  github.com/tmaxmax/go-sse v0.11.0
   +  github.com/xuri/excelize/v2 v2.11.0
   +  golang.org/x/crypto v0.57.0
   -  github.com/gocarina/gocsv v0.0.0-20260908110832-9ab82d65b1cc // indirect
   ```
   Artinya: `tidy` akan mempromosikan keempat modul ke blok direct dan MENGHAPUS `gocsv` seluruhnya. Komentar `// indirect` basi (repo tidak menjalankan `tidy` setelah menambah import) — validator benar pada titik ini. (`go help modules` pada toolchain ini tidak mencetak definisi `// indirect`; bukti `why` + `tidy -diff` di atas lebih kuat dan operasional.)
4. `grep -rn "gocsv" --include="*.go" .` → nihil import di kode maupun test; satu-satunya kemunculan di `go.mod:14` + `go.sum`. `internal/handlers/export.go:3-16` mengimpor `"encoding/csv"` (stdlib). Jadi (b) tidak tersentuh koreksi (a): `gocsv` diklaim §2 sebagai mesin ekspor CSV tetapi tidak diimpor siapa pun — bahkan `tidy` ingin membuangnya.
5. Ruang lingkup JS — kalimat spec yang ada (dikutip persis, tidak ada yang menyebut JS):
   - §2: "**Closed dependency list** — exactly 8 **direct** modules …" + "**Dependency list is closed** — adding any dependency requires asking first (§10)."
   - §10: "Stay within the closed 8-module list (§2): `echo/v5`, …" (daftar 8 nama modul Go).
   - §11.21: "…no dependencies outside the closed list."
   - Kata yang dipakai selalu "modules / dependency / `go.mod` / `govulncheck`" — kosakata Go-module. TIDAK ADA kalimat yang memasukkan atau mengeluarkan aset JS bervendor. Konsekuensi jujur: (c) tidak dapat dirumuskan sebagai "melanggar closed list" — diturunkan menjadi **undocumented runtime dependency** (dependensi runtime tak terdokumentasi).
   - Namun materialitas tetap: §4 struktur `web/vendor/` hanya mendaftar `(bootstrap-icons/, theme.css)` (spec baris 99; struktur §4 baris ~99-100) — Chart.js tidak ada di struktur, tidak ada di tabel §2, tidak ada di batasan §10; `cmd/server/main.go:52` komentar mount menyebut "(theme.css, bootstrap-icons/)" saja. Sementara eksekusi nyata: `views/teacher/dashboard.html:72-84` (tiga `<canvas id="dash-chart-…">`), `dashboard.html:130` (`<script src="{{asset "/assets/chartjs/chart.umd.min.js"}}">`), `web/js/teacher_dashboard.js:247,257,268` (`new Chart(kelas, …)`, `new Chart(jurusan, …)`, `new Chart(nilai, …)`), `:290` (`if (typeof Chart === "undefined") return;`). Header vendor `web/vendor/chartjs/chart.umd.min.js:1-6`: `Chart.js v4.5.1 … MIT License`.
6. Inventarisasi `web/vendor/` yang DIEKSEKUSI template (bukan sekadar ada di disk):
   - `bootstrap-icons/bootstrap-icons.css` + `fonts/{bootstrap-icons.woff,bootstrap-icons.woff2}` — dimuat ketiga head partial (`app_head.html:11`, `head.html:11`, `landing_head.html:11`) — TERDAFTAR di spec (§4) sebagai bagian vendor. Bukan temuan.
   - `theme.css` — dimuat ketiga head partial (`:12`) — TERDAFTAR (§4, §10, §11.20). Bukan temuan.
   - `chartjs/chart.umd.min.js` — dimuat `dashboard.html:130` — TAK TERDAFTAR di mana pun. Satu-satunya artefak runtime tak-terdaftar.
   - `web/css/app.css` (`/css`, bukan vendor) + Google Fonts stylesheet (sanctioned exception §2, dimuat `:7-9` ketiga head) — keduanya dideklarasikan spec. Bukan temuan.

**Reasoning:** Validator (khususnya B-Q1) benar bahwa `// indirect` adalah komentar informatif yang bisa basi; definisi operasional Go untuk directness adalah keterjangkauan import dari paket main module (`go mod why`), dan empat modul memenuhinya. Kesalahan Hunter B pada (a) adalah memperlakukan komentar sebagai definisi — ditarik. Tetapi koreksi ini TIDAK merambat ke (b): `go mod why gocsv` + `tidy -diff` justru memperkuat bahwa `gocsv` yatim total. Untuk (c): karena spec tidak pernah mendefinisikan yurisdiksi closed list atas aset JS, klaim pelanggaran langsung atas §11.21 tidak dapat dipertahankan kata-per-kata; rumusan yang tepat adalah dependensi runtime tak terdokumentasi yang melanggar kelengkapan dokumen (§4 struktur, §2 library selection "resolved", §12 "None") dan lolos dari pemantauan versi (`govulncheck` tidak melihat Chart.js v4.5.1).

**Counterargument (yang diantisipasi):** "Chart.js hanya aset frontend, di luar cakupan closed list Go." Jawaban: tepat — karena itu vonisnya bukan "pelanggaran daftar" melainkan "omission material": dokumen yang mengklaim §4 memetakan seluruh struktur vendor, §2 menyelesaikan seluruh seleksi library, dan §11.19 menjanjikan "identical images … Bootstrap 5.3.8 vendored" tidak menyebut artefak JS 208 KB yang dieksekusi di halaman guru. Examiner berhak tahu ia ada, versinya, dan dari mana ia dipantau keamanannya (jawaban saat ini: tidak dari mana pun).

**Conclusion:** (a) dipersempit menjadi "komentar `// indirect` basi untuk 4 modul (kesalahan redaksi manifest, bukan bukti tidak-dipakai)"; (b) `gocsv` yatim DIPERTAHANKAN PENUH; (c) DITURUNKAN menjadi "undocumented runtime dependency (Chart.js v4.5.1)" — tetap material. Severity HIGH dipertahankan (kombinasi klaim-dependensi-tanpa-pakai + dependensi-pakai-tanpa-klaim meruntuhkan keterujian §11.21).

**Confidence:** tinggi — `why` + `tidy -diff` + grep import + inventarisasi vendor semuanya bukti berkas/perintah langsung.

---

## DEFENSE RESPONSE — N-007 (F-B-002: metodologi testing)

**Finding:** F-B-002

**Validator Question:**
(1) Tarik leg "7 subtests"? (2) Kutip kalimat spec yang memaksa seam jam ke handler transaksional — bila tak ada, persempit. Bedakan call-site presentasi vs penentu-hasil. (3) Buktikan green-vakum + prasyarat DB CI; konfirmasi ketiadaan test paralel/CI paralel.

**Position:** partially concede — kaki (d) dicabut; kaki (b) dipersempit; vonis overclaim bertahan pada (a)/(c)/(e)+sebagian (b).

**Evidence:**

1. (d) DICABUT. `internal/middleware/assets_test.go:83-125`: `func TestStaticCache` + 7 entri tabel (`matched v → immutable`; `missing v → no-cache + ETag`; `wrong v …`; `If-None-Match hit → body-less 304`; `If-None-Match miss → served`; `unknown static path …`; `non-static path → no-store`) dieksekusi via `t.Run(tc.name, …)` di baris 125. Menurut konvensi Go (`testing.T.Run` = subtest), ini 7 subtests. Klaim spec "header matrix (7 subtests)" AKURAT. Hunter B keliru memakai definisi "fungsi uji mandiri" yang bukan konvensi. Quibble ditarik tanpa kualifikasi.
2. (b) Kalimat spec yang ada — dan hanya itu:
   - Levels (§9): "`tests/unit/` — pure logic, **no DB/HTTP**; injectable fake clock; seeded RNG for shuffle. Fast, deterministic, parallel-safe."
   - Determinism (§9): "inject clock (no `time.Now` in logic), seeded RNG in tests, truncate relevant tables per test → safe to re-run and run sequentially."
   - TIDAK ADA kalimat yang menuntut seam jam di handler transaksional (`ends_at`/freeze/collapse). Jadi rumusan "melanggar no-time.Now" ditarik; diganti: **seam jam berhenti di batas `quizengine`** (`timer.go:1-11` murni — "take explicit times … never calls time.Now() directly" — vs handler memanggil `time.Now()` langsung: `auth.go:118,148`; `global.go:243,265,956`; `live.go:140,143`; `student.go:846,868,970,1091,1198,1410,1675,1773`).
   - Pembedaan yang diminta validator — presentasi vs penentu-hasil — JUSTRU memperkuat sisa kaki (b): di antara call-site handler ada yang murni presentasi (`"server_now": time.Now().Unix()` di `global.go:243,265`, `student.go:846,868,1773`), tetapi ada juga yang penentu-hasil: `student.go:1091` (pemeriksaan wall-clock `ends_at` DI DALAM transaksi jawab → 410/auto-finish), `student.go:970-972` (komputasi `ends_at` personal), `student.go:1675-1676` (gerbang collapse anti-cheat `ShouldRecord(lastAt, now=…)`), `student.go:1410` (`current_q_since`), `global.go:956` (`SweepDisconnects(ctx, time.Now(), …)` — penentu freeze), `live.go:140,143` (heartbeat/disconnect). Konsekuensinya: cabang penentu-hasil hanya dapat diuji via integrasi berbasis tidur (`time.Sleep(600ms/1300ms)` di `flow_global_test.go:277,346,486`, silence 400ms di `:465`) — deterministik-lemah, bukan deterministik-kuat seperti yang diimplikasikan "inject clock … deterministic".
3. (a)+(e) Bukti green-vakum (diekseskusi putaran ini, tanpa DB di 127.0.0.1:3306):
   - `go test ./tests/integration/ -run TestRegisterRedirect -count=1 -v` → `--- SKIP: TestRegisterRedirectsToLoginWithoutSession (0.00s)` dengan pesan `test database unreachable: dial tcp 127.0.0.1:3306 … — start docker compose up -d db`, diakhiri `PASS ok`.
   - Penuh: `go test ./tests/integration/ -count=1 -v` → **119 `--- SKIP`, 6 `--- PASS`, 0 `--- FAIL`**, exit `ok` (1.8s). Enam PASS adalah test yang tidak menyentuh DB (level unit/cache/config). Seluruh suite integrasi lulus-vakum tanpa database.
   - Gerbangnya: `internal/testutil/testutil.go:62-77` (`func DB`: `PingContext` gagal → `t.Skipf("test database unreachable …")`). Pemakai gerbang: 9 berkas (`db_test`, `auth_test`, `chrome_test`, `flow_global_test`, `quiz_crud_test`, `realtime_test`, `register_redirect_test`, `session_test`, `upload_test` — hasil `grep -rln testutil.DB`).
   - Ketiadaan paralelisme: `grep -rn "t\.Parallel" tests/ internal/ --include="*.go"` → nihil; tidak ada `.github/` (CI), tidak ada konfig paralel test di `docker-compose.yml`/`Dockerfile`/`.env.example` (satu-satunya penyebutan `go test` di `.env.example:6` hanya komentar DSN). Jadi klaim "parallel-safe" (Levels) + "run sequentially" (Determinism) adalah pasangan kontradiktif yang keduanya tanpa bukti: tidak ada test paralel yang membuktikan aman-paralel, dan "sequentially" meniadakan kebutuhan klaim paralel.
4. (c) Atribusi unit — tidak berubah: header `tests/unit/authlogic_test.go:1-14` menyatakan match/mismatch forgot-password + generic login message "are DB-backed and covered … by tests/integration/auth_test.go" dan join-code retry "covered by tests/integration/quiz_crud_test.go", sementara baris tabel §9 menaruh keempatnya di kolom `authlogic_test.go`. Klaim-vs-berkas tetap salah atribusi.

**Reasoning:** Validator A+C benar pada (d) dan sebagian (b): spec tidak menjanjikan apa yang Hunter B tuntut. Hunter B menarik yang overstate. Sisa yang bertahan adalah inti metodologis: dokumen mengklaim sifat-sifat kuat (paralel-aman, deterministik-penuh, cakupan-per-level, hijau-penuh) yang buktinya lemah/salah-jenis/kondisional. Khusus (e): "fully green … nothing disabled" (§11.14) tidak menyebut prasyarat DB; dengan 119 skip + exit 0, kriteria ini lulus tanpa menguji apa pun — green vakum. Ini bukan tuduhan test buruk (test integrasi freeze/rehydrate sesungguhnya kuat — diakui di N-010), melainkan klaim dokumen yang melebihi instrumennya.

**Counterargument:** "Skip saat DB mati adalah praktik standar; -race tetap green." Jawaban: setuju sebagai praktik — yang disengketakan adalah RUMUSAN kriteria (§11.14) yang tidak menyatakan prasyarat DB/CI, sehingga "green" tidak falsifiable sebagai gerbang kelulusan thesis. Perbaikannya redaksional (nyatakan prasyarat), bukan perombakan suite.

**Conclusion:** (d) dicabut; (b) dipersempit menjadi "seam jam berhenti di batas quizengine; cabang penentu-hasil di handler hanya teruji via sleep-based integration"; (a)/(c)/(e) dipertahankan. Vonis keseluruhan: overclaim metodologi — PARTIALLY CONFIRMED dalam kerangka validator (inti benar, dua kaki dikoreksi).

**Confidence:** tinggi — hitungan subtest, teks spec, keluaran `go test -v`, dan daftar call-site semuanya bukti langsung.

---

## DEFENSE RESPONSE — N-008 (F-B-003: drift living spec `users.aktif`)

**Finding:** F-B-003

**Validator Question (hanya C, LOW):** Semantik deactivation apa yang harus masuk §5 agar sinkron? Rujuk perilaku kode per efek + usulan redaksi §5. Vonis CONFIRMED tak bergantung.

**Position:** defend — vonis CONFIRMED; melengkapi amendemen semantik yang diminta.

**Evidence (per efek, semua dibaca putaran ini):**

1. Efek login — `internal/handlers/auth.go:89-115`: `SELECT id, password_hash, must_change_pw, aktif FROM users WHERE username = ?` (fallback email di `:98-99`); `if !must && bcrypt.CompareHashAndPassword(…) != nil → 401 MsgLoginFailed`; `if !aktif → 401 MsgLoginInactive`. Komentar normatif di `auth.go:37-39`: "MsgLoginInactive is only reachable AFTER the password matched (or the forced-change flag skipped it): a stranger still sees MsgLoginFailed." Pesan: `MsgLoginInactive = "Akun ini dinonaktifkan. Hubungi guru Anda."` (`auth.go:39`).
2. Sesi berjalan mati seketika — `internal/middleware/session.go:77-93`: setiap pemuatan sesi murid membaca `SELECT must_change_pw, aktif …`; `if !aktif { ClearSessionCookie(c); return next(c) }` (dikenali sebagai anonim). Bukan hanya "gagal login berikutnya".
3. Pencabutan proaktif saat penonaktifan — `internal/handlers/teacher_students.go:228-260`: `DeactivateStudent → setStudentActive(false)` → `UPDATE users SET aktif = ? WHERE id = ?` + `if !active { mw.RevokeUserSessions(ctx, t.DB, t.Store, id) }`. Komentar `:17-23`: "deactivation revokes every session …, so a signed-in student is out immediately". `:228-229` menegaskan sama.
4. Riwayat/peringkat — TIDAK ADA penyaringan `aktif` di jalur baca murid: `internal/handlers/history.go` (seluruh berkas, grep `aktif` nihil) membaca `participants JOIN quizzes` tanpa join/filter `users.aktif`; `results.go` (grep `aktif` nihil). Artinya baris riwayat akun nonaktif tetap ada di DB dan tetap terhitung di hasil guru; yang hilang adalah AKSES (login + sesi), bukan data. Daftar akun guru justru mengekspos status: `teacher_students.go:37` (`Aktif bool`), `:41` (filter `?status=aktif|nonaktif`), `:53-56` (penyaringan), `:78` (`SELECT …, u.aktif`), `StudentsPage` me-render manajer "Kelola akun murid".
5. Reaktivasi — `setStudentActive(c, true)` hanya membalik flag; TIDAK ada penerbitan sesi otomatis (tidak ada `InsertSession` di jalur itu) — akun harus login ulang. Tidak ada penghapusan data attempt/answer pada nonaktif/reaktivasi (tidak ada `DELETE` di `setStudentActive`).
6. Bukti drift (dipertahankan dari laporan awal): `migrations/0001_init.sql:20-32` (blok users tanpa `aktif`) → `migrations/0003_users_aktif.sql:1-8` (ALTER + komentar "Manage Akun Murid … blocks sign-in (auth.go) and any live session (middleware LoadSession)") vs blok `users` §5 (kolom berakhir di `must_change_pw → created_at`, tanpa `aktif`; `grep aktif` pada spec nihil untuk kolom ini).

**Reasoning:** Agar "living spec" (§10: "keep schema in sync with §5") terpenuhi, §5 harus mencatat bukan sekadar kolom melainkan SEMANTIKnya — karena kolom ini mengubah makna kriteria login (§11.1) dan akses riwayat (§11.10) tanpa jejak di thesis.

**Usulan redaksi §5 (blok `users`, setelah `must_change_pw`):**

```sql
aktif TINYINT(1) NOT NULL DEFAULT 1,  -- 0 = akun dinonaktifkan guru: login ditolak
                                       -- (MsgLoginInactive, hanya setelah kredensial cocok —
                                       -- bukan oracle), SEMUA sesi berjalan dicabut seketika
                                       -- (middleware + RevokeUserSessions saat penonaktifan);
                                       -- data attempts/answers/riwayat TIDAK dihapus;
                                       -- reaktivasi = flag kembali 1, login ulang manual
```

plus satu baris di §8 (Auth & session): "Deactivation (`users.aktif=0`,elola di Manage Akun Murid): menolak login + membunuh sesi berjalan; reaktivasi tidak menerbitkan sesi."

**Counterargument:** "Kolom operasional kecil, tak perlu masuk thesis." Jawaban: kolom ini user-visible (pesan khusus, tendangan sesi, filter daftar guru) dan mengubah hasil evaluasi kriteria sukses — tepat jenis hal yang harus ada di living spec menurut aturan spec sendiri.

**Conclusion:** CONFIRMED dipertahankan; amendemen di atas menutup lacuna secara presisi tanpa mengubah vonis.

**Confidence:** tinggi — lima efek masing-masing dirujuk ke berkas + baris.

---

## DEFENSE RESPONSE — N-009 (F-B-004: klaim KBBI)

**Finding:** F-B-004

**Validator Question:**
(1) Akui `status` (serapan baku) dan render `Dasbor` (patuh) bukan pelanggaran; persempit ke ENUM-mentah + `{{.TimerType}} timer`. (2) Pisahkan inkonsistensi istilah dokumen-vs-render sebagai isu istilah. (3) Kamus EN→ID per literal + lokasi render yang dikunci test; konfirmasi ketiadaan kamus di JS/CSS untuk seluruh render Status/TimerType.

**Position:** partially concede — dua kaki diakui bukan pelanggaran; vonis dipersempit dan dipertahankan pada intinya.

**Evidence:**

1. YANG DIAKUI BUKAN PELANGGARAN:
   - Kata `status` — serapan baku Indonesia (padanan "state/standing"); kehadirannya di label ("status <span…>", filter `?status=`) bukan bukti pelanggaran KBBI. Klaim awal yang menyasar kata ini ditarik.
   - Render `Dasbor` — patuh (kamus baku untuk "dashboard"): implementasi memakai `{Label: "Dasbor"}` (`breadcrumb.go:39,54,64,132`), test mengunci `Dasbor` statis (`nav_no_root_links_test.go:56-60`, `breadcrumb_test.go:63-103`). Yang Inggris ("Dashboard") hanya hidup di KOMENTAR/dokumen — dipindah ke isu (2).
2. YANG DIPERTAHANKAN — literal ENUM Inggris mencapai DOM tanpa kamus:
   - Riwayat murid: `views/student/history.html:37-41` dan `:83-87`: `{{else}}{{.Status}}{{end}}` — cabang else mencetak `p.status` DB apa adanya (`pending`/`registered`/`started`/`selesai`/`dikeluarkan`). Rantai: `history.go:61` (`SELECT …, p.status, …`) → `:80` (`Scan(…, &r.Status, …)`) → `:85` (`r.Removed = r.Status == "dikeluarkan"`) — hanya `dikeluarkan` dan `cheating` yang diterjemahkan ke badge (`dikeluarkan`/`ditandai`); TIGA literal Inggris (`pending`, `registered`, `started`) lolos mentah. (`selesai`/`dikeluarkan` memang Indonesia — tidak dipermasalahkan.)
   - Monitor guru: `views/teacher/monitor.html:52-53` (`<span class="badge text-bg-secondary">{{.Status}}</span>`) + render JS yang sama `web/js/monitor.js:165` (`badges.appendChild(el("span", "badge text-bg-secondary", c.status))`) + `:108` (filter `c.status === "pending"` — literal Inggris sebagai logika UI). Nilai yang mengalir: `global.go:78,143` (`Status string json:"status"` dari DB), `:169` (cabang `pending`), `:220` (`started`).
   - `{{.TimerType}} timer` — `views/teacher/monitor.html:8`: `{{.TimerType}} timer` mencetak `global`/`per_soal`/`tanpa_timer` + kata Inggris "timer". Kontras positif: halaman lain MEMETAKAN timer ke Indonesia — `quiz_list.html:32` (`Global`/`Tanpa timer`/`Per pertanyaan`), `settings_fields.html:14-16` (label Indonesia penuh). Jadi monitor adalah satu-satunya render mentah — inkonsistensi internal, bukan keterbatasan kosakata (padanan tersedia: "pengatur waktu"/"pewaktu"/"tanpa pewaktu", atau pakai peta `quiz_list` yang sudah ada).
   - Ketiadaan kamus: `grep -rn "TimerType|timerType" views/ web/js/` hanya menemukan penerus mentah (`monitor.html:8,60`; `monitor.js:16`) vs pemeta (`quiz_list.html:32`); tidak ada modul/fungsi kamus status di `web/js/` maupun `web/css/` (grep `status` di `workspace.js` hanya badge jawab `data-q-status`; di `teacher_dashboard.js` hanya filter/status kuis `q.status` server-side `:77,125` dengan `chip/label` dari server — bukan kamus literal partisipan). Tidak ada test pengunci bahasa untuk nilai-nilai ini (test breadcrumb/nav hanya mengunci `Dasbor`, bukan literal status).
3. ISU ISTILAH (dipisah, bukan bukti KBBI): "Dashboard" muncul di `breadcrumb.go:11` ("Dashboard" label), `:35` ("Dashboard / Guru / Quiz"), `:61` ("walks /teacher/<segments>: Dashboard / …"), `:128` ("Dashboard / Beranda"), `breadcrumb.html:4` (`"Dashboard" crumb`), `nav_no_root_links_test.go:14` (`"Dashboard" crumb`), tabel §9 ("static "Dashboard" crumb") — sementara render + test perilaku memakai `Dasbor`. Ini inkonsistensi istilah dokumen-vs-kode (terminology control), bukan pelanggaran bahasa pengguna. Dipindah kategorinya, tidak dihapus relevansinya: thesis memakai istilah yang tidak pernah dirender.
4. Keterbatasan yang diakui: tidak ada akses kamus KBBI daring pada putaran ini; penilaian "serapan baku" untuk `status`/`dasbor` bersandar pada pengetahuan bahasa umum, bukan sitasi KBBI. Klaim sempit yang dipertahankan (literal Inggris `pending/registered/started`, kata `timer`) tidak membutuhkan sitasi kamus — keduanya bukan kosakata Indonesia menurut definisi (tidak ada padanan bakunya yang disengketakan; padanan Indonesia tersedia dan dipakai di halaman lain).

**Kamus EN→ID yang diusulkan (per literal, satu arah, konsisten dengan halaman lain):**

| Literal (DB/kode) | Render sekarang | Usulan | Lokasi render yang harus dikunci test |
|---|---|---|---|
| `pending` (partisipan) | `pending` | `Menunggu persetujuan` | `history.html:40,86`; `monitor.html:53`; `monitor.js:165` (+ logika `:108`) |
| `registered` | `registered` | `Terdaftar` | sama |
| `started` | `started` | `Mengerjakan`/`Berjalan` (pilih satu, konsisten) | sama |
| `TimerType` + kata `timer` | `global timer` dsb. | peta `quiz_list.html:32` (`Global`/`Per pertanyaan`/`Tanpa timer`) + label `Pewaktu`/hilangkata Inggris | `monitor.html:8` |

**Reasoning:** Rumusan absolut spec ("All UI text", "never user-facing") membuat satu counterexample valid menggugurkan klaim — dan counterexample-nya ada tiga literal + satu frasa, lengkap dengan rantai DB→handler→template→JS. Pengakuan atas `status`/`Dasbor` justru memperkuat vonis sisa: yang tertinggal adalah inti yang tak terbantahkan. Pemisahan isu istilah menjaga kejujuran kategoris (tidak semua inkonsistensi adalah pelanggaran bahasa).

**Counterargument:** "ENUM internal, pengguna memaklumi." Jawaban: spec sendiri (§7) yang menutup pembelaan itu ("never user-facing"); kenyataannya facing. Dan halaman lain membuktikan pemetaan itu mudah (sudah dilakukan untuk `quiz_list`).

**Conclusion:** Vonis dipersempit tetapi DIPERTAHANKAN pada kaki ENUM-mentah + `TimerType timer`; dua kaki lain ditarik/dipindah kategori. Severity MEDIUM dipertahankan (klaim universal gugur oleh bukti, bukan oleh selera).

**Confidence:** tinggi untuk kaki yang dipertahankan (rantai render lengkap + kontras positif halaman lain); sedang untuk penilaian serapan (`status`) karena tanpa sitasi KBBI langsung — dinyatakan terbuka untuk koreksi validator berliteratur.

---

## DEFENSE RESPONSE — N-010 (F-B-005: skip-validasi + robustness + §12)

**Finding:** F-B-005

**Validator Question:**
(1) Matriks ekshaustif tests/integration → §11.15/16/17: akui freeze/rehydrate yang sudah diuji atau tunjukkan mengapa tak memadai (kutip assert hangup+200ms). Tegaskan sisa benar-benar tanpa pasangan = §11.17 + failure-injection generik (grep ekshaustif + pemetaan). (b) dipersempit PARTIALLY. (2) Untuk (c): "misleading-by-omission" vs "palsu" — satu paragraf posisi. Leg (a) tak disengketakan — pertahankan tegas.

**Position:** partially concede pada (b) — akui cakupan freeze/rehydrate yang kuat; tegaskan sisa gap; pertahankan penuh (a) dan akibat (c) dengan redaksi yang disepakati.

**Evidence:**

1. Leg (a) — DIPERTAHANKAN PENUH, tak disengketakan validator mana pun:
   - `internal/handlers/auth.go:104-115` (urutan menentukan, kutipan struktur): `if !must && bcrypt.CompareHashAndPassword(…) != nil → 401` — bila `must==true` perbandingan DILEWATI — `if !aktif → 401 Inactive` — `target := "/student"; if must { target = "/change-password" }` + `startSession(c, uid, "murid", target)` — sesi diterbitkan tanpa bukti pengetahuan kata sandi. `session.go:130-139` (`ForceChangePassword`) hanya me-redirect (`/change-password` vs `/logout`), tidak mencabut sesi.
   - Dikunci oleh test (bukan kebetulan): `tests/integration/auth_test.go:548-551` — login dengan `password: totally-wrong` pada akun flag-must-change → `302 /change-password` + cookie sesi diterbitkan (`c2 := sessionCookie(resp)`; `if c2 == nil { t.Fatal("no session cookie…") }`). Perilaku ini adalah Kontrak Teruji — dan itulah masalahnya: thesis memandatkan (§11.2 "next login skips password validation") apa yang seharusnya dilarang.
2. Matriks §11.15/16/17 → berkas (akui yang ada — koreksi terhadap laporan awal):
   - §11.16 (disconnect-freeze, last heartbeat): **DIUJI, dan kuat.**
     - `flow_global_test.go:452-515` (`TestDisconnectFreezesAtLastHeartbeat`): silence 400ms (`:465`), hangup (`:484-486` + sleep 600ms), sweep hingga freeze (`:489-502`), assert inti `:503-507`: `if frozen.After(hangupAt.Add(200 * time.Millisecond)) { t.Fatalf("frozen at %s — detection-time freeze, not last heartbeat …") }` + batas bawah `:508-510` + reconnect tanpa-hilang (`:513+`).
     - `flow_persoal_test.go:768-835` (`TestPerQuestionDisconnectFreezeAndResume`): struktur identik untuk timer per-soal, assert sama (`frozen.After(hangupAt.Add(200ms)) → Fatal`).
     - `tests/unit/timer_test.go:45-86` (`TestOnDisconnectFreezesAtLastHeartbeatNotDetection` + EdgeCases): `freeze == lastBeat-second`, `remaining == 70`, plus tepi (no-deadline, no-beat, skew, beyond-deadline).
     - Penilaian jujur: assert `hangup+200ms` MEMBEDAKAN freeze-heartbeat dari freeze-detection (silence 400ms > toleransi 200ms) — test ini memadai dan akan gagal bila kode memakai detection-time. Klaim awal "tanpa uji" untuk §11.16 DITARIK.
   - §11.15 (rehydrate): **DIUJI pada tingkat kontrak yang diklaim.**
     - `realtime_test.go:412+` (`TestSSERestartRehydrate`): hub#1 mati (`hub1.Close()`, koneksi lama mati `:457-460`), hub#2 segar hanya melihat yang di-publish ulang dari DB (`SELECT user_id FROM participants …` → `Publish(snapshot)` → assert event `snapshot` + decode `Data []uint64` + himpunan `{uidA, uidB}`). Ini membuktikan properti "hub baru + snapshot dari DB" — sesuai rumusan §11.15 pada level transport. Keterbatasan yang diakui (bukan kegagalan test): ia mensimulasikan restart pada level hub/SSE, bukan restart kontainer penuh (cache hangat, `ends_at` lewat-selama-mati, antrean approval) — sisi itu ditutup sebagian oleh test boot/finish lain, tetapi bukan pasangan 1:1 untuk setiap klausa §11.15.
   - §11.17 ("DB write failure → no broadcast"): **TANPA PASANGAN — gap sesungguhnya.**
     - Grep ekshaustif (putaran ini): `no broadcast|write failure|fault injection|kill DB|killDB|close DB|sqlmock|DB down|database down|unreachable` → nihil kecuali `testutil.go:54,66` (Skip DB) + `upload_test.go:26` (komentar skip). Tidak ada `sqlmock`/fault-injection di `go.mod` maupun `tests/`.
     - Kontras: jalur publish positif diuji (`realtime_test.go:277-330` `TestSSEPublishReachesSubscribers`: publish → assert `event == "answered"` + round-trip JSON + isolasi topik). Arah GAGAL (matikan DB di tengah mutasi → assert tidak ada frame) tidak ada. Seluruh call-site `Hub.Publish` (`global.go:345,569,731-732,914-915,1017,1188-1189`; `streams.go:104`; `student.go:155,1184`) tidak memiliki pasangan uji-negatif.
     - Kaitan dengan green-vakum (N-007): suite yang memuat klaim ini adalah suite yang sama yang men-skip massal tanpa DB — sehingga klaim failure-handling justru paling lemah diuji pada kondisi yang paling membutuhkannya.
3. Leg (c) — POSISI: "misleading-by-omission, bukan fabrikasi" (disepakati; satu paragraf alasan):
   §12 "None — all 24 clarification decisions … are resolved" dibaca paling jujur sebagai pernyataan status-resolusi internal penulis (mereka merasa selesai), bukan pemalsuan fakta yang diketahui ("penulis tahu ada lubang lalu menulis None"). Bukti untuk kesengajaan tidak ada dan tidak dicari — standar akademik tidak menuntut pembuktian niat. Tetapi akibatnya SAMA dengan yang dilaporkan: empat lacunae terbukti (dependensi yatim + tak-terdaftar di N-006, atribusi uji di N-007, kolom `aktif` di N-008, literal Inggris di N-009) adalah — menurut definisi — hal yang belum resolved, sehingga kalimat "all … are resolved" menyesatkan secara kelengkapan (omission). Perbaikan yang dituntut identik (buka §12, daftarkan sisa pertanyaan) tanpa menuduh fabrikasi. Redaksi "palsu" pada laporan awal diganti "menyesatkan-secara-kelengkapan" — vonis material tidak berubah.

**Reasoning:** Koreksi terbesar putaran ini ada pada kaki (b) Hunter B: laporan awal overstate dengan menggabungkan "tanpa uji freeze" (salah — ada dan kuat) dengan "tanpa uji no-broadcast" (benar). Setelah pemisahan, struktur vonis menjadi: (a) CONFIRMED penuh, (b) PARTIALLY (freeze/rehydrate teruji; no-broadcast + failure-injection tanpa pasangan), (c) omission terkonfirmasi dengan redaksi yang adil. Ini melemah di satu kaki tetapi menguat secara keseluruhan — karena sisa klaim kini presisi dan tak terbantahkan.

**Counterargument:** "Write-order (DB→commit→invalidate→broadcast) terbukti dari review kode; tak perlu failure-injection." Jawaban: test-quality bar spec sendiri (§9) menuntut "fails when the code is wrong — not merely does not panic"; urutan kode yang benar hari ini bukan bukti ia akan gagal-bersuara bila seseorang memindahkan `Publish` sebelum `Commit` esok. Hanya uji-negatif yang mengunci invarian.

**Conclusion:** (a) dipertahankan penuh dan tegas; (b) dipersempit menjadi PARTIALLY dengan gap presisi (§11.17 + failure-injection generik); (c) redaksi diganti "misleading-by-omission", konsekuensi sama (buka §12). Severity HIGH dipertahankan — ditopang penuh oleh (a) yang tak disengketakan.

**Confidence:** tinggi untuk (a) (rantai kode + test pengunci); tinggi untuk matriks (b) setelah koreksi (nama-berkas→klaim→assert dikutip); tinggi untuk (c) sebagai omission (konsekuensi logis N-006–N-009).

---

## Ringkasan DEFENSE RESPONSE per temuan

- **N-006 (F-B-001):** PARTIALLY CONCEDE — (a) komentar `// indirect` basi ditarik sebagai bukti (4 modul terbukti dipakai via `go mod why` + `tidy -diff`); (b) `gocsv` yatim + (c) Chart.js v4.5.1 sebagai *undocumented runtime dependency* bertahan; inventarisasi vendor: hanya Chart.js yang tak terdaftar. Severity HIGH bertahan.
- **N-007 (F-B-002):** PARTIALLY CONCEDE — (d) 7-subtests dicabut (klaim spec akurat); (b) dipersempit (seam berhenti di batas quizengine; bedakan presentasi vs penentu-hasil); overclaim bertahan pada paralel (tanpa `t.Parallel`/CI), atribusi unit, green-vakum (119 SKIP / 6 PASS / 0 FAIL, exit 0 tanpa DB).
- **N-008 (F-B-003):** DEFEND — CONFIRMED; semantik deactivation per efek dirujuk (login/`MsgLoginInactive`, sesi-mati-seketika, riwayat tak difilter, reaktivasi tanpa sesi) + usulan redaksi §5.
- **N-009 (F-B-004):** PARTIALLY CONCEDE — `status`/`Dasbor` diakui bukan pelanggaran; isu istilah dipisah kategori; vonis bertahan pada ENUM-mentah (`pending/registered/started` via `history.html`+`history.go`+`monitor.html`/`monitor.js`) + `{{.TimerType}} timer`; kamus EN→ID + lokasi kunci test diusulkan.
- **N-010 (F-B-005):** PARTIALLY CONCEDE pada (b) — freeze/rehydrate DIAKUI teruji (assert `hangup+200ms` dikutip); gap presisi = §11.17 no-broadcast + failure-injection (grep nihil); (a) skip-validasi dipertahankan penuh (dikunci `auth_test.go:548-551`); (c) redaksi menjadi *misleading-by-omission*, konsekuensi sama.
