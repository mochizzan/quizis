package unit

import (
	"encoding/json"
	"reflect"
	"testing"

	"quiz/internal/quizengine"
)

// goldenInputs mirrors makeQOrder's inputs for a fixed 8-question quiz
// (even ids = 4-option mcq, odd ids = 3-option mcq — no essays here).
func goldenInputs() ([]uint64, map[uint64]int) {
	ids := []uint64{101, 102, 103, 104, 105, 106, 107, 108}
	counts := map[uint64]int{
		101: 4, 102: 4, 103: 3, 104: 4,
		105: 3, 106: 4, 107: 4, 108: 3,
	}
	return ids, counts
}

// Same (pid, quiz, attempt, ids, counts) must reproduce byte-identical
// snapshots: the qorder JSON is persisted once and must regenerate exactly
// for export/review (spec §6.7).
func TestBuildOrderByteIdenticalDeterminism(t *testing.T) {
	ids, counts := goldenInputs()
	seed := quizengine.SeedFor(11, 3, 1)

	a := quizengine.BuildOrder(seed, ids, counts, true, true)
	b := quizengine.BuildOrder(seed, ids, counts, true, true)
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	if string(ja) != string(jb) {
		t.Errorf("same inputs must be byte-identical:\n%s\n%s", ja, jb)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("same inputs must produce an identical Order")
	}
}

// Per-murid golden pins: fixed participants get FIXED permutations (hardcoded
// expected sequences — the per-participant seed must never drift silently).
func TestBuildOrderPerMuridGolden(t *testing.T) {
	ids, counts := goldenInputs()

	cases := []struct {
		name      string
		pid       uint64
		questions []uint64
		options   map[uint64][]int
	}{
		{
			name:      "participant 11",
			pid:       11,
			questions: []uint64{104, 107, 103, 106, 101, 102, 108, 105},
			options: map[uint64][]int{
				101: {0, 1, 2, 3},
				102: {2, 1, 0, 3},
				103: {0, 2, 1},
				104: {1, 3, 2, 0},
				105: {1, 0, 2},
				106: {0, 1, 2, 3},
				107: {1, 2, 0, 3},
				108: {2, 1, 0},
			},
		},
		{
			name:      "participant 22",
			pid:       22,
			questions: []uint64{107, 101, 104, 108, 102, 106, 103, 105},
			options: map[uint64][]int{
				101: {0, 1, 3, 2},
				102: {0, 1, 3, 2},
				103: {2, 0, 1},
				104: {0, 1, 3, 2},
				105: {1, 2, 0},
				106: {1, 0, 2, 3},
				107: {3, 2, 1, 0},
				108: {0, 1, 2},
			},
		},
	}

	orders := make([]quizengine.Order, len(cases))
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := quizengine.BuildOrder(quizengine.SeedFor(tc.pid, 3, 1), ids, counts, true, true)
			orders[i] = o
			if !reflect.DeepEqual(o.Questions, tc.questions) {
				t.Errorf("questions = %v, want %v", o.Questions, tc.questions)
			}
			for qid, want := range tc.options {
				if got := o.Options[qid]; !reflect.DeepEqual(got, want) {
					t.Errorf("options[%d] = %v, want %v", qid, got, want)
				}
			}
			if len(o.Options) != len(tc.options) {
				t.Errorf("options map size = %d, want %d", len(o.Options), len(tc.options))
			}
		})
	}

	// different seeds → different per-murid orders
	if reflect.DeepEqual(orders[0].Questions, orders[1].Questions) {
		t.Error("participants 11 and 22 must see different question orders")
	}
	if reflect.DeepEqual(orders[0].Options, orders[1].Options) {
		t.Error("participants 11 and 22 must see different option permutations")
	}
}

// DisplayLetters: stored ORIGINAL indexes → the letters THIS attempt saw.
// perm slot→original: slot0 shows original 2, slot1 shows original 0,
// slot2 shows original 1 — so original 0 displays as B under [2,0,1].
func TestDisplayLetters(t *testing.T) {
	cases := []struct {
		name   string
		order  *quizengine.Order
		qid    uint64
		stored []int
		want   string
	}{
		{
			name:   "perm [2,0,1] stored 2 → slot 0 = A",
			order:  &quizengine.Order{Options: map[uint64][]int{0: {2, 0, 1}}},
			qid:    0,
			stored: []int{2},
			want:   "A",
		},
		{
			name:   "perm [2,0,1] stored 0 → slot 1 = B",
			order:  &quizengine.Order{Options: map[uint64][]int{0: {2, 0, 1}}},
			qid:    0,
			stored: []int{0},
			want:   "B",
		},
		{
			name:   "perm [2,0,1] stored [2,1] → A, C",
			order:  &quizengine.Order{Options: map[uint64][]int{0: {2, 0, 1}}},
			qid:    0,
			stored: []int{2, 1},
			want:   "A, C",
		},
		{
			name:   "perm [2,1,0] stored 0 → slot 2 = C",
			order:  &quizengine.Order{Options: map[uint64][]int{0: {2, 1, 0}}},
			qid:    0,
			stored: []int{0},
			want:   "C",
		},
		{
			name:   "perm [2,1,0] stored [2,1] → A, B",
			order:  &quizengine.Order{Options: map[uint64][]int{0: {2, 1, 0}}},
			qid:    0,
			stored: []int{2, 1},
			want:   "A, B",
		},
		{
			name:   "nil order → identity",
			order:  nil,
			qid:    7,
			stored: []int{0, 3},
			want:   "A, D",
		},
		{
			name:   "missing perm entry → identity",
			order:  &quizengine.Order{Options: map[uint64][]int{}},
			qid:    9,
			stored: []int{1},
			want:   "B",
		},
		{
			name:   "empty input → empty string",
			order:  &quizengine.Order{Options: map[uint64][]int{0: {2, 0, 1}}},
			qid:    0,
			stored: []int{},
			want:   "",
		},
		{
			name:   "nil stored → empty string",
			order:  nil,
			qid:    0,
			stored: nil,
			want:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := quizengine.DisplayLetters(tc.order, tc.qid, tc.stored)
			if got != tc.want {
				t.Errorf("DisplayLetters(%v, %d, %v) = %q, want %q",
					tc.order, tc.qid, tc.stored, got, tc.want)
			}
			if len(tc.stored) > 0 && got == "" {
				t.Error("non-empty input must never render empty")
			}
		})
	}
}
