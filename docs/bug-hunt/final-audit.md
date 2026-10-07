# Final Thesis Defense Audit — Quiz Webapp

> Main Agent — moderator + chairman + evidence coordinator (skill `thesis-bug-hunter` §2, §15, §24–§25).
> Thesis/dokumen: `docs/superpowers/specs/2026-09-26-quiz-website-design.md` (spec §1–§12).
> Repository: `D:/Project/Mother/quiz` (branch `main`). Bahasa: Indonesia.
> Tidak ada perubahan kode produksi pada seluruh workflow (investigasi + artifact saja).
> File ini adalah verdict final Main Agent setelah 2 ronde penuh (hunt → normalize → examine → defense → re-verdict).

## Main Agent Final Gate (§24)

```text
Bug Hunter A: SUBMITTED → DEFENDED (defense/hunter-a-defense-r1.md) → COMPLETE
Bug Hunter B: SUBMITTED → DEFENDED (defense/hunter-b-defense-r1.md) → COMPLETE
Validator A: SUBMITTED Round 1 (7 open) → Round 2 FINAL, 10/10 CLOSED, Open 0, Ready YES
Validator B: SUBMITTED Round 1 (2 open) → Round 2 FINAL, 10/10 CLOSED, Open 0, Ready YES
Validator C: SUBMITTED Round 1 (10 open) → Round 2 FINAL, 10/10 CLOSED, Open 0, Ready YES

Open Questions: 0
Unresolved Objections: 0
Unverified Findings: 0
Repeated Objections: 0 (keberatan berulang disetop via §18; semua Q terjawab bukti baru)
```

Completion (§24) terpenuhi: Open Questions = 0, setiap temuan berverdict
CONFIRMED / PARTIALLY CONFIRMED, tidak ada evidence request validator yang belum dijawab.

Verdict final per validator (rekonsiliasi di bawah):

| ID | A-final | B-final | C-final | **Verdict Main (final)** |
|----|---------|---------|---------|--------------------------|
| N-001 (F-A-001) guard closeQuiz | CONFIRMED | CONFIRMED | CONFIRMED | **CONFIRMED** |
| N-002 (F-A-002) stale MustChangePW | CONFIRMED | PARTIALLY (HIGH→MEDIUM) | CONFIRMED (MEDIUM) | **CONFIRMED, severity MEDIUM** |
| N-003 (F-A-003) mutex Live × I/O | PARTIALLY | CONFIRMED | PARTIALLY | **PARTIALLY CONFIRMED** |
| N-004 (F-A-004) join TOCTOU | PARTIALLY | CONFIRMED | PARTIALLY | **PARTIALLY CONFIRMED, severity LOW** |
| N-005 (F-A-005) collapse konkuren | PARTIALLY | CONFIRMED | PARTIALLY | **PARTIALLY CONFIRMED** |
| N-006 (F-B-001) closed-list/gocsv/Chart.js | PARTIALLY | PARTIALLY | CONFIRMED (dipersempit) | **CONFIRMED (dipersempit: (b)+(c))** |
| N-007 (F-B-002) metodologi testing | PARTIALLY | CONFIRMED | PARTIALLY | **PARTIALLY CONFIRMED** |
| N-008 (F-B-003) drift users.aktif | CONFIRMED | CONFIRMED | CONFIRMED | **CONFIRMED** |
| N-009 (F-B-004) KBBI | PARTIALLY | CONFIRMED | CONFIRMED (dipersempit) | **CONFIRMED (dipersempit: ENUM-mentah + TimerType)** |
| N-010 (F-B-005) skip-validasi/robustness/§12 | PARTIALLY ((a) CONFIRMED di dalam) | PARTIALLY ((a) CONFIRMED penuh) | CONFIRMED ((a) penuh, (b)(c) partial) | **CONFIRMED ((a) penuh · (b)(c) partial)** |

Aturan agregasi Main: bulat CONFIRMED ×3 → CONFIRMED; campuran CONFIRMED/PARTIALLY →
ditentukan bukti defense (fakta decisive + konsesi presisi), bukan voting buta.
Sub-butir yang ditarik konsensus dicatat eksplisit agar vonis tidak bertumpu pada kaki yang gugur.

## Executive Verdict

Penulis **BELUM** dapat mempertahankan dokumen sebagaimana dirumuskan.
Dari 10 temuan ternormalisasi: **6 CONFIRMED** (N-001, N-002, N-006, N-008, N-009, N-010)
dan **4 PARTIALLY CONFIRMED** (N-003, N-004, N-005, N-007); **0 DISPUTED, 0 UNPROVEN, 0 REJECTED**.
Tidak ada temuan yang gugur — setiap klaim hunter bertahan dalam bentuk penuh atau
dipersempit, dan setiap keberatan validator yang valid dijawab dengan konsesi + bukti baru.

Yang paling memberatkan thesis (harus ditutup sebelum sidang):
(1) **N-010(a)** — §11.2 memandatkan login tanpa verifikasi kata sandi (takeover targeted,
dikunci `auth_test.go:548-551`); (2) **N-006** — klaim closed-8-direct gugur
(`gocsv` yatim + Chart.js v4.5.1 tak terdokumentasi); (3) **N-002** — approve-reset
tanpa invalidasi mirror (evasi ≤TTL); (4) **N-008** — kolom `users.aktif` + semantik
deactivation hilang dari living spec §5; (5) **N-009** — literal ENUM Inggris mencapai DOM;
(6) **N-007** — klaim metodologi melebihi instrumen (paralel/atribusi/green-vakum 119 SKIP).
N-003/N-004/N-005 adalah hardening/race berjendela/kualifikasi — dicatat tanpa menahan
kelulusan bila enam di atas ditutup.

## Confirmed Bugs / Findings (6)

### N-001 — Guard `closeQuiz` longgar vs spec §6.6 (MEDIUM, non-keamanan) — CONFIRMED
- Lokasi: `internal/handlers/global.go:887-889` (`IN ('aktif','berjalan')`),
  komentar-vs-kode `:815-821` (klaim `WHERE status='berjalan'`),
  `Stop` `:746-781` tanpa pra-cek, `personal.go:16-18,56-66` (berbagi-rutin disengaja),
  spec §6.6 + §8 (funnel tunggal `WHERE status='berjalan'`), §6.5 (per-question tak pernah `berjalan`).
- Fakta: STOP-sebelum-START pada kuis global `aktif` sukses menutup (registered/pending → NULL
  "not attempted", `global.go:880-886`); kalimat §6.6 gugur untuk rutin bersama.
  Test `finish_lifecycle_test.go:76-232` mem-pin close-dari-`aktif` via `/status`, bukan via `POST /stop`.
- Defense: Hunter A defend penuh + bukti niat (penyatuan-rutin disengaja, guard-ganda tak terbukti);
  rekomendasi (b) diterima semua validator.
- Dampak keamanan: NIHIL (aktor guru terautentikasi — konsensus A/B/C).
- Remediasi: **(b)** amendemen §6.6/§8 — pisahkan guard per jalur
  (global `WHERE status='berjalan'`; per-question `WHERE status='aktif'`) atau pecah rutin.

### N-002 — Approve-reset tanpa invalidasi mirror sesi (MEDIUM; diturunkan dari HIGH) — CONFIRMED
- Lokasi: `internal/handlers/password_resets.go:74-110` (nihil `Store/Revoke/SessionKey/cache`,
  grep terverifikasi), `internal/middleware/session.go:58-97` (snapshot + `store.Set`),
  `:130-139` (redirect baca snapshot), `internal/cache/keys.go:11` (`TTLSession = 5 min`).
- Fakta: satu-satunya transisi 0→1 tanpa invalidasi adalah ApproveReset (`password_resets.go:102`);
  `ChangePassword` (`auth.go:383+409` + `RevokeUserSessions`) aman. Jendela = sisa TTL ∈ (0,5] mnt
  per sesi hangat (laporan awal sudah "hingga/maks" — konsisten; dikonfirmasi defense).
- Severity: HIGH→MEDIUM disetujui (B pengusul; A netral; C setuju) — evasi penegakan sesi-sendiri,
  bukan takeover (takeover = N-010(a)).
- Relasi sah (C): N-002 (sesi-aktif-basi) ↔ N-010(a) (sesi-baru-tanpa-verifikasi) = dua klaim berbeda,
  saling menguatkan, satu tidak menyerap yang lain.
- Remediasi: **(a)** code-fix satu pola mapan — `RevokeUserSessions`/hapus entri mirror user
  setelah commit approve.

### N-006 — Klaim closed-8-direct gugur (HIGH metodologis, bukan RCE) — CONFIRMED (dipersempit)
- Lokasi: `go.mod:5-29`; `internal/handlers/export.go:3-16` (`encoding/csv` stdlib);
  `web/vendor/chartjs/chart.umd.min.js:1-6` (v4.5.1 MIT); `views/teacher/dashboard.html:72-84,130`;
  `web/js/teacher_dashboard.js:247,257,268,290` (`new Chart(...)`); spec §2/§10/§11.21/§4.
- Fakta final (setelah konsesi): (a) komentar `// indirect` basi DITARIK sebagai bukti
  (`go mod why`: gocsv yatim "does not need"; 4 modul dipakai langsung; `tidy -diff` promosikan 4,
  hapus gocsv) — konsensus A/B/C; (b) `gocsv` yatim DIPERTAHANKAN PENUH (nol import `.go`,
  `tidy` ingin membuang; §2 klaim peran CSV tanpa bukti); (c) Chart.js DITURUNKAN jujur menjadi
  **undocumented runtime dependency** (tak ada kalimat §2/§10/§11.21 yang yurisdiksinya mencakup aset JS;
  tetapi §4 struktur vendor + §2 library-selection + §11.19 identical-images tak menyebutnya,
  sementara ia dieksekusi di dashboard guru; inventarisasi: hanya Chart.js yang tak terdaftar).
- Dampak: §11.21 tak-falsifiable sebagaimana dirumuskan; `govulncheck` tak mencakup artefak tak-terdaftar.
- Remediasi: **(b)** tulis ulang §2/§10/§11.21 sesuai `go.mod` aktual (`tidy`), putuskan nasib gocsv
  (pakai/hapus), daftarkan Chart.js (versi+sumber+kebijakan pantau vuln) atau keluarkan dari runtime.

### N-008 — Drift living spec: `users.aktif` (MEDIUM, material) — CONFIRMED (bulat A/B/C)
- Lokasi: `migrations/0003_users_aktif.sql:1-8`; `migrations/0001_init.sql:20-32` (tanpa `aktif`);
  blok `users` §5 (tanpa `aktif`); enforcement `auth.go:89-115` (`MsgLoginInactive` pasca-kredensial-cocok,
  bukan oracle) + `session.go:77-93` (bunuh sesi berjalan) + `teacher_students.go:228-260`
  (revoke saat penonaktifan); `history.go`/`results.go` grep `aktif` nihil (data tak difilter).
- Fakta: fitur user-visible + security-relevant (pesan khusus, tendangan sesi, filter daftar guru,
  reaktivasi tanpa sesi) hidup di kode+migrasi, mati di dokumen. `timer_type` (2→3 nilai,
  `0001:47` vs `0004` vs §5 final) adalah kontras positif: sinkronisasi mampu tetapi selektif.
- Remediasi: **(b)** tambahkan `aktif` + semantik ke §5 (usulan redaksi ada di defense-B:
  login/sesi-mati/riwayat-tak-dihapus/reaktivasi-login-ulang) + satu baris §8 + catat ask-first §10.

### N-009 — Klaim KBBI gugur (MEDIUM defensibilitas; LOW teknis) — CONFIRMED (dipersempit)
- Lokasi inti: `views/student/history.html:37-41,83-87` (`{{else}}{{.Status}}`) via
  `history.go:61,80,85` (hanya `dikeluarkan`/cheating diterjemahkan; `pending/registered/started` lolos);
  `views/teacher/monitor.html:52-53` (`{{.Status}}`) + `:8` (`{{.TimerType}} timer`) +
  `monitor.js:165,108` (render + logika literal Inggris); kontras positif `quiz_list.html:32`
  (peta Indonesia `Global/Per pertanyaan/Tanpa timer`).
- Konsesi tercatat (A/C disepakati; B sudah CLOSED): kata `status` (serapan baku) dan render
  `Dasbor` (patuh; Inggris "Dashboard" hanya di komentar/dokumen: `breadcrumb.go:11,35,61,128`,
  `breadcrumb.html:4`, test `:14`, tabel §9) BUKAN pelanggaran — dipisah sebagai isu istilah,
  bukan bukti KBBI. Klaim universal ("All/full/never", §1/§11.21/§7) gugur oleh counterexample
  rantai-penuh yang tersisa (3 literal + 1 frasa).
- Remediasi: **(a)+(b)** kamus EN→ID terpusat (`pending→Menunggu persetujuan`,
  `registered→Terdaftar`, `started→Mengerjakan/Berjalan`, TimerType pakai peta `quiz_list`) +
  test pengunci string + satukan Dasbor/Dashboard + ukur klaim absolut.

### N-010 — §11.2 skip-validasi + robustness + §12 (HIGH oleh (a)) — CONFIRMED ((a) penuh · (b)(c) partial)
- (a) SKIP-VALIDASI — CONFIRMED penuh, tak disengketakan validator mana pun:
  `auth.go:104-115` (`if !must && Compare...` → `must==true` lewati seluruh verifikasi →
  `startSession` tanpa bukti kata sandi; `session.go:130-139` hanya redirect);
  dikunci test `auth_test.go:548-551` (`totally-wrong` → `302 /change-password` + cookie sesi).
  Rantai takeover targeted: username (+nama untuk forgot-match) + approve guru → login password-asal →
  sesi terautentikasi → tetapkan password baru → `ChangePassword` revoke semua sesi lama korban.
  Batas: butuh approve per akun (bukan massal); `aktif` tetap dicek; lock hanya berisi ke
  `/change-password` (tak menggagalkan serangan). Kriteria sukses memandatkan perilaku tak-aman =
  cacat validitas kriteria terberat.
- (b) ROBUSTNESS — PARTIALLY: freeze/rehydrate DIAKUI teruji kuat (koreksi hunter diterima semua):
  `flow_global_test.go:452-515` + `flow_persoal_test.go:768-835` (assert `frozen.After(hangup+200ms)`),
  `timer_test.go:45-86`, `realtime_test.go:412+` (hub-baru-dari-DB); limitasi diakui (level-hub,
  bukan restart-kontainer-penuh). Gap presisi = **§11.17 "DB write failure → no broadcast"** +
  failure-injection generik (grep ekshaustif nihil; seluruh call-site `Hub.Publish`
  `global.go:345,569,731-732,914-915,1017,1188-1189`, `streams.go:104`, `student.go:155,1184`
  tanpa pasangan uji-negatif; kontras publish-positif `realtime_test.go:277-330`).
  Argumen test-quality-bar §9 tepat: urutan kode benar ≠ invarian terkunci.
- (c) §12 "None" — redaksi disepakati **misleading-by-omission, bukan fabrikasi**
  (tanpa tuduhan niat; konsekuensi sama: buka §12, daftarkan sisa pertanyaan).
  Gugur kondisional pada N-006…N-009 yang bertahan.
- Remediasi: **(a)+(b)** rumuskan ulang §11.2 menjadi sifat aman (reset disetujui TAK melemahkan
  autentikasi; verifikasi identitas tetap dituntut; sesi lama mati) + implementasi; tambah uji negatif
  §11.17 + failure-injection atau lunakkan klaim; buka §12.

## Partially Confirmed Findings (4)

### N-003 — `SweepDisconnects` menahan mutex melintasi I/O (MEDIUM, availability; LOW-leaning) — PARTIALLY
- Fakta CONFIRMED: `live.go:186-209` lock melingkupi `QueryRowContext` + `ExecContext` per beat;
  tick 1-detik (`global.go:955-956`, `WatchLoop`); threshold 15 dtk (`global.go:24-27`);
  steady-state murah (skip cepat `:192`) — diakui hunter.
- Koreksi presisi (diterima): yang terblokir = `Connect/Disconnect/Page/AddSpent/Spent` +
  **akuisisi** `Ranker()` (`live.go:118-127`; `Ranker.mu` sendiri, `Upsert/Snapshot` + sort di luar
  `Live.mu`, `ranking.go:20-58`) — BUKAN `Publish`/sort; klaim pelanggaran-spec non-blocking
  registry DITARIK (§6.1/§8 hanya hub/publish). Magnitude belum diukur (kondisional pada
  disconnect-massal + DB lambat — skenario restart/network-flap, bukan imajiner, tetapi tanpa angka).
- Remediasi: **(a)** hardening (snapshot kandidat di bawah lock singkat, I/O di luar, commit ber-guard)
  — engineering note, bukan syarat kelulusan.

### N-004 — Gerbang join snapshot basi TOCTOU (LOW; static-pattern) — PARTIALLY
- Fakta statis CONFIRMED: `quizByCode` pra-tx tanpa lock (`student.go:211-214`) →
  `joinQuiz` (`:236-280`) cabang no-row `quizGates(quiz)` objek luar-tx (`:302-316` murni switch,
  tanpa DB) → `createAttempt` INSERT (`:320-331`) → Commit; `FOR UPDATE` hanya participants
  (`:160-165`); tanpa `SELECT quizzes ... FOR UPDATE`/re-read; kontras pola benar
  `Start` (`UPDATE ... WHERE status='aktif'`, `global.go:615-638`) + `approvePending` (`:408-427`).
- Realisasi konkuren (START tepat di jendela → baris `registered`/`pending` yatim pada kuis `berjalan`,
  hilir 409 IN_PROGRESS + "not attempted" fiktif) belum didemo (butuh harness timing; investigasi-only) —
  hunter terima static-TOCTOU/LOW/PARTIALLY; duplicate-key retry (`:270-273`) hanya sesama-join,
  bukan vs START. Spec §10 ("guard ... inside transactions") dilanggar pada cabang ini.
- Remediasi: **(a)** baca ulang/kunci baris kuis di tx (atau `INSERT...SELECT` ber-guard status).

### N-005 — Collapse anti-cheat tembus saat konkuren (LOW; kualifikasi dokumen) — PARTIALLY
- Fakta pola CONFIRMED: baca last-event pra-tx (`student.go:1651-1700`) + `ShouldRecord` murni
  (`anticheat.go:14-22`, `CollapseWindow = 10s`) + INSERT tanpa row-lock/advisory/unique
  (window tak dapat di-UNIQUE-kan alami).
- Spec §6.9 kategorik ("duplicate kind within 10 s collapses to one row", §8 "Flood → 10 s collapse")
  tanpa kualifikasi single-flight — dibaca literal menjanjikan lebih dari yang dijamin kode.
  Demo dua-POST-konkuren belum dijalankan (diakui; confidence sedang). Dampak: badge `count` absolut
  terinflasi; skor/peringkat/hak tak tersentuh (malah over-detection).
- Posisi hunter (disetujui C): **(b)** kualifikasi dokumental, bukan fix wajib.
- Remediasi: **(b)** satu kalimat §6.9/§8 — "collapse untuk submisi sekuensial/single-flight;
  duplikat konkuren dapat tercatat ganda; count monoton dinormalisasi di tampilan".

### N-007 — Metodologi testing overclaim (MEDIUM; validitas konstruk) — PARTIALLY
- (a) Paralel: `t.Parallel` nihil (`tests/`+`internal/`, grep; `AGENTS.md` bahkan melarang) —
  "parallel-safe" tak-terbukti (tepatnya UNPROVEN, bukan falsified) + tegang internal
  ("parallel-safe" vs "run sequentially", §9). Tanpa test/CI paralel.
- (b) Jam: seam berhenti di batas `quizengine` (`timer.go` murni + `Clock`); handler penentu-hasil
  (`student.go:1091` wall-clock-dalam-tx→410, `:970-972` komputasi `ends_at`, `:1675-1676` gerbang
  collapse, `:1410` `current_q_since`, `global.go:956` freeze, `live.go:140,143`) hanya teruji
  sleep-integration. Klaim "no time.Now in logic" dipersempit (spec tak menuntut seam di handler).
- (c) Atribusi: tabel §9 → `authlogic_test.go` vs header berkas (`:8-14`) delegasikan ketiganya ke
  integrasi (`auth_test.go`, `quiz_crud_test.go`) — mismatch harfiah, decisive.
- (d) 7-subtests: DICABUT konsensus (quibble; `assets_test.go:83-125` = 7 `t.Run` = 7 subtests konvensi Go;
  klaim spec akurat). Satu-satunya (c)-murni dalam workflow.
- (e) Green-vakum: DIBUKTIKAN eksekusi — tanpa DB: **119 SKIP / 6 PASS / 0 FAIL, exit 0**
  (`testutil.go:62-77` Skipf; 9 berkas pemakai gerbang). "Fully green … nothing disabled" (§11.14)
  tanpa prasyarat DB = lulus tanpa menguji; praktik skip wajar, rumusan kriteria tidak falsifiable.
- Suite tidak divonis gagal (freeze/rehydrate kuat — diakui N-010); yang gugur klaim dokumennya.
- Remediasi: **(b)** hapus/turunkan "parallel-safe" (atau buktikan via `t.Parallel`+`-race`),
  perbaiki atribusi tabel §9, nyatakan seam-hanya-quizengine + risiko wall-clock,
  nyatakan prasyarat DB untuk "green".

## Disputed Findings

Nihil — tidak ada temuan dengan bukti kredibel dua arah yang berimbang setelah defense.
(N-002 severity diperdebatkan HIGH vs MEDIUM → diputus MEDIUM konsensus; bukan sengketa fakta.)

## Unproven Findings

Nihil — semua pola yang belum didemo (N-004 race-live, N-005 flood-konkuren, magnitude N-003)
diklasifikasikan PARTIALLY dengan batas eksplisit, bukan UNPROVEN (faktanya terbukti, realisasinya terbuka).

## Rejected Findings

Nihil — tidak ada temuan yang terbukti salah. Sub-butir yang gugur (dicabut, bukan temuan):
N-006(a) `// indirect` sebagai bukti-tak-dipakai; N-007(d) 7-subtests; N-008(b) sebagai kesalahan
(kontras positif); N-009 `status`/`Dasbor`-render sebagai bukti-KBBI; N-010(b)-freeze-tanpa-uji.
Penarikan parsial memperkuat — bukan melemahkan — vonis induk (disertifikasi A/B/C).

## Strongest Validator Objections (keberatan yang mengubah hasil)

1. **B-Q1 + A-N-006 + C-N-006: semantik `// indirect`** — "komentar `go mod tidy`, bukan definisi
   direct; 4 modul benar diimpor" → hunter tarik (a), vonis pindah ke kaki decisive (b)+(c).
2. **A-N-007 + C-N-007: 7-subtests akurat** (`t.Run` ×7 = konvensi Go) → hunter cabut leg (d) tanpa kualifikasi.
3. **A-N-010 + B-Q2 + C-N-010: freeze/rehydrate SUDAH diuji** (assert `hangup+200ms`,
   hub-baru-dari-DB) → hunter akui + persempit gap ke §11.17 no-broadcast + failure-injection.
4. **A-N-009 + C-N-009: `status` serapan baku + `Dasbor` patuh** → hunter eksonerasi + pisah isu istilah;
   sisa ENUM-mentah justru menguat.
5. **A-N-003 + C-N-003: steady-state murah (threshold 15 dtk) + split-lock Connect + ranker-lock
   sendiri** → hunter koreksi presisi (akuisisi vs eksekusi) + tarik link-spec + terima PARTIALLY.
6. **C-N-010(c): §12-"None" = status-resolusi yang menyesatkan-kelengkapan, bukan fabrikasi** →
   hunter setuju redaksi omission, konsekuensi sama.
7. **B-N-002: HIGH→MEDIUM** (evasi sesi-sendiri ≤TTL, bukan takeover) → hunter netral, C setuju.

## Strongest Bug Hunter Defenses (pembelaan yang bertahan)

1. **A N-001**: komentar-vs-kode (`global.go:815-821` vs `:887-889`) + §6.5 (per-question tak pernah
   `berjalan`) + berbagi-rutin disengaja (`personal.go:16-18`) + test pin `/status` — counterexample
   STOP-sebelum-START tak terbantahkan ketiga validator.
2. **B N-006**: `go mod why` + `tidy -diff` + grep-import + inventarisasi `web/vendor/` yang dieksekusi
   (hanya Chart.js tak-terdaftar) — vonis bertahan di atas dua kaki decisive.
3. **B N-007(e)**: eksekusi green-vakum (119 SKIP/6 PASS/0 FAIL exit-0) — bukti perintah langsung.
4. **B N-010(a)**: rantai `auth.go:104-115` + test pengunci `auth_test.go:548-551` — tak disengketakan
   validator mana pun; kriteria memandatkan perilaku tak-aman.
5. **B N-010(b)-koreksi**: matriks berkas→klaim + kutipan assert `hangup+200ms` — keberatan A/B/C
   diakui dengan kutipan, gap sisa (§11.17) justru mengeras.
6. **A N-002**: inventarisasi penulis `must_change_pw` lengkap (hanya ApproveReset tanpa invalidasi) +
   sisa-TTL presisi — rekomendasi satu-baris tak terbantahkan.
7. **B N-008/N-009**: semantik deactivation per-efek + usulan redaksi §5; kamus EN→ID + kontras
   `quiz_list.html:32` — rekomendasi siap-pakai.

## Remaining Risks (diurutkan bobot thesis, bukan ketakutan)

1. **Pengambilalihan akun via §11.2** (N-010(a), HIGH): username + approve → sesi tanpa kata sandi →
   password baru → revoke sesi korban. Mendesak diamankan sebelum deployment luas.
2. **Penegakan reset tertunda ≤TTL** (N-002, MEDIUM): murid sesi-hangat terus memakai kredensial lama
   hingga 5 menit pasca-approve.
3. **Blind-spot supply-chain** (N-006, HIGH-metodologis): Chart.js v4.5.1 di luar `go.mod`/`govulncheck`;
   `gocsv` yatim memperlebar permukaan deklarasi.
4. **Kriteria hijau-vakum** (N-007(e), MEDIUM): 119 skip + exit-0 tanpa DB — regresi tak terdeteksi
   pada mesin tanpa database.
5. **Skema tak-terdokumen** (N-008): operator tak tahu semantik `aktif` (t ಯಾವ pesan, sesi-mati, data kekal).
6. **Roster fiktif** (N-004, LOW): baris `registered` yatim pada kuis `berjalan` (waiting-count,
   modal "N working", "not attempted" fiktif) bila START-vs-join beririsan.
7. **Tail-latency registry** (N-003, LOW-MEDIUM): N round-trip sekuensial di bawah satu mutex tiap tick
   saat disconnect-massal + DB lambat.
8. **Badge terinflasi** (N-005, LOW): duplikat konkuren <10 dtk menggandakan count monitor.
9. **Premature close** (N-001, MEDIUM): kuis global tertutup tanpa pernah START.
10. **Bahasa campur di depan pengguna** (N-009, MEDIUM): `pending/registered/started`, `global timer`.

## Recommended Thesis Corrections (8 paket, berurutan)

1. **Amankan §11.2 + code-fix auth** (N-010(a), N-002): reset yang disetujui TAK melemahkan autentikasi —
   tuntut verifikasi identitas pemilik sah; sesi lama mati; ApproveReset panggil `RevokeUserSessions`
   (satu pola mapan). Perbarui `auth_test.go:548-551` dari "skips verification" menjadi kontrak aman.
2. **Rapikan dependensi** (N-006): `go mod tidy` (4 modul → direct, hapus `gocsv` atau pakai);
   tulis ulang §2/§10/§11.21; daftarkan Chart.js v4.5.1 (§2/§4 + kebijakan pantau) atau keluarkan
   dari runtime; lengkapi §4 `web/vendor/` (chartjs) + komentar mount `main.go:52`.
3. **Turunkan klaim testing + kunci invarian** (N-007, N-010(b)): hapus "parallel-safe" (atau buktikan),
   perbaiki atribusi tabel §9, nyatakan seam-hanya-quizengine + prasyarat DB §11.14;
   tambah uji negatif §11.17 (gagal-tulis → tanpa broadcast) + failure-injection, atau lunakkan klaim.
4. **Sinkronkan skema** (N-008): tambah `users.aktif` + semantik ke §5 (redaksi defense-B siap pakai) +
   satu baris §8 + catat ask-first §10.
5. **Kamus bahasa + satukan istilah** (N-009): petakan ENUM di render (pakai peta `quiz_list`),
   hapus `{{.Status}}`/`{{.TimerType}} timer` mentah, kunci via test string, satukan Dasbor/Dashboard,
   ukur klaim §1/§7/§11.21.
6. **Pisahkan guard penutup** (N-001): amendemen §6.6/§8 per jalur (global `berjalan`, per-question `aktif`).
7. **Kualifikasi collapse** (N-005): satu kalimat single-flight/best-effort §6.9/§8.
8. **Buka §12** (N-010(c)): daftarkan sisa pertanyaan (dependensi, skema, bahasa, uji-negatif).
   N-003/N-004 sebagai hardening/guard-segar dicatat pada paket (1)/(8) tanpa menahan kelulusan.

## Defense Readiness

**NOT READY** — sebagaimana dirumuskan. Alasan: 6 temuan CONFIRMED mencakup 2 HIGH
(N-010(a) perilaku tak-aman yang dimandatkan + N-006 ketertutupan gugur), 1 jalur keamanan
tertunda (N-002), 1 drift skema material (N-008), 1 kriteria bahasa absolut gugur (N-009),
1 inkonsistensi state-machine (N-001), plus 4 PARTIALLY yang guard-nya belum dikunci uji.
**READY bersyarat** setelah 8 paket koreksi di atas ditunjukkan (kode + dokumen + uji-negatif),
dengan N-003/N-004 dicatat sebagai hardening/race-berjendela.
Klaim yang selamat dari pemeriksaan keras (rantai tulis DB→commit→invalidate→broadcast,
skor FULL-count, finish idempotent, guard `WHERE status` pada Start/Edit/Approve,
upload 25 MB/magic/sha/path, guard injeksi ekspor) patut dipertahankan eksplisit di sidang.

## Audit Trail (ringkas kronologis)

- 2026-10-07 — [SETUP] Main: skill `thesis-bug-hunter` ditemukan
  (`.omp/skills/thesis-bug-hunter/SKILL.md`), dibaca utuh; `list_projects` → `D-Project-Mother-quiz`;
  `index_status` ready (5115 nodes/19351 edges); arsitektur dipetakan
  (Go 104 / HTML 43 / JS 13 / SQL 6; hotspot `realtime.Close`, `handlers.fail`, `cache.Set/Get`).
- 2026-10-07 — [TEMPLATE] Main: `docs/bug-hunt/thesis-template.md` kanonis (§4) ditulis; struktur stabil.
- 2026-10-07 — [SPAWN-1] Main: BugHunterA (implementasi) + BugHunterB (akademik) paralel,
  kontrak + keep-alive sampai verdict final; IRC `agent://BugHunterA/B` disediakan.
- 2026-10-07 — [SUBMIT-A] Hunter A: 5 temuan F-A-001…F-A-005 → `findings/hunter-a-report.md`.
- 2026-10-07 — [SUBMIT-B] Hunter B: 5 temuan F-B-001…F-B-005 → `findings/hunter-b-report.md`
  (independen; `hunter-a-report.md` sengaja tak dibuka).
- 2026-10-07 — [GATE-1] Main: verifikasi silang 10 temuan ke berkas aktual (go.mod, guard closeQuiz,
  ApproveReset-grep-nihil, SweepDisconnect-lock+IO, joinQuiz-quizGates-luar-tx, ShouldRecord-murni,
  t.Parallel-nihil, time.Now-handler, authlogic-delegasi, testutil-Skipf, migrasi 0003/0004,
  history/monitor-mentah, breadcrumb-Dasbor, auth-skip-CompareHash, Chart.js-vendor+load);
  0 duplikat mentah; relasi N-002↔N-010(a) dicatat saling-menguatkan; `normalized.md` N-001…N-010.
- 2026-10-07 — [SPAWN-2] Main: ValidatorA (correctness) + ValidatorB (security/metodologi) +
  ValidatorC (readiness) paralel, independen, posisi default "belum terbukti".
- 2026-10-07 — [EXAM-R1] ValidatorB: 8 CLOSED + Q1 (N-006a) + Q2 (N-010b) OPEN.
  ValidatorC: 10 verdict + 10 REQUEST_TO_HUNTER OPEN. ValidatorA: 3 RESOLVED + 7 OPEN.
- 2026-10-07 — [DEFENSE-R1] Main → HunterA (N-001…N-005) + HunterB (N-006…N-010) via IRC;
  HunterA → `defense/hunter-a-defense-r1.md` (N-001 defend; N-002 defend+kualifikasi; N-003/N-004/N-005
  terima PARTIALLY + koreksi presisi); HunterB → `defense/hunter-b-defense-r1.md`
  (N-006(a) ditarik/(b)(c) bertahan; N-007(d) dicabut/(b) dipersempit/green-vakum dibuktikan;
  N-008 defend+redaksi; N-009 dipersempit+kamus; N-010(a) penuh/(b) dipersempit/(c) omission).
- 2026-10-07 — [EXAM-R2] ValidatorA/B/C: seluruh Q terjawab bukti baru decisive (§17);
  tidak ada keberatan baru (§18); 10/10 CLOSED ×3; Ready YES ×3.
- 2026-10-07 — [FINAL] Main: agregasi verdict + severity/remediasi final → berkas ini;
  0 open / 0 unresolved / 0 unverified / 0 repetitive; tanpa perubahan kode produksi.
