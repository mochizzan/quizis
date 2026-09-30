// Package analytics turns raw dashboard rows into the guru-overview
// payload. Pure by design: no DB, no HTTP, no time.Now — callers (the
// handlers) fetch plain rows and hand them over.
package analytics

import (
	"errors"
	"math"
	"strconv"
)

// ErrInvalidFilter is returned by ParseFilter for malformed query params
// (non-numeric kelas/jurusan, status outside the quiz-status set).
var ErrInvalidFilter = errors.New("analytics: invalid filter")

// validStatus is the ?status= contract ("" = no filter).
var validStatus = map[string]bool{
	"": true, "aktif": true, "berjalan": true, "selesai": true, "nonaktif": true,
}

// bucketLabels are the five fixed score bands. The score scale is 0..100
// with 2 decimals (quizengine.CentiPercent, GradeAnswer's 0..100 check),
// so five 20-wide bands cover it exactly.
var bucketLabels = [5]string{"0–19", "20–39", "40–59", "60–79", "80–100"}

// Filter is the validated query state: 0 / "" means no filter.
type Filter struct {
	Kelas   uint32
	Jurusan uint32
	Status  string
}

// ParseFilter validates the three query params (string → Filter).
// Malformed values (non-numeric id, status not in the set) return
// ErrInvalidFilter; numeric-but-unknown ids pass through and simply match
// nothing downstream.
func ParseFilter(kelas, jurusan, status string) (Filter, error) {
	var f Filter
	var err error
	if f.Kelas, err = parseID(kelas); err != nil {
		return Filter{}, err
	}
	if f.Jurusan, err = parseID(jurusan); err != nil {
		return Filter{}, err
	}
	if !validStatus[status] {
		return Filter{}, ErrInvalidFilter
	}
	f.Status = status
	return f, nil
}

func parseID(s string) (uint32, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, ErrInvalidFilter
	}
	return uint32(n), nil
}

// --- raw input rows ---------------------------------------------------------

// Ref is one dropdown reference row (id order).
type Ref struct {
	ID   uint64
	Nama string
}

// UserRow is one users row reduced to the aggregate dimensions.
type UserRow struct {
	KelasID   uint32
	JurusanID uint32
	Aktif     bool
}

// QuizRow is one quizzes row (ID/Judul/Code/Status).
type QuizRow struct {
	ID     uint64
	Judul  string
	Code   string
	Status string
}

// PartRow is one participants row with the user's dimensions denormalized
// (the handler JOINs users when loading).
type PartRow struct {
	QuizID     uint64
	KelasID    uint32
	JurusanID  uint32
	Status     string
	FinalScore *float64 // nil = not graded / not applicable
}

// Input carries every raw row plus the applied filter.
type Input struct {
	Filter  Filter
	Kelas   []Ref
	Jurusan []Ref
	Users   []UserRow
	Quizzes []QuizRow // created_at DESC (order preserved)
	Parts   []PartRow
}

// --- payload ----------------------------------------------------------------

// RefOption is one filter-dropdown entry {id,nama}.
type RefOption struct {
	ID   uint64 `json:"id"`
	Nama string `json:"nama"`
}

// StatusOption is one status-dropdown entry {value,label}.
type StatusOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Filters holds the three filter option lists.
type Filters struct {
	Kelas   []RefOption    `json:"kelas"`
	Jurusan []RefOption    `json:"jurusan"`
	Status  []StatusOption `json:"status"`
}

// Applied echoes the filter values actually in effect.
type Applied struct {
	Kelas   uint32 `json:"kelas"`
	Jurusan uint32 `json:"jurusan"`
	Status  string `json:"status"`
}

// Summary holds the headline counters.
type Summary struct {
	TotalMurid         int `json:"total_murid"`
	MuridNonaktif      int `json:"murid_nonaktif"`
	QuizAktif          int `json:"quiz_aktif"`
	QuizNonaktif       int `json:"quiz_nonaktif"`
	PesertaMengerjakan int `json:"peserta_mengerjakan"`
}

// KV is one chart entry.
type KV struct {
	Label string `json:"label"`
	Value int    `json:"value"`
}

// QuizItem is one quiz row for the overview list. Label/Chip are
// presentation fields — Build leaves them zero; handlers fill them via
// quizChip.
type QuizItem struct {
	ID      uint64 `json:"id"`
	Judul   string `json:"judul"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Label   string `json:"label"`
	Chip    string `json:"chip"`
	Peserta int    `json:"peserta"`
}

// Rekap is the finished-scores aggregate (selesai + final_score present).
type Rekap struct {
	Rata2    float64 `json:"rata2"`
	Jumlah   int     `json:"jumlah"`
	NilaiMin float64 `json:"nilai_min"`
	NilaiMax float64 `json:"nilai_max"`
	Buckets  []KV    `json:"buckets"`
}

// Overview is the full chart-ready payload (JSON shape is a fixed contract
// with the frontend).
type Overview struct {
	Filters    Filters    `json:"filters"`
	Applied    Applied    `json:"applied"`
	Summary    Summary    `json:"summary"`
	PerKelas   []KV       `json:"per_kelas"`
	PerJurusan []KV       `json:"per_jurusan"`
	Quizzes    []QuizItem `json:"quizzes"`
	Rekap      Rekap      `json:"rekap_nilai"`
}

// --- Build ------------------------------------------------------------------

// Build turns the raw rows into the payload. Filter semantics (uniform):
//   - kelas/jurusan narrow every user-based aggregate (users AND the
//     user-joined participant numbers) and cross-narrow each other chart;
//   - status narrows the quiz universe (list, quiz_aktif/nonaktif and the
//     quiz-joined participant numbers), never the users charts;
//   - every ref row is emitted even when its value is 0;
//   - Label/Chip on QuizItem stay zero (presentation belongs in handlers).
func Build(in Input) Overview {
	f := in.Filter
	ov := Overview{
		Filters: Filters{
			Kelas:   refOptions(in.Kelas),
			Jurusan: refOptions(in.Jurusan),
			Status: []StatusOption{
				{Value: "", Label: "Semua"},
				{Value: "aktif", Label: "Aktif"},
				{Value: "berjalan", Label: "Berjalan"},
				{Value: "selesai", Label: "Selesai"},
				{Value: "nonaktif", Label: "Tidak aktif"},
			},
		},
		Applied:    Applied{Kelas: f.Kelas, Jurusan: f.Jurusan, Status: f.Status},
		PerKelas:   make([]KV, 0, len(in.Kelas)),
		PerJurusan: make([]KV, 0, len(in.Jurusan)),
		Quizzes:    []QuizItem{},
	}
	ov.Rekap.Buckets = make([]KV, len(bucketLabels))
	for i, label := range bucketLabels {
		ov.Rekap.Buckets[i] = KV{Label: label}
	}

	dimsMatch := func(kelasID, jurusanID uint32) bool {
		return (f.Kelas == 0 || kelasID == f.Kelas) &&
			(f.Jurusan == 0 || jurusanID == f.Jurusan)
	}

	// users summary
	for _, u := range in.Users {
		if !dimsMatch(u.KelasID, u.JurusanID) {
			continue
		}
		if u.Aktif {
			ov.Summary.TotalMurid++
		} else {
			ov.Summary.MuridNonaktif++
		}
	}

	// per-ref charts: active students only, so under no filter the values
	// sum to total_murid; the filter (incl. the chart's own dimension)
	// applies on top of the group condition → non-matching rows go to 0.
	for _, r := range in.Kelas {
		kv := KV{Label: r.Nama}
		id := uint32(r.ID)
		for _, u := range in.Users {
			if u.Aktif && u.KelasID == id && dimsMatch(u.KelasID, u.JurusanID) {
				kv.Value++
			}
		}
		ov.PerKelas = append(ov.PerKelas, kv)
	}
	for _, r := range in.Jurusan {
		kv := KV{Label: r.Nama}
		id := uint32(r.ID)
		for _, u := range in.Users {
			if u.Aktif && u.JurusanID == id && dimsMatch(u.KelasID, u.JurusanID) {
				kv.Value++
			}
		}
		ov.PerJurusan = append(ov.PerJurusan, kv)
	}

	// quiz universe (status filter only)
	statusMatch := func(status string) bool {
		return f.Status == "" || status == f.Status
	}
	quizStatus := make(map[uint64]string, len(in.Quizzes))
	for _, q := range in.Quizzes {
		quizStatus[q.ID] = q.Status
	}
	for _, q := range in.Quizzes {
		if !statusMatch(q.Status) {
			continue
		}
		if q.Status == "aktif" || q.Status == "berjalan" {
			ov.Summary.QuizAktif++
		} else {
			ov.Summary.QuizNonaktif++
		}
	}

	// finished participants: user dims (kelas/jurusan) + quiz universe
	// (status) both apply; per-quiz counts feed the list, the total feeds
	// peserta_mengerjakan, and the graded scores feed rekap_nilai.
	selesaiByQuiz := make(map[uint64]int, len(in.Quizzes))
	var scores []float64
	for _, p := range in.Parts {
		if p.Status != "selesai" {
			continue
		}
		quizStatusOf, exists := quizStatus[p.QuizID]
		if !exists || !statusMatch(quizStatusOf) {
			continue
		}
		if !dimsMatch(p.KelasID, p.JurusanID) {
			continue
		}
		selesaiByQuiz[p.QuizID]++
		ov.Summary.PesertaMengerjakan++
		if p.FinalScore != nil {
			scores = append(scores, *p.FinalScore)
		}
	}
	for _, q := range in.Quizzes { // input order = created_at DESC
		if !statusMatch(q.Status) {
			continue
		}
		ov.Quizzes = append(ov.Quizzes, QuizItem{
			ID: q.ID, Judul: q.Judul, Code: q.Code, Status: q.Status,
			Peserta: selesaiByQuiz[q.ID],
		})
	}

	// rekap_nilai over the graded scores
	ov.Rekap.Jumlah = len(scores)
	if len(scores) > 0 {
		sum := 0.0
		min, max := scores[0], scores[0]
		for _, s := range scores {
			sum += s
			if s < min {
				min = s
			}
			if s > max {
				max = s
			}
			idx := int(s / 20)
			if idx < 0 {
				idx = 0
			}
			if idx > len(bucketLabels)-1 {
				idx = len(bucketLabels) - 1
			}
			ov.Rekap.Buckets[idx].Value++
		}
		ov.Rekap.Rata2 = round2(sum / float64(len(scores)))
		ov.Rekap.NilaiMin = round2(min)
		ov.Rekap.NilaiMax = round2(max)
	}
	return ov
}

func refOptions(refs []Ref) []RefOption {
	out := make([]RefOption, 0, len(refs))
	for _, r := range refs {
		out = append(out, RefOption{ID: r.ID, Nama: r.Nama})
	}
	return out
}

// round2 rounds half away from zero to 2 decimals — the display precision
// of every score in the app (fmtScore "%.2f").
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
