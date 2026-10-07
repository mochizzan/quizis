# Hunter A — Defense Round 1 (N-001…N-005)

> Respons pertahanan per temuan, format skill §14
> (DEFENSE RESPONSE: Finding / Validator Question / Position /
> Evidence / Reasoning / Counterargument / Conclusion / Confidence).
> Semua klaim diverifikasi ulang terhadap berkas aktual pada 2026-10-07.
> Tanpa perubahan kode produksi.

## DEFENSE RESPONSE — N-001 (F-A-001: guard closeQuiz longgar)

- Finding: `closeQuiz` memakai `WHERE id=? AND status IN ('aktif','berjalan')`
  (`internal/handlers/global.go:887-889`); `Stop` (`global.go:746-781`)
  tanpa pra-cek status; spec §6.6 menetapkan funnel tunggal
  `WHERE status='berjalan'`.
- Validator Question (C; A+B sudah CONFIRMED): disengaja untuk menyatukan
  jalur per-question-close (personal.go, yang memang menutup `aktif`) dengan
  STOP global, atau kelalaian? Bukti niat apa selain struktur berbagi-rutin
  (komentar/commit/test/§6.5)?
- Position: defend (fakta dan vonis CONFIRMED dipertahankan) +
  partially-concede pada atribusi niat (disengaja-vs-lalai tidak dapat
  diputuskan dari bukti yang ada) + rekomendasi remediasi (b) pisahkan-guard.
- Evidence:
  1. Literal guard (`global.go:887-889`, re-verified 2026-10-07):
     `UPDATE quizzes SET status = 'selesai' WHERE id = ? AND status IN
     ('aktif', 'berjalan')`.
  2. Komentar fungsi yang sama (`global.go:815-821`, re-verified) menulis
     sebaliknya: "then `UPDATE quizzes ... WHERE status='berjalan'`" —
     komentar-vs-kode inkonsisten internal (poin independen Validator B).
  3. Spec §6.6 (re-read): "All three funnel through `UPDATE ... SET
     status='selesai' WHERE status='berjalan'` — first commit wins, others
     no-op." — untuk jalur GLOBAL (timeout/STOP/all-finished).
  4. Spec §6.5 (state machine, re-read 2026-10-07): jalur PER-QUESTION
     adalah `nonaktif ──activate──▶ aktif ──close──▶ selesai` — kuis
     per-question/tanpa-timer TIDAK PERNAH memasuki `berjalan`
     (diperkuat `Start` global.go:597-606 yang menolak timer_type
     non-global: "per-question and no-timer quizzes never enter berjalan").
     Maka penutup per-question HARUS menerima `aktif` — guard longgar
     benar untuk jalur itu.
  5. Berbagi-rutin eksplisit dan disengaja: `personal.go:16-18` ("closing
     runs the same closeQuiz routine as STOP and the timeout watchdog"),
     `SetQuizStatus` adalah "the ONLY status route" (`personal.go:24-30`),
     `closeWithModal` memanggil `g.closeQuiz(ctx, quizID, "teacher_close")`
     (`personal.go:66`).
  6. Test (`finish_lifecycle_test.go:76-232`, re-read): "finish with zero
     participants" (setup `finishActiveFixture` = kuis GLOBAL aktif,
     tanpa START) mengharapkan `200 selesai` via `/status` — konsisten
     dengan perilaku berbagi-rutin; "finish while nonaktif" → 409;
     "double finish" → 409 "tidak sedang berjalan". Tidak ada kasus
     "STOP-sebelum-START pada kuis global harus 409" — perilaku yang
     saya laporkan justru DIPIN oleh test zero-participant untuk jalur
     `/status`.
- Reasoning: Guard tunggal `IN ('aktif','berjalan')` benar untuk
  per-question-close (§6.5) tetapi melanggar kalimat §6.6 untuk STOP
  global. Karena kedua jalur sengaja berbagi satu rutin (bukti 5),
  kalimat §6.6 "all three funnel through WHERE status='berjalan'"
  tidak dapat dipertahankan apa adanya untuk implementasi berbagi-rutin:
  ia benar sebagai spesifikasi jalur global, salah sebagai deskripsi
  rutin bersama. Ini tepat klasifikasi saya: MEDIUM, spec↔code
  inconsistency, bukan kerentanan (aktor guru terautentikasi — setuju
  dengan Validator B: security impact NIHIL).
- Counterargument (yang saya antisipasi): "karena test mem-pin close
  dari `aktif` → perilaku disengaja, bukan bug". Jawab: test mem-pin
  jalur `/status` (yang melayani KEDUA timer-type); tidak ada test yang
  mem-pin `POST /stop` pada kuis global `aktif` yang belum START.
  Counterexample saya (STOP-sebelum-START → 200, bukan 409) tetap
  berdiri dan tetap bertentangan dengan kalimat §6.6. Vonis CONFIRMED
  tidak bergantung pada niat — seperti yang ditegaskan permintaan C
  sendiri ("Vonis CONFIRMED tidak bergantung padanya").
- Conclusion: CONFIRMED dipertahankan. Pada pertanyaan niat: bukti
  menunjukkan penyatuan-rutin DISENGAJA (bukti 5 + test), tetapi bukti
  tidak menunjukkan guard-ganda-disengaja (komentar fungsi mengklaim
  `WHERE status='berjalan'` seorang). Rekomendasi: remediasi (b) —
  pisahkan guard per jalur di spec (amendemen §6.6: guard global
  `WHERE status='berjalan'`; guard per-question `WHERE status='aktif'`)
  atau pecah rutin; BUKAN sekadar mengklaim kalimat §6.6 sudah benar.
- Confidence: tinggi untuk fakta + vonis; sedang untuk atribusi
  niat (tidak ada commit message yang saya periksa — riwayat git di
  luar berkas yang saya verifikasi; saya tidak mengklaim lebih dari
  yang didukung komentar + struktur + test).

## DEFENSE RESPONSE — N-002 (F-A-002: snapshot MustChangePW basi)

- Finding: `ApproveReset` (`password_resets.go:74-110`) tidak menyentuh
  cache; `LoadSession` (`session.go:75-97`) menyimpan snapshot
  `MustChangePW` ke mirror 5 menit; `ForceChangePassword`
  (`session.go:132-143`) membaca snapshot basi.
- Validator Questions (C; A CONFIRMED, B PARTIALLY HIGH→MEDIUM):
  (a) jendela basi = sisa TTL (maks 5 mnt, keys.go:11), bukan selalu
  penuh — setuju? (b) adakah penulis flag lain selain ApproveReset?
- Position: defend vonis + concede presisi kuantifikasi (a) + lengkapi
  inventarisasi penulis (b). Netral terhadap penurunan HIGH→MEDIUM —
  saya tidak menentangnya bila panel menilai eksploitasi butuh sesi
  aktif yang sudah hangat.
- Evidence:
  1. (a) `keys.go:11` (re-verified): `TTLSession = 5 * time.Minute`.
     `LoadSession` (`session.go:97`): `store.Set(cache.SessionKey(id),
     s, cache.TTLSession)` — TTL dihitung sejak LOAD, bukan sejak
     approve. Maka jendela basi per sesi = sisa umur entri pada momen
     approve, ∈ (0, 5] menit. Laporan awal menulis "hingga 5 menit" /
     "maks 5 menit" (§3.1 Impact dan status block: "hingga 5 menit per
     sesi aktif") — KONSISTEN dengan (a), bukan "selalu 5 menit penuh".
     Saya setuju dan menegaskan: bukan 5 menit penuh deterministik.
  2. (b) grep `must_change_pw` (re-run 2026-10-07, internal+migrations):
     penulis flag: `password_resets.go:102`
     (`UPDATE users SET must_change_pw = 1`), `auth.go:383`
     (`UPDATE users SET password_hash=?, must_change_pw=0` — jalur
     ChangePassword, DI DALAM tx yang sama lalu `RevokeUserSessions`
     di `auth.go:409`, sehingga aman); pembaca: `auth.go:93,98`
     (Login), `session.go:79` (LoadSession). Migrasi hanya definisi
     kolom (`0001_init.sql:26`, `0003_users_aktif.sql:6`). Tidak ada
     penulis ketiga. Satu-satunya penulis 0→1 tanpa invalidasi adalah
     ApproveReset.
  3. Pembanding disiplin (re-verified): `ChangePassword` +
     `RevokeUserSessions` (`auth.go:409`); aktivasi/nonaktif murid
     (`teacher_students.go:256-260`, penghapusan `:305-309`) — pola
     mirror-dulu-lalu-DB ada di semua jalur lain; ApproveReset satu-
     satunya yang absen (grep `Revoke|Store|cache` di
     `password_resets.go` — nihil, re-verified via read penuh berkas).
- Reasoning: Karena satu-satunya transisi 0→1 yang relevan
  (ApproveReset) tidak menginvalidasi, dan transisi 1→0
  (ChangePassword) sudah merevokasi, rekomendasi presisi: tambahkan
  `RevokeUserSessions(ctx, db, store, userID)` (atau `Delete`
  per-sesi milik user) di ApproveReset setelah commit — satu baris
  pola yang sudah mapan. Tidak ada jalur keempat yang butuh
  inventarisasi ulang.
- Counterargument (B: HIGH→MEDIUM): saya terimaffs — dampak butuh
  (i) sesi murid sudah hangat SEBELUM approve, (ii) murid aktif dalam
  jendela sisa TTL. Itu konfigurasi umum (murid login lalu meminta
  reset dari sesi lain/perangkat), tetapi bukan eskalasi deterministik
  penuh. Severity MEDIUM dapat saya terima tanpa mengubah fakta/vonis;
  yang saya pertahankan: propertinya ("revocation is instant",
  §6.2/§6.4) dilanggar pada jalur ini.
- Conclusion: CONFIRMED dipertahankan (fakta + rekomendasi satu-jalur);
  kualifikasi (a) diterima dan laporan awal sudah memakainya ("hingga/
  maks 5 menit"); inventarisasi (b) lengkap: hanya ApproveReset yang
  butuh perbaikan.
- Confidence: tinggi.

## DEFENSE RESPONSE — N-003 (F-A-003: mutex Live melintasi I/O)

- Finding: `SweepDisconnects` (`live.go:186-209`) memegang `l.mu` selama
  `QueryRowContext` + `ExecContext` per beat; dipanggil tiap detik oleh
  `WatchOnce` (`global.go:956`).
- Validator Questions (A+C; B CONFIRMED-CLOSED): (1) durasi
  Ranker()/Upsert/Snapshot menahan Live.mu; (2) skenario kongesti
  berangka atau turunkan klaim; (3) mengapa split-lock Connect tidak
  diterapkan ke sweep + kutipan spec non-blocking untuk registry.
- Position: defend FAKTA sepenuhnya; concede-terukur pada MAGNITUDE
  (terima PARTIALLY bila panel menuntut angka yang belum saya ukur);
  jawab (1) dan (3) dengan bukti harfiah; untuk (2) saya pilih opsi
  kedua validator: turunkan klaim dampak ke rumusan terukur.
- Evidence:
  1. `Live.Ranker` (`live.go:118-127`, re-verified): `l.mu.Lock();
     defer l.mu.Unlock()` — hanya lookup/buat map + return pointer
     (orde mikrodetik, tanpa I/O). `quizengine.Ranker` punya `mu
     sync.RWMutex` sendiri (`ranking.go:20-23`); `Upsert` memakai
     `r.mu.Lock` (`ranking.go:30-35`); `Snapshot` memakai `r.mu.RLock`
     lalu sort DI LUAR lock (`ranking.go:43-58`, re-verified 2026-10-07).
     Maka: Upsert/Snapshot TIDAK butuh `Live.mu` — HANYA akuisisi
     `Ranker()` (pembuatan/lookup) yang antre di belakang sweep.
     Klaim presisi saya: sweep memblokir (i) `Connect/Disconnect/Page/
     AddSpent/Spent` (seluruh body di bawah `l.mu`), dan (ii) AKUISISI
     ranker, bukan Upsert/Snapshot yang sedang berjalan. Ini menjawab
     (1) — dan saya koreksi rumusan laporan awal sejauh ia terbaca
     "publish/ranking ikut tertunda": yang tertunda adalah akuisisi +
     presence/page/ledger, bukan `Publish` (tanpa lock) atau sort.
  2. Steady-state murah (poin Validator A, saya verifikasi):
     `SweepDisconnects` skip cepat bila `b.connected || b.frozen ||
     now.Sub(b.last) <= threshold` (`live.go:192`); threshold default
     `disconnectSilence = 15s` (`global.go:24-27`); tick tiap 1 detik
     (`WatchLoop` 1027-1041). Dalam steady-state (semua connected),
     critical section = iterasi map tanpa I/O. Biaya I/O muncul jika
     dan hanya jika ada beat disconnect yang sunyi >15 dtk: 2 round-
     trip DB SEQUENSIAL per beat (SELECT ends_at + UPDATE ends_at)
     sambil memegang satu mutex global. Saya TIDAK memiliki angka
     latensi DB/beat dari lingkungan ini → saya TIDAK mengklaim angka
     (sesuai opsi validator): klaim dampak saya turunkan menjadi
     "critical section melintasi I/O; magnitude belum diukur; pola
     terbalik dari Connect yang split-lock/Unlock-sebelum-Exec".
  3. Split-lock Connect (`live.go:131-155`, re-verified): `l.mu.Lock()`
     → mutasi beat → `l.mu.Unlock()` → `ExecContext resume` DI LUAR
     lock. Sweep (`live.go:186-209`) melakukan sebaliknya — komentar
     (`live.go:179-185`) menjelaskan niat: "The map lock is held
     across the few queries so a concurrent Connect cannot
     interleave." Niat atomicity freeze-vs-resume itu SAH; keberatan
     saya bukan pada niat melainkan pada granularity (satu mutex
     global untuk seluruh beats + ranker-map + pages + spent, bukan
     per-pid). Alternatif baku: snapshot kandidat di bawah lock
     singkat → I/O di luar → commit dengan guard (`frozen` flag /
     `WHERE status='started'` sudah ada sebagai guard kedua).
     Mengenai kutipan spec non-blocking untuk REGISTRY: saya mengakui
     — spec §6.1/§8 yang saya kutip menjanjikan non-blocking untuk
     HUB/SSE publish path ("Publish broadcasts ... never blocks",
     `hub.go:84-92`; "slow-client drop", §8), BUKAN untuk registry
     Live/presence. Tidak ada kalimat spec yang menjanjikan
     non-blocking registry. Maka link "melanggar janji non-blocking"
     saya TARIK; yang tersisa adalah temuan engineering yang valid
     (lock-melintasi-I/O pada tick 1-detik) dengan dampak tak
     terkuantifikasi. Ini-level PARTIALLY, bukan CONFIRMED-penuh —
     saya terima.
- Reasoning: Fakta (lock+DB-I/O dalam satu critical section 1-detik)
  harfiah dan tak terbantahkan; satu-satunya yang overstate adalah
  besar-dampak dan atribusi-spec. Dengan koreksi (1) [akuisisi vs
  eksekusi] dan penarikan link-spec, temuan menjadi: fakta benar,
  rekomendasi split-lock/per-pid valid, magnitude terbuka.
- Counterargument (A: "steady-state murah, threshold 15 dtk"): setuju
  untuk steady-state; temuan saya kondisional pada scenario disconnect
  massal (refresh serentak N murid + DB lambat) — scenario yang justru
  paling mungkin saat paling dibutuhkan (restart/network flap).
  Kondisional ≠ imajiner; tetapi tanpa angka saya tidak menuntut
  severity di atas LOW-MEDIUM engineering note.
- Conclusion: PARTIALLY CONFIRMED saya terima (fakta CONFIRMED,
  dampak/doktrin-spec PARTIALLY); koreksi presisi (akuisisi-ranker vs
  Upsert/Snapshot; penarikan klaim pelanggaran-spec non-blocking
  registry) dicatat di sini sebagai amendemen laporan awal.
- Confidence: tinggi untuk fakta; rendah-sedang untuk magnitude
  (belum diukur — dinyatakan eksplisit).

## DEFENSE RESPONSE — N-004 (F-A-004: gerbang join TOCTOU)

- Finding: cabang no-row `joinQuiz` memvalidasi status kuis dari objek
  `quiz` pra-tx (`quizGates(quiz)`), tanpa re-read/lock baris kuis di
  dalam tx; `FOR UPDATE` hanya mengunci baris participants.
- Validator Questions (A+C; B CONFIRMED-CLOSED): demonstrasikan
  interleaving START-vs-INSERT (tahan createAttempt/perlambat Commit
  `student.go:320-331` + START konkuren pada kuis `aktif` → baris
  registered yatim pada kuis `berjalan`), atau terima kualifikasi
  static-TOCTOU/LOW.
- Position: pertahankan pola-race sebagai FAKTA STATIS terbukti;
  terima kualifikasi static-TOCTOU (tanpa demo live di sini) — vonis
  PARTIALLY dapat saya terima; yang saya TOLAK adalah penolakan fakta
  ("tidak ada race karena ...").
- Evidence (re-verified 2026-10-07):
  1. `quizByCode` pra-tx tanpa lock (`student.go:211-214`):
     `loadQuiz(ctx, db, "code = ?", ...)` — plain SELECT.
  2. `joinQuiz` (`student.go:236-280`, re-read penuh 239-335):
     `BeginTx` → `latestParticipant(ctx, tx, quiz.ID, userID, true)`
     (FOR UPDATE atas participants) → cabang tak-ditemukan:
     `quizGates(quiz)` — objek dari (1), di luar tx — →
     `createAttempt` (INSERT) → `Commit`. TIDAK ADA `SELECT ...
     FROM quizzes ... FOR UPDATE` di dalam tx (grep FOR UPDATE di
     student.go: hanya `latestParticipant`).
  3. `quizGates` (`student.go:302-316`): murni switch atas
     `quiz.Status` yang diteruskan — tidak menyentuh DB.
  4. Kontras pola benar `Start` (`global.go:615-638`): `UPDATE
     quizzes SET status='berjalan' ... WHERE id=? AND status='aktif'`
     + klasifikasi 0-rows — guard DI DALAM tx. `approvePending`
     (`global.go:408-427`): satu UPDATE menggabungkan kedua kondisi.
     Join cabang no-row adalah satu-satunya gerbang status yang
     memakai snapshot luar-tx.
  5. Jendela konkret: antara `quizByCode` (pra-tx) dan `Commit`
     INSERT — mencakup round-trip `latestParticipant` + (untuk
     cabang selesai) `SELECT COUNT(*)` + INSERT. START konkuren yang
     commit `aktif→berjalan` di dalam jendela ini tidak terlihat oleh
     `quizGates`. Hasil: baris `registered`/`pending` pada kuis
     `berjalan`; `classifyView` menanganinya sebagai "missed START"
     (409 IN_PROGRESS) dan `closeQuiz` menutupnya "not attempted" —
     koheren hilir, tetapi ada EFEK SAMPING tulis yang seharusnya
     tidak ada (pesan join-after-START eksak §6.12/§11.18 dijanjikan
     sebagai penolakan murni).
- Reasoning: Ini TOCTOU klasik check-outside/use-inside-transaction.
  Saya tidak menjalankan demo live (menahan createAttempt butuh
  instrumentasi/harness DB; di luar tugas investigasi tanpa perubahan
  kode) — maka saya terima kualifikasi: static-TOCTOU, severity LOW
  (atau MEDIUM-LOW), vonis PARTIALLY hingga demo dieksekusi. Fakta
  struktural (stale-read + absennya re-read/lock + kontras pola benar)
  cukup untuk PARTIALLY; klaim "tereksploitasi deterministik" tidak
  saya buat (laporan awal: "Eksploitasi butuh timing", confidence
  sedang-tinggi — konsisten).
- Counterargument ("hilir koheren → bukan bug"): koherensi hilir
  (missed-START → 409; close → not-attempted) adalah mitigasi
  accidental, bukan guard; ia tidak menghapus baris yatim dari
  waiting-room count, modal "N working"/"N murid", dan hasil "not
  attempted" fiktif. Spec §10 Always-do ("Guard quiz state
  server-side (WHERE status='...' inside transactions)") dilanggar
  pada cabang ini — itu temuan yang sah walau dampaknya LOW.
- Conclusion: terima PARTIALLY/static-TOCTOU/LOW; fakta + rekomendasi
  (re-read/lock status kuis di tx, atau INSERT...SELECT ber-guard)
  dipertahankan.
- Confidence: tinggi untuk fakta statis; sedang untuk realisasi
  (perlu interleaving; belum didemo di sini).

## DEFENSE RESPONSE — N-005 (F-A-005: collapse anti-cheat konkuren)

- Finding: cek collapse 10-detik (`ShouldRecord`) memakai bacaan
  last-event pra-tx; INSERT di dalam tx tanpa lock baris terakhir —
  dua POST sejenis konkuren sama-sama lolos.
- Validator Questions (A+C; B CONFIRMED-CLOSED): (a) buktikan dua POST
  konkuren → dua baris, atau kutip kalimat spec yang menjanjikan
  collapse atomik; (b) menuntut fix kode atau cukup kualifikasi
  dokumen? (C sudah merekomendasikan remediasi dokumental.)
- Position: pilih opsi (b) — kualifikasi dokumental sebagai posisi
  UTAMA; fakta race dipertahankan sebagai static pattern (bukan klaim
  demo). Terima PARTIALLY.
- Evidence (re-verified 2026-10-07):
  1. `ReportVisibility` (`student.go:1651-1700`, re-read): `latest-
     Participant(..., false)` → `SELECT created_at, kind ...
     ORDER BY id DESC LIMIT 1` (di luar tx) → `ShouldRecord(lastAt,
     now, ...)` → `BeginTx` → `INSERT` → `SELECT COUNT(*)` →
     `Commit`. Tidak ada `FOR UPDATE`/advisory-lock/unique-constraint
     yang mencakup pembacaan.
  2. `ShouldRecord` (`anticheat.go:14-22` via get_code_snippet):
     fungsi murni waktu (`now.Sub(lastAt) < CollapseWindow`,
     `CollapseWindow = 10s`, `anticheat.go:5-8` — re-verified via
     grep) tanpa sinkronisasi.
  3. Spec §6.9 (re-read): "**Event flood control:** duplicate kind
     within 10 s collapses to one row; events after quiz end are
     ignored." Kalimat ini TANPA kualifikasi single-flight/atomik —
     dibaca sebagai jaminan umum ("collapses to one row"). Di bawah
     konkurensi, jaminan itu gugur (dua bacaan pra-tx yang sama →
     dua INSERT). Tidak ada kalimat spec yang membatasi collapse ke
     submisi sekuensial — maka bacaan literal spec menjanjikan lebih
     dari yang dijamin kode. Inilah inti temuan: inkonsistensi
     spec↔kode pada kondisi konkuren, BUKAN klaim saya sudah
     mendemokan duplikat.
  4. Saya tidak mendemokan dua POST konkuren di sini (butuh harness
     timing/parallel-client; investigasi-only) — maka saya tidak
     mengklaim "terbukti dua baris", hanya "dapat lolos" (laporan
     awal: confidence sedang, "demonstrasi konkuren belum dijalankan").
- Reasoning: Dengan (3), pilihannya: (i) kuatkan kode (lock/constraint
  — mahal untuk guard best-effort), atau (ii) kualifikasi spec ("collapse
  berlaku untuk submisi sekuensial/single-flight; duplikat konkuren
  dapat tercatat ganda; count monitor monoton-naik dinormalisasi di
  tampilan"). Saya merekomendasikan (ii) — sesuai rekomendasi C —
  karena dampak terbatas pada akurasi badge "N violation(s)"
  (bukan skor/peringkat/hak). Ini pengakuan eksplisit: temuan saya
  adalah kualifikasi-dokumen + static-pattern, bukan tuntutan fix
  wajib.
- Counterargument ("flood-control memang best-effort → bukan temuan"):
  bila best-effort yang dimaksud, spec harus mengatakannya; kalimat
  sekarang kategorik. Temuan berdiri sebagai inkonsistensi — dengan
  remediasi termurah (satu kalimat kualifikasi), bukan perubahan kode.
- Conclusion: PARTIALLY diterima; posisi (b) kualifikasi dokumental;
  fakta pola check-then-act dipertahankan; tidak ada klaim demo yang
  saya buat-buat.
- Confidence: sedang (pola + bacaan spec terbukti; realisasi konkuren
  belum didemo).

## Ringkasan posisi akhir

- N-001: CONFIRMED dipertahankan (defend penuh). Niat: penyatuan-rutin
  disengaja, guard-ganda tak terbukti disengaja; rekomendasi (b)
  pisahkan-guard/amendemen §6.6. Confidence tinggi.
- N-002: CONFIRMED dipertahankan; kualifikasi (a) diterima (sisa TTL,
  laporan awal sudah "hingga/maks"); inventarisasi (b) lengkap — hanya
  ApproveReset butuh `RevokeUserSessions`. Netral pada HIGH→MEDIUM.
  Confidence tinggi.
- N-003: fakta dipertahankan, klaim dipersempit — akuisisi-ranker (bukan
  Upsert/Snapshot) yang terblokir; klaim pelanggaran-spec non-blocking
  registry DITARIK; magnitude belum diukur. Terima PARTIALLY.
  Confidence fakta tinggi / magnitude rendah-sedang.
- N-004: fakta statis dipertahankan; terima static-TOCTOU/LOW/PARTIALLY
  tanpa demo live. Confidence fakta tinggi / realisasi sedang.
- N-005: pola dipertahankan; posisi (b) kualifikasi dokumental §6.9;
  terima PARTIALLY; tidak ada klaim demo. Confidence sedang.

## Adendum pasca-drift (working copy berubah di bawah investigasi)

Pada pemeriksaan ulang, working copy produksi berubah (global.go,
results.go, student.go, order.go, app.css, monitor.js, workspace.js +
`internal/handlers/answer_display.go` baru). Seluruh bukti kutipan
N-001…N-005 diverifikasi ulang dan tetap berlaku; nomor baris bergeser
beberapa baris, substansi tidak berubah:

- N-001: guard `IN ('aktif','berjalan')` kini `global.go:888`
  (sebelumnya :889); rutin bersama `personal.go:66` tetap.
- N-002: `password_resets.go` tetap nol referensi Revoke/Store/cache.
- N-003: `SweepDisconnects` lock-melintasi-I/O tetap (`live.go:186+`).
- N-004: `quizGates(quiz)` pra-tx kini `student.go:286`; tetap tanpa
  `SELECT quizzes ... FOR UPDATE` di student.go.
- N-005: baca-sebelum-tx + `ShouldRecord` murni tetap
  (`student.go:1660+`, `anticheat.go:14-22`).
