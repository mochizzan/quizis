package unit

import (
	"testing"
	"time"

	"quiz/internal/quizengine"
)

func TestShouldRecord(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		lastAt    time.Time
		lastKind  string
		now       time.Time
		kind      string
		quizEnded bool
		want      bool
	}{
		{"first event always records", time.Time{}, "", t0, "tab_blur", false, true},
		{"same kind within 10s collapsed", t0.Add(-9 * time.Second), "tab_blur", t0, "tab_blur", false, false},
		{"same kind exactly at 10s records", t0.Add(-10 * time.Second), "tab_blur", t0, "tab_blur", false, true},
		{"different kind within 10s records", t0.Add(-1 * time.Second), "tab_blur", t0, "devtools_open", false, true},
		{"same kind after window records", t0.Add(-11 * time.Second), "window_blur", t0, "window_blur", false, true},
		{"post-finish ignored", time.Time{}, "", t0, "tab_blur", true, false},
		{"post-finish ignores even a different kind", t0.Add(-time.Hour), "tab_blur", t0, "devtools_open", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := quizengine.ShouldRecord(c.lastAt, c.now, c.kind, c.lastKind, c.quizEnded)
			if got != c.want {
				t.Errorf("ShouldRecord = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCollapseWindowIsTenSeconds(t *testing.T) {
	if quizengine.CollapseWindow != 10*time.Second {
		t.Errorf("CollapseWindow = %v, want 10s (spec §6.5)", quizengine.CollapseWindow)
	}
}
