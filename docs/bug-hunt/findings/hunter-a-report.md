# Thesis Bug Defense Report — Quiz Webapp (Hunter A: Implementation / Technical Correctness)

> Template kanonis (skill `thesis-bug-hunter` §4). Struktur stabil.
> Hunter A menulis ke berkas ini saja: `docs/bug-hunt/findings/hunter-a-report.md`.
> Kode produksi tidak diubah (investigasi + artifact saja).

## 1. Investigation Context

- Project: quiz — server-rendered quiz/exam webapp (guru/murid), single Go binary (`module quiz`, Go 1.26.6, Echo v5.3.1, MariaDB 12, `html/template`, SSE via `tmaxmax/go-sse`)
- Thesis: `docs/superpowers/specs/2026-09-26-quiz-website-design.md` — approved spec (skema §5, perilaku §6–§8, testing §9, boundaries §10, success criteria §11)
- Scope: seluruh kode sumber proyek sebagai objek investigasi (Go 104 berkas, HTML 43, JS 13, SQL 6, YAML 1 — per codegraph)
- Repository: `D:/Project/Mother/quiz` (branch `main`)
- Methodology: adversarial review — klaim vs bukti, reproduksibilitas, konsistensi spec↔implementasi; hierarki bukti §20 skill
- Technology: Go 1.26.6, Echo v5.3.1, MariaDB 12, `html/template`, SSE (bukan WebSocket), cache in-process TTL mirror, `html/template` + vanilla JS
- Defense standard: academic — "Can the thesis author scientifically and technically defend the claim?"
- Citation format: `path-relatif:rentang-baris` + kutipan kode + cara verifikasi (`read` / MCP `get_code_snippet`)
- Language: Bahasa Indonesia
- Completion: Open Questions = 0; setiap finding berverdict CONFIRMED / PARTIALLY CONFIRMED / DISPUTED / UNPROVEN / REJECTED

## 2. Executive Summary

Lima temuan implementasi diajukan dari sisi technical correctness, semuanya
diverifikasi terhadap berkas aktual (bukan inferensi tanpa bukti). Pola umum:
jalur tulis inti (DB→commit→invalidate→broadcast, skor atas FULL count,
idempotent finish, guard `WHERE status=...`) rapi dan disiplin; celah ada di
(1) guard status `closeQuiz` yang lebih longgar dari spec, (2) mirror sesi
yang menyimpan snapshot `MustChangePW` tanpa invalidasi saat approve-reset,
(3) `SweepDisconnects` menahan mutex melintasi I/O DB, (4) gerbang join
memakai snapshot status kuis basi di luar transaksi, (5) collapse anti-cheat
10-detik check-then-insert di luar lock. Tidak ada kode produksi diubah.

## 3. Bug Hunter Findings

### 3.1 Finding F-A-001

- Title: Guard `closeQuiz` menerima `aktif` padahal spec §6.6 menetapkan transisi tunggal `WHERE status='berjalan'`
- Severity: MEDIUM
- Category: State-machine guard / spec↔code inconsistency
- Location: `internal/handlers/global.go:887-889` (guard), `internal/handlers/global.go:746-781` (Stop tanpa pra-cek status), `internal/handlers/personal.go:56-66` (closeWithModal memanggil rutin yang sama)
- Claim: Rutin penutup kuis bersama (`closeQuiz`) menutup kuis berstatus `aktif` maupun `berjalan`, sementara spec §6.6 menetapkan perebutan STOP-vs-timeout-vs-all-finished diselesaikan oleh satu transisi `WHERE status='berjalan'`.
- Problem: `Stop` (global) tidak memeriksa status sebelum memanggil `closeQuiz`; `closeQuiz` mengeksekusi `UPDATE quizzes SET status='selesai' WHERE id=? AND status IN ('aktif','berjalan')`. Akibatnya STOP pada kuis global `aktif` yang belum pernah START tetap sukses menutup kuis (peserta registered/pending ikut diselesaikan sebagai "not attempted"). Untuk jalur per-question (`aktif`→`selesai` via `closeWithModal`, spec §6.5) guard longgar ini benar; untuk jalur global ia menyimpang dari spec dan membuat semantik STOP tidak seragam (STOP-sebelum-START seharusnya 409 "tidak sedang berjalan", bukan sukses tutup).
- Evidence: kutipan aktual `internal/handlers/global.go:887-889`:
  ```
  res, err := tx.ExecContext(ctx,
      `UPDATE quizzes SET status = 'selesai'
       WHERE id = ? AND status IN ('aktif', 'berjalan')`, quizID)
  ```
  dan `Stop` (`global.go:746-781`) hanya memvalidasi `confirm`, me-resolve id, lalu `g.closeQuiz(ctx, id, reason)` tanpa memeriksa `aktif` vs `berjalan`; pesan `msgNotRunning = "Kuis ini tidak sedang berjalan."` hanya dipakai saat `closed==false`. Spec §6.6: "All three funnel through `UPDATE ... SET status='selesai' WHERE status='berjalan'` — first commit wins, others no-op."
- Reproduction / Verification: `read internal/handlers/global.go:744-790` (Stop), `read internal/handlers/global.go:815-921` (closeQuiz + guard), `read internal/handlers/personal.go:56-80` (closeWithModal memakai rutin sama); bandingkan literal `IN ('aktif', 'berjalan')` dengan kalimat spec §6.6. Verifikasi perilaku: buat kuis global `aktif` berisi peserta registered, panggil `POST /teacher/quiz/:id/stop {"confirm":true}` → `200 {"status":"selesai"}` (padahal START belum pernah ditekan).
- Expected: STOP pada kuis global yang belum `berjalan` ditolak (409, "tidak sedang berjalan"); guard transisi penutup global adalah `WHERE status='berjalan'` sesuai spec; atau spec diamendemen bila perilaku berbagi-rutin ini disengaja.
- Actual: STOP pada kuis `aktif` sukses menutup kuis karena guard `IN ('aktif','berjalan')`.
- Impact: Hasil/riwayat dapat tertutup tanpa pernah dimulai (registered/pending difinalkan NULL); semantik STOP-vs-timeout race tidak lagi tunggal; klaim skripsi tentang state machine melemah.
- Academic Impact: Klaim "semua end-quiz via satu transisi `WHERE status='berjalan'`" tidak dapat dipertahankan apa adanya — penguji dapat menunjukkan contoh tandingan STOP-sebelum-START yang sukses.
- Confidence: tinggi (literal guard + alur Stop terbaca langsung; satu-satunya inferensi adalah penilaian bahwa berbagi-rutin disengaja untuk jalur per-question, yang justru memperkuat perlunya pemisahan guard per jalur).
- Status: SUBMITTED

### 3.2 Finding F-A-002

- Title: Approve-reset tidak menginvalidasi mirror sesi → snapshot `MustChangePW` basi hingga 5 menit
- Severity: HIGH
- Category: Session/cache invalidation (security-relevant)
- Location: `internal/handlers/password_resets.go:74-110` (ApproveReset tanpa sentuhan cache), `internal/middleware/session.go:75-97` (snapshot MustChangePW ke mirror), `internal/middleware/session.go:132-143` (ForceChangePassword membaca snapshot), `internal/cache/keys.go:11` (TTLSession 5 menit)
- Claim: Setelah guru menyetujui reset (`must_change_pw=1` di DB), sesi murid yang sudah ter-cache tetap membawa `MustChangePW=false` sampai TTL 5 menit kedaluwarsa, sehingga middleware tidak mengarahkan ke `/change-password` dalam jendela tersebut.
- Problem: `LoadSession` menyimpan `&Session{..., MustChangePW: must}` ke mirror 5 menit (`store.Set(cache.SessionKey(id), s, cache.TTLSession)`); hit berikutnya memakai snapshot tanpa baca DB. `ApproveReset` hanya `UPDATE password_resets ... WHERE status='pending'` + `UPDATE users SET must_change_pw=1` dalam satu transaksi — tidak ada `Delete(SessionKey)` / `RevokeUserSessions` untuk user tersebut. Bandingkan dengan `ChangePassword` (`auth.go:409`) dan penonaktifan murid (`teacher_students.go:256-260`) yang memanggil `RevokeUserSessions` (mirror-dulu lalu DB). Jendela basi = sisa TTL entri sesi (maks 5 menit) per sesi aktif.
- Evidence: `ApproveReset` (`password_resets.go:74-105`, diverifikasi via read penuh berkas) tidak mereferensikan `Store`/`cache`/`Revoke` sama sekali; `LoadSession` (`session.go:75-97`):
  ```
  s := &Session{ID: row.ID, UserID: row.UserID, Role: row.Role}
  if row.Role != "guru" {
      ... SELECT must_change_pw, aktif FROM users ...
      s.MustChangePW = must
  }
  store.Set(cache.SessionKey(id), s, cache.TTLSession)
  ```
  dan hit cache (`session.go:58-63`) mengembalikan snapshot tanpa revalidasi flag.
- Reproduction / Verification: `read internal/handlers/password_resets.go` (cari `Store`/`Revoke` — nihil); `read internal/middleware/session.go:45-101`; `grep RevokeUserSessions internal/handlers` (hanya `auth.go:409`, `teacher_students.go:257,306` — tidak di `password_resets.go`). Uji perilaku: login murid (mirror hangat) → approve-reset oleh guru → request berikutnya dengan sesi sama tidak di-redirect ke `/change-password` sampai ±5 menit / sampai mirror dihapus.
- Expected: Approve-reset menginvalidasi mirror sesi milik user tersebut (atau menandai ulang) sehingga `must_change_pw=1` berlaku seketika, konsisten dengan golden rule "cache tidak pernah lebih baru dari DB" dan klaim §6.4 "revocation is instant".
- Actual: Mirror sesi tetap menyajikan `MustChangePW=false` basi hingga TTL.
- Impact: Penegakan wajib-ganti-kata-sandi tertunda hingga 5 menit per sesi aktif; murid yang sedang login dapat terus memakai kredensial lama dalam jendela itu. Bukan RCE, tetapi pelanggaran properti keamanan yang diklaim spec.
- Academic Impact: Klaim "must_change_pw ditegakkan middleware dari setiap halaman" dan "invalidasi eksplisit membuat revokasi instan" gugur untuk jalur approve — penguji cukup meminta demonstrasi approve → akses halaman tanpa redirect.
- Confidence: tinggi (ketiadaan invalidasi + snapshot+d TTL terbukti harfiah; dampak kuantitatif dibatasi sisa TTL, bukan klaim berlebihan).
- Status: SUBMITTED

### 3.3 Finding F-A-003

- Title: `SweepDisconnects` menahan mutex `Live` melintasi query DB (watchdog 1-detik memblokir seluruh registry)
- Severity: MEDIUM
- Category: Concurrency / availability
- Location: `internal/handlers/live.go:186-209` (lock melintasi I/O), pemanggil `internal/handlers/global.go:955-956` (`WatchOnce` tiap 1 detik)
- Claim: Setiap tick watchdog memegang `l.mu` selama seluruh sweep — termasuk `QueryRowContext` + `ExecContext` per peserta disconnect — sehingga `Connect`/`Disconnect`/`Page`/`Ranker`/`AddSpent`/`Spent` (semuanya butuh `l.mu`) terblokir selama I/O DB.
- Problem: Struktur fungsi:
  ```
  func (l *Live) SweepDisconnects(...) []uint64 {
      l.mu.Lock()
      defer l.mu.Unlock()
      for pid, b := range l.beats {
          ... l.DB.QueryRowContext(ctx, `SELECT ends_at ...`) ...
          ... l.DB.ExecContext(ctx, `UPDATE participants SET ends_at = ...`) ...
      }
  }
  ```
  Komentar mengklaim "map lock dipegang melintasi query agar Connect konkuren tidak interleave" — niat benar (atomicity freeze-vs-resume) tetapi implementasi mengorbankan liveness seluruh registry pada tiap tick. Pada DB lambat/timeout, publish SSE (`Publish` tidak butuh lock, tetapi `Ranker(...).Upsert/Snapshot` dan presence `Connect` butuh) ikut tertunda; dengan banyak peserta disconnect, satu tick melakukan N round-trip sekuensial sambil memegang satu mutex global. Perbaikan baku (salin kandidat di bawah lock singkat, I/O di luar lock, commit pembekuan dengan guard `frozen`/CAS atau `WHERE status='started'`) tidak diterapkan.
- Evidence: `read internal/handlers/live.go:131-213` — `Connect` (131), `Connected` (158), `Disconnect` (167) semuanya `l.mu.Lock`; `SweepDisconnects` (186) `l.mu.Lock(); defer Unlock()` lalu loop berisi `QueryRowContext` (195) dan `ExecContext` (205). Pemanggil: `WatchOnce` → `g.Live.SweepDisconnects(ctx, time.Now(), g.disconnectThreshold())` tiap detik (`global.go:955-956`, `WatchLoop` 1027-1041).
- Reproduction / Verification: `read` rentang di atas; statis: konfirmasi tidak ada pelepasan lock di antara iterasi; dinamis (opsional): lambatkan DB (proxy/latency) + banyak beat disconnect + ukur latensi `Connect`/`Ranker().Snapshot` selama tick — terblokir selama sweep.
- Expected: Lock hanya untuk snapshot/pembaruan struktur mikro (salin kandidat, tandai hasil), I/O DB di luar critical section; atau sharding lock per-peserta; watchdog tidak pernah memblokir registry lebih dari orde mikrodetik.
- Actual: Satu mutex global dipegang selama N round-trip DB setiap detik.
- Impact: Tail-latency monitor/ranking/presence naik bersamaan dengan beban disconnect; pada skenario banyak murid refresh serentak + DB lambat, watchdog menjadi penghambat global. Bukan kebocoran goroutine (tidak ada klaim itu), melainkan contention.
- Academic Impact: Klaim ketahanan realtime ("slow client tidak memblokir hub", "freeze di heartbeat terakhir") melemah pada sisi registry: desain mengklaim non-blocking tetapi critical section terbesar justru melintasi I/O.
- Confidence: tinggi untuk fakta lock-melintasi-I/O (harfiah); sedang untuk besaran dampak (bergantung latensi DB dan jumlah disconnect — tidak diukur dalam detik di sini, maka severity MEDIUM bukan HIGH).
- Status: SUBMITTED

### 3.4 Finding F-A-004

- Title: Gerbang join memakai snapshot status kuis basi (TOCTOU) — baris peserta dapat terbuat setelah START
- Severity: MEDIUM
- Category: Race / integrity (check-then-act across transaction boundary)
- Location: `internal/handlers/student.go:236-300` (joinQuiz + `quizGates(quiz)` memakai argumen luar), `internal/handlers/student.go:302-316` (quizGates), `internal/handlers/student.go:409-433` (Join: `quizByCode` lalu `joinQuiz` tanpa re-read di tx)
- Claim: Untuk cabang "belum ada baris", validasi status kuis (`nonaktif`/`selesai`/`berjalan`) dijalankan terhadap objek `quiz` yang dimuat SEBELUM transaksi, tanpa `SELECT ... FOR UPDATE` / re-read status kuis di dalam transaksi. START konkuren yang membalik `aktif`→`berjalan` di antara pemuatan dan `INSERT` tidak terlihat, sehingga `createAttempt` dapat menyisipkan baris `registered`/`pending` pada kuis yang sudah `berjalan`.
- Problem: Alur `Join`: `quiz, rerr, err := quizByCode(ctx, s.DB, body.Code)` (read-committed, tanpa lock) → `s.joinQuiz(ctx, quiz, sess.UserID)` → di dalam loop: `BeginTx` → `latestParticipant(... FOR UPDATE)` (mengunci baris peserta, bukan baris kuis) → cabang tak-ditemukan memanggil `quizGates(quiz)` (objek basi) → `createAttempt` (INSERT). Tidak ada `SELECT status FROM quizzes WHERE id=? FOR UPDATE` di dalam tx. Bandingkan dengan `Start` yang benar memakai `UPDATE ... WHERE status='aktif'` (guard di dalam tx) dan `approvePending` yang menggabungkan kedua kondisi dalam satu UPDATE (`global.go:408-427`). Akibat konkret: baris yatim `registered` pada kuis `berjalan`; `classifyView` kemudian mengklasifikasikannya sebagai "missed START" (409 IN_PROGRESS) dan `closeQuiz` menutupnya sebagai "not attempted" — perilaku akhirnya masih koheren, tetapi (a) pesan join-after-START yang dijanjikan spec ("Anda tidak dapat bergabung", tanpa efek samping) dilanggar karena ada efek samping (baris terbuat), (b) hitungan waiting-room/rank exclusion tercemar oleh baris yang seharusnya tidak pernah ada.
- Evidence: `joinQuiz` (`student.go:236-280`, diverifikasi via read 180-407):
  ```
  func (s *Student) joinQuiz(ctx context.Context, quiz quizDetail, userID uint64) ... {
      maxAttempts := quizengine.MaxAttemptsFor(...)
      for attempt := 0; attempt < 3; attempt++ {
          tx, err := s.DB.BeginTx(ctx, nil)
          ...
          part, found, err := latestParticipant(ctx, tx, quiz.ID, userID, true)
          ...
          // no row: quiz-status gates, then create attempt 1
          if rerr := quizGates(quiz); rerr != nil { ... }   // ← objek luar-tx
          if err := s.createAttempt(ctx, tx, quiz, userID, 1); err != nil { ... }
  ```
  `quizGates` hanya switch atas `quiz.Status` yang diteruskan. Jalur invite/QR (`WorkspacePage`, `student.go:443-470`) memakai `joinQuiz` yang sama.
- Reproduction / Verification: `read internal/handlers/student.go:179-330` (tidak ada SELECT quizzes di dalam tx joinQuiz); `grep "FOR UPDATE" internal/handlers/student.go` (hanya mengunci participants); uji race: tahan `createAttempt` (atau perlambat antara `quizByCode` dan `Commit`) sambil menembakkan `POST /teacher/quiz/:id/start` konkuren pada kuis `aktif` → baris `registered` baru muncul pada kuis `berjalan` (cek via `SELECT status FROM participants`).
- Expected: Status kuis dibaca ulang/dikunci di dalam transaksi join (atau INSERT digabungkan dengan guard status, mis. `INSERT ... SELECT ... WHERE quizzes.status='aktif'` / `WHERE NOT status='berjalan'`), sehingga join-setelah-START murni ditolak tanpa efek samping, sesuai spec §6.12/§11.18.
- Actual: Penolakan bergantung pada snapshot pra-tx; jendela race menghasilkan baris yang seharusnya tidak ada.
- Impact: Integritas roster (waiting-room count, "N working" pada modal close, baris "not attempted" fiktif di hasil); pesan error eksak join-after-START tidak lagi murni (ada efek samping). Eksploitasi butuh timing, tetapi jendela terbuka pada setiap join konkuren dengan START.
- Academic Impact: Klaim "guard server-side di dalam transaksi, jangan percaya state klien" (§10 Always-do) tidak berlaku untuk gerbang join — penguji dapat menuntut bukti guard-dalam-tx untuk cabang no-row, dan kode saat ini tidak memilikinya.
- Confidence: sedang-tinggi (fakta stale-read + tidak-adanya re-read/lock terbukti harfiah; realisasi race membutuhkan interleaving START-vs-INSERT yang belum dieksekusi di sini, maka bukan klaim "tereksploitasi deterministik").
- Status: SUBMITTED

### 3.5 Finding F-A-005

- Title: Collapse anti-cheat 10-detik dapat ditembus oleh submit konkuren (check-then-insert tanpa lock)
- Severity: LOW
- Category: Race / best-effort guard
- Location: `internal/handlers/student.go:1651-1700` (ReportVisibility: baca last-event di luar tx, lalu BeginTx+INSERT)
- Claim: Pengecekan "same-kind dalam 10 detik → collapse" (`quizengine.ShouldRecord`) dan INSERT dilakukan dalam dua fase tanpa row-lock yang mencakup pembacaan, sehingga dua POST visibility sejenis yang bersamaan sama-sama lolos collapse dan menghasilkan dua baris.
- Problem: Urutan kode: `latestParticipant(..., false)` (tanpa lock) → `SELECT created_at, kind ... ORDER BY id DESC LIMIT 1` (di luar tx) → `ShouldRecord(lastAt, now, ...)` → `BeginTx` → `INSERT` → `SELECT COUNT(*)` → `Commit`. Tidak ada `SELECT ... FOR UPDATE` atas baris terakhir, advisory lock, atau constraint unik (collapse window tidak dapat di-UNIQUE-kan secara alami). Di bawah `-race`/beban, double-submit (retry jaringan, dua tab, atau flood sengaja) dalam <10 detik dapat menggandakan baris dan menggelembungkan `count` yang di-push ke monitor (`cheat` frame membawa total absolut). Dampak terbatas pada akurasi badge "N violation(s)" — bukan eskalasi hak.
- Evidence: `read internal/handlers/student.go:1629-1705` — komentar mengklaim "10 s same-kind collapse (quizengine.ShouldRecord), then a teacher-topic `cheat` push"; `ShouldRecord` (`internal/quizengine/anticheat.go:14-22`, via get_code_snippet):
  ```
  func ShouldRecord(lastAt, now time.Time, kind, lastKind string, quizEnded bool) bool {
      if quizEnded { return false }
      if kind == lastKind && !lastAt.IsZero() && now.Sub(lastAt) < CollapseWindow { return false }
      return true
  }
  ```
  murni fungsi waktu tanpa sinkronisasi; pemanggilan di handler memakai `lastAt/lastKind` yang dibaca sebelum `BeginTx`.
- Reproduction / Verification: `read` rentang handler di atas + `get_code_snippet ShouldRecord`; uji: kirim dua `POST /quiz/:code/visibility {"kind":"blur"}` bersamaan pada attempt yang sama → dua baris `anti_cheat_events` berselang <10 detik (cek `SELECT COUNT(*)`, `created_at`).
- Expected: Collapse berlaku atomik (wegen дверь: kunci baris peserta/event-terakhir dalam tx sebelum cek, atau serialisasi per-participant, atau toleransi didokumentasikan sebagai best-effort dengan kuantifikasi).
- Actual: Dua submisi konkuren sama-sama tercatat.
- Impact: Badge count monitor dapat terinflasi; flood-control yang diklaim 10-detik tidak kedap-concurrency. Rendah karena tidak mengubah skor/peringkat dan mudah dinormalisasi di tampilan.
- Academic Impact: Klaim §6.9 "duplicate kind within 10 s collapses to one row" perlu kualifikasi "single-flight" — dalam bentuk sekarang ia hanya benar untuk submisi sekuensial.
- Confidence: sedang (pola check-then-act + verifikasi `ShouldRecord` murni + tidak-adanya lock terbukti; demonstrasi konkuren belum dijalankan di sini).
- Status: SUBMITTED

## 4. Cross-Hunter Discussion

### Agreement

<not-established>

### Disagreement

<not-established>

### Open Questions

- Apakah guard `IN ('aktif','berjalan')` pada `closeQuiz` disengaja untuk menyatukan jalur per-question-close dan global-STOP (putusan desain), atau kelalaian? Bila disengaja, spec §6.6 perlu amendemen yang memisahkan guard per jalur.
- Berapa besar skew jam riil antara kontainer app dan DB (Go `time.Now()` pada `SubmitAnswer` vs `NOW()` pada `finishExpired`)? Tidak diangkat menjadi finding karena belum diukur; dicatat sebagai pertanyaan terbuka, bukan klaim.
- Apakah collapse anti-cheat dimaksudkan sebagai best-effort (didokumentasikan) atau kedap-concurrency (dijamin)? Jawaban menentukan apakah F-A-005 adalah bug atau kualifikasi spec.

## 5. Validator Examination

### Finding F-A-001

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: <OPEN>

### Finding F-A-002

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: <OPEN>

### Finding F-A-003

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: <OPEN>

### Finding F-A-004

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: <OPEN>

### Finding F-A-005

- Validator: <belum ada>
- Challenge: <not-established>
- Evidence Requested: <not-established>
- Bug Hunter Response: <not-established>
- Validator Verdict: <NEEDS EVIDENCE>
- Remaining Objection: <not-established>
- Status: <OPEN>

## 6. Final Verdict

### Confirmed Findings

<not-established — menunggu defense round>

### Rejected Findings

<not-established>

### Unresolved Findings

- F-A-001: guard `closeQuiz` longgar vs spec §6.6
- F-A-002: snapshot `MustChangePW` basi pasca approve-reset
- F-A-003: mutex `Live` melintasi I/O DB tiap detik
- F-A-004: gerbang join memakai snapshot basi (TOCTOU)
- F-A-005: collapse anti-cheat tembus saat konkuren

### Defense Readiness

Laporan awal siap diuji examiner; konteks peran Hunter A dipertahankan untuk ronde defense berikutnya.

## 7. Audit Trail

- 2026-10-07 — [INDEX] — `list_projects` → `D-Project-Mother-quiz`; `index_status` ready (indexed 2026-10-07T08:10:16Z); tidak menjalankan `index_repository`.
- 2026-10-07 — [MAP] — `get_architecture` (overview/structure/routes/hotspots); `codegraph_explore` closeQuiz/lifecycle, sesi/middleware, answer/ends_at, export-guard, upload/stream; `get_file_outline` hub.go + keys.go.
- 2026-10-07 — [VERIFY] — `read` global.go (Start/commit/publish, closeQuiz, WatchOnce, Rehydrate/finishExpired), personal.go, live.go, student.go (joinQuiz, StartAttempt, SubmitAnswer+publish, NextQuestion, ReportPage, ReportVisibility, scoreAndFinish, FinishAttempt), streams.go, respond.go, password_resets.go, media.go, teacher_quiz.go (EditQuiz/DeleteQuiz/guard), results.go (GradeAnswer), main.go (routes + rehydrate + watchdog), cache/keys.go, realtime/hub.go; `get_code_snippet` CentiPercent + ShouldRecord; `grep` ends_at, closeQuiz, Join/Approve, c.JSON langsung, mirror Set/Get, afterRosterChange, session revoke, visibility, StartAttempt/GradeAnswer, compose/remove guard.
- 2026-10-07 — [NEGATIVE] — Jalur tulis inti disiplin (DB→commit→invalidate→broadcast: Start, Stop/closeQuiz, SubmitAnswer, FinishAttempt, removeParticipant, cheatToggle, approve/reject, GradeAnswer semua publish setelah commit; skor atas FULL count via `attemptScore`; finish idempotent; guard `WHERE status=` pada Start/Edit/Approve/Reject) — tidak dijadikan temuan.
- 2026-10-07 — [SUBMIT] — 5 temuan F-A-001…F-A-005 ditulis ke berkas ini; tanpa perubahan kode produksi.

```
STATUS: SUBMITTED
Findings: F-A-001 Guard closeQuiz longgar vs spec §6.6; F-A-002 Snapshot MustChangePW basi pasca approve-reset; F-A-003 Mutex Live melintasi I/O DB tiap detik; F-A-004 Gerbang join memakai snapshot basi (TOCTOU); F-A-005 Collapse anti-cheat tembus saat konkuren
Evidence: F-A-001 global.go:887-889 + Stop 746-781 vs spec §6.6; F-A-002 password_resets.go:74-110 tanpa Store/Revoke + session.go:58-97 TTL 5 mnt; F-A-003 live.go:186-209 lock+Query/Exec dipanggil WatchOnce global.go:955-956 tiap 1 dtk; F-A-004 student.go:236-316 quizGates(quiz) luar-tx + tanpa SELECT quizzes FOR UPDATE; F-A-005 student.go:1651-1700 baca-di-luar-tx + ShouldRecord anticheat.go:14-22 murni tanpa lock
Open Questions: Intent guard IN (aktif,berjalan) — desain vs lalai; skew jam app-vs-DB belum diukur; collapse anti-cheat best-effort vs guarantee
Confidence: F-A-001 tinggi (literal guard terbaca); F-A-002 tinggi (ketiadaan invalidasi harfiah); F-A-003 tinggi-fakta/sedang-dampak (magnitude tak diukur); F-A-004 sedang-tinggi (race perlu interleaving); F-A-005 sedang (pola terbukti, demo konkuren belum jalan)
Files Changed: docs/bug-hunt/findings/hunter-a-report.md
Ready for Examination: YES
```
