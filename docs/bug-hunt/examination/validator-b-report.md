# Validator B Report — SECURITY IMPACT & METHODOLOGICAL VALIDITY (Round 1)

> Validator B (persistent). Fokus: SECURITY IMPACT & METHODOLOGICAL VALIDITY.
> Repository: `D:/Project/Mother/quiz` (branch `main`).
> Thesis: `docs/superpowers/specs/2026-09-26-quiz-website-design.md`.
> Normalized: `docs/bug-hunt/normalized.md` (N-001…N-010) dibaca utuh + kedua laporan hunter mentah.
> Output milik Validator B saja. Tidak ada perubahan kode produksi.
> Posisi default: "klaim belum terbukti". Sikap: hostile-but-fair, independen.

## 0. Index & Method

- `mcp__codebase_memory_mcp_list_projects` → `D-Project-Mother-quiz` terdaftar (branch `main`).
- `mcp__codebase_memory_mcp_index_status` → `ready`, 5206 nodes / 19442 edges, indexed `2026-10-07T08:27:10Z`. `index_repository` TIDAK dijalankan (indeks segar).
- Verifikasi independen via `read` langsung terhadap berkas aktual (bukan mengutip hunter):
  `internal/handlers/auth.go:80-163,380-413`, `internal/middleware/session.go` (penuh),
  `internal/handlers/password_resets.go` (penuh), `internal/cache/keys.go`,
  `internal/handlers/global.go:740-923`, `internal/handlers/personal.go:40-80`,
  `internal/handlers/live.go:130-215`, `internal/handlers/student.go:230-333,1639-1713`,
  `internal/quizengine/anticheat.go`, `go.mod`, `internal/handlers/export.go:1-43`,
  `views/teacher/dashboard.html:65-135`, `views/student/history.html:30-90`,
  `views/teacher/monitor.html:1-63`, `migrations/0003_users_aktif.sql`,
  `tests/unit/authlogic_test.go:1-33`, `tests/integration/auth_test.go:499-583`,
  grep `t.Parallel` / `gocsv` / import langsung / uji-negatif robustness.
- Hierarki bukti §20: kode reproduksibel > test tereksekusi > bukti source > dokumen spec > inferensi. Spekulasi tidak diperlakukan sebagai bukti.

## 1. Examination per Temuan (§5)

### N-001 (F-A-001) — Guard closeQuiz longgar vs spec §6.6

- Validator: Validator B
- Challenge: Apakah `IN ('aktif','berjalan')` benar menyimpang dari §6.6, atau putusan desain untuk menyatukan jalur per-question-close (§6.5) dan global-STOP? Apakah ada dampak keamanan (akses tak sah / eskalasi)?
- Evidence (independen): `global.go:887-889` harfiah `WHERE id=? AND status IN ('aktif','berjalan')`; komentar di `closeQuiz` (`global.go:824-829`) sendiri menulis `WHERE status='berjalan'` — komentar vs kode sudah inkonsisten internal. `Stop` (`global.go:746-781`) resolve id lalu `closeQuiz` tanpa pra-cek status; `msgNotRunning` hanya saat `closed==false`. `closeWithModal` (`personal.go:56-80`) memakai rutin sama untuk `aktif→selesai` (kasus per-question, benar memerlukan `aktif`). Spec §6.6: funnel tunggal `WHERE status='berjalan'`.
- Security impact: NIHIL. Aktor = guru terautentikasi (`/teacher` + AuthTeacher). Tidak ada bypass auth, tidak ada akses murid, tidak ada kebocoran sesi. Dampak murni integritas/akurasi: STOP-sebelum-START pada kuis global `aktif` sukses menutup (registered/pending difinalkan NULL "not attempted") padahal seharusnya 409.
- Metodologi: Klaim spec "satu transisi" gugur oleh satu counterexample (STOP pada `aktif`). Pembelaan "disengaja untuk dua jalur" justru menuntut amendemen spec (pisahkan guard per jalur), bukan mempertahankan kalimat §6.6 apa adanya.
- Evidence Requested: nihil (bukti literal decisive; anti-loop §18 — tidak mengarang keberatan setelah bukti decisive).
- Bug Hunter Response: <menunggu via Main>
- Validator Verdict: **CONFIRMED** (fakta). Severity dinilai ulang: **MEDIUM → MEDIUM (batas bawah; tanpa komponen keamanan)**. Overclaim severity keamanan: tidak ada; ini defect spec↔code, bukan vuln.
- Remaining Objection: nihil.
- Status: CLOSED

### N-002 (F-A-002) — Approve-reset tanpa invalidasi mirror sesi (stale MustChangePW)

- Validator: Validator B
- Challenge: Apakah jendela basi benar reachable dan apakah HIGH proporsional? Apakah ini trade-off terdokumentasi atau defect? Apakah ForceChangePassword lock / TTL / RevokeUserSessions membatasi dampak?
- Evidence (independen): `ApproveReset` (`password_resets.go:74-110`) hanya `UPDATE password_resets` + `UPDATE users SET must_change_pw=1` dalam tx; nihil `Store/Revoke/SessionKey/cache` (verifikasi read penuh). `LoadSession` (`session.go:58-97`): hit mirror → kembalikan snapshot tanpa revalidasi; miss → baca DB + `store.Set(SessionKey, s, TTLSession)` dengan `TTLSession=5min` (`cache/keys.go:11`). `ForceChangePassword` (`session.go:130-139`) hanya redirect berbasis snapshot. Pembanding: `ChangePassword` (`auth.go:380-413`) dan deaktivasi murid memanggil `RevokeUserSessions` (mirror-dulu lalu DB) — pola invalidasi instan ada, tetapi tidak dipakai di Approve.
- Rantai eksploitasi (reachable, presisi): precondition = sesi murid aktif HANGAT (mirror sudah terisi sebelum approve). Trigger = guru approve. Dalam sisa TTL entri itu (maks 5 menit), request berikutnya dengan sesi sama membawa `MustChangePW=false` basi → TIDAK di-redirect ke `/change-password` → murid terus memakai kredensial lama. Ini evasi penegakan, bukan pengambilalihan akun orang lain. Batas: (i) TTL 5 menit per sesi (bukan permanen); (ii) sesi DINGIN (dibuat setelah approve) membaca DB segar → langsung `must=true`; (iii) ForceChangePassword lock tidak membantu karena ia membaca snapshot yang basi itu sendiri; (iv) sesi lama tetap mati saat password akhirnya diganti (`RevokeUserSessions` di ChangePassword).
- Design trade-off vs defect: Komentar `Live.SweepDisconnects` mendokumentasikan trade-off lock-vs-atomicity, tetapi TIDAK ada komentar serupa di ApproveReset yang membenarkan penundaan. Spec §6.2 tabel Sessions: "explicit delete on logout / password change (instant revocation)" + golden rule "cache never newer than DB". Mirror yang menyajikan `false` saat DB `true` melanggar golden rule. Maka ini defect (kelalaian invalidasi), bukan trade-off yang didokumentasikan.
- Severity: HIGH overclaim. Dampak = penundaan penegakan ≤5 menit pada sesi sendiri, masih terautentikasi sebagai diri sendiri, tanpa eskalasi hak / akses silang. Tidak ada RCE, tidak ada takeover. Proporsional: **HIGH → MEDIUM**.
- Evidence Requested: nihil (ketiadaan invalidasi harfiah + TTL harfiah decisive).
- Bug Hunter Response: <menunggu via Main>
- Validator Verdict: **PARTIALLY CONFIRMED** (fakta stale-window CONFIRMED; severity HIGH overclaim → MEDIUM; dampak keamanan = evasi sementara, bukan vuln takeover).
- Remaining Objection: nihil.
- Status: CLOSED

### N-003 (F-A-003) — SweepDisconnects menahan mutex melintasi I/O DB

- Validator: Validator B
- Challenge: Apakah lock-melintasi-I/O terbukti harfiah? Apakah ada dampak keamanan (availability/DoS) atau murni liveness? Apakah komentar kode menjadikannya trade-off yang didokumentasikan?
- Evidence (independen): `live.go:186-209`: `l.mu.Lock(); defer Unlock()` melingkupi loop berisi `QueryRowContext` (195) + `ExecContext` (205) per pid. Semua akses Live lain butuh `l.mu` (`Connect` kecuali fase resume-DB, `Connected`, `Disconnect`). Pemanggil `WatchOnce` tiap 1 detik (`global.go:955-956`). Komentar (`live.go:180-185`): "map lock dipegang melintasi query agar Connect konkuren tidak interleave" — niat atomicity freeze-vs-resume eksplisit.
- Security impact: TIDAK ada dampak confidentiality/integrity/auth. Availability: contention tail-latency (Connect/Ranker/Snapshot terblokir selama N round-trip tiap tick; memburuk saat DB lambat + banyak disconnect). Bukan kebocoran goroutine (tidak diklaim). Eksploitasi adversarial (murid sengaja refresh massal untuk memblokir registry) membutuhkan banyak partisipan + DB lambat bersamaan; tidak ada amplifikasi jarak jauh yang praktis dari satu aktor. Klasifikasi tepat: availability/liveness, bukan vuln keamanan.
- Trade-off vs defect: Trade-off DIDOKUMENTASIKAN (komentar), tetapi pilihan salah arah: critical section terbesar justru melintasi I/O; perbaikan baku (snapshot kandidat di bawah lock singkat, I/O di luar lock, commit dengan guard `WHERE status='started'`/CAS) tidak diterapkan. Dokumentasi niat tidak menyembuhkan contention.
- Severity: MEDIUM proporsional (fakta lock+IO tinggi-confidence; magnitude tak diukur → bukan HIGH). Tepat dipertahankan MEDIUM.
- Evidence Requested: nihil (struktur decisive; kuantifikasi latensi akan memperkuat tetapi tidak dibutuhkan untuk verdict fakta).
- Bug Hunter Response: <menunggu via Main>
- Validator Verdict: **CONFIRMED**. Severity **MEDIUM dipertahankan (availability, bukan keamanan)**.
- Remaining Objection: nihil.
- Status: CLOSED

### N-004 (F-A-004) — Gerbang join memakai snapshot basi (TOCTOU)

- Validator: Validator B
- Challenge: Apakah snapshot basi + tanpa re-read/lock kuis terbukti? Apakah ada dampak keamanan (join tak sah / eskalasi) atau murni integritas roster?
- Evidence (independen): `student.go:236-316`: `joinQuiz(ctx, quiz, ...)` menerima `quiz` dari `quizByCode` pra-tx (read-committed tanpa lock); dalam loop: `BeginTx` → `latestParticipant(... FOR UPDATE)` (mengunci baris participants, bukan quizzes) → cabang no-row memanggil `quizGates(quiz)` atas objek luar-tx → `createAttempt` (INSERT). `grep FOR UPDATE` di student.go hanya participants. Tidak ada `SELECT quizzes ... FOR UPDATE` / re-read status di dalam tx. Pembanding Start (`UPDATE ... WHERE status='aktif'`) benar guard-di-dalam-tx — kontras memperkuat.
- Rantai reachable: jendela = antara `quizByCode` dan `Commit` INSERT; trigger = guru START (`aktif→berjalan`) konkuren dengan join. Hasil = baris `registered`/`pending` yatim pada kuis `berjalan`; `classifyView` kemudian 409 IN_PROGRESS ("missed START"). Precondition timing ketat, tidak dikendalikan penyerang secara deterministik; belum didemonstrasikan konkuren di sini (hunter juga jujur: confidence sedang-tinggi, bukan "tereksploitasi deterministik").
- Security impact: NIHIL untuk auth/otorisasi. Baris yatim tidak memberi akses soal/jawab (malah berujung 409); tidak ada eskalasi murid→guru, tidak ada akses silang akun. Dampak = integritas roster (waiting-room count, "N working", "not attempted" fiktif) + pesan error tidak murni (ada efek samping). Klaim §10 "guard server-side di dalam tx" gugur untuk cabang no-row.
- Severity: MEDIUM borderline-atas untuk integritas; dari sudut keamanan murni LOW. Pertahankan MEDIUM sebagai defect integritas, dengan catatan eksplisit "tanpa dampak keamanan".
- Evidence Requested: nihil untuk verdict fakta (stale-read + tanpa re-read decisive). Repro race konkuren akan menaikkan confidence realisasi tetapi tidak dibutuhkan untuk CONFIRMED pola.
- Bug Hunter Response: <menunggu via Main>
- Validator Verdict: **CONFIRMED**. Severity **MEDIUM dipertahankan sebagai integritas; dampak keamanan = NIHIL**.
- Remaining Objection: nihil.
- Status: CLOSED

### N-005 (F-A-005) — Collapse anti-cheat tembus saat konkuren

- Validator: Validator B
- Challenge: Apakah check-then-insert tanpa lock terbukti? Apakah ada dampak keamanan atau murni akurasi badge?
- Evidence (independen): `student.go:1651-1700`: `latestParticipant(false)` tanpa lock → `SELECT created_at,kind ... LIMIT 1` di luar tx → `ShouldRecord(lastAt,now,...)` → `BeginTx` → `INSERT` → `SELECT COUNT(*)` → `Commit`. `ShouldRecord` (`anticheat.go:14-22`) murni fungsi waktu (`CollapseWindow=10s`), tanpa sinkronisasi. Tidak ada `SELECT ... FOR UPDATE` atas event terakhir / advisory lock / constraint unik (window tidak dapat di-UNIQUE-kan alami).
- Security impact: NIHIL. Duplikat menggembungkan `count` milik penyerang sendiri (self-harm, lebih terlihat di monitor guru); tidak mengubah skor/peringkat (`cheat` frame membawa total absolut, mudah dinormalisasi di tampilan); tidak ada eskalasi, tidak ada bypass deteksi (malah over-detection). Flood-control 10-detik hanya benar untuk submisi sekuensial; perlu kualifikasi "single-flight".
- Severity: LOW tepat dan proporsional. Bukan best-effort yang didokumentasikan sebagai best-effort (kode berkomentar seolah guarantee), maka ini kualifikasi spec yang hilang, bukan non-issue.
- Evidence Requested: nihil (pola decisive; demo konkuren dua POST sejajar akan konklusif tetapi verdict fakta tidak bergantung padanya).
- Bug Hunter Response: <menunggu via Main>
- Validator Verdict: **CONFIRMED**. Severity **LOW dipertahankan; dampak keamanan = NIHIL (murni akurasi)**.
- Remaining Objection: nihil.
- Status: CLOSED

### N-006 (F-B-001) — Klaim closed-8-direct gugur

- Validator: Validator B
- Challenge (metodologi): (i) Apakah `// indirect` benar menggugurkan klaim, atau semantik Go modules disalahbaca? (ii) Apakah Chart.js membatalkan "closed" atau kategori berbeda (aset web vs modul Go)?
- Evidence (independen):
  - `go.mod:5-9` blok direct hanya 3 entri; `go.mod:12-24` menandai `gocsv, go-qrcode, go-sse, excelize/v2, x/crypto` (+ transitif) sebagai `// indirect`. Verbatim: `gocsv v0.0.0-... // indirect`.
  - Import langsung AKTUAL: `cleanenv` (`internal/config/config.go:12`), `x/crypto/bcrypt` (`handlers/auth.go:16`), `excelize/v2` (`handlers/export_sheets.go:6`), `go-qrcode` (`handlers/teacher_quiz.go:16`), `go-sse` (`realtime/hub.go:16`). Artinya 5 modul yang diklaim-direct BENAR dipakai langsung, terlepas dari komentar `// indirect` yang basi (komentar = output `go mod tidy`, bukan definisi operasional "direct"; semantik Go: direct = diimpor paket main module). Sub-butir (a) hunter sebagai "salah terhadap manifest" benar secara literal-label tetapi SALAH secara semantik-bahasa-Go bila dibaca sebagai "tidak dipakai langsung". Ini formalisme yang melemahkan, bukan menguatkan.
  - Sub-butir (b) decisive: `grep gocsv` hanya `go.mod/go.sum`/plan/docs; nol import `.go`; `export.go:3-16` memakai `encoding/csv` stdlib (tag `csv:"..."` gaya-gocsv ditulis manual). `gocsv` = dependensi yatim yang diklaim sebagai mesin CSV (§2) tanpa bukti implementasi.
  - Sub-butir (c) decisive: `web/vendor/chartjs/chart.umd.min.js:1-6` header `Chart.js v4.5.1 MIT`; `dashboard.html:72-84` tiga `<canvas>` + `:130` `<script src="{{asset "/assets/chartjs/chart.umd.min.js"}}">`; komentar template sendiri mengakui "Chart.js, vendored". Spec §2/§4/§10 nihil menyebut Chart.js; §4 `web/vendor/` hanya menyebut `bootstrap-icons/` + `theme.css`; §2 "sanctioned CDN exception" hanya Google Fonts (Chart.js lokal, jadi tidak melanggar kalimat CDN, tetapi melanggar kelengkapan §4 + spirit closed + §11.21 "no dependencies outside closed list").
- Metodologi: Klaim closed-world (§2 + §10 + §11.21) adalah klaim terkuat dan falsifiable — dan gugur oleh (b)+(c) tanpa memerlukan (a). (a) harus DITOLAK sebagai argumen (misread semantik), tetapi vonis keseluruhan bertahan pada dua kaki yang decisive.
- Security impact: `gocsv` yatim = permukaan berlebih yang dideklarasikan tetapi tidak tereksekusi (risiko rendah, justru memperlemah klaim "security floors via govulncheck" karena yang dipin tidak dipakai). Chart.js = blind spot supply-chain nyata: versi JS tidak dipantau `govulncheck`/`go.mod`; eksekusi di dashboard guru (konteks guru). Bukan RCE yang terbukti, tetapi celah ketertutupan keamanan. Dari sudut eksploitasi murni LOW; dari sudut validitas klaim-keamanan HIGH (klaim ketertutupan + "govulncheck must report no vulns" tidak menutup artefak yang dieksekusi).
- Bug Hunter Response (R2, `hunter-b-defense-r1.md` N-006): PARTIALLY CONCEDE — (a) ditarik sebagai bukti (komentar `// indirect` basi; `go mod why`: gocsv "does not need", go-sse/crypto-bcrypt/go-qrcode dipakai langsung; `go mod tidy -diff` promosikan 4 ke direct + HAPUS gocsv); (b) gocsv yatim DIPERTAHANKAN PENUH; (c) DITURUNKAN menjadi "undocumented runtime dependency" Chart.js v4.5.1 (tak ada kalimat §2/§10/§11.21 yang memasukkan aset JS ke closed list; inventarisasi vendor: hanya Chart.js tak-terdaftar; bootstrap-icons/theme.css terdaftar; font sanctioned). §17: bukti baru decisive, keberatan terjawab.
- Validator Verdict: **PARTIALLY CONFIRMED (FINAL)** — (a) DITOLAK/ditarik kedua belah pihak (konsensus); (b)+(c) CONFIRMED. Severity **HIGH dipertahankan sebagai cacat metodologis/ketertutupan dokumen (kombinasi klaim-tanpa-pakai + pakai-tanpa-klaim meruntuhkan keterujian §11.21; bukan vuln remote)**. §18: tidak ada keberatan baru (anti-loop — rumusan (c) yang diturunkan diterima; materialitas omission tetap).
- Remaining Objection: nihil.
- Status: CLOSED

### N-007 (F-B-002) — Metodologi testing §9 overclaim

- Validator: Validator B
- Challenge (metodologi): Apakah ketiadaan `t.Parallel` membatalkan "parallel-safe", atau klaim itu aspirasional dan suite tetap valid? Apakah tiap sub-butir didukung bukti?
- Evidence (independen):
  - (a) `grep t.Parallel` nihil di `tests/`+`internal/` (terkonfirmasi; `AGENTS.md:116` bahkan melarang: "no `t.Parallel()`, no build tags anywhere (verified by grep)"). Spec §9: "Fast, deterministic, parallel-safe" + "safe to re-run and run sequentially" — internal kontradiksi (paralel vs sekuensial) + nol bukti paralel. "Parallel-safe" tanpa satu pun test paralel = unproven/vacuous, bukan disproven. Suite tetap valid sekuensial; yang gugur adalah kata "parallel-safe".
  - (b) `time.Now()` langsung di handler: `auth.go:118,148`, `global.go:243,265,956`, `student.go` (8 titik), `live.go:140,143` vs `quizengine/timer.go:6-11` murni ("never calls time.Now() directly") + `Clock` seam. Klaim "no time.Now in logic" hanya benar di `quizengine`; seam jam berhenti di batas handler yang justru menentukan kelulusan (GC login, server_now, freeze, ends_at, collapse). Overclaim scope.
  - (c) `authlogic_test.go:8-14` header mendelegasikan forgot-password/join-retry ke integrasi (`auth_test.go`, `quiz_crud_test.go`), sementara tabel §9 mengatribusikannya ke unit. Misatribusi terbukti harfiah.
  - (d) "7 subtests": `assets_test.go:83-125` memang 7 entri tabel dalam 1 `t.Run` loop. Menghitungnya sebagai "7 subtests" adalah perumusan longgar tetapi hitungan ada — keberatan hunter atas butir ini LEMAH dan harus dipersempit (bukan vonis, melainkan catatan redaksional).
  - (e) `testutil.go:62-77` `Skipf` saat DB tak terjangkau → "fully green" (§11.14) kondisional/vakum pada mesin tanpa DB. Praktik skip standar, tetapi mengubah arti "green" (green = pass-or-skip, bukan all-pass). Overclaim kondisional terbukti.
- Metodologi: Cacat validitas konstruk (instrumen tidak mengukur apa yang diklaim: paralelisme, determinisme penuh, cakupan per-level, green tanpa syarat). Bukan berarti suite gagal — berarti klaim harus diturunkan ke yang terbukti (hapus "parallel-safe", perbaiki atribusi tabel, nyatakan prasyarat DB).
- Validator Verdict: **CONFIRMED** (dengan koreksi: sub-butir "7 subtests" dipersempit menjadi catatan redaksional, bukan bukti overclaim substantif). Severity **MEDIUM dipertahankan; suite tetap valid sekuensial, klaim yang gugur**.
- Remaining Objection: nihil (koreksi redaksional tidak menahan verdict).
- Status: CLOSED

### N-008 (F-B-003) — Drift living spec: users.aktif + riwayat timer_type

- Validator: Validator B
- Challenge (metodologi): Apakah drift material bagi thesis atau kosmetik? Apakah §5 "living spec" + §10 "keep schema in sync" + "ask-first" dilanggar?
- Evidence (independen): `0003_users_aktif.sql:1-8` (`ADD COLUMN aktif ... AFTER must_change_pw` + komentar enforcement); `0001_init.sql:20-32` blok users tanpa `aktif`; spec §5 blok users (id…must_change_pw→created_at) tanpa `aktif`; `grep aktif` spec hanya status kuis/TTL, nihil kolom users. Enforcement dua lapis: `auth.go:89-115` (`SELECT must_change_pw, aktif`; `if !aktif → MsgLoginInactive`, dicek setelah password known-good agar bukan oracle) + `session.go:77-93` (`if !aktif → ClearSessionCookie`, sesi berjalan mati seketika). `0001_init.sql:47` `timer_type ENUM('global','per_soal')` vs `0004` lahir `tanpa_timer` vs §5 tiga nilai sekaligus tanpa tanda evolusi.
- Material vs kosmetik: `users.aktif` MATERIAL. Fitur user-visible + security-relevant (deaktivasi akun, pesan login khusus, pencabutan sesi berjalan) yang mengubah makna kriteria §11.1/§11.10 tanpa jejak di thesis; reviewer tidak bisa menelusuri "fitur apa yang dinilai" dari dokumen saja; klaim §10 "schema in sync (living spec)" terbukti salah untuk satu kolom. `timer_type` SENDIRI kosmetik (final state §5 benar; living spec boleh tampil final), tetapi sebagai kontras positif ia membuktikan sinkronisasi SELEKTIF (mampu sinkron untuk timer, gagal untuk aktif) — bukan keterbatasan format.
- Validator Verdict: **CONFIRMED**. Severity **MEDIUM dipertahankan dan proporsional (material traceability, bukan kosmetik)**.
- Remaining Objection: nihil.
- Status: CLOSED

### N-009 (F-B-004) — Klaim KBBI gugur

- Validator: Validator B
- Challenge (metodologi): Apakah drift/inkonsistensi material atau kosmetik? Apakah satu counterexample cukup menggugurkan klaim universal?
- Evidence (independen): `history.html:37-41` + `:83-87`: `{{else}}{{.Status}}` mencetak mentah `p.status` DB (`pending/registered/started/selesai/...`, via `history.go:60-88` tanpa kamus bahasa). `monitor.html:52-53`: `{{.Status}}` mentah; `monitor.html:7-10`: `{{.TimerType}} timer` (kata Inggris "timer") + label Inggris "status" + nilai ENUM kuis Inggris campur dua bahasa. Breadcrumb: render `{Label:"Dasbor"}` (`breadcrumb.go:39,54,64,132`, dikunci test `nav_no_root_links_test.go:56-60`) vs dokumen+komentar normatif "Dashboard" (`breadcrumb.go:11,35,61,128`, `breadcrumb.html:4`, test comment `:14`, tabel §9). Spec §1 + §11.21: "All UI text KBBI"; §7: "ENUM literals … never user-facing" — kalimat terakhir dibantah rantai DB→handler→template yang lengkap (4 counterexample independen).
- Material vs kosmetik: MATERIAL. Klaim "All" bersifat universal — satu counterexample valid sudah menggugurkan; di sini empat rantai penuh. §11.21 tidak lulus sebagaimana dirumuskan; murid melihat literal teknis Inggris (`registered/started/pending`) yang bukan kosakata KBBI/domain. Dasbor/Dashboard = inkonsistensi istilah dokumen↔kode (bukti kosakata tidak dikendalikan; tidak ada test/lint bahasa, tidak ada sensus string). Bukan typo kosmetik.
- Validator Verdict: **CONFIRMED**. Severity **MEDIUM dipertahankan (kriteria sukses yang dirumuskan absolut gugur oleh bukti)**.
- Remaining Objection: nihil.
- Status: CLOSED

### N-010 (F-B-005) — §11.2 skip-validasi + robustness tanpa uji negatif + §12 None

- Validator: Validator B
- Challenge (keamanan + metodologi): (a) rantai eksploitasi konkret apa yang reachable, dengan precondition/batas apa (sesi hangat, username diketahui, TTL 5 mnt, ForceChangePassword lock, sesi lama)? Apakah HIGH proporsional atau overclaim? Design trade-off terdokumentasi atau defect? (b) Apakah ketiadaan uji negatif ekshaustif atau ada test terlewat? (c) Apakah §12 None gugur?
- Evidence (independen):
  - (a) `auth.go:104-115` harfiah: `if !must && bcrypt.CompareHashAndPassword(...) != nil → Unauthorized`; bila `must==true` perbandingan DILEWATI seluruhnya → `target=/change-password` → `startSession` tanpa bukti pengetahuan kata sandi. `session.go:130-139` hanya redirect, tidak mencabut. Test MENGUNCI perilaku: `auth_test.go:548-551` ("next login SKIPS password verification (even a wrong one) → /change-password") + `:TestApproveResetForcesPasswordChangeAndRevokesSessions` (approve → login password salah → sesi → change → revoke). Spec §11.2 memandatkan verbatim: "next login skips password validation".
  - Rantai eksploitasi (reachable, targeted): precondition = (i) username (+ nama_lengkap untuk lolos forgot-password match — lihat `TestForgotPasswordFlow` match/mismatch generik) diketahui; (ii) guru menyetujui request (rekayasa sosial / guru terkecoh; approve idempoten `WHERE status='pending'`). Trigger = penyerang login dengan password ASAL → sesi terautentikasi terbit → dikunci ke `/change-password` oleh middleware. Penyerang MENETAPKAN password baru → `ChangePassword` merevoke SEMUA sesi lama korban (`RevokeUserSessions`) → pengambilalihan penuh. Batas: (i) bukan massal tanpa interaksi (butuh approve guru per akun); (ii) ForceChangePassword lock MENGANDUNG sesi ke `/change-password`+`/logout` saja — tetapi itu TIDAK menggagalkan serangan karena tujuan penyerang memang menetapkan password; lock bukan mitigasi takeover; (iii) TTL 5 mnt N-002 tidak membatasi serangan ini (sesi BARU membaca DB segar); (iv) sesi lama korban tetap hidup sampai penyerang menetapkan password (jendela deteksi pasif). Ini auth bypass by-design yang dimandatkan kriteria sukses — HIGH proporsional, bukan overclaim. Bukan trade-off terdokumentasi (tidak ada analisis ancaman/risiko yang membenarkan skip; §12 malah "None").
  - Relasi N-002↔N-010(a): satu alur `must_change_pw`, klaim berbeda, saling menguatkan bukan duplikat. N-002 = evasi pada sesi HANGAT (stale mirror, ≤5 mnt, tanpa bukti baru). N-010(a) = sesi BARU tanpa bukti (takeover, tanpa batas TTL). Presisi ini dipertahankan.
  - (b) Klaim "tanpa uji negatif" TIDAK ekshaustif sebagaimana dirumuskan — ada test yang terlewat oleh hunter: `flow_global_test.go:454 TestDisconnectFreezesAtLastHeartbeat` + `flow_persoal_test.go:771 TestPerQuestionDisconnectFreezeAndResume` + `timer_test.go OnDisconnect` BENAR menguji freeze-at-last-heartbeat vs detection-time (assert `frozen ≤ hangup+200ms`, bukan sekadar jalur sukses); `realtime_test.go:277-484` menguji rehydrate via hub baru dari DB + ordering + guard buffer. Yang BENAR-BENAR tanpa pasangan negatif: "DB write failure → no broadcast" (grep `write failure|no broadcast|fault|inject.*fail` nihil skenario gagal-tulis; suite realtime hanya publish positif). Juga tidak ada failure-injection DB-mati-di-tengah-mutasi. Maka (b) = PARTIAL: freeze/rehydrate PUNYA uji yang gagal-bila-kode-salah; no-broadcast-on-write-failure TIDAK PUNYA.
  - (c) §12 "None — all 24 decisions resolved": konsekuensi logis — bila N-006…N-009 bertahan (khususnya N-006(b)(c), N-008 aktif, N-009 ENUM), maka ketertutupan palsu. CONFIRMED kondisional pada vonis tersebut (yang di sini bertahan).
- Severity: HIGH dipertahankan OLEH (a) sendiri, bahkan setelah (b) dipersempit. (a) = kriteria sukses memandatkan perilaku tidak aman + test menguncinya — cacat validitas kriteria terberat dalam bundel ini.
- Evidence Requested: lihat REQUEST_TO_HUNTER Q2 (pemetaan ekshaustif §11.15-17 ↔ nama test + konsesi freeze tests).
- Bug Hunter Response: <menunggu via Main>
- Bug Hunter Response (R2, `hunter-b-defense-r1.md` N-010): PARTIALLY CONCEDE pada (b) — §11.16 freeze DIAKUI teruji kuat (`flow_global_test.go:452-515` + `flow_persoal_test.go:768-835`, assert `hangup+200ms` dikutip; `timer_test.go:45-86`); §11.15 rehydrate DIAKUI teruji level transport (`realtime_test.go:412+` hub-baru-dari-DB); gap presisi = §11.17 "DB write failure → no broadcast" + failure-injection generik (grep ekshaustif nihil; seluruh call-site `Hub.Publish` tanpa pasangan negatif). (a) dipertahankan penuh (tak disengketakan; dikunci `auth_test.go:548-551`). (c) redaksi disepakati "misleading-by-omission, bukan fabrikasi", konsekuensi sama (buka §12). §17: bukti baru decisive (matriks + assert + grep), keberatan terjawab.
- Validator Verdict: **PARTIALLY CONFIRMED (FINAL)** — **(a) CONFIRMED penuh; (b) PARTIALLY (freeze/rehydrate teruji, §11.17 no-broadcast tanpa pasangan); (c) CONFIRMED sebagai omission**. Severity **HIGH (FINAL) ditopang penuh oleh (a)** yang tak disengketakan validator mana pun. §18: tidak ada keberatan baru.
- Remaining Objection: nihil.
- Status: CLOSED

## 2. Ringkasan Verdict

| ID | Hunter | Verdict Validator B | Severity ulang | Dampak keamanan | Status |
|----|--------|---------------------|----------------|-----------------|--------|
| N-001 | A-001 | CONFIRMED | MEDIUM (batas bawah, non-keamanan) | NIHIL (guru-only) | CLOSED |
| N-002 | A-002 | PARTIALLY CONFIRMED (fakta ya, HIGH→MEDIUM) | MEDIUM | Evasi sementara ≤5 mnt sesi-sendiri | CLOSED |
| N-003 | A-003 | CONFIRMED | MEDIUM (availability) | NIHIL (liveness saja) | CLOSED |
| N-004 | A-004 | CONFIRMED | MEDIUM (integritas) | NIHIL | CLOSED |
| N-005 | A-005 | CONFIRMED | LOW | NIHIL (akurasi badge) | CLOSED |
| N-006 | B-001 | PARTIALLY CONFIRMED FINAL ((a) ditarik konsensus; (b)(c) ya) | HIGH (metodologis) | Blind-spot supply-chain, bukan RCE | CLOSED |
| N-007 | B-002 | CONFIRMED (koreksi redaksional 7-subtests) | MEDIUM | NIHIL (validitas konstruk) | CLOSED |
| N-008 | B-003 | CONFIRMED | MEDIUM (material) | Nonaktif-enforcement tak terdokumen | CLOSED |
| N-009 | B-004 | CONFIRMED | MEDIUM (material) | NIHIL (kriteria bahasa gugur) | CLOSED |
| N-010 | B-005 | PARTIALLY CONFIRMED FINAL ((a)(c) ya, (b) partial) | HIGH oleh (a) | Takeover targeted via skip-validasi | CLOSED |

Catatan relasi (dipakai untuk proporsionalitas, bukan duplikasi): N-002 (evasi hangat ≤TTL) ↔ N-010(a) (takeover sesi-baru tanpa TTL) satu alur `must_change_pw`, saling menguatkan. N-004 (race tx) ↔ N-007(c) (atribusi test) hanya berbagi path join-code, sudut berbeda.

## 3. Pertanyaan ke Hunter (REQUEST_TO_HUNTER, via Main Agent)

### REQUEST_TO_HUNTER Q1 — N-006(a) semantik `// indirect`

- Finding: N-006
- Question: Lima modul (`x/crypto, go-qrcode, go-sse, excelize/v2, gocsv`) yang berlabel `// indirect` di `go.mod:12-24` BENAR diimpor langsung (`config.go:12, auth.go:16, export_sheets.go:6, teacher_quiz.go:16, hub.go:16`). Bukankah `// indirect` adalah artefak `go mod tidy` (komentar informatif), sedangkan definisi operasional "direct" dalam Go modules adalah "diimpor paket main module"? Jika ya, apakah sub-butir (a) ditarik/dipersempit menjadi "komentar basi, bukan bukti tidak-dipakai", dengan vonis keseluruhan tetap bertahan pada (b) gocsv yatim + (c) Chart.js?
- Required evidence: `go help modules` / Dok Go tentang `// indirect` + `go list -m all` (atau `go mod why` per modul) yang menunjukkan 4 modul (minus gocsv) dibutuhkan langsung; atau akui komentar basi tanpa mengubah vonis (b)(c).
- Reason: Menentukan apakah (a) formalisme yang harus dibuang agar vonis HIGH bertumpu pada kaki decisive (anti-loop §18: satu keberatan presisi, bukan pengulangan).
- Priority: MEDIUM

### REQUEST_TO_HUNTER Q2 — N-010(b) presisi klaim ketiadaan uji negatif

- Finding: N-010(b)
- Question: Klaim "tiga klaim robustness tanpa uji negatif" tampaknya melewatkan `TestDisconnectFreezesAtLastHeartbeat` (`flow_global_test.go:454`), `TestPerQuestionDisconnectFreezeAndResume` (`flow_persoal_test.go:771`), `OnDisconnect` (`timer_test.go`), dan rehydrate-hub-baru (`realtime_test.go:277-484`) yang menegaskan freeze-at-last-heartbeat (bukan detection-time) dan snapshot-dari-DB. Bersediakah memetakan §11.15/16/17 satu-per-satu ke nama test + menegaskan sisa yang BENAR-BENAR tanpa pasangan adalah "DB write failure → no broadcast" ( + failure-injection generik), sehingga (b) dipersempit menjadi PARTIALLY tanpa menggugurkan (a)/(c)?
- Required evidence: daftar berkas `tests/integration/` yang dipetakan ke §11.15, §11.16, §11.17 + grep `no broadcast|write failure|fault injection` nihil untuk §11.17 + kutip assert `hangup+200ms` sebagai bukti freeze test gagal-bila-kode-salah.
- Reason: Klaim ketiadaan adalah bukti negatif yang harus ekshaustif; presisi ini menentukan apakah (b) overclaim parsial (hostile-but-fair: mengakui test yang ada memperkuat kredibilitas hunter untuk (a)).
- Priority: HIGH (menahan ketepatan N-010, bukan menahan HIGH oleh (a))

## 4. Audit Trail

- 2026-10-07 — [INDEX] — `list_projects` → `D-Project-Mother-quiz`; `index_status` ready 5206/19442; tidak menjalankan `index_repository`.
- 2026-10-07 — [READ] — `normalized.md` utuh + kedua laporan hunter + section spec §1/§2/§5/§6.2/§6.6/§9/§10/§11/§12 yang dirujuk.
- 2026-10-07 — [VERIFY] — read independen: auth login + ChangePassword + ApproveReset + session middleware + cache TTL + closeQuiz/Stop/closeWithModal + SweepDisconnects + joinQuiz/quizGates + ReportVisibility/ShouldRecord + go.mod + import langsung + export.go + dashboard/Chart.js + history/monitor ENUM + 0003/migrasi + authlogic header + auth_test skip-password pin + freeze tests + grep negatif robustness.
- 2026-10-07 — [JUDGE] — 10 verdict beralasan keamanan/metodologi; severity dinilai ulang (N-002 HIGH→MEDIUM; N-006(a) ditolak, HIGH bertahan via (b)(c); N-007 koreksi 7-subtests; N-010(b) dipersempit, HIGH bertahan via (a)); 2 pertanyaan presisi (Q1/Q2); tanpa perubahan kode produksi.
- 2026-10-07 — [SUBMIT] — `docs/bug-hunt/examination/validator-b-report.md` ditulis; persistent untuk ronde defense via Main (`write agent://ValidatorB`).

## 5. Round 2 — Defense Evaluation (FINAL)

Sumber: `docs/bug-hunt/defense/hunter-b-defense-r1.md` (format §14, lengkap) + `docs/bug-hunt/defense/hunter-a-defense-r1.md` (konteks N-002).

- Q1 (N-006a) TERJAWAB DECISIVE: hunter setuju `// indirect` = komentar basi (`go mod why` + `go mod tidy -diff` sebagai bukti perintah langsung); (a) ditarik sebagai argumen; vonis bertahan pada (b)+(c) dengan (c) diturunkan jujur menjadi "undocumented runtime dependency" + inventarisasi vendor lengkap. §17: konsesi diterima, keberatan ditutup. §18: tidak mengarang keberatan baru atas rumusan yang sudah presisi.
- Q2 (N-010b) TERJAWAB DECISIVE: hunter setuju sebagian dengan matriks §11.15/16/17 per berkas + kutipan assert `hangup+200ms`; gap presisi ditegaskan §11.17 + failure-injection; (a) tak disengketakan; (c) redaksi disepakati omission. §17: konsesi diterima, keberatan ditutup. §18: tidak mengarang keberatan baru.
- N-002 relasi: Hunter A netral pada HIGH→MEDIUM, terima sisa-TTL ∈(0,5], inventarisasi penulis lengkap (hanya ApproveReset tanpa invalidasi) — konsisten dengan verdict PARTIALLY/MEDIUM Validator B; tidak ada sengketa tersisa.
- N-001/N-003/N-004/N-005/N-007/N-008/N-009: CLOSED Round 1, tidak dibuka kembali (tidak ada bukti baru yang menuntut revisi dari sudut security/metodologi Validator B; konsesi Hunter A pada N-003/N-004/N-005 yang dicatat di defense-A searah dengan verdict B dan tidak mengubahnya).
- Hasil: N-001…N-010 CLOSED. Open Questions: 0.

## 6. Audit Trail (lanjutan Round 2)

- 2026-10-07 — [DEFENSE-READ] — `hunter-b-defense-r1.md` + `hunter-a-defense-r1.md` dibaca utuh; tanpa perubahan kode produksi.
- 2026-10-07 — [CLOSE-Q1] — N-006 → PARTIALLY CONFIRMED FINAL, CLOSED (konsensus (a) ditarik; (b)(c) bertahan).
- 2026-10-07 — [CLOSE-Q2] — N-010 → PARTIALLY CONFIRMED FINAL, CLOSED ((a)(c) ya; (b) partial presisi).
- 2026-10-07 — [FINAL] — 10/10 CLOSED, 0 open; siap Final Verdict Main.

Round: 2

```
STATUS: SUBMITTED
Verdicts: N-001=CONFIRMED; N-002=PARTIALLY CONFIRMED; N-003=CONFIRMED; N-004=CONFIRMED; N-005=CONFIRMED; N-006=PARTIALLY CONFIRMED; N-007=CONFIRMED; N-008=CONFIRMED; N-009=CONFIRMED; N-010=PARTIALLY CONFIRMED
Open Questions: 0
Files Changed: docs/bug-hunt/examination/validator-b-report.md
Ready for Final Verdict: YES
```
