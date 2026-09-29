package quizengine

import (
	"math/rand/v2"
)

// Order is the per-participant shuffle snapshot persisted as the
// participants.qorder JSON. It is written once at attempt start and never
// regenerated (spec §6.8): answers always store ORIGINAL option indexes, so
// display mappings always go through this snapshot.
type Order struct {
	Questions []uint64         `json:"questions"` // display order of question IDs
	Options   map[uint64][]int `json:"options"`   // questionID → permutation of original option indexes
}

// SeedFor derives the deterministic shuffle seed (plan/spec §6.8): identical
// inputs always produce the identical shuffle, so qorder snapshots reproduce
// in export and review.
func SeedFor(participantID, quizID uint64, attemptNo uint64) int64 {
	return int64(participantID)*1_000_003 + int64(quizID)*97 + int64(attemptNo)
}

// BuildOrder produces the shuffled display order for one attempt.
// Deterministic: rand.NewPCG(seed, seed>>32) and every shuffle drawn in the
// fixed question order (never map order). shuffleQuestions=false → identity
// question order (sequential mode); shuffleOptions=false → identity
// permutation for every question (spec §6.7: sequential chosen forces
// shuffle options off — the caller passes false).
func BuildOrder(seed int64, questionIDs []uint64, optionCounts map[uint64]int,
	shuffleQuestions, shuffleOptions bool) Order {

	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)>>32))

	questions := make([]uint64, len(questionIDs))
	copy(questions, questionIDs)
	if shuffleQuestions {
		rng.Shuffle(len(questions), func(i, j int) {
			questions[i], questions[j] = questions[j], questions[i]
		})
	}

	opts := make(map[uint64][]int, len(optionCounts))
	for _, qid := range questions {
		n := optionCounts[qid]
		if n <= 0 {
			continue // essay questions carry no options
		}
		perm := make([]int, n)
		for i := range perm {
			perm[i] = i
		}
		if shuffleOptions {
			rng.Shuffle(len(perm), func(i, j int) {
				perm[i], perm[j] = perm[j], perm[i]
			})
		}
		opts[qid] = perm
	}

	return Order{Questions: questions, Options: opts}
}

// LetterOf maps an original option index to its display position in the
// shuffled permutation (0 → first slot). Returns -1 if the index is not in
// the permutation.
func LetterOf(perm []int, originalIndex int) int {
	for pos, orig := range perm {
		if orig == originalIndex {
			return pos
		}
	}
	return -1
}
