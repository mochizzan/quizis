package unit

import (
	"reflect"
	"sort"
	"testing"

	"quiz/internal/quizengine"
)

func testQuestions(n int) []uint64 {
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	return ids
}

func testCounts(ids []uint64) map[uint64]int {
	m := make(map[uint64]int, len(ids))
	for _, id := range ids {
		if id%2 == 0 {
			m[id] = 4 // even-numbered questions are multiple choice
		}
		// odd-numbered questions: essays, no options
	}
	return m
}

func TestBuildOrderSameSeedIdentical(t *testing.T) {
	ids := testQuestions(20)
	counts := testCounts(ids)

	a := quizengine.BuildOrder(42, ids, counts, true, true)
	b := quizengine.BuildOrder(42, ids, counts, true, true)
	if !reflect.DeepEqual(a, b) {
		t.Error("same seed must reproduce an identical order")
	}

	// different seed → different shuffle (fixed seeds: deterministic result)
	c := quizengine.BuildOrder(43, ids, counts, true, true)
	if reflect.DeepEqual(a, c) {
		t.Error("different seeds must produce different orders")
	}
}

func TestBuildOrderIdentityWhenShuffleOff(t *testing.T) {
	ids := testQuestions(20)
	counts := testCounts(ids)

	// sequential mode: shuffleQuestions=false forces identity question order;
	// the caller must also pass shuffleOptions=false (spec §6.7).
	o := quizengine.BuildOrder(7, ids, counts, false, false)
	if !reflect.DeepEqual(o.Questions, ids) {
		t.Errorf("question order not identity: %v", o.Questions)
	}
	for qid, n := range counts {
		want := make([]int, n)
		for i := range want {
			want[i] = i
		}
		if !reflect.DeepEqual(o.Options[qid], want) {
			t.Errorf("option perm for q%d = %v, want identity %v", qid, o.Options[qid], want)
		}
	}
}

func TestBuildOrderOptionsArePermutations(t *testing.T) {
	ids := testQuestions(30)
	counts := testCounts(ids)

	for _, seed := range []int64{1, 99, 123456789} {
		o := quizengine.BuildOrder(seed, ids, counts, true, true)
		if len(o.Questions) != len(ids) {
			t.Fatalf("seed %d: dropped questions", seed)
		}
		for qid, n := range counts {
			perm := append([]int(nil), o.Options[qid]...)
			if len(perm) != n {
				t.Fatalf("seed %d q%d: perm length %d, want %d", seed, qid, len(perm), n)
			}
			sort.Ints(perm)
			for i, v := range perm {
				if v != i {
					t.Errorf("seed %d q%d: %v is not a permutation of 0..%d", seed, qid, perm, n-1)
					break
				}
			}
		}
	}
}

func TestBuildOrderExcludesEssays(t *testing.T) {
	ids := []uint64{1, 2}
	counts := map[uint64]int{1: 4, 2: 0} // q2 is an essay
	o := quizengine.BuildOrder(5, ids, counts, true, true)
	if _, ok := o.Options[2]; ok {
		t.Error("essay question must not appear in the options map")
	}
	if len(o.Options[1]) != 4 {
		t.Errorf("mcq options = %v, want 4 entries", o.Options[1])
	}
}

func TestBuildOrderOnlyShufflingQuestions(t *testing.T) {
	ids := testQuestions(15)
	counts := testCounts(ids)
	o := quizengine.BuildOrder(11, ids, counts, true, false)
	if reflect.DeepEqual(o.Questions, ids) {
		t.Error("shuffleQuestions=true should reorder questions")
	}
	for qid, n := range counts {
		for i, v := range o.Options[qid] {
			if v != i {
				t.Errorf("shuffleOptions=false → q%d must be identity, got %v", qid, o.Options[qid])
				_ = n
				break
			}
		}
	}
}

func TestLetterOf(t *testing.T) {
	perm := []int{2, 0, 1} // display slot 0 shows original option 2
	if got := quizengine.LetterOf(perm, 2); got != 0 {
		t.Errorf("LetterOf(2) = %d, want 0", got)
	}
	if got := quizengine.LetterOf(perm, 0); got != 1 {
		t.Errorf("LetterOf(0) = %d, want 1", got)
	}
	if got := quizengine.LetterOf(perm, 1); got != 2 {
		t.Errorf("LetterOf(1) = %d, want 2", got)
	}
	if got := quizengine.LetterOf(perm, 9); got != -1 {
		t.Errorf("LetterOf(9) = %d, want -1", got)
	}
}

func TestSeedForFormula(t *testing.T) {
	// participantID*1_000_003 + quizID*97 + attemptNo
	got := quizengine.SeedFor(5, 3, 1)
	if want := int64(5)*1_000_003 + int64(3)*97 + 1; got != want {
		t.Errorf("SeedFor(5,3,1) = %d, want %d", got, want)
	}
	// same inputs → same seed (shuffle reproducible per participant+attempt)
	if quizengine.SeedFor(9, 4, 2) != quizengine.SeedFor(9, 4, 2) {
		t.Error("SeedFor must be deterministic")
	}
	// different attempts → different seed
	if quizengine.SeedFor(9, 4, 1) == quizengine.SeedFor(9, 4, 2) {
		t.Error("attempt number must change the seed")
	}
}
