package cache

import (
	"strconv"
	"time"
)

// TTLs for every mirror in the spec §6.2 table. The app config mirror is the
// *config.Config value itself (process lifetime, set once at boot).
const (
	TTLSession   = 5 * time.Minute  // session:<id>
	TTLQuizState = 3 * time.Second  // quizstate:<quizID>
	TTLQuizList  = 10 * time.Second // quizlist:all
	TTLBank      = 10 * time.Second // bank:<length>
	TTLHistory   = 10 * time.Second // history:<userID>
	TTLQuizSet   = 30 * time.Second // quizset:<quizID>
	TTLRefs      = 60 * time.Second // refs:kelas, refs:jurusan
)

// Key builders — one function per mirror row so invalidation callers and
// producers cannot drift apart.
func SessionKey(id string) string       { return "session:" + id }
func QuizStateKey(quizID uint64) string { return "quizstate:" + strconv.FormatUint(quizID, 10) }
func QuizListKey() string               { return "quizlist:all" }
func BankKey(length string) string      { return "bank:" + length }
func HistoryKey(userID uint64) string   { return "history:" + strconv.FormatUint(userID, 10) }
func QuizSetKey(quizID uint64) string   { return "quizset:" + strconv.FormatUint(quizID, 10) }
func RefsKey(kind string) string        { return "refs:" + kind } // "kelas" | "jurusan"
