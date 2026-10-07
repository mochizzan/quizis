# Validator A — Technical Correctness & Evidence Sufficiency (Round 1)

> Peran: hostile-but-fair examiner. Default: "klaim belum terbukti".
> Indeks: `list_projects` → `D-Project-Mother-quiz` terdaftar (branch `main`);
> `index_status` → `ready`, 5206 nodes / 19442 edges, indexed 2026-10-07T08:27:10Z;
> `index_repository` TIDAK dijalankan. Verifikasi independen via `read` rentang persis + `grep`.
> Dokumen dibaca utuh: `normalized.md`, `hunter-a-report.md`, `hunter-b-report.md`,
> spec §2, §5–§12 (kutipan persis di bawah).

## N-001 (F-A-001) — Guard closeQuiz longgar vs spec §6.6

- Finding: `closeQuiz` memakai `WHERE id=? AND status IN ('aktif','berjalan')`
  (`internal/handlers/global.go:887-889`); `Stop` (`global.go:746-781`) tanpa pra-cek status;
  spec §6.6: "All three funnel through `UPDATE ... SET status='selesai' WHERE status='berjalan'`".
- Challenge: Saya coba menggugurkan dengan tiga jalur. (1) Desain-sengaja:
  `closeWithModal` (`personal.go:56-66`, komentar "moves an active quiz to selesai")
  MEMBUTUHKAN `aktif→selesai` untuk jalur per-question (diagram §6.5:
  `aktif ──close──▶ selesai`), jadi `IN (...)` bukan salah ketik melainkan unifikasi
  dua jalur dalam satu rutin. (2) Watchdog timeout (`global.go:955-956` + query
  `WHERE status='berjalan'`) sudah memfilter, jadi `IN` tak berpengaruh di sana.
  (3) Mungkin UI menyembunyikan tombol STOP saat `aktif`. Ketiganya GAGAL menggugurkan:
  (1) justru membuktikan spec §6.6 salah untuk jalur global — kalimat "single transition
  `WHERE status='berjalan'`" tidak mengakui pemisahan guard per jalur yang kodenya sendiri
  perlukan; (2) `Stop` adalah rute terdaftar (`cmd/server/main.go:210`
  `teacher.POST("/quiz/:id/stop", globals.Stop)`) yang dapat dipanggil langsung pada kuis
  `aktif` (resolve id via `quizByID`, cek `confirm` saja, lalu `closeQuiz`);
  `msgNotRunning` hanya saat `closed==false`. Artinya STOP-sebelum-START sukses menutup
  (registered/pending difinalkan NULL per `global.go:880-886`). Ini inkonsistensi
  spec↔code yang decisive, bukan korelasi. Severity MEDIUM proporsional
  (integritas hasil + klaim state-machine melemah, bukan RCE).
- Evidence Requested: —
- Validator Verdict: CONFIRMED
- Remaining Objection: —
- Status: RESOLVED
- Round: 1

## N-002 (F-A-002) — Approve-reset tanpa invalidasi mirror sesi

- Finding: `ApproveReset` (`password_resets.go:74-110`) hanya
  `UPDATE password_resets ... WHERE status='pending'` + `UPDATE users SET must_change_pw=1`,
  tanpa `Store`/`Revoke`/`SessionKey`/`cache`; `LoadSession` (`session.go:58-97`)
  menyajikan snapshot `MustChangePW` dari mirror TTL 5 mnt (`cache/keys.go:11`
  `TTLSession = 5 * time.Minute`); enforcement `ForceChangePassword` (`session.go:130-139`)
  membaca snapshot.
- Challenge: Saya uji empat guguran. (1) Mungkin TTL-staleness adalah semantik yang diterima
  ("cache tidak pernah lebih baru dari DB" = boleh basi ≤TTL)? GAGAL: spec §6.2/§6.4
  eksplisit — tabel mirror: "Sessions | 5 min | explicit delete on logout / password change
  (instant revocation)"; teks: "Mutations (login/logout/password change): write DB first,
  then explicitly delete cache entry → revocation is instant; TTL is only a safety net."
  Approve adalah mutasi keamanan sekelas password-change (menaikkan `must_change_pw`),
  dan pola pembanding ada: `ChangePassword` (`auth.go:409`) + penonaktifan
  (`teacher_students.go:257,306`) memanggil `RevokeUserSessions`, approve tidak.
  (2) Mungkin ada jalur invalidasi lain (trigger/background)? GAGAL:
  `grep RevokeUserSessions|SessionKey\(` → prod hanya `auth.go:409`,
  `teacher_students.go:257,306`, `session.go:58,97,175-187`; `password_resets.go` nihil.
  (3) Hit-cache tanpa revalidasi? TERBUKTI: `session.go:58-63` kembalikan snapshot,
  baca DB + `store.Set` hanya saat miss (`:75-97`). (4) Kuantifikasi "hingga 5 menit"?
  Tepat dibatasi sisa TTL per sesi aktif — tidak overclaim. Ketiadaan invalidasi harfiah +
  snapshot+TTL harfiah = bukti decisive.
- Evidence Requested: —
- Validator Verdict: CONFIRMED
- Remaining Objection: —
- Status: RESOLVED
- Round: 1

## N-003 (F-A-003) — SweepDisconnects menahan mutex melintasi I/O DB

- Finding: `SweepDisconnects` (`live.go:186-209`) `l.mu.Lock(); defer Unlock()` melingkupi
  loop berisi `QueryRowContext` + `ExecContext` per pid; dipanggil tiap detik
  (`global.go:955-956`).
- Challenge: Fakta lock-melintasi-I/O TERBUKTI harfiah, termasuk komentar pengaku
  ("map lock dipegang melintasi query agar Connect konkuren tidak interleave").
  Namun klaim dampak ("memblokir seluruh registry", "watchdog menjadi penghambat global")
  OVERSTATED dan ada penjelasan alternatif yang bertahan. (1) Steady-state murah:
  threshold default 15 dtk (`global.go:969-973`); tiap tick hanya scan map + `continue`
  kecuali beat yang `!connected && !frozen && silent>threshold` — N query hanya saat
  massa-disconnect, bukan "N round-trip setiap detik". (2) `Connect` (`live.go:131-155`)
  sudah direfaktor: `l.mu.Unlock()` SEBELUM `ExecContext` resume-clock — jadi Connect
  hanya terblokir untuk bagian map, bukan I/O. (3) `Publish` tidak butuh lock (klaim
  "publish SSE ikut tertunda" salah arah — yang tertunda hanya `Ranker()`/`Connected`/
  `Disconnect` yang butuh `l.mu`, dan `Upsert/Snapshot` ranker punya lock sendiri).
  (4) Niat atomicity (freeze-vs-resume) legitimate — perbaikan baku (snapshot-di-bawah-lock,
  I/O-di-luar, commit-dengan-guard) belum diterapkan, tetapi ini trade-off
  correctness-vs-liveness, bukan kelalaian murni. Tanpa pengukuran latensi/kontensi,
  MEDIUM untuk "availability" belum earned — sebagai code-smell konkurensi ia valid,
  sebagai klaim penghambat global ia belum dibuktikan.
- Evidence Requested: (i) Tunjukkan `Ranker()` (`live.go:118-130`) menahan/melepas `l.mu`
  seberapa lama dan apakah `Upsert/Snapshot` ranker butuh `Live.mu` atau lock sendiri;
  (ii) buktikan skenario kongesti nyata (jumlah beat disconnect + latensi DB) dengan angka
  — atau turunkan klaim dampak ke "critical section melintasi I/O, magnitude belum diukur";
  (iii) jelaskan mengapa `Connect` yang sudah split-lock/unlock tidak cukup sebagai pola
  untuk sweep yang sama.
- Validator Verdict: PARTIALLY CONFIRMED
- Remaining Objection: Besaran dampak availability belum dibuktikan; alternatif
  "disengaja demi atomicity freeze-vs-resume" belum dibantah dengan pengukuran.
- Status: OPEN
- Round: 1

## N-004 (F-A-004) — Gerbang join memakai snapshot basi (TOCTOU)

- Finding: `Join` (`student.go:409-433`) `quizByCode` (read-committed tanpa lock,
  `student.go:211-214` → `loadQuiz`) lalu `joinQuiz(ctx, quiz, ...)`; di dalam tx
  (`student.go:236-280`) cabang no-row memanggil `quizGates(quiz)` atas objek luar-tx,
  tanpa `SELECT quizzes ... FOR UPDATE`/re-read; `FOR UPDATE` yang ada hanya di
  `latestParticipant` (`student.go:160-165`, mengunci baris participants).
- Challenge: Pola check-then-act lintas-batas-tx TERBUKTI (tak ada `SELECT status FROM
  quizzes ... FOR UPDATE` di `joinQuiz`; `grep FOR UPDATE` di `student.go` hanya
  participants; pembanding `Start` memakai `UPDATE ... WHERE status='aktif'` di dalam tx,
  `teacher_quiz.go:868-869,1075-1076,1124-1125` menunjukkan pola kunci-baris-kuis yang benar).
  Jendela race nyata (antara `quizByCode` dan `INSERT` via `createAttempt`,
  `student.go:320-331` tanpa guard status). Namun realisasi konkuren (START membalik
  `aktif→berjalan` tepat di jendela) BELUM dieksekusi — hunter sendiri "belum dieksekusi".
  Untuk klaim race, hierarki bukti menuntut interleaving démontré, bukan hanya
  keterbacaan pola. Alternatif "duplicate-key retry menutupnya" (`student.go:270-273`)
  hanya untuk sesama join, bukan untuk START. Jadi mekanisme confirmed, eksploitasi
  unproven. MEDIUM tepat JIKA interleaving ditunjukkan; tanpanya LOW/parsial.
- Evidence Requested: (i) Reproduksi interleaving (tahan `createAttempt`/perlambat
  Commit sambil tembak `POST /teacher/quiz/:id/start` konkuren pada kuis `aktif`,
  lalu `SELECT status FROM participants` tunjukkan baris `registered` yatim pada kuis
  `berjalan`); atau (ii) akui sebagai static-TOCTOU tanpa demo dan terima penurunan
  ke LOW/best-effort-guard.
- Validator Verdict: PARTIALLY CONFIRMED
- Remaining Objection: Bukti pola decisive, bukti realisasi race belum ada.
- Status: OPEN
- Round: 1

## N-005 (F-A-005) — Collapse anti-cheat tembus saat konkuren

- Finding: `ReportVisibility` (`student.go:1651-1700`): baca last-event di luar tx
  (`SELECT created_at, kind ... LIMIT 1`), `ShouldRecord` murni
  (`anticheat.go:14-22`, tanpa lock), lalu `BeginTx→INSERT→COUNT→Commit`.
- Challenge: Pola check-then-insert tanpa row-lock TERBUKTI; dua POST sejenis bersamaan
  secara teori sama-sama lolos. Tetapi: (1) Spec §6.9/§8 merumuskan "Flood → 10 s collapse
  per kind" — tak ada janji atomicity/serialisasi; hunter sendiri membuka pertanyaan
  "best-effort vs guarantee". Tanpa janji kedap-concurrency, ini kualifikasi spec,
  bukan bug. (2) Tak ada demo konkuren (dua `POST /quiz/:code/visibility {"kind":"blur"}`
  bersamaan → dua baris <10 dtk) — klaim "dapat ditembus" belum direproduksi.
  (3) Dampak LOW sudah proporsional (badge `count` absolut terinflasi, bukan skor).
  Fakta pola vs vonis "guard tembus" perlu dipisah.
- Evidence Requested: (i) Demo konkuren dua submit sejenis <10 dtk menghasilkan dua baris
  (`SELECT COUNT(*), created_at`); atau (ii) kutip kalimat spec yang menjanjikan collapse
  atomik (bukan sekadar flood-control) — bila tak ada, rumuskan ulang sebagai
  "collapse hanya single-flight; perlu kualifikasi best-effort di §6.9".
- Validator Verdict: PARTIALLY CONFIRMED
- Remaining Objection: Interpretasi guarantee-vs-best-effort belum diputus; bukti
  konkuren belum ada.
- Status: OPEN
- Round: 1

## N-006 (F-B-001) — Klaim closed-8-direct gugur

- Finding: (a) `go.mod:5-9` hanya 3 entri direct, 5 berlabel `// indirect`;
  (b) `gocsv` nol import `.go`, export CSV memakai `encoding/csv` (`export.go:3-16`);
  (c) Chart.js v4.5.1 bervendor (`chart.umd.min.js:1-6`) + dimuat
  (`dashboard.html:72-84,130`) tanpa diakui spec.
- Challenge: Tiga sub-bukti tak sama kuatnya. (a) LEMAH secara semantik Go:
  `// indirect` adalah komentar `go mod tidy` ("tidak diimpor langsung oleh paket main"),
  BUKAN definisi operasional "direct". Kode justru MENGIMPOR 4 dari 5 yang dilabel
  indirect: `bcrypt` (`auth.go:16`), `excelize` (`export_sheets.go:6`),
  `go-qrcode` (`teacher_quiz.go:16`), `go-sse` (`realtime/hub.go:16`) — jadi manifest
  *salah label* (stale `go mod tidy`), bukan spec salah menghitung kebutuhan.
  `AGENTS.md:86` sendiri mengoreksi: "Direct deps: echo/v5, go-sql-driver/mysql, cleanenv."
  Frasa spec "exactly 8 direct modules" memang salah terhadap teks manifest, tetapi
  vonis "hanya 3 direct" memakai definisi komentar-manifest yang juga bukan
  kebenaran impor. (b) DECISIVE: `grep gocsv` → hanya `go.mod/go.sum/plan/docs`,
  nol import `.go`; `export.go` memakai `encoding/csv` stdlib (tag `csv:"..."` tanpa
  library tak membuktikan pemakaian); tabel §2 "CSV export — gocsv" + §6.11
  "gocsv for CSV" gugur. (c) KUAT-TAPI-RUANG-LINGKUP: Chart.js dieksekusi di dashboard
  (3 canvas + `<script src="...chart.umd.min.js">`), spec §2/§4/§10 nihil
  (`grep chart|Chart|vendor` hanya struktur `bootstrap-icons/theme.css` + rute overview
  generik); namun daftar "closed 8-module" secara harfiah adalah modul Go —
  apakah JS bervendor termasuk "dependency" §10/§11.21? Bootstrap/icons pun bervendor
  tanpa dihitung modul. Tanpa putusan ruang-lingkup, (c) = "dependensi runtime tak
  diakui" (benar) vs "pelanggaran closed-list Go" (debatable). HIGH untuk keseluruhan
  overstates — inti yang decisive adalah (b)+(c-dokumentasi).
- Evidence Requested: (i) `go list -m all` / `go mod why` per 8 modul untuk memisahkan
  "diimpor" vs "komentar indirect" (akui 4 terimpor + gocsv yatim); (ii) kutip kalimat
  spec yang memasukkan/mengeluarkan aset JS bervendor dari "closed list" (§2/§10/§11.21)
  — bila tak ada, turunkan (c) ke "undocumented runtime dependency" bukan "modul ke-9";
  (iii) tunjukkan `web/js/teacher_dashboard.js` benar memakai global `Chart`.
- Validator Verdict: PARTIALLY CONFIRMED
- Remaining Objection: Sub-klaim (a) salah memaknai semantik `// indirect`; (c) butuh
  putusan ruang-lingkup JS-vs-Go-module.
- Status: OPEN
- Round: 1

## N-007 (F-B-002) — Metodologi testing §9 overclaim

- Finding: (a) "parallel-safe" tanpa satu `t.Parallel` (`grep` nihil di `tests/`,
  `internal/`); (b) "no time.Now in logic" hanya di quizengine, handler memanggil
  langsung (`auth.go:118,148`, `global.go:243,265,956`, `live.go:140,143`,
  `student.go:846,868,970,1091,1198,1410,1675,1773` vs `timer.go:1-11`);
  (c) tabel §9 mengatribusikan ke unit apa yang header `authlogic_test.go:8-14`
  delegasikan ke integrasi; (d) "fully green" kondisional via `Skipf` (`testutil.go:62-77`).
- Challenge: Bundel tak seragam — satu leg GUGUR, sisanya terbelah. (a) SEBAGIAN:
  nihil `t.Parallel` TERBUKTI; tetapi "parallel-safe" = properti (aman diparalelkan),
  bukan "diparalelkan" — ketiadaan `t.Parallel` membuktikan *untested*, bukan *false*.
  Ditambah spec kontradiktif internal ("parallel-safe" + "run sequentially",
  spec §9:635,677) — vonis tepatnya UNPROVEN, bukan falsified. (b) SENGKETA-RUANG-LINGKUP:
  spec Levels (§9:635) "tests/unit/ — pure logic, no DB/HTTP; injectable fake clock"
  + Determinism (§9:677) "inject clock (no time.Now in logic)" — "logic" paling wajar
  dibaca sebagai unit/quizengine (yang memang punya `Clock` + komentar "never calls
  time.Now()"), bukan seluruh handler yang wajar memakai wall-clock untuk
  `server_now`/`ends_at`/GC. Tanpa kutip yang memaksa "semua lapisan", ini
  over-reading. (c) CONFIRMED: tabel §9:650 menaruh forgot-password/join-retry di
  `authlogic_test.go`, header berkas eksplisit mendelegasikan ke
  `integration/auth_test.go` + `quiz_crud_test.go` — misatribusi harfiah.
  (d-7-subtests) DITOLAK: `assets_test.go:83-125` memuat 7 entri tabel dieksekusi via
  `t.Run` — itu 7 subtests menurut konvensi Go; klaim spec "header matrix (7 subtests)"
  AKURAT. (e-green) SEBAGIAN: `Skipf` saat DB mati TERBUKTI; `go test` tetap exit-0
  dengan SKIP — "green" vakum tanpa DB benar, tetapi dengan DB (CI/compose) green itu
  riil; "nothing disabled" (§11.14:733) vs Skip massal = misleading, bukan pemalsuan.
  MEDIUM untuk keseluruhan bundel overstates — (c) ringan, sisanya kualifikasi.
- Evidence Requested: (i) Tarik leg "7 subtests" (akui `t.Run` ×7 = 7 subtests) atau
  definisikan "subtest" yang dipakai; (ii) kutip kalimat spec yang memaksa "no time.Now"
  ke handler (bila tak ada, persempit ke "seam jam berhenti di batas quizengine");
  (iii) tunjukkan keluaran `go test ./...` tanpa DB (deretan SKIP + exit 0) sebagai bukti
  "green vakum", dan nyatakan prasyarat DB CI.
- Validator Verdict: PARTIALLY CONFIRMED
- Remaining Objection: Satu leg gugur (7-subtests akurat), dua leg perlu penyempitan
  ruang-lingkup (parallel = untested-bukan-false; time.Now = batas-quizengine).
- Status: OPEN
- Round: 1

## N-008 (F-B-003) — Drift living spec: users.aktif + riwayat timer_type

- Finding: (a) `users.aktif` (`0003_users_aktif.sql:1-8`) + enforcement dua lapis
  (`auth.go:89-115` tolak login nonaktif; `session.go:77-93` matikan sesi berjalan)
  tak ada di blok `users` §5 (`spec:129-134`: ... `must_change_pw` langsung ke
  `created_at`); (b) `timer_type` lahir 2-nilai (`0001_init.sql:47`
  `ENUM('global','per_soal')`), ketiga di `0004` (`tanpa_timer`), §5 tampil final 3-nilai.
- Challenge: Saya uji guguran "terdokumentasi di tempat lain" dan "living = final-state".
  (1) `grep aktif` pada spec → hanya status kuis/TTL/state-machine §6.5, nihil kolom
  `users.aktif`; `grep must_change_pw|Nama lengkap` → blok §5:129-134 tanpa `aktif`;
  `0001_init.sql:20-32` (CREATE users) juga tanpa `aktif` — jadi kolom benar-benar
  lahir di 0003 dan hidup di kode, mati di dokumen. Klaim "fitur user-visible tanpa jejak
  thesis" (deactivation + pesan khusus + pencabutan sesi) bertahan. (2) Untuk timer_type,
  "living spec" = sinkron dengan keadaan akhir (§10: "keep schema in sync with §5") —
  §5 menampilkan 3 nilai = BENAR sebagai living; hunter pun mengakui "dapat dimaafkan"
  dan memakainya sebagai kontras positif selektivitas sinkronisasi. Jadi (b) bukan defect,
  melainkan konteks yang memperkuat (a): sinkronisasi mampu (timer_type) tetapi lalai
  (aktif). (3) Komentar migrasi 0003 eksplisit ("aktif=0 blocks sign-in and any live
  session") — niat didokumentasikan di SQL, bukan di thesis. Vonis drift untuk (a)
  decisive; MEDIUM proporsional (traceability, bukan keamanan).
- Evidence Requested: —
- Validator Verdict: CONFIRMED
- Remaining Objection: —
- Status: RESOLVED
- Round: 1

## N-009 (F-B-004) — Klaim KBBI gugur

- Finding: ENUM Inggris dirender mentah (`history.html:37-41,83-87`
  `{{else}}{{.Status}}`; `monitor.html:52-53` `{{.Status}}`; `monitor.html:7-10`
  `{{.TimerType}} timer` + label `status`); dokumen+komentar "Dashboard" vs render "Dasbor"
  (`breadcrumb.go:11,35,39,61,64,128,132`, `nav_no_root_links_test.go:14,56-60`).
- Challenge: Inti vs perifer perlu dipisah. INTI CONFIRMED: rantai DB→handler→template
  lengkap — `history.go:60-88` memindai `p.status` ke `r.Status` tanpa kamus bahasa,
  cabang `{{else}}{{.Status}}` mencetak `pending/registered/started/...` mentah ke murid;
  kartu mobile duplikat; monitor mencetak `{{.Status}}` + `{{.TimerType}}` mentah.
  Kalimat §7 "Schema column/ENUM literals ... never user-facing" GUGUR oleh
  counterexample (satu saja cukup untuk klaim universal "All/never", di sini banyak).
  Namun PERIFER OVERSTATED: (i) "Dashboard vs Dasbor" — render memakai "Dasbor"
  (benar), spec/komentar memakai "Dashboard" (Inggris di dokumen, bukan di UI) —
  ini inkonsistensi istilah dokumen↔kode, BUKAN pelanggaran "All UI text" (UI-nya
  justru patuh). (ii) Label "status" — kata `status` ADA di KBBI (serapan baku),
  bukan bukti pelanggaran; "timer" (Inggris, KBBI: pewaktu/pengatur waktu) yang
  dipermasalahkan perlu rujukan kamus, tak disediakan hunter. Klaim universal gugur
  oleh inti, tetapi 4-counterexample hunter menyusut ke 2 leg inti + 2 leg lemah.
- Evidence Requested: (i) Rujukan KBBI untuk tiap kata yang dipermasalahkan
  (`status` vs `timer` vs `pending/registered/started`) — akui `status`/`Dasbor` bukan
  pelanggaran; (ii) persempit vonis ke leg ENUM-mentah (DB→handler→template) +
  `{{.TimerType}} timer`, pisahkan leg dokumen-vs-render sebagai inkonsistensi istilah,
  bukan bukti KBBI; (iii) alternatif: tunjukkan pemetaan bahasa di JS/CSS bila ada
  (bukti negatif hunter "tanpa kamus" perlu dikonfirmasi ekshaustif untuk seluruh
  render `Status`/`TimerType`).
- Validator Verdict: PARTIALLY CONFIRMED
- Remaining Objection: Inti ENUM-mentah decisive; leg Dasbor/Dashboard + "status"
  bukan bukti KBBI dan harus dikeluarkan dari hitungan counterexample.
- Status: OPEN
- Round: 1

## N-010 (F-B-005) — §11.2 skip-validasi + robustness tanpa uji negatif + §12 None

- Finding: (a) `if !must && bcrypt.Compare...` (`auth.go:104-115`) → `must_change_pw=1`
  peroleh sesi tanpa bukti kata sandi (`startSession`), middleware (`session.go:130-139`)
  hanya redirect; (b) robustness (§11.15-17) tanpa uji negatif pasangan
  (`realtime_test.go:277-484` positif saja); (c) §12 "None" dibantah N-006…N-009.
- Challenge: (a) DECISIVE dan tak terbantahkan — rantai linier: `SELECT ... must_change_pw`
  → `if !must && Compare != nil` (must==true = LEWATI seluruh verifikasi) → cek `aktif`
  → `target=/change-password` → `startSession` (sesi + cookie + redirect). Siapa pun tahu
  username akun must=1 dapat masuk lalu menguasai akun via `/change-password`
  (password baru tanpa kredensial lama). Argumen "teacher-approval sebagai verifikasi
  out-of-band" (desain recovery tanpa email) menjelaskan NIAT tetapi tidak menghapus
  jendela takeover bermodalkan username — ini persyaratan tak-aman by-design,
  HIGH tepat sebagai cacat kriteria, bukan bug kode semata. (b) CAMPURAN — satu leg
  GUGUR FAKTUAL: klaim "tidak ada test freeze/no-time-lost" SALAH. Ada
  `flow_global_test.go:452-515` (`TestDisconnectFreezesAtLastHeartbeat`: sweep hingga
  freeze, tolak detection-time-freeze, reconnect tanpa-hilang-waktu),
  `flow_persoal_test.go:768-835` (per-question sama), `unit/timer_test.go:45-86`
  (`OnDisconnect` last-heartbeat). Rehydrate pun ada simulasi hub-baru
  (`realtime_test.go` rehydrate). Yang BENAR-BENAR nihil (perlu grep ekshaustif) hanya
  "DB write failure → no broadcast" failure-injection — kode menaati urutan
  commit-dulu-broadcast-kemudian (§6.2) by-inspection, tetapi tanpa uji-gagal.
  Hunter menggabungkan tiga robustness menjadi "tanpa uji negatif" — over-bundling.
  (c) KONSEKUENSI-LOGIS: §12 "None — all 24 resolved" gugur JIKA salah satu N-006…N-009
  bertahan — dengan N-008 CONFIRMED + N-006/N-007/N-009 parsial, (c) bertahan sebagai
  akibat, bukan bukti independen.
- Evidence Requested: (i) Pecah (b) per klaim §11.15/16/17 dengan inventarisasi test
  pasangan (akui freeze/rehydrate SUDAH diuji via berkas di atas, atau tunjukkan mengapa
  test itu tak memadai); (ii) untuk "DB write failure → no broadcast", berikan grep
  ekshaustif (`no broadcast|write failure|failure injection|kill DB`) + daftar
  `tests/integration/` yang dipetakan ke tiap klaim §11.15-17; (iii) nyatakan (c)
  sebagai konsekuensi N-006…N-009 (tak perlu bukti baru).
- Validator Verdict: PARTIALLY CONFIRMED
- Remaining Objection: Leg (a) confirmed-HIGH; leg (b-freeze) refuted oleh test yang ada;
  leg (b-no-broadcast) belum dibuktikan ekshaustif; (c) kondisional.
- Status: OPEN
- Round: 1

## Ringkasan verdict

| ID | Hunter | Verdict | Status |
|----|--------|---------|--------|
| N-001 | A | CONFIRMED | RESOLVED |
| N-002 | A | CONFIRMED | RESOLVED |
| N-003 | A | PARTIALLY CONFIRMED | OPEN |
| N-004 | A | PARTIALLY CONFIRMED | OPEN |
| N-005 | A | PARTIALLY CONFIRMED | OPEN |
| N-006 | B | PARTIALLY CONFIRMED | OPEN |
| N-007 | B | PARTIALLY CONFIRMED | OPEN |
| N-008 | B | CONFIRMED | RESOLVED |
| N-009 | B | PARTIALLY CONFIRMED | OPEN |
| N-010 | B | PARTIALLY CONFIRMED | OPEN |

## Pertanyaan ke Hunter

IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-003

Question: Seberapa besar kontensi riil SweepDisconnects? Tunjukkan durasi tahan-locks vs beban, peran Ranker lock, dan mengapa pola split-lock Connect tak diterapkan ke sweep.

Required evidence: `live.go:118-155,186-209`; `global.go:955-973`; angka/latensi atau penurunan klaim dampak.

Reason: Fakta lock-melintasi-I/O decisive, klaim availability belum earned; ada desain atomicity alternatif.

Priority: MEDIUM

---

IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-004

Question: Demonstrasikan interleaving START-vs-INSERT (tahan createAttempt + START konkuren → baris registered yatim pada kuis berjalan) atau terima kualifikasi static-TOCTOU/LOW.

Required evidence: `student.go:211-214,236-280,320-331`; hasil `SELECT status FROM participants`; kontras `UPDATE ... WHERE status='aktif'` pada Start.

Reason: Pola check-then-act lintas-tx terbukti, realisasi race belum dieksekusi.

Priority: MEDIUM

---

IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-005

Question: Buktikan dua POST visibility sejenis konkuren <10 dtk menghasilkan dua baris, atau kutip janji atomicity spec §6.9 — bila tak ada, rumuskan ulang sebagai kualifikasi best-effort/single-flight.

Required evidence: `student.go:1651-1700`; `anticheat.go:14-22`; `SELECT COUNT(*), created_at` pasca-flood konkuren.

Reason: Pola terbukti, garansi vs best-effort belum diputus, demo konkuren nihil.

Priority: LOW

---

IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-006

Question: Pisahkan "diimpor" vs "komentar // indirect" via `go list -m all`/`go mod why` (akui bcrypt/excelize/qrcode/go-sse terimpor + gocsv yatim), dan putuskan ruang-lingkup JS bervendor terhadap closed-list §10/§11.21; tunjukkan pemakaian global Chart di `teacher_dashboard.js`.

Required evidence: `go.mod:5-29`; grep impor Go; `export.go:3-16`; `chart.umd.min.js:1-6`; `dashboard.html:72-84,130`; kutipan §2/§10/§11.21 soal JS.

Reason: Sub-klaim (a) salah memaknai semantik `// indirect`; (b) decisive; (c) butuh putusan Go-module-vs-aset-JS.

Priority: HIGH

---

IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-007

Question: Tarik leg 7-subtests (akui `t.Run` ×7), persempit "no time.Now" ke batas-quizengine kecuali ada kutip pemaksa-handler, dan buktikan green-vakum tanpa DB (deretan SKIP + exit 0) beserta prasyarat DB CI.

Required evidence: `assets_test.go:83-125`; `authlogic_test.go:1-14` vs tabel §9:650; `testutil.go:62-77`; grep `t.Parallel` + `time.Now()`; kutipan §9:635,677 + §11.14:733.

Reason: Satu leg gugur faktual, dua leg over-reading ruang-lingkup, sisanya kualifikasi.

Priority: MEDIUM

---

IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-009

Question: Sertakan rujukan KBBI per kata (akui `status`/`Dasbor` bukan pelanggaran), persempit ke ENUM-mentah + `TimerType timer`, dan pisahkan inkonsistensi istilah dokumen-vs-render dari vonis KBBI.

Required evidence: `history.html:37-41,83-87`; `monitor.html:7-10,52-53`; `history.go:60-88`; `breadcrumb.go:11-132`; entri KBBI.

Reason: Inti decisive untuk klaim universal, perifer bukan bukti KBBI.

Priority: MEDIUM

---

IRC → Main Agent

REQUEST_TO_HUNTER

Finding: N-010

Question: Pecah klaim robustness per §11.15/16/17 — akui freeze/rehydrate yang SUDAH diuji (`flow_global_test:452-515`, `flow_persoal_test:768-835`, `timer_test:45-86`) atau tunjukkan kekurangannya; untuk no-broadcast berikan grep ekshaustif + pemetaan test.

Required evidence: inventarisasi `tests/integration/` per klaim; grep `no broadcast|write failure|failure injection`; `auth.go:104-115` (leg-a tak disengketakan).

Reason: Leg (a) decisive-HIGH, leg (b-freeze) refuted, leg (b-no-broadcast) belum ekshaustif, (c) kondisional.

Priority: HIGH

## Round 2 — Final Evaluation (2026-10-07)

> Defense dibaca: `docs/bug-hunt/defense/hunter-a-defense-r1.md` (N-001…N-005),
> `docs/bug-hunt/defense/hunter-b-defense-r1.md` (N-006…N-010). §17–§18 diterapkan:
> tidak ada keberatan baru setelah bukti decisive; keberatan Round 1 yang dijawab
> dengan bukti baru ditutup CLOSED.

### N-001 Round 2
- Defense: komentar-vs-kode inkonsisten (`global.go:815-821` klaim `WHERE status='berjalan'`
  vs guard `IN ('aktif','berjalan')` `:887-889`); penyatuan-rutin disengaja
  (`personal.go:16-18,66`); test `finish_lifecycle_test.go:76-232` pin close-dari-`aktif`
  via `/status`, bukan via `POST /stop` global-belum-START. Niat guard-ganda tak diputuskan.
- Evaluation: Memperkuat CONFIRMED — counterexample STOP-sebelum-START tetap berdiri.
  Rekomendasi pisahkan-guard/amendemen §6.6 diterima.
- Final Verdict: CONFIRMED — Status: CLOSED — Round: 2

### N-002 Round 2
- Defense: jendela = sisa TTL ∈ (0,5] mnt (`keys.go:11`, `session.go:97`) — konsisten dengan
  "hingga/maks 5 menit" laporan awal; inventarisasi penulis lengkap (hanya ApproveReset 0→1
  tanpa invalidasi; `auth.go:383+409` aman); netral pada HIGH→MEDIUM.
- Evaluation: Seluruh request terjawab dengan bukti berkas. Vonis tetap CONFIRMED
  (severity bukan bagian verdict final skill).
- Final Verdict: CONFIRMED — Status: CLOSED — Round: 2

### N-003 Round 2
- Defense: koreksi presisi — yang terblokir = akuisisi `Ranker()` + presence/page/ledger,
  BUKAN `Upsert/Snapshot`/`Publish` (`live.go:118-127`, `ranking.go:20-58`); klaim pelanggaran-spec
  non-blocking registry DITARIK (§6.1/§8 = hub/publish); magnitude belum diukur → terima PARTIALLY.
- Evaluation: Tepat seperti yang saya minta (opsi turunkan klaim). Fakta lock-melintasi-I/O decisive,
  dampak tak terkuantifikasi, link-spec ditarik. §17: tutup keberatan.
- Final Verdict: PARTIALLY CONFIRMED — Status: CLOSED — Round: 2

### N-004 Round 2
- Defense: terima static-TOCTOU/LOW/PARTIALLY tanpa demo live; fakta stale-read dipertahankan
  (`student.go:211-214,236-280,160-165` vs `Start` guard-dalam-tx `global.go:615-638`).
- Evaluation: Persis opsi (ii) saya. Fakta struktural decisive untuk PARTIALLY; realisasi unproven.
  §18: tidak ada pengulangan — tutup.
- Final Verdict: PARTIALLY CONFIRMED — Status: CLOSED — Round: 2

### N-005 Round 2
- Defense: pilih kualifikasi dokumental §6.9 (kalimat kategorik "collapses to one row" tanpa
  kualifikasi single-flight); terima PARTIALLY; fakta check-then-act dipertahankan; tanpa klaim demo.
- Evaluation: Persis opsi (ii) saya. Inkonsistensi spec↔kode kondisional-konkuren, remediasi
  satu kalimat — PARTIALLY tepat. Tutup.
- Final Verdict: PARTIALLY CONFIRMED — Status: CLOSED — Round: 2

### N-006 Round 2
- Defense: (a) ditarik sebagai bukti (`go mod why` + `tidy -diff`: 4 modul dipakai, komentar basi);
  (b) gocsv yatim DIPERTAHANKAN PENUH (`why` = tidak dibutuhkan siapa pun; `tidy` ingin menghapus);
  (c) DITURUNKAN menjadi undocumented-runtime-dep (Chart.js v4.5.1:
  `dashboard.html:72-84,130`, `teacher_dashboard.js:247,257,268,290`, tak ada di §2/§4/§10);
  inventarisasi vendor: hanya Chart.js tak-terdaftar.
- Evaluation: Seluruh request terjawab dengan bukti perintah + berkas. Sisa (b)+(c-dokumentasi)
  decisive; (a) dibuang. PARTIALLY CONFIRMED final.
- Final Verdict: PARTIALLY CONFIRMED — Status: CLOSED — Round: 2

### N-007 Round 2
- Defense: (d) 7-subtests DICABUT (akui akurat — `t.Run` ×7 konvensi Go); (b) dipersempit
  (seam berhenti di batas quizengine; penentu-hasil `student.go:1091,970-972,1675-1676,1410`,
  `global.go:956`, `live.go:140,143` hanya teruji via sleep-integration); green-vakum DIBUKTIKAN
  (119 SKIP/6 PASS/0 FAIL exit-0 tanpa DB, `testutil.go:62-77`); paralel tanpa `t.Parallel`/CI;
  atribusi (c) tetap salah.
- Evaluation: Semua request terjawab + bukti eksekusi. Satu leg gugur, satu leg persempit,
  inti overclaim bertahan. PARTIALLY final.
- Final Verdict: PARTIALLY CONFIRMED — Status: CLOSED — Round: 2

### N-008 Round 2
- Defense: semantik deactivation per efek + usulan redaksi §5 (`auth.go:89-115,37-39`,
  `session.go:77-93`, `teacher_students.go:228-260`, riwayat tak-difilter, reaktivasi tanpa sesi).
- Evaluation: Melengkapi amendemen; vonis tak berubah. Konfirmasi tetap CONFIRMED.
- Final Verdict: CONFIRMED — Status: CLOSED — Round: 2

### N-009 Round 2
- Defense: `status`/`Dasbor` diakui BUKAN pelanggaran; isu istilah dipisah; bertahan pada ENUM-mentah
  (`pending/registered/started` via `history.html:40,86` + `history.go:61,80,85` + `monitor.html:53` /
  `monitor.js:165,108`) + `{{.TimerType}} timer` (`monitor.html:8`, kontras `quiz_list.html:32`);
  kamus EN→ID + lokasi kunci test diusulkan; ketiadaan kamus dikonfirmasi via grep.
- Evaluation: Persis yang saya minta. Inti decisive untuk klaim universal; perifer dikeluarkan.
  PARTIALLY final.
- Final Verdict: PARTIALLY CONFIRMED — Status: CLOSED — Round: 2

### N-010 Round 2
- Defense: (a) DIPERTAHANKAN PENUH + dikunci test (`auth.go:104-115`, `auth_test.go:548-551`
  wrong-password → 302 + cookie); (b) DIPERSEMPIT: freeze/rehydrate DIAKUI teruji
  (`flow_global_test.go:452-515` assert `hangup+200ms`, `flow_persoal_test.go:768-835`,
  `timer_test.go:45-86`, `realtime_test.go:412+` rehydrate-hub); gap presisi = §11.17 no-broadcast
  + failure-injection (grep nihil, `Hub.Publish` tanpa pasangan negatif); (c) redaksi
  misleading-by-omission, konsekuensi sama (buka §12).
- Evaluation: Koreksi (b-freeze) yang saya tuntut diakui dengan kutipan assert — keberatan saya
  pada leg itu CLOSED sebagai REFUTED-bagian-dari-bundel. Sisa (a) decisive +
  (b-no-broadcast) gap nyata + (c) omission kondisional = PARTIALLY untuk bundel,
  dengan leg (a) skip-validasi = CONFIRMED penuh di dalamnya. Tidak ada keberatan baru (§17).
- Final Verdict: PARTIALLY CONFIRMED — Status: CLOSED — Round: 2

## Ringkasan verdict final

| ID | Verdict final | Status |
|----|---------------|--------|
| N-001 | CONFIRMED | CLOSED |
| N-002 | CONFIRMED | CLOSED |
| N-003 | PARTIALLY CONFIRMED | CLOSED |
| N-004 | PARTIALLY CONFIRMED | CLOSED |
| N-005 | PARTIALLY CONFIRMED | CLOSED |
| N-006 | PARTIALLY CONFIRMED | CLOSED |
| N-007 | PARTIALLY CONFIRMED | CLOSED |
| N-008 | CONFIRMED | CLOSED |
| N-009 | PARTIALLY CONFIRMED | CLOSED |
| N-010 | PARTIALLY CONFIRMED | CLOSED |

```
STATUS: SUBMITTED
Verdicts: N-001=CONFIRMED; N-002=CONFIRMED; N-003=PARTIALLY CONFIRMED; N-004=PARTIALLY CONFIRMED; N-005=PARTIALLY CONFIRMED; N-006=PARTIALLY CONFIRMED; N-007=PARTIALLY CONFIRMED; N-008=CONFIRMED; N-009=PARTIALLY CONFIRMED; N-010=PARTIALLY CONFIRMED
Open Questions: 0
Files Changed: docs/bug-hunt/examination/validator-a-report.md
Ready for Final Verdict: YES
```