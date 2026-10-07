# Normalized Findings — Thesis Bug Defense (Main Agent Gate §9)

> Hasil gate Main Agent setelah kedua Hunter SUBMITTED. Dibaca dari:
> `docs/bug-hunt/findings/hunter-a-report.md` (5 temuan implementasi) dan
> `docs/bug-hunt/findings/hunter-b-report.md` (5 temuan akademik).
> Verifikasi silang Main Agent via `read`/`grep` langsung (2026-10-07):
> go.mod 3-direct, guard closeQuiz, ApproveReset tanpa Store/Revoke,
> SweepDisconnects lock+IO, joinQuiz quizGates luar-tx, ShouldRecord murni +
> baca-di-luar-tx, t.Parallel nihil, time.Now di handler, authlogic delegasi,
> testutil Skipf, migrasi 0003/0004, history/monitor raw status, breadcrumb
> Dasbor-vs-Dashboard, auth.go skip-CompareHash saat must, Chart.js vendor+load.
> Tidak ada duplikat mentah — F-A-002 (stale mirror pasca-approve) dan
> F-B-005(a) (login skip-validasi saat must) berbagi area `must_change_pw`
> tetapi klaim berbeda, keduanya dipertahankan dengan relasi tercatat.
> ID ternormalisasi: N-001…N-010 (pemetaan ke ID hunter asli dicantumkan).

## N-001 (F-A-001) — Guard closeQuiz longgar vs spec §6.6

- Severity: MEDIUM — Category: State-machine guard / spec↔code
- Location: `internal/handlers/global.go:887-889`, `:746-781`; `internal/handlers/personal.go:56-66`
- Claim: `closeQuiz` menutup kuis `aktif` maupun `berjalan` (`WHERE id=? AND status IN ('aktif','berjalan')`);
  spec §6.6 menetapkan funnel tunggal `WHERE status='berjalan'`.
- Evidence (terverifikasi): literal guard + Stop tanpa pra-cek status; pesan `msgNotRunning` hanya saat `closed==false`.
- Status: SUBMITTED → siap examination.

## N-002 (F-A-002) — Approve-reset tanpa invalidasi mirror sesi

- Severity: HIGH — Category: Session/cache invalidation (security-relevant)
- Location: `internal/handlers/password_resets.go:74-110`; `internal/middleware/session.go:75-97,132-143`; `internal/cache/keys.go:11`
- Claim: `ApproveReset` tidak menyentuh `Store`/`Revoke` (grep `Store|Revoke|SessionKey|cache` di berkas = nihil);
  snapshot `MustChangePW` di mirror 5-menit basi hingga TTL.
- Evidence (terverifikasi): read penuh ApproveReset + grep nihil + LoadSession snapshot+TTL.
- Status: SUBMITTED → siap examination.

## N-003 (F-A-003) — SweepDisconnects menahan mutex melintasi I/O DB

- Severity: MEDIUM — Category: Concurrency / availability
- Location: `internal/handlers/live.go:186-209`; pemanggil `internal/handlers/global.go:955-956`
- Claim: `l.mu.Lock(); defer Unlock()` melingkupi loop berisi `QueryRowContext` + `ExecContext` per pid;
  dipanggil tiap 1 detik oleh WatchOnce.
- Evidence (terverifikasi): read langsung struktur fungsi + semua akses Live lain butuh `l.mu`.
- Status: SUBMITTED → siap examination.

## N-004 (F-A-004) — Gerbang join memakai snapshot basi (TOCTOU)

- Severity: MEDIUM — Category: Race / integrity
- Location: `internal/handlers/student.go:236-316,409-433`
- Claim: cabang no-row memanggil `quizGates(quiz)` atas objek pra-tx tanpa `SELECT quizzes ... FOR UPDATE`/re-read
  di dalam tx; `FOR UPDATE` yang ada hanya mengunci baris participants.
- Evidence (terverifikasi): read joinQuiz + quizGates; banding-pembanding Start (`UPDATE ... WHERE status='aktif'`) benar di dalam tx.
- Status: SUBMITTED → siap examination.

## N-005 (F-A-005) — Collapse anti-cheat tembus saat konkuren

- Severity: LOW — Category: Race / best-effort guard
- Location: `internal/handlers/student.go:1651-1700`; `internal/quizengine/anticheat.go:14-22`
- Claim: baca last-event di luar tx + `ShouldRecord` murni tanpa lock + INSERT di tx → dua POST sejenis
  bersamaan sama-sama tercatat.
- Evidence (terverifikasi): read urutan handler + ShouldRecord murni.
- Status: SUBMITTED → siap examination.

## N-006 (F-B-001) — Klaim closed-8-direct gugur

- Severity: HIGH — Category: klaim-vs-bukti / batasan (§2, §10, §11.21)
- Location: `go.mod:5-29`; `internal/handlers/export.go:3-16`; `web/vendor/chartjs/chart.umd.min.js:1-6`; `views/teacher/dashboard.html:72-84,130`
- Claim: (a) hanya 3 direct di go.mod, 5 berlabel `// indirect`; (b) `gocsv` nol import `.go`
  (hanya go.mod/go.sum/plan/docs) sementara export CSV memakai `encoding/csv`;
  (c) Chart.js v4.5.1 bervendor + dieksekusi di dashboard tanpa diakui spec.
- Evidence (terverifikasi): read go.mod + grep gocsv + read export header/dashboard/chart header.
- Status: SUBMITTED → siap examination.

## N-007 (F-B-002) — Metodologi testing §9 overclaim

- Severity: MEDIUM — Category: metodologi (§9, §11.14)
- Location: `tests/unit/authlogic_test.go:1-14`; `internal/testutil/testutil.go:62-77`;
  `internal/middleware/assets_test.go:83-125`; `time.Now()` di auth/global/live/student vs `internal/quizengine/timer.go:1-11`
- Claim: (a) "parallel-safe" tanpa satu `t.Parallel` (grep nihil di tests+internal);
  (b) "no time.Now in logic" hanya benar di quizengine, handler memanggil langsung;
  (c) tabel §9 mengatribusikan ke unit apa yang header test sendiri delegasikan ke integrasi;
  (d) "fully green" kondisional via Skipf DB-gate.
- Evidence (terverifikasi): grep + read keempat lokasi.
- Status: SUBMITTED → siap examination.

## N-008 (F-B-003) — Drift living spec: users.aktif + riwayat timer_type

- Severity: MEDIUM — Category: konsistensi dokumen (§5, §10, migrasi)
- Location: `migrations/0003_users_aktif.sql:1-8`; `migrations/0001_init.sql:20-32,47`;
  `migrations/0004_timer_type_tanpa.sql:1-6`; `internal/handlers/auth.go:89-115`; `internal/middleware/session.go:77-93`
- Claim: (a) kolom `users.aktif` + enforcement dua lapis tak ada di §5;
  (b) `timer_type` lahir 2-nilai di 0001, ketiga di 0004, §5 tampil final tanpa tanda evolusi (kontras positif
  bahwa sinkronisasi selektif, bukan living).
- Evidence (terverifikasi): read migrasi + enforcement + blok users §5 (tanpa `aktif`).
- Status: SUBMITTED → siap examination.

## N-009 (F-B-004) — Klaim KBBI gugur

- Severity: MEDIUM — Category: kebahasaan (§1, §7, §11.21)
- Location: `views/student/history.html:37-41,83-87`; `views/teacher/monitor.html:7-10,52-53`;
  `internal/handlers/history.go:60-88`; `internal/handlers/breadcrumb.go:11-132`;
  `tests/integration/nav_no_root_links_test.go:14,56-60`
- Claim: ENUM Inggris dirender mentah (`{{else}}{{.Status}}`, `{{.Status}}`, `{{.TimerType}} timer`,
  label `status`); dokumen+komentar memakai "Dashboard" sementara render mengunci "Dasbor".
- Evidence (terverifikasi): read template + handler + breadcrumb.
- Status: SUBMITTED → siap examination.

## N-010 (F-B-005) — §11.2 skip-validasi + robustness tanpa uji negatif + §12 None

- Severity: HIGH — Category: keamanan / falsifiabilitas (§11.2, §11.15-17, §12)
- Location: `internal/handlers/auth.go:104-115`; `internal/middleware/session.go:130-139`;
  `tests/integration/realtime_test.go:277-484`; `internal/testutil/testutil.go:62-77`
- Claim: (a) `if !must && bcrypt.Compare...` → akun must_change_pw=1 mendapat sesi tanpa bukti kata sandi
  (middleware hanya me-redirect, tak mencabut); (b) klaim robustness (rehydrate/freeze/no-broadcast-on-write-failure)
  tanpa uji negatif pasangan; (c) §12 "None" dibantah N-006…N-009.
- Evidence (terverifikasi): read cabang login + middleware; suite realtime skenario positif (perlu konfirmasi
  validator untuk kelengkapan klaim ketiadaan).
- Status: SUBMITTED → siap examination.

## Relasi antar-temuan

- N-002 ↔ N-010(a): satu alur `must_change_pw` — N-002 = enforcement tertunda pada sesi aktif (stale mirror),
  N-010(a) = sesi baru terbit tanpa verifikasi. Saling menguatkan, bukan duplikat.
- N-004 ↔ N-007(c): keduanya menyentuh join-code path dari sudut berbeda (race tx vs atribusi test).
- N-006 ↔ N-008/§10: closed-list (§10) vs ask-first skema (§10) — dua sisi klaim ketertutupan yang sama-sama gugur.

## Audit Trail (Main Gate)

- 2026-10-07 — [GATE] — Kedua hunter SUBMITTED; file diperiksa isinya (bukan sekadar status).
- 2026-10-07 — [VERIFY] — 10 temuan dicek silang ke berkas aktual (daftar di atas); tidak ada yang UNVERIFIED.
- 2026-10-07 — [NORMALIZE] — ID N-001…N-010; duplikat nihil; relasi dicatat; siap Phase 2 Validator.
