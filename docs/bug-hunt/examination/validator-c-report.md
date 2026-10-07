# Validator C — Academic Defense Readiness & Claim-to-Evidence Alignment (Round 1)

> Role: hostile-but-fair examiner. Default: "klaim belum terbukti".
> Pertanyaan pamungkas (§23): bukan "apakah software bekerja?" melainkan
> **"dapatkah penulis thesis mempertahankan klaimnya secara ilmiah?"**
> Indeks: `mcp__codebase_memory_mcp_list_projects` → `D-Project-Mother-quiz` terdaftar;
> `mcp__codebase_memory_mcp_index_status` → `ready` (5206 nodes / 19442 edges,
> indexed 2026-10-07T08:27:10Z); `index_repository` TIDAK dijalankan (indeks segar).
> Verifikasi independen via `read`/`grep` langsung ke berkas aktual.
> Tidak ada perubahan kode produksi.

## Metode penilaian (sudut Validator C)

1. Apakah klaim thesis yang diserang dirumuskan **absolut** (`exactly`, `all`, `full`,
   `never`, `none`, `closed`, universal) sehingga **satu counterexample valid**
   menggugurkannya — atau hunter menyerang rumusan longgar (maka vonis harus
   `PARTIALLY`/`DISPUTED`, bukan `CONFIRMED`)?
2. Klasifikasi remediasi per temuan: **(a)** perbaikan kode · **(b)** amendemen dokumen ·
   **(c)** bukan masalah (quibble).
3. Apakah severity hunter proporsional terhadap **keterpertahanan di depan penguji**,
   bukan sekadar dampak teknis (satu ENUM mentah bisa LOW teknis tapi MEDIUM
   klaim-bahasa absolut).

---

## 5. Validator Examination

### N-001 (F-A-001) — Guard closeQuiz longgar vs spec §6.6

- Validator: C
- Challenge: Klaim thesis yang diserang ABSOLUT dan gugur oleh satu counterexample.
  Spec §6.6: "All three funnel through `UPDATE ... SET status='selesai' WHERE
  status='berjalan'` — first commit wins, others no-op." Diulang §8:
  "STOP vs timeout vs all-finished race → single transition `WHERE status='berjalan'`".
  Kode aktual `internal/handlers/global.go:886-889`:
  `UPDATE quizzes SET status='selesai' WHERE id=? AND status IN ('aktif','berjalan')`.
  Lebih memberatkan: komentar fungsi `closeQuiz` sendiri (§6.6: "then `UPDATE quizzes ...
  WHERE status='berjalan'`") **bertentangan dengan literal guard di bawahnya** —
  dokumen-dalam-kode pun tidak dapat dipertahankan. `Stop` (`global.go:746-781`)
  tidak memeriksa status sebelum memanggil `closeQuiz`; STOP pada kuis `aktif` yang
  belum START sukses menutup (peserta registered/pending ikut diselesaikan).
  Satu-satunya pembelaan yang tersisa: guard longgar **disengaja** untuk menyatukan
  jalur per-question-close (`closeWithModal`, `personal.go:56-66` — menutup kuis
  `aktif` memang benar) dengan STOP global. Tetapi pembelaan itu justru menuntut
  amendemen spec (pisahkan guard per jalur), bukan penolakan temuan.
- Evidence Requested: lihat REQUEST_TO_HUNTER N-001 di bawah.
- Bug Hunter Response: <menunggu ronde defense>
- Validator Verdict: **CONFIRMED**
- Remaining Objection: Niat desain (`IN` disengaja vs lalai) belum diputus — menentukan
  remediasi (a) vs (b), bukan eksistensi gap.
- Remediasi: **(a)/(b)** — (a) ketatkan guard jalur STOP global ke `WHERE status='berjalan'`
  ATAU (b) amendemen §6.6/§8 yang memisahkan guard per jalur (global vs per-question).
- Status: OPEN (menunggu jawaban hunter untuk penajaman remediasi).

### N-002 (F-A-002) — Approve-reset tanpa invalidasi mirror sesi

- Validator: C
- Challenge: Fakta kode CONFIRMED harfiah. `ApproveReset`
  (`password_resets.go:74-110`) hanya `UPDATE password_resets` + `UPDATE users SET
  must_change_pw=1` — nol referensi `Store`/`Revoke`/`SessionKey`/`cache` (grep di
  berkas nihil; bandingkan `ChangePassword` `auth.go:409` dan penonaktifan
  `teacher_students.go:257,306` yang memanggil `RevokeUserSessions`).
  `LoadSession` (`session.go:58-97`) menyajikan snapshot `MustChangePW` dari mirror
  TTL 5 menit tanpa revalidasi. Jendela basi = sisa TTL per sesi aktif.
  Uji klaim-bahasa: spec §6.2 tabel Sessions — "explicit delete on logout / password
  change (instant revocation)"; "Mutations (login/logout/password change): write DB
  first, then explicitly delete cache entry → revocation is instant"; golden rule
  "cache never answers newer than DB". Pembelaan terbaik penulis: "approve bukan
  password-change, jadi instant-tidak-dijanjikan untuk jalur ini." Pembelaan itu
  GAGAL di depan penguji: (i) golden rule tidak mengkualifikasi jalur; (ii) examiner
  cukup meminta demo approve → request berikutnya tanpa redirect (bukti decisive);
  (iii) pola `RevokeUserSessions` sudah mapan di dua jalur tetangga, sehingga
  ketiadaannya di sini adalah inkonsistensi, bukan desain. Severity HIGH hunter
  proporsional: relevan-keamanan + properti yang diklaim ("ditegakkan middleware
  dari setiap halaman").
- Evidence Requested: lihat REQUEST_TO_HUNTER N-002.
- Bug Hunter Response: <menunggu>
- Validator Verdict: **CONFIRMED**
- Remaining Objection: Kuantifikasi jendela (sisa TTL, bukan selalu 5 menit penuh) —
  minor, tidak mengubah vonis.
- Remediasi: **(a)** perbaikan kode — panggil `RevokeUserSessions`/hapus entri mirror
  milik user tersebut dalam/segara setelah transaksi approve.
- Status: OPEN.

### N-003 (F-A-003) — SweepDisconnects menahan mutex melintasi I/O DB

- Validator: C
- Challenge: Fakta struktural CONFIRMED harfiah: `live.go:186-209`
  `l.mu.Lock(); defer Unlock()` melingkupi loop berisi `QueryRowContext` (195) +
  `ExecContext` (205); semua akses Live lain (`Connect` 131, `Connected` 158,
  `Disconnect` 167) butuh `l.mu`; pemanggil `WatchOnce` (`global.go:955-956`) tiap
  1 detik. TETAPI klaim thesis yang diserang TIDAK absolut untuk registry ini.
  Spec §8 "never block the hub" mengatur **slow-SSE-client publish path**, bukan
  mutex `Live`; tidak ada kalimat "watchdog tidak pernah memegang lock melintasi
  I/O". Komentar kode bahkan **mendokumentasikan tradeoff secara terbuka**
  ("map lock dipegang melintasi query agar Connect konkuren tidak interleave") —
  penulis dapat mempertahankannya sebagai keputusan atomicity-vs-liveness yang
  disadari, bukan kelalaian. Besaran dampak tidak diukur (bergantung latensi DB ×
  jumlah disconnect; N round-trip sekuensial per tick). Di depan penguji, hunter
  menang pada fakta ("lock melintasi I/O — tunjukkan polanya") tetapi kalah pada
  vonis "ketahanan realtime gugur" tanpa angka tail-latency.
- Evidence Requested: lihat REQUEST_TO_HUNTER N-003.
- Bug Hunter Response: <menunggu>
- Validator Verdict: **PARTIALLY CONFIRMED** (pola lock-across-IO terbukti; klaim
  "desain non-blocking gugur" overstate tanpa pengukuran).
- Remaining Objection: Butuh (i) kutipan klaim non-blocking yang tepat mencakup
  registry (jika ada), atau (ii) angka blokir (latensi `Connect`/`Snapshot` selama
  sweep di bawah DB lambat) untuk menaikkan ke CONFIRMED.
- Remediasi: **(a)** perbaikan kode (hardening: snapshot kandidat di bawah lock
  singkat, I/O di luar lock, commit dengan guard `WHERE status='started'`/CAS) —
  bukan syarat kelulusan defense, melainkan perbaikan kualitas.
- Status: OPEN.

### N-004 (F-A-004) — Gerbang join memakai snapshot basi (TOCTOU)

- Validator: C
- Challenge: Fakta stale-read CONFIRMED harfiah: `Join` memuat `quizByCode` pra-tx
  → `joinQuiz(ctx, quiz, ...)` → cabang no-row memanggil `quizGates(quiz)` (objek
  luar-tx, `student.go:236-316`); satu-satunya `FOR UPDATE` mengunci baris
  participants (`student.go:163-164`), bukan baris kuis; tidak ada
  `SELECT quizzes ... FOR UPDATE`/re-read di dalam tx. Klaim thesis §10:
  "Guard quiz state server-side (`WHERE status='...'` inside transactions); never
  trust client state." Pembelaan "objek `quiz` bukan client state melainkan DB read"
  lemah: yang dituntut §10 adalah guard **di dalam transaksi** atas state
  **segar**; snapshot pra-tx secara definisi bukan guard-dalam-tx untuk cabang
  no-row. Banding-pembanding `Start` (`UPDATE ... WHERE status='aktif'`) membuktikan
  penulis tahu pola benar. TETAPI realisasi race (interleaving START-vs-INSERT yang
  menghasilkan baris yatim `registered` pada kuis `berjalan`) **belum didemonstrasikan**
  — butuh timing; hunter sendiri menilai confidence sedang-tinggi. Di depan penguji,
  penulis tidak dapat menunjukkan guard-dalam-tx untuk cabang no-row (kalah
  klaim-vs-bukti), tetapi hunter belum menunjukkan baris yatim lahir (belum menang
  eksploitasi). Severity MEDIUM proporsional untuk gap integritas berjendela-timing.
- Evidence Requested: lihat REQUEST_TO_HUNTER N-004.
- Bug Hunter Response: <menunggu>
- Validator Verdict: **PARTIALLY CONFIRMED** (pola stale-guard terbukti; TOCTOU
  terrealisasi belum dibuktikan — naik ke CONFIRMED bila interleaving didemo/uji).
- Remaining Objection: Demo race konkret atau sketsa interleaving yang tak terbantahkan.
- Remediasi: **(a)** perbaikan kode — baca ulang/kunci baris kuis di dalam tx join
  (atau gabungkan INSERT dengan guard status).
- Status: OPEN.

### N-005 (F-A-005) — Collapse anti-cheat tembus saat konkuren

- Validator: C
- Challenge: Fakta pola CONFIRMED: baca last-event di luar tx
  (`student.go:1651-1700`: `SELECT created_at, kind ... LIMIT 1` pra-`BeginTx`) +
  `ShouldRecord` murni tanpa sinkronisasi (`anticheat.go:14-22`) + `INSERT` di tx
  tanpa row-lock/advisory-lock/constraint. Klaim thesis ABSOLUT tanpa kualifikasi:
  §6.9 "duplicate kind within 10 s collapses to one row", §8 "Flood → 10 s collapse
  per kind". Satu pasangan POST konkuren sejenis <10 detik yang sama-sama tercatat
  menggugurkan rumusan itu. TETAPI: (i) demo konkuren belum dijalankan (confidence
  sedang, diakui hunter); (ii) collapse window secara alami tidak dapat di-UNIQUE-kan,
  sehingga fix atomik (kunci baris per-participant/serialisasi) tidak proporsional
  dengan dampak (badge count terinflasi; skor/peringkat tak tersentuh); (iii) penulis
  punya pembelaan wajar: "flood-control best-effort single-flight" — yang hanya
  menuntut kualifikasi dokumen, bukan pembuktian kedap-concurrency. Severity LOW
  hunter SUDAH proporsional dan fair (tidak digelembungkan ke MEDIUM klaim-bahasa —
  tepat, karena dampak akademiknya kualifikasi satu kalimat).
- Evidence Requested: lihat REQUEST_TO_HUNTER N-005.
- Bug Hunter Response: <menunggu>
- Validator Verdict: **PARTIALLY CONFIRMED** (pola check-then-insert + rumusan absolut
  terbukti; penetrasi konkuren belum didemo — tetapi vonis tidak bergantung padanya
  karena remediasinya dokumental).
- Remaining Objection: Tidak ada yang menahan remediasi; demo konkuren hanya memperkuat,
  bukan prasyarat.
- Remediasi: **(b)** amendemen dokumen — kualifikasi §6.9/§8 menjadi
  "single-flight/best-effort; submisi sekuensial dijamin, konkuren dapat tercatat ganda".
- Status: OPEN.

### N-006 (F-B-001) — Klaim closed-8-direct gugur

- Validator: C
- Challenge: Klaim thesis PALING absolut dalam dokumen, dirumuskan tiga lapis:
  §2 "**Closed dependency list** — exactly 8 **direct** modules" + daftar 8 nama +
  peran ("CSV export — `gocarina/gocsv`"); §10 "Stay within the closed 8-module
  list"; §11.21 "no dependencies outside the closed list" (kriteria kelulusan).
  Tiga counterexample independen, masing-masing decisive: (a) `go.mod:5-9` blok
  direct hanya 3 entri (`mysql`, `cleanenv`, `echo/v5`); lima nama klaim-direct
  tercatat `// indirect` (`go.mod:12-24`) — frasa "exactly 8 direct" salah terhadap
  satu-satunya definisi operasional "direct" (manifest build). (b) `gocsv` nol import
  di seluruh `*.go` (grep repo hanya kena `go.mod`/`go.sum`/plan/docs) sementara
  `export.go:3-16` memakai `encoding/csv` stdlib — klaim peran tanpa bukti
  implementasi; tag `csv:"..."` pada struct adalah gaya penamaan, bukan pemakaian.
  (c) Dependensi runtime ke-9 yang dieksekusi namun tak diakui di §2/§4/§10 mana pun:
  `Chart.js v4.5.1` bervendor (`web/vendor/chartjs/`) + tiga canvas
  (`dashboard.html:72-84`) + `<script src=".../chart.umd.min.js">`
  (`dashboard.html:130`); satu-satunya kemunculan "chart" di spec adalah rute
  analytics generik (`overview`), bukan deklarasi dependensi. Satu counterexample
  sudah cukup; di sini tiga. Catatan fair: marker `// indirect` untuk 4 modul selain
  gocsv (excelize, go-sse, go-qrcode, x/crypto) saja tidak membuktikan "tak dipakai"
  — bisa artefak belum-`tidy` — maka sub-butir (a) saya batasi pada apa yang literal:
  **manifest menyatakan 3 direct**, sehingga kalimat "exactly 8 direct" gugur apa pun
  status impornya; pembuktian "yatim" hanya solid untuk gocsv. Itu tidak melemahkan
  vonis karena (b)+(c) masing-masing berdiri sendiri. Severity HIGH proporsional:
  klaim ketertutupan adalah tulang punggung kejujuran-metodologis + kriteria
  kelulusan §11.21 menjadi tak-falsifiable + `govulncheck` tak mencakup artefak
  tak-terdaftar.
- Evidence Requested: lihat REQUEST_TO_HUNTER N-006 (konfirmasi sempit, bukan prasyarat vonis).
- Bug Hunter Response: <menunggu>
- Validator Verdict: **CONFIRMED**
- Remaining Objection: Tidak ada yang menggugurkan; klarifikasi impor 4 modul hanya
  mempertajam rekomendasi (tambah-tidy vs hapus-dep).
- Remediasi: **(b)** amendemen dokumen — tulis ulang §2/§10/§11.21 sesuai `go.mod`
  aktual (rapikan direct/indirect), putuskan nasib gocsv (pakai atau keluarkan dari
  daftar), daftarkan Chart.js (versi + sumber + kebijakan pemantauan vuln) atau
  keluarkan dari jalur runtime.
- Status: OPEN.

### N-007 (F-B-002) — Metodologi testing §9 overclaim

- Validator: C
- Challenge: Temuan gabungan empat sub-butir — kekuatannya tidak merata, maka vonis
  harus dipilah (hostile-but-fair melarang vonis selimut):
  (a) "parallel-safe" — grep `t.Parallel` nihil di `tests/`+`internal/` (terverifikasi).
  Klaim gugur sebagai klaim **terbukti**; pembelaan "parallel-safe = aman-jika-diparalelkan,
  bukan memakai `t.Parallel`" gagal karena spec juga menuntut pembuktian ("run
  sequentially" vs "parallel-safe" tegang secara internal) tanpa satu pun eksekusi
  paralel. Kuat. (b) "no `time.Now` in logic" — hanya benar di `quizengine`
  (`timer.go` murni + `Clock`); handler pemutus-kelulusan memanggil `time.Now()`
  langsung (`auth.go:118,148`; `global.go:243,265,956`; `live.go:140,143`;
  `student.go:846,868,970,1091,1198,1410,1675,1773`). Pembelaan "logic = quizengine
  saja" hanya bertahan untuk presentasi (`server_now`) — tidak untuk `ends_at`/freeze/
  collapse yang menentukan hasil di dalam transaksi. Kuat-sebagian. (c) Atribusi tabel
  §9 (`authlogic_test.go` mencakup forgot-password/join-retry) vs komentar berkas
  sendiri (`authlogic_test.go:8-14` mendelegasikan ketiganya ke integrasi) — mismatch
  literal, decisive. Sangat kuat. (d) "7 subtests" `assets_test.go:83-125` — 7 entri
  tabel dieksekusi via satu `t.Run` loop; secara teknis 7 subtests memang ada.
  Quibble — hunter sendiri menilai sedang; saya tandai **(c) bukan masalah** pada
  sub-butir ini saja. (e) "fully green, nothing disabled" (§11.14) vs `testutil.go:62-77`
  `Skipf` saat DB tak terjangkau — "green vakum" pada mesin tanpa DB; pembelaan "skip
  itu standar" gagal terhadap kata "nothing disabled". Kuat.
  Severity MEDIUM proporsional: cacat validitas-konstruk (instrumen tidak mengukur apa
  yang diklaim), bukan kegagalan fungsional.
- Evidence Requested: lihat REQUEST_TO_HUNTER N-007.
- Bug Hunter Response: <menunggu>
- Validator Verdict: **PARTIALLY CONFIRMED** (sub-butir (a)/(c)/(e) + sebagian (b)
  terbukti; (d) quibble ditolak — vonis keseluruhan bertahan sebagai overclaim
  metodologi, bukan tiap kata hunter).
- Remaining Objection: Presisi batas "logic" untuk (b) — menentukan redaksi amendemen,
  bukan vonis.
- Remediasi: **(b)** amendemen dokumen — hapus/turunkan "parallel-safe" (atau buktikan
  dengan `t.Parallel` + `-race`), perbaiki atribusi tabel §9 ke berkas sebenarnya,
  nyatakan seam jam hanya di `quizengine` + risiko wall-clock handler, nyatakan
  prasyarat DB untuk arti "green" §11.14.
- Status: OPEN.

### N-008 (F-B-003) — Drift living spec: users.aktif + riwayat timer_type

- Validator: C
- Challenge: Klaim §10 absolut: "keep schema in sync with §5 (living spec)".
  Bukti kolom-per-kolom decisive untuk (a): `0003_users_aktif.sql` menambah
  `users.aktif TINYINT(1) NOT NULL DEFAULT 1`; `0001_init.sql:20-32` + blok `users`
  §5 berakhir di `must_change_pw → created_at` tanpa `aktif`; grep `aktif` pada spec
  hanya kena status kuis/TTL — nihil kolom `users.aktif`; enforcement dua lapis nyata
  (`auth.go:89-115` tolak login nonaktif; `session.go:77-93` bunuh sesi berjalan).
  Fitur user-visible (deactivation) hidup di kode+migrasi, mati di dokumen —
  ketertelusuran requirements→implementasi putus; dampak §11 (login gagal pesan khusus,
  sesi dicabut) tanpa kriteria sukses. Sub-butir (b) `timer_type`: `0001:47` hanya
  2 nilai, `0004` melahirkan `tanpa_timer`, §5 tampil final 3 nilai — itu **sinkronisasi
  yang berhasil**, bukan drift; hunter memakainya sebagai kontras positif dengan tepat,
  dan saya mengadopsinya: (b) bukan kesalahan, melainkan bukti bahwa kelalaian `aktif`
  adalah inkonsistensi selektif, bukan keterbatasan format. Pembelaan "living spec =
  tampilkan final" justru menghukum penulis: final §5 pun kehilangan `aktif`.
  Severity MEDIUM proporsional.
- Evidence Requested: tidak ada yang menahan vonis (fakta kolom lengkap); lihat
  REQUEST_TO_HUNTER N-008 hanya untuk kelengkapan semantik.
- Bug Hunter Response: <menunggu>
- Validator Verdict: **CONFIRMED**
- Remaining Objection: Tidak ada.
- Remediasi: **(b)** amendemen dokumen — tambahkan `aktif` ke blok `users` §5 +
  semantik deactivation (efek login/sesi/riwayat) + catat keputusan ask-first §10.
- Status: OPEN.

### N-009 (F-B-004) — Klaim KBBI gugur

- Validator: C
- Challenge: Klaim universal tiga lapis: §1 "full bahasa Indonesia baku (KBBI)";
  §11.21 "All UI text in bahasa Indonesia baku"; §7 "Schema column/ENUM literals ...
  never user-facing". "All/full/never" = satu counterexample valid menggugurkan.
  Empat counterexample independen dengan rantai DB→handler→template lengkap:
  (1) `history.html:37-41` + duplikat kartu mobile `:83-87`
  `{{else}}{{.Status}}` mencetak mentah `p.status` (`history.go:60-88` tanpa pemetaan
  bahasa: `pending`/`registered`/`started`/… ke mata murid). (2) `monitor.html:52-53`
  `{{.Status}}` partisipan + `:7-10` `{{.TimerType}} timer` (kata Inggris "timer") +
  label Inggris "status" + nilai ENUM kuis Inggris — campur dua bahasa satu baris.
  (3) Istilah ganda Dashboard/Dasbor: render+test mengunci "Dasbor"
  (`breadcrumb.go:39,54,64,132`; `nav_no_root_links_test.go:56-60`) sementara
  dokumen+komentar normatif memakai "Dashboard" (`breadcrumb.go:11,35,61,128`;
  `breadcrumb.html:4`; test `:14`; tabel §9) — bukti istilah tak dikendalikan.
  Pembelaan "ENUM adalah identifier, bukan UI text" dibunuh kalimat §7 sendiri
  ("never user-facing") — implementasi membuatnya user-facing. Pembelaan "§1
  membolehkan schema literals stay original" hanya mencakup **bentuk di kode/skema**,
  bukan **render ke DOM**. Severity MEDIUM hunter TEPAT dan patut dibela dari
  validator teknis yang akan menurunkannya ke LOW: LOW secara kosmetik, MEDIUM secara
  keterpertahanan — penguji cukup membuka `/history` akun `registered` dan membaca
  kata Inggris di depan sidang.
- Evidence Requested: tidak ada yang menahan vonis; lihat REQUEST_TO_HUNTER N-009
  (klarifikasi kamus yang diinginkan).
- Bug Hunter Response: <menunggu>
- Validator Verdict: **CONFIRMED**
- Remaining Objection: Tidak ada.
- Remediasi: **(a)+(b)** — (a) petakan ENUM ke label Indonesia di render (kamus
  terpusat + test pengunci string) dan satukan Dasbor/Dashboard; (b) rumuskan ulang
  klaim absolut menjadi terukur ("semua string di file X lolos kamus Y").
- Status: OPEN.

### N-010 (F-B-005) — §11.2 skip-validasi + robustness tanpa uji negatif + §12 None

- Validator: C
- Challenge: Tiga sub-klaim berkekuatan berbeda — dipilah eksplisit:
  (a) **Skip-validasi — CONFIRMED, inti HIGH.** Spec §11.2 memandatkan harfiah:
  "approve → **next login skips password validation** → locked on change-password".
  Kode menegakkannya harfiah: `auth.go:104-115`
  `if !must && bcrypt.CompareHashAndPassword(...) != nil` → bila `must==true`
  perbandingan dilewati seluruhnya → `startSession` menerbitkan sesi tanpa bukti
  pengetahuan kata sandi; `session.go:130-139` hanya me-redirect, tak mencabut.
  Penulis DAPAT membela korespondensi (kode = spec) tetapi TIDAK DAPAT membela
  keamanan: siapa pun bermodal username akun yang baru disetujui-reset memperoleh
  sesi terautentikasi (lalu memang dikunci di `/change-password` — sesi sudah di
  tangan). Mitigasi parsial (`aktif` tetap dicek; sesi lama mati saat change) tidak
  menutup jendela pengambilalihan. Kriteria sukses yang memandatkan perilaku tak-aman
  = cacat validitas kriteria. (b) **Robustness tanpa uji negatif — PARTIALLY
  CONFIRMED.** Klaim §11.15-17 (rehydrate identik, freeze-tanpa-kehilangan-waktu,
  "DB write failure → no broadcast") tanpa pasangan uji-gagal: suite
  `realtime_test.go:277-484` skenario positif (publish, ordering, guard buffer,
  rehydrate-simulasi); grep skenario gagal-tulis nihil. Ketiadaan-terbukti pada
  repo ini (pencarian ekshaustif wajar), tetapi "tanpa test" ≠ "salah" — kode bisa
  benar; ini gap falsifiabilitas, bukan disproof fungsional. (c) **§12 None —
  PARTIALLY CONFIRMED dengan koreksi logis penting.** §12: "None — all 24
  clarification decisions … are resolved." Hunter membaca "None" sebagai klaim
  ketiadaan lacunae, dibantah N-006…N-009. Bacaan strict yang fair: "None" adalah
  **status resolusi klarifikasi** (tak ada pertanyaan terbuka yang diakui), bukan
  proposisi "dokumen tanpa cacat". Yang hunter BUKTIKAN adalah **ketidaklengkapan**
  (topik material di luar 24 keputusan), bukan kepalsuan sadar. Vonis tepat:
  §12 menyesatkan-secara-kelengkapan (examiner yang memercayainya berhenti mencari
  risiko yang nyata ada), tetapi bukan "palsu" dalam arti fabrikasi. Konsekuensinya
  sama (buka kembali §12), logikanya dibedakan.
  Uji konvergensi N-002↔N-010(a): **keduanya klaim berbeda yang sama-sama valid,
  satu TIDAK menyerap yang lain.** N-002 = enforcement tertunda pada **sesi aktif**
  (stale mirror `MustChangePW=false` hingga TTL); N-010(a) = **sesi baru terbit
  tanpa verifikasi** (skip `CompareHash`). Mekanisme, lokasi, dan jendela serangan
  berbeda; keduanya mengalir pada `must_change_pw` dan **saling menguatkan**
  (penulis yang memperbaiki satu masih gugur oleh yang lain). Relasi Main Gate
  ("saling menguatkan, bukan duplikat") saya sahkan.
- Evidence Requested: lihat REQUEST_TO_HUNTER N-010.
- Bug Hunter Response: <menunggu>
- Validator Verdict: **CONFIRMED** (dengan sub-vonis: (a) CONFIRMED · (b) PARTIALLY
  CONFIRMED · (c) PARTIALLY CONFIRMED-dengan-koreksi).
- Remaining Objection: Daftar uji-negatif ekshaustif final (b); redaksi §12 pengganti (c).
- Remediasi: **(a)+(b)** — (a) code-fix: rumuskan ulang §11.2 menjadi sifat keamanan
  yang benar (reset yang disetujui TIDAK melemahkan autentikasi; verifikasi identitas
  tetap dituntut; sesi lama mati) + implementasikan; (b) doc-amendment + uji:
  tambahkan uji negatif robustness (gagal-tulis→tanpa-broadcast; freeze-vs-jam-maju)
  atau lunakkan klaim; buka §12 dengan sisa risiko (dependensi, skema, bahasa).
- Status: OPEN.

---

## Ringkasan verdict

| ID | Hunter verdict | Validator C verdict | Remediasi | Severity C |
|----|---------------|---------------------|-----------|------------|
| N-001 | SUBMITTED (MEDIUM) | CONFIRMED | (a)/(b): ketatkan guard STOP global ATAU pisahkan guard per jalur di §6.6/§8 | MEDIUM — setuju |
| N-002 | SUBMITTED (HIGH) | CONFIRMED | (a) code-fix: invalidasi mirror saat approve | HIGH — setuju |
| N-003 | SUBMITTED (MEDIUM) | PARTIALLY CONFIRMED | (a) hardening lock/I-O (bukan syarat lulus) | MEDIUM→LOW-leaning; fakta kuat, link-klaim lemah |
| N-004 | SUBMITTED (MEDIUM) | PARTIALLY CONFIRMED | (a) code-fix: guard segar dalam-tx | MEDIUM — setuju (naik ke CONFIRMED bila race didemo) |
| N-005 | SUBMITTED (LOW) | PARTIALLY CONFIRMED | (b) kualifikasi single-flight/best-effort §6.9/§8 | LOW — setuju |
| N-006 | SUBMITTED (HIGH) | CONFIRMED | (b) tulis ulang §2/§10/§11.21 + nasib gocsv + daftarkan Chart.js | HIGH — setuju |
| N-007 | SUBMITTED (MEDIUM) | PARTIALLY CONFIRMED ((a)/(c)/(e)+sebagian (b); (d) quibble ditolak) | (b) turunkan klaim §9/§11.14 | MEDIUM — setuju |
| N-008 | SUBMITTED (MEDIUM) | CONFIRMED ((b)-timer_type kontras positif, bukan kesalahan) | (b) sinkronkan `users.aktif` + semantik §5 | MEDIUM — setuju |
| N-009 | SUBMITTED (MEDIUM) | CONFIRMED | (a)+(b): kamus ENUM + satukan istilah + ukur klaim | MEDIUM — setuju (LOW teknis, MEDIUM defensibilitas) |
| N-010 | SUBMITTED (HIGH) | CONFIRMED ((a) CONFIRMED · (b)(c) PARTIALLY) | (a)+(b): amankan §11.2 + uji negatif + buka §12 | HIGH — setuju (ditopang (a)) |

Klasifikasi remediasi per temuan: N-001=(a)/(b); N-002=(a); N-003=(a);
N-004=(a); N-005=(b); N-006=(b); N-007=(b); N-008=(b); N-009=(a)+(b); N-010=(a)+(b).
Nihil (c) murni — tidak ada temuan yang saya tolak sebagai quibble penuh; sub-butir
quibble (N-007(d), N-008(b-sebagai-kesalahan)) saya cabut dari dalam tanpa mengubah
vonis induk. Tidak ada perubahan kode produksi (mandat Validator).

## Pertanyaan ke Hunter (REQUEST_TO_HUNTER) — Round 1

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-001
Question: Apakah guard IN ('aktif','berjalan') menurut investigasimu disengaja
  untuk menyatukan jalur per-question-close (yang memang menutup 'aktif') dengan
  STOP global — atau kelalaian? Jika disengaja, bukti niat apa (komentar/commit/test)
  yang kamu punya selain struktur berbagi-rutin?
Required evidence: kutipan spec §6.5 yang memandatkan/mengizinkan close dari 'aktif'
  untuk jalur per-question; test close-modal yang menegaskan perilaku per jalur.
Reason: Menentukan remediasi (a) ketatkan vs (b) pisahkan-guard di spec; vonis
  CONFIRMED tidak bergantung padanya.
Priority: medium
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-002
Question: Konfirmasi kuantifikasi jendela basi: sisa TTL entri sesi (maks 5 menit),
  bukan selalu 5 menit penuh — setuju? Dan apakah ada jalur lain yang menulis
  must_change_pw selain ApproveReset (yang ikut butuh invalidasi)?
Required evidence: grep penulis must_change_pw di internal/ + migrations/.
Reason: Presisi redaksi rekomendasi; vonis CONFIRMED tidak bergantung padanya.
Priority: low
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-003
Question: (1) Kutip kalimat spec yang menurutmu menjanjikan non-blocking untuk
  registry Live/presence (bukan hanya publish path §6.1/§8 slow-client)?
  (2) Atau sediakan angka: latensi Connect/Ranker.Snapshot selama sweep di bawah
  DB yang diperlambat + N disconnect.
Required evidence: kutipan §-persis ATAU hasil ukur (metode + angka).
Reason: Tanpa salah satunya, vonis bertahan PARTIALLY (fakta benar, link-klaim lemah).
  Saya tidak menerima eskalasi ke CONFIRMED tanpa alasan.
Priority: high
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-004
Question: Sediakan demo interleaving START-konkuren-vs-INSERT (atau sketsa urutan
  operasional yang tak terbantahkan + query verifikasi baris yatim) untuk cabang
  no-row joinQuiz.
Required evidence: langkah repro + SELECT pembuktian (participants.status pada kuis
  'berjalan').
Reason: Mengangkat PARTIALLY → CONFIRMED-penuh; tanpanya remediasi (a) tetap
  direkomendasikan atas dasar pola.
Priority: high
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-005
Question: (Opsional) demo dua POST visibility konkuren sejenis <10 detik →
  dua baris. Tidak prasyarat: remediasi (b) dokumental sudah saya rekomendasikan.
  Pertanyaan substansial: apakah kamu menuntut constraint/fix kode, atau cukup
  kualifikasi "single-flight/best-effort" di §6.9/§8?
Required evidence: jawaban posisi + (bila ada) hasil demo konkuren.
Reason: Menutup perbedaan (a) vs (b); saya memutus (b).
Priority: low
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-006
Question: Konfirmasi sempit: (1) grep import Go untuk keempat modul
  (excelize/v2, go-sse, go-qrcode, x/crypto) — apakah dipakai tapi salah-cap
  indirect (artefak belum-tidy), atau juga yatim seperti gocsv?
  (2) Apakah ada artefak runtime tak-terdaftar lain (font, ikon) selain Chart.js?
Required evidence: grep import per modul + daftar web/vendor yang dieksekusi template.
Reason: Mempertajam rekomendasi (tidy vs hapus-dep vs daftarkan); vonis CONFIRMED
  sudah berdiri di atas (b) gocsv + (c) Chart.js.
Priority: medium
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-007
Question: (1) Apakah ada file test paralel / konfigurasi CI yang menjalankan
  suite secara paralel di luar grep t.Parallel (yang nihil)? (2) Presisikan batas
  "logic" pada "no time.Now in logic": apakah kamu menuntut seam jam sampai handler
  transaksional (ends_at/freeze/collapse), atau cukup quizengine?
Required evidence: kutipan CI/config bila ada; daftar call-site time.Now yang
  menurutmu memutus determinisme uji (vs presentasi murni).
Reason: Menentukan redaksi amendemen (b); sub-butir (d) 7-subtests saya nilai quibble.
Priority: medium
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-008
Question: Semantik deactivation apa yang harus masuk §5 agar sinkron: efek pada
  login (pesan), sesi berjalan (langsung mati), riwayat/peringkat, dan reaktivasi?
Required evidence: rujukan perilaku kode per efek (auth.go/session.go/teacher_students.go)
  + usulan redaksi §5.
Reason: Kelengkapan amendemen (b); vonis CONFIRMED tidak bergantung padanya.
Priority: low
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-009
Question: Kamus pemetaan yang kamu usulkan untuk tiap literal ENUM yang dirender
  (pending/registered/started/kuis-status/timer_type) + pilihan istilah tunggal
  Dasbor vs Dashboard?
Required evidence: daftar pasangan EN→ID + lokasi render yang dikunci test.
Reason: Kelengkapan rekomendasi (a)+(b); vonis CONFIRMED tidak bergantung padanya.
Priority: low
```

```
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-010
Question: (1) Untuk (b): daftar ekshaustif berkas tests/integration yang kamu petakan
  ke tiap klaim §11.15-17 sehingga klaim "tanpa uji negatif" dapat diaudit
  (nama-berkas → klaim → verdict ada/tidak-ada)? (2) Untuk (c): setuju dengan koreksi
  saya bahwa §12-"None" adalah status-resolusi yang menyesatkan-secara-kelengkapan
  (omission), bukan fabrikasi — atau kamu bersikeras "palsu" dengan bukti pengetahuan
  penulis saat menulis?
Required evidence: (1) matriks berkas→klaim; (2) posisi + alasan satu paragraf.
Reason: (1) menutup celah bukti-negatif; (2) presisi logika vonis tanpa mengubah
  konsekuensi (buka §12).
Priority: high
```

Round: 1 (closed — semua pertanyaan terjawab di Round 2)

---

# Round 2 — Final Evaluation (jawaban defense R1 dipertimbangkan, §17–§18)

> Sumber: `docs/bug-hunt/defense/hunter-a-defense-r1.md` (N-001…N-005) +
> `docs/bug-hunt/defense/hunter-b-defense-r1.md` (N-006…N-010).
> Prinsip §17: tidak menerima tanpa alasan; tidak mengarang keberatan setelah
> bukti decisive. Prinsip §18: keberatan yang sudah dijawab bukti baru/posisi
> tegas tidak diulang — ditutup sebagai CLOSED.
> Hasil: 10/10 pertanyaan Round 1 terjawab (bukti baru atau konsesi tegas).
> Tanpa perubahan kode produksi.

## N-001 — Round 2: CONFIRMED (final) · remediasi (b) · CLOSED

- Jawaban hunter: defend penuh + bukti niat terpilah — penyatuan-rutin DISENGAJA
  (`personal.go:16-18` "closing runs the same closeQuiz routine as STOP",
  `SetQuizStatus` ONLY-route, test `finish_lifecycle_test.go:76-232` mem-pin
  close-dari-`aktif` untuk jalur `/status`); guard-ganda tak terbukti disengaja
  (komentar fungsi `global.go:815-821` justru mengklaim `WHERE status='berjalan'`).
  Rekomendasi hunter sendiri: (b) pisahkan-guard/amendemen §6.6.
- Penilaian C: pertanyaan niat TERJAWAB dengan bukti, bukan klaim.
  Atribusi tepat: berbagi-rutin disengaja, guard-tunggal-tanpa-pemisah tidak
  terbukti disengaja. Counterexample STOP-sebelum-START → 200 vs kalimat absolut
  §6.6 tetap berdiri; test yang ada mem-pin jalur `/status`, bukan `POST /stop`
  pada kuis global `aktif`. Tidak ada keberatan tersisa yang decisive.
- Verdict final: **CONFIRMED**. Remediasi final: **(b)** amendemen §6.6/§8
  (guard global `WHERE status='berjalan'` vs guard per-question
  `WHERE status='aktif'`), atau pecah rutin. Status: **CLOSED**.

## N-002 — Round 2: CONFIRMED (final) · remediasi (a) · CLOSED

- Jawaban hunter: CONFIRMED dipertahankan; (a) kuantifikasi disetujui —
  jendela = sisa TTL ∈ (0,5] mnt (`keys.go:11` + `session.go:97`; laporan awal
  memang menulis "hingga/maks 5 menit"); (b) inventarisasi lengkap —
  satu-satunya penulis 0→1 tanpa invalidasi adalah `ApproveReset`
  (`password_resets.go:102`); `ChangePassword` (`auth.go:383→409`) aman via
  `RevokeUserSessions`. Netral terhadap HIGH→MEDIUM.
- Penilaian C: kedua pertanyaan TERJAWAB presisi. Saya terima penurunan
  severity ke **MEDIUM** (eksploitasi butuh sesi hangat pra-approve) — properti
  "revocation is instant" (§6.2/§6.4) tetap dilanggar pada jalur ini, sehingga
  vonis tidak goyang. Rekomendasi satu-baris `RevokeUserSessions` pasca-commit
  tepat dan berpola mapan.
- Verdict final: **CONFIRMED**. Remediasi final: **(a)** code-fix.
  Severity final C: **MEDIUM**. Status: **CLOSED**.

## N-003 — Round 2: PARTIALLY CONFIRMED (final) · remediasi (a, hardening) · CLOSED

- Jawaban hunter: fakta dipertahankan dengan koreksi presisi — yang terblokir
  adalah `Connect/Disconnect/Page/AddSpent/Spent` + AKUISISI `Ranker()`
  (`live.go:118-127`, orde mikrodetik), BUKAN `Upsert`/`Snapshot` yang berjalan
  (`ranking.go:20-58`, lock sendiri + sort di luar lock; `Publish` tanpa lock);
  klaim pelanggaran-spec non-blocking registry **DITARIK** (§6.1/§8 hanya janji
  publish path); magnitude belum diukur → terima PARTIALLY; steady-state murah
  (`live.go:192` skip-cepat, threshold 15 dtk `global.go:24-27`) diakui.
- Penilaian C: ini konsesi yang diminta (§17: jangan terima tanpa alasan —
  di sini alasannya ada: kutipan baris + penarikan eksplisit). Fakta
  lock-melintasi-I/O pada tick 1-detik benar; link-klaim absolut dan angka
  tidak ada. Tetap PARTIALLY adalah vonis tepat, bukan kegagalan hunter.
- Verdict final: **PARTIALLY CONFIRMED**. Remediasi final: **(a)** hardening
  (snapshot-kandidat di bawah lock singkat + I/O di luar + guard `frozen`/
  `WHERE status='started'`; opsional per-pid) — engineering note, bukan syarat
  lulus defense. Status: **CLOSED**.

## N-004 — Round 2: PARTIALLY CONFIRMED (final) · remediasi (a) · CLOSED

- Jawaban hunter: fakta statis dipertahankan (`quizByCode` pra-tx tanpa lock
  `student.go:211-214`; `quizGates(quiz)` objek luar-tx; satu-satunya
  `FOR UPDATE` di `latestParticipant`; kontras pola benar `Start`
  `global.go:615-638`); terima kualifikasi static-TOCTOU/LOW/PARTIALLY tanpa
  demo live (instrumentasi di luar mandat investigasi); tolak penolakan fakta.
- Penilaian C: pertanyaan TERJAWAB sejauh yang dapat dijawab tanpa harness DB
  (sketsa jendela konkret + efek-samping: waiting-room count, modal N,
  "not attempted" fiktif). Demo live tidak ada — maka eskalasi ke CONFIRMED
  penuh DITOLAK secara fair, dan hunter tidak menuntutnya. Fakta
  stale-guard + pelanggaran §10 Always-do pada cabang no-row cukup untuk
  PARTIALLY. Severity final: **LOW** (turun dari MEDIUM, sesuai konsesi).
- Verdict final: **PARTIALLY CONFIRMED**. Remediasi final: **(a)** code-fix
  (re-read/lock baris kuis di tx atau INSERT ber-guard). Status: **CLOSED**.

## N-005 — Round 2: PARTIALLY CONFIRMED (final) · remediasi (b) · CLOSED

- Jawaban hunter: pilih posisi (b) — kualifikasi dokumental sebagai posisi UTAMA;
  fakta pola dipertahankan (`student.go:1651-1700` baca-pra-tx + `ShouldRecord`
  murni `anticheat.go:14-22` + INSERT tanpa lock); spec §6.9 kategorik tanpa
  kualifikasi single-flight ("duplicate kind within 10 s collapses to one row");
  tidak ada klaim demo yang dibuat-buat; terima PARTIALLY.
- Penilaian C: pertanyaan TERJAWAB (posisi tegas + kutipan spec). Remediasi (b)
  yang saya rekomendasikan di Round 1 DIADOPSI hunter — konvergensi penuh,
  tidak ada sisa oposisi. Satu kalimat kualifikasi menutup temuan.
- Verdict final: **PARTIALLY CONFIRMED**. Remediasi final: **(b)** amendemen
  §6.9/§8 ("single-flight/best-effort; konkuren dapat tercatat ganda").
  Status: **CLOSED**.

## N-006 — Round 2: CONFIRMED, dipersempit (final) · remediasi (b) · CLOSED

- Jawaban hunter: partially concede — (a) `// indirect` sebagai bukti ditarik
  (`go mod why`: 4 modul dipakai langsung — go-sse via `realtime`,
  x/crypto/bcrypt via `handlers`, go-qrcode via `handlers`, excelize via
  `export_sheets.go:6`; `go mod tidy -diff` akan promosikan keempatnya ke direct
  dan MENGHAPUS gocsv); (b) gocsv yatim DIPERTAHANKAN (`go mod why gocsv` =
  tidak dibutuhkan siapa pun; nol import `*.go`; `export.go` memakai
  `encoding/csv`); (c) DITURUNKAN jujur menjadi **undocumented runtime
  dependency** (spec hanya berkosakata Go-module — tidak pernah mendefinisikan
  yurisdiksi atas aset JS) namun MATERIAL (tak ada di §4/§2/§10; eksekusi nyata
  `dashboard.html:72-84,130` + `teacher_dashboard.js:247,257,268,290` +
  header `chart.umd.min.js:1-6` v4.5.1 MIT); inventarisasi vendor: hanya Chart.js
  yang tak terdaftar (bootstrap-icons + theme.css + app.css + Fonts terdaftar).
- Penilaian C: koreksi (a) TEPAT dan saya adopsi — vonis tidak pernah bergantung
  pada (a) sendiri (Round 1 sudah membatasi: manifest=3-direct + (b)+(c) berdiri
  sendiri). (b)+(c) masing-masing decisive dan tak terbantahkan. Severity HIGH
  bertahan (kombinasi diklaim-tanpa-pakai + dipakai-tanpa-klaim meruntuhkan
  keterujian §11.21 + lubang `govulncheck`).
- Verdict final: **CONFIRMED** (ruang lingkup: (a) ditarik sebagai pelanggaran,
  tersisa kesalahan redaksi manifest; (b)+(c) terkonfirmasi). Remediasi final:
  **(b)** amendemen (rapikan direct via `tidy`, putuskan nasib gocsv,
  daftarkan Chart.js + kebijakan pantau-vuln, lengkapi §4). Status: **CLOSED**.

## N-007 — Round 2: PARTIALLY CONFIRMED (final) · remediasi (b) · CLOSED

- Jawaban hunter: (d) 7-subtests **DICABUT** (konvensi `t.Run` = subtest —
  klaim spec akurat); (b) dipersempit (spec tidak menuntut seam di handler;
  seam berhenti di batas quizengine; pembedaan presentasi vs penentu-hasil
  justru memperkuat sisa: `student.go:1091` wall-clock-dalam-tx, `:970-972`
  komputasi `ends_at`, `:1675-1676` gerbang collapse, `:1410`,
  `global.go:956`, `live.go:140,143` adalah penentu-hasil yang hanya teruji
  via sleep-based integration); (a)+(e) DIBUKTIKAN: `t.Parallel` nihil + tanpa
  CI paralel + kontradiksi internal ("parallel-safe" vs "sequentially");
  green-vakum DIEKSEKUSI: `tests/integration` tanpa DB → **119 SKIP / 6 PASS /
  0 FAIL, exit ok** via `testutil.go:62-77` (9 berkas memakai gerbang);
  (c) atribusi unit tetap salah (`authlogic_test.go:1-14` vs tabel §9).
- Penilaian C: dua konsesi (d, sebagian b) TEPAT dan saya adopsi — (d) saya
  nilai quibble sejak Round 1. Sisa inti ((a)/(c)/(e) + sebagian (b))
  justru MENGUAT oleh bukti eksekusi. Vonis overclaim metodologi bertahan
  dengan presisi lebih baik.
- Verdict final: **PARTIALLY CONFIRMED** (sub-butir (d) = **(c) non-issue**,
  dicabut; sisanya terkonfirmasi). Remediasi final: **(b)** amendemen
  (§9: hapus/turunkan "parallel-safe" atau buktikan; perbaiki atribusi tabel;
  nyatakan seam-hanya-quizengine + risiko wall-clock handler; §11.14: nyatakan
  prasyarat DB/CI untuk "green"). Status: **CLOSED**.

## N-008 — Round 2: CONFIRMED (final) · remediasi (b) · CLOSED

- Jawaban hunter: CONFIRMED + semantik per efek dirujuk baris:
  login/`MsgLoginInactive`-hanya-setelah-kredensial-cocok (`auth.go:37-39,89-115`),
  sesi-mati-seketika (`session.go:77-93`), pencabutan proaktif
  (`teacher_students.go:228-260` + komentar `:17-23`), riwayat tak difilter
  (`history.go`/`results.go` grep `aktif` nihil — data tetap, akses hilang),
  reaktivasi tanpa sesi (login ulang manual, tanpa DELETE),
  + usulan redaksi §5 + baris §8.
- Penilaian C: pertanyaan TERJAWAB lengkap — amendemen (b) kini presisi
  tanpa sisa ambiguitas. Sub-butir (b)-timer_type tetap kontras positif
  (bukan kesalahan), adopsi penuh. Tidak ada keberatan tersisa.
- Verdict final: **CONFIRMED**. Remediasi final: **(b)** amendemen
  (kolom `aktif` + semantik deactivation + keputusan ask-first §10).
  Status: **CLOSED**.

## N-009 — Round 2: CONFIRMED, dipersempit (final) · remediasi (a)+(b) · CLOSED

- Jawaban hunter: partially concede — `status` (serapan baku) dan render
  `Dasbor` (patuh; Inggris hanya di komentar/dokumen → dipisah sebagai isu
  istilah/terminology-control) diakui BUKAN pelanggaran; vonis bertahan pada
  inti tak terbantahkan: ENUM-mentah `pending`/`registered`/`started`
  (`history.html:37-41,83-87` via `history.go:61,80,85`; `monitor.html:52-53`
  + `monitor.js:165` + logika `:108`; nilai dari `global.go:78,143,169,220`)
  + `{{.TimerType}} timer` (`monitor.html:8`; kontras positif
  `quiz_list.html:32` + `settings_fields.html:14-16` yang MEMETAKAN —
  membuktikan pemetaan itu mudah); ketiadaan kamus di `web/js`/`web/css`
  + tanpa test bahasa; kamus EN→ID + lokasi kunci test diusulkan; keterbatasan
  sitasi KBBI dinyatakan jujur (klaim sempit tak butuh sitasi).
- Penilaian C: pemisahan kategoris TEPAT (isu istilah ≠ pelanggaran bahasa)
  dan memperkuat — bukan melemahkan — sisa vonis. Klaim absolut §1/§11.21/§7
  ("All/full/never") gugur oleh counterexample rantai-penuh. Severity MEDIUM
  bertahan (LOW teknis, MEDIUM defensibilitas — penguji cukup buka `/history`).
- Verdict final: **CONFIRMED** (ruang lingkup: ENUM-mentah + TimerType-timer;
  `status`/`Dasbor`-render dieksonerasi; Dashboard-dokumen = isu istilah).
  Remediasi final: **(a)+(b)** (kamus terpusat + test pengunci + satukan istilah
  + ukur klaim). Status: **CLOSED**.

## N-010 — Round 2: CONFIRMED (final; (a) CONFIRMED · (b)(c) PARTIALLY) · remediasi (a)+(b) · CLOSED

- Jawaban hunter: (a) DIPERTAHANKAN PENUH, tak disengketakan validator mana pun,
  dikunci test sebagai Kontrak Teruji (`auth.go:104-115` skip-`CompareHash` bila
  `must`; `session.go:130-139` hanya redirect; `auth_test.go:548-551` login
  `totally-wrong` → `302 /change-password` + cookie sesi); (b) DIPERSEMPIT jujur —
  freeze/rehydrate DIAKUI teruji dan kuat (`flow_global_test.go:452-515`,
  `flow_persoal_test.go:768-835`, assert `frozen.After(hangupAt.Add(200ms))`;
  `timer_test.go:45-86`; `realtime_test.go:412+` hub-baru-dari-DB; limitasi
  diakui: simulasi level-hub bukan restart-kontainer-penuh); gap presisi =
  **§11.17 no-broadcast + failure-injection generik** (grep ekshaustif nihil;
  `Hub.Publish` `global.go:345,569,731-732,914-915,1017,1188-1189`,
  `streams.go:104`, `student.go:155,1184` tanpa pasangan uji-negatif; kontras
  publish-positif `realtime_test.go:277-330`); (c) redaksi disepakati
  **misleading-by-omission, bukan fabrikasi** (tanpa tuduhan niat; konsekuensi
  sama: buka §12).
- Penilaian C: (1) matriks berkas→klaim TERSEDIA dan saya adopsi —
  koreksi (b) Hunter B valid dan memperkuat keseluruhan (sisa presisi tak
  terbantahkan; argumen test-quality-bar §9 "fails when the code is wrong"
  tepat: urutan kode benar hari ini ≠ invarian terkunci). (2) koreksi logika
  (c) saya adopsi penuh — vonis saya Round 1 sudah berredaksi demikian.
  Uji konvergensi N-002↔N-010(a) FINAL: **dua klaim berbeda, sama-sama valid,
  saling menguatkan, satu tidak menyerap yang lain** (sesi-aktif-basi vs
  sesi-baru-tanpa-verifikasi; mekanisme/lokasi/jendela berbeda) — relasi Main
  Gate saya sahkan permanen. Severity HIGH bertahan, ditopang penuh oleh (a).
- Verdict final: **CONFIRMED** ((a) CONFIRMED · (b) PARTIALLY · (c) PARTIALLY
  misleading-by-omission). Remediasi final: **(a)+(b)** (amankan §11.2 menjadi
  sifat keamanan benar + implementasi; uji negatif §11.17 + failure-injection
  atau lunakkan klaim; buka §12). Status: **CLOSED**.

## Tabel remediasi final

| ID | Verdict final | Remediasi final | Severity final | Status |
|----|---------------|-----------------|----------------|--------|
| N-001 | CONFIRMED | (b) amendemen §6.6/§8 (pisahkan guard per jalur) | MEDIUM | CLOSED |
| N-002 | CONFIRMED | (a) code-fix (invalidasi mirror saat approve) | MEDIUM (diturunkan dari HIGH, disetujui) | CLOSED |
| N-003 | PARTIALLY CONFIRMED | (a) hardening lock/I-O (engineering note) | LOW-leaning MEDIUM (fakta kuat, link-klaim ditarik) | CLOSED |
| N-004 | PARTIALLY CONFIRMED | (a) code-fix (guard segar dalam-tx) | LOW (diturunkan, disetujui) | CLOSED |
| N-005 | PARTIALLY CONFIRMED | (b) kualifikasi single-flight §6.9/§8 | LOW | CLOSED |
| N-006 | CONFIRMED (dipersempit: (a)-redaksional, (b)+(c) material) | (b) amendemen §2/§4/§10/§11.21 + tidy + daftarkan Chart.js | HIGH | CLOSED |
| N-007 | PARTIALLY CONFIRMED ((d) non-issue dicabut) | (b) amendemen §9/§11.14 | MEDIUM | CLOSED |
| N-008 | CONFIRMED | (b) amendemen §5 + semantik deactivation | MEDIUM | CLOSED |
| N-009 | CONFIRMED (dipersempit: ENUM-mentah + TimerType) | (a)+(b) kamus + test kunci + satukan istilah | MEDIUM | CLOSED |
| N-010 | CONFIRMED ((a) penuh · (b)(c) partially) | (a)+(b) amankan §11.2 + uji negatif + buka §12 | HIGH | CLOSED |

Klasifikasi (c) murni: N-007(d) 7-subtests (dicabut — klaim spec akurat).
Isu yang dipisah kategori (bukan ditolak relevansinya): N-006(a) sebagai
kesalahan redaksi manifest (bukan bukti-tak-dipakai); N-008(b)-timer_type
sebagai kontras positif; N-009 `status`/`Dasbor`-render (patuh) +
Dashboard-dokumen (isu istilah); N-010(b)-freeze/rehydrate (teruji kuat).
Tidak ada perubahan kode produksi.

## Jawaban pertahanan thesis (ringkas — dapatkah penulis bertahan?)

Penulis BELUM dapat mempertahankan dokumen sebagaimana dirumuskan. Yang harus
ditunjukkan sebelum sidang: (1) §6.6/§8 guard per jalur; (2) invalidasi mirror
saat approve + rumusan ulang §11.2 menjadi sifat aman; (3) `tidy` + nasib gocsv
+ daftarkan Chart.js (§2/§4/§10/§11.21); (4) turunkan klaim §9/§11.14 +
uji-negatif §11.17; (5) sinkronkan `users.aktif` + semantik (§5/§8);
(6) kamus ENUM + satukan istilah (§1/§7/§11.21); (7) kualifikasi collapse
single-flight (§6.9); (8) buka §12. N-003/N-004 bersifat hardening/race
berjendela — dicatat tanpa menahan kelulusan bila yang lain ditutup.

```
STATUS: SUBMITTED
Verdicts: <N-001=CONFIRMED; N-002=CONFIRMED; N-003=PARTIALLY CONFIRMED; N-004=PARTIALLY CONFIRMED; N-005=PARTIALLY CONFIRMED; N-006=CONFIRMED; N-007=PARTIALLY CONFIRMED; N-008=CONFIRMED; N-009=CONFIRMED; N-010=CONFIRMED>
Open Questions: <0>
Files Changed: <docs/bug-hunt/examination/validator-c-report.md>
Ready for Final Verdict: <YES>
```
