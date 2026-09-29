package unit

import (
	"math"
	"testing"

	"quiz/internal/analytics"
)

func score(v float64) *float64 { return &v }

var (
	ovKelas = []analytics.Ref{
		{ID: 1, Nama: "Grade 10"}, {ID: 2, Nama: "Grade 11"}, {ID: 3, Nama: "Grade 12"},
	}
	ovJurusan = []analytics.Ref{
		{ID: 1, Nama: "Science"}, {ID: 2, Nama: "Social"},
		{ID: 3, Nama: "Language"}, {ID: 4, Nama: "Vocational"},
	}
)

// --- ParseFilter -------------------------------------------------------------

func TestParseFilter(t *testing.T) {
	cases := []struct {
		name               string
		kelas, jurusan, st string
		want               analytics.Filter
		wantErr            bool
	}{
		{"all params absent", "", "", "", analytics.Filter{}, false},
		{"explicit zero kelas means no filter", "0", "", "", analytics.Filter{}, false},
		{"numeric ids and valid status", "2", "3", "berjalan",
			analytics.Filter{Kelas: 2, Jurusan: 3, Status: "berjalan"}, false},
		{"unknown numeric id passes (matches nothing)", "999", "", "",
			analytics.Filter{Kelas: 999}, false},
		{"status selesai valid", "", "", "selesai", analytics.Filter{Status: "selesai"}, false},
		{"status nonaktif valid", "", "", "nonaktif", analytics.Filter{Status: "nonaktif"}, false},
		{"non-numeric kelas", "abc", "", "", analytics.Filter{}, true},
		{"fractional kelas", "1.5", "", "", analytics.Filter{}, true},
		{"negative jurusan", "", "-1", "", analytics.Filter{}, true},
		{"overflowing id", "99999999999999999999", "", "", analytics.Filter{}, true},
		{"status outside the set", "", "", "running", analytics.Filter{}, true},
		{"status wrong case", "", "", "AKTIF", analytics.Filter{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := analytics.ParseFilter(tc.kelas, tc.jurusan, tc.st)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseFilter(%q,%q,%q) = %+v, want error",
						tc.kelas, tc.jurusan, tc.st, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFilter(%q,%q,%q): %v", tc.kelas, tc.jurusan, tc.st, err)
			}
			if got != tc.want {
				t.Errorf("ParseFilter(%q,%q,%q) = %+v, want %+v",
					tc.kelas, tc.jurusan, tc.st, got, tc.want)
			}
		})
	}
}

// --- empty inputs ------------------------------------------------------------

func TestBuildEmptyInputsAreAllZeros(t *testing.T) {
	ov := analytics.Build(analytics.Input{
		Kelas: ovKelas, Jurusan: ovJurusan,
		Users: []analytics.UserRow{}, Quizzes: []analytics.QuizRow{},
		Parts: []analytics.PartRow{},
	})
	if ov.Summary != (analytics.Summary{}) {
		t.Errorf("summary = %+v, want all zeros", ov.Summary)
	}
	// every ref row is still emitted, with label order and zero value
	wantKelas := []analytics.KV{
		{Label: "Grade 10"}, {Label: "Grade 11"}, {Label: "Grade 12"}}
	if len(ov.PerKelas) != 3 || ov.PerKelas[0] != wantKelas[0] ||
		ov.PerKelas[1] != wantKelas[1] || ov.PerKelas[2] != wantKelas[2] {
		t.Errorf("per_kelas = %+v, want every ref row with value 0", ov.PerKelas)
	}
	if len(ov.PerJurusan) != 4 {
		t.Errorf("per_jurusan len = %d, want 4", len(ov.PerJurusan))
	}
	for _, kv := range append(append([]analytics.KV{}, ov.PerKelas...), ov.PerJurusan...) {
		if kv.Value != 0 {
			t.Errorf("ref value = %d for %q, want 0", kv.Value, kv.Label)
		}
	}
	if ov.Quizzes == nil || len(ov.Quizzes) != 0 {
		t.Errorf("quizzes = %#v, want non-nil empty slice", ov.Quizzes)
	}
	if ov.Rekap.Rata2 != 0 || ov.Rekap.Jumlah != 0 ||
		ov.Rekap.NilaiMin != 0 || ov.Rekap.NilaiMax != 0 {
		t.Errorf("rekap = %+v, want zeros", ov.Rekap)
	}
	if len(ov.Rekap.Buckets) != 5 {
		t.Fatalf("buckets len = %d, want 5", len(ov.Rekap.Buckets))
	}
	for i, kv := range ov.Rekap.Buckets {
		if kv.Value != 0 {
			t.Errorf("bucket %d value = %d, want 0", i, kv.Value)
		}
	}
	// the status option list is part of the pinned contract
	wantStatus := []analytics.StatusOption{
		{Value: "", Label: "All"},
		{Value: "aktif", Label: "Active"},
		{Value: "berjalan", Label: "Running"},
		{Value: "selesai", Label: "Finished"},
		{Value: "nonaktif", Label: "Not active"},
	}
	if len(ov.Filters.Status) != len(wantStatus) {
		t.Fatalf("status options = %+v", ov.Filters.Status)
	}
	for i, want := range wantStatus {
		if ov.Filters.Status[i] != want {
			t.Errorf("status option %d = %+v, want %+v", i, ov.Filters.Status[i], want)
		}
	}
}

// --- binary status mapping ---------------------------------------------------

func TestBuildBinaryStatusMapping(t *testing.T) {
	quizzes := []analytics.QuizRow{
		{ID: 1, Judul: "a", Code: "AAA", Status: "aktif"},
		{ID: 2, Judul: "b", Code: "BBB", Status: "berjalan"},
		{ID: 3, Judul: "c", Status: "selesai"},
		{ID: 4, Judul: "d", Status: "nonaktif"},
	}
	cases := []struct {
		status       string
		wantAktif    int
		wantNonaktif int
		wantListed   int
	}{
		{"", 2, 2, 4}, // no filter: aktif+berjalan vs the rest, sum = universe
		{"aktif", 1, 0, 1},
		{"berjalan", 1, 0, 1}, // berjalan counts as running → binary "on" side
		{"selesai", 0, 1, 1},
		{"nonaktif", 0, 1, 1},
	}
	for _, tc := range cases {
		t.Run("status="+tc.status, func(t *testing.T) {
			ov := analytics.Build(analytics.Input{
				Filter: analytics.Filter{Status: tc.status}, Quizzes: quizzes,
			})
			if ov.Summary.QuizAktif != tc.wantAktif ||
				ov.Summary.QuizNonaktif != tc.wantNonaktif {
				t.Errorf("quiz_aktif/nonaktif = %d/%d, want %d/%d",
					ov.Summary.QuizAktif, ov.Summary.QuizNonaktif,
					tc.wantAktif, tc.wantNonaktif)
			}
			if ov.Summary.QuizAktif+ov.Summary.QuizNonaktif != tc.wantListed {
				t.Errorf("universe sum = %d, want %d (aktif+nonaktif = list length)",
					ov.Summary.QuizAktif+ov.Summary.QuizNonaktif, tc.wantListed)
			}
			if len(ov.Quizzes) != tc.wantListed {
				t.Errorf("list len = %d, want %d", len(ov.Quizzes), tc.wantListed)
			}
		})
	}
}

// --- user filters cross-applied ----------------------------------------------

func TestBuildUserFiltersCrossApplied(t *testing.T) {
	users := []analytics.UserRow{
		{KelasID: 1, JurusanID: 1, Aktif: true},  // A
		{KelasID: 1, JurusanID: 1, Aktif: false}, // B (inactive)
		{KelasID: 2, JurusanID: 2, Aktif: true},  // C
		{KelasID: 3, JurusanID: 1, Aktif: true},  // D
	}
	cases := []struct {
		name           string
		filter         analytics.Filter
		wantTotal      int
		wantInactive   int
		wantPerKelas   []int
		wantPerJurusan []int
	}{
		{"default", analytics.Filter{}, 3, 1, []int{1, 1, 1}, []int{2, 1, 0, 0}},
		{"kelas narrows summary and both charts",
			analytics.Filter{Kelas: 2}, 1, 0, []int{0, 1, 0}, []int{0, 1, 0, 0}},
		{"jurusan cross-filters the per-kelas chart",
			analytics.Filter{Jurusan: 1}, 2, 1, []int{1, 0, 1}, []int{2, 0, 0, 0}},
		{"status never touches the users charts",
			analytics.Filter{Status: "aktif"}, 3, 1, []int{1, 1, 1}, []int{2, 1, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ov := analytics.Build(analytics.Input{Filter: tc.filter, Users: users,
				Kelas: ovKelas, Jurusan: ovJurusan})
			if ov.Summary.TotalMurid != tc.wantTotal ||
				ov.Summary.MuridNonaktif != tc.wantInactive {
				t.Errorf("total/nonaktif = %d/%d, want %d/%d",
					ov.Summary.TotalMurid, ov.Summary.MuridNonaktif,
					tc.wantTotal, tc.wantInactive)
			}
			assertValues(t, "per_kelas", ov.PerKelas, tc.wantPerKelas)
			assertValues(t, "per_jurusan", ov.PerJurusan, tc.wantPerJurusan)
		})
	}
}

func assertValues(t *testing.T, name string, kvs []analytics.KV, want []int) {
	t.Helper()
	if len(kvs) != len(want) {
		t.Fatalf("%s len = %d, want %d", name, len(kvs), len(want))
	}
	for i, v := range want {
		if kvs[i].Value != v {
			t.Errorf("%s[%d] (%q) = %d, want %d", name, i, kvs[i].Label, kvs[i].Value, v)
		}
	}
}

// --- participant counts: status universe × user dims --------------------------

func TestBuildPesertaCounts(t *testing.T) {
	quizzes := []analytics.QuizRow{
		{ID: 1, Judul: "run", Code: "AAA", Status: "aktif"},
		{ID: 2, Judul: "idle", Code: "BBB", Status: "nonaktif"},
	}
	parts := []analytics.PartRow{
		{QuizID: 1, KelasID: 1, JurusanID: 1, Status: "selesai", FinalScore: score(55.5)},
		{QuizID: 1, KelasID: 1, JurusanID: 1, Status: "selesai"}, // finished, ungraded
		{QuizID: 1, KelasID: 1, JurusanID: 1, Status: "registered", FinalScore: score(99)},
		{QuizID: 2, KelasID: 2, JurusanID: 2, Status: "selesai", FinalScore: score(80)},
		{QuizID: 2, KelasID: 3, JurusanID: 1, Status: "selesai", FinalScore: score(70)},
	}
	cases := []struct {
		name        string
		filter      analytics.Filter
		wantPeserta int
		wantRekap   int
		wantPerQuiz []int // by quiz id order 1, 2
	}{
		{"no filter counts every finished row", analytics.Filter{}, 4, 3, []int{2, 2}},
		{"status narrows the quiz universe", analytics.Filter{Status: "aktif"}, 2, 1, []int{2}},
		// kelas narrows the per-quiz COUNTS but never the quiz list itself
		// (the list is status-governed only)
		{"kelas narrows through the user join", analytics.Filter{Kelas: 1}, 2, 1, []int{2, 0}},
		{"both filters compose", analytics.Filter{Kelas: 1, Status: "nonaktif"}, 0, 0, []int{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ov := analytics.Build(analytics.Input{
				Filter: tc.filter, Quizzes: quizzes, Parts: parts,
			})
			if ov.Summary.PesertaMengerjakan != tc.wantPeserta {
				t.Errorf("peserta_mengerjakan = %d, want %d",
					ov.Summary.PesertaMengerjakan, tc.wantPeserta)
			}
			if ov.Rekap.Jumlah != tc.wantRekap {
				t.Errorf("rekap jumlah = %d, want %d", ov.Rekap.Jumlah, tc.wantRekap)
			}
			if len(ov.Quizzes) != len(tc.wantPerQuiz) {
				t.Fatalf("list len = %d, want %d", len(ov.Quizzes), len(tc.wantPerQuiz))
			}
			for i, want := range tc.wantPerQuiz {
				if ov.Quizzes[i].Peserta != want {
					t.Errorf("quizzes[%d].peserta = %d, want %d",
						i, ov.Quizzes[i].Peserta, want)
				}
			}
		})
	}
}

// --- rekap buckets + rounding -------------------------------------------------

func TestBuildRekapBucketsAndRounding(t *testing.T) {
	parts := []analytics.PartRow{
		{QuizID: 1, Status: "selesai", FinalScore: score(0)},
		{QuizID: 1, Status: "selesai", FinalScore: score(19.99)},
		{QuizID: 1, Status: "selesai", FinalScore: score(20)},
		{QuizID: 1, Status: "selesai", FinalScore: score(55.5)},
		{QuizID: 1, Status: "selesai", FinalScore: score(79.99)},
		{QuizID: 1, Status: "selesai", FinalScore: score(80)},
		{QuizID: 1, Status: "selesai", FinalScore: score(100)},
	}
	ov := analytics.Build(analytics.Input{
		Quizzes: []analytics.QuizRow{{ID: 1, Status: "aktif"}}, Parts: parts,
	})
	wantLabels := []string{"0–19", "20–39", "40–59", "60–79", "80–100"}
	wantValues := []int{2, 1, 1, 1, 2} // 0+19.99 | 20 | 55.5 | 79.99 | 80+100
	if len(ov.Rekap.Buckets) != 5 {
		t.Fatalf("buckets len = %d, want 5", len(ov.Rekap.Buckets))
	}
	for i, want := range wantLabels {
		if ov.Rekap.Buckets[i].Label != want {
			t.Errorf("bucket %d label = %q, want %q", i, ov.Rekap.Buckets[i].Label, want)
		}
		if ov.Rekap.Buckets[i].Value != wantValues[i] {
			t.Errorf("bucket %d (%q) = %d, want %d",
				i, want, ov.Rekap.Buckets[i].Value, wantValues[i])
		}
	}
	if ov.Rekap.Jumlah != 7 {
		t.Errorf("jumlah = %d, want 7", ov.Rekap.Jumlah)
	}
	// mean = 355.48/7 = 50.7828… → 50.78; min/max are the extremes
	if math.Abs(ov.Rekap.Rata2-50.78) > 1e-9 {
		t.Errorf("rata2 = %v, want 50.78", ov.Rekap.Rata2)
	}
	if ov.Rekap.NilaiMin != 0 || ov.Rekap.NilaiMax != 100 {
		t.Errorf("min/max = %v/%v, want 0/100", ov.Rekap.NilaiMin, ov.Rekap.NilaiMax)
	}

	// 2-decimal display rounding (the app's "%.2f" precision)
	ov = analytics.Build(analytics.Input{
		Quizzes: []analytics.QuizRow{{ID: 1, Status: "aktif"}},
		Parts: []analytics.PartRow{
			{QuizID: 1, Status: "selesai", FinalScore: score(66.666)},
			{QuizID: 1, Status: "selesai", FinalScore: score(33.334)},
		},
	})
	if math.Abs(ov.Rekap.Rata2-50) > 1e-9 {
		t.Errorf("rata2 = %v, want 50", ov.Rekap.Rata2)
	}
	if ov.Rekap.NilaiMin != 33.33 {
		t.Errorf("min = %v, want 33.33", ov.Rekap.NilaiMin)
	}
	if ov.Rekap.NilaiMax != 66.67 {
		t.Errorf("max = %v, want 66.67", ov.Rekap.NilaiMax)
	}
}

// --- presentation stays zero, order preserved --------------------------------

func TestBuildLeavesLabelChipZeroAndKeepsOrder(t *testing.T) {
	quizzes := []analytics.QuizRow{
		{ID: 7, Judul: "newest", Code: "NNN", Status: "selesai"},
		{ID: 3, Judul: "older", Code: "OOO", Status: "aktif"},
		{ID: 1, Judul: "oldest", Code: "PPP", Status: "berjalan"},
	}
	ov := analytics.Build(analytics.Input{
		Filter:  analytics.Filter{Kelas: 5, Jurusan: 6, Status: "selesai"},
		Quizzes: quizzes,
	})
	// input order (created_at DESC) survives the filter
	wantIDs := []uint64{7}
	if len(ov.Quizzes) != 1 || ov.Quizzes[0].ID != wantIDs[0] {
		t.Fatalf("quizzes = %+v, want only id 7", ov.Quizzes)
	}
	if ov.Quizzes[0].Label != "" || ov.Quizzes[0].Chip != "" {
		t.Errorf("Label/Chip = %q/%q, want zero (handlers fill via quizChip)",
			ov.Quizzes[0].Label, ov.Quizzes[0].Chip)
	}
	// applied echoes the filter back verbatim
	if ov.Applied.Kelas != 5 || ov.Applied.Jurusan != 6 || ov.Applied.Status != "selesai" {
		t.Errorf("applied = %+v", ov.Applied)
	}
	// and the unfiltered build preserves full input order
	full := analytics.Build(analytics.Input{Quizzes: quizzes})
	if len(full.Quizzes) != 3 ||
		full.Quizzes[0].ID != 7 || full.Quizzes[1].ID != 3 || full.Quizzes[2].ID != 1 {
		t.Errorf("order = %+v, want 7,3,1", full.Quizzes)
	}
}
