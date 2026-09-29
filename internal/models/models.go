// Package models defines structs mirroring the schema in spec §5 exactly.
// Nullable columns use sql.NullX variants; field names are CamelCase with a
// `db` tag naming the column for documentation.
package models

import (
	"database/sql"
	"time"
)

// User is a row of `users`.
type User struct {
	ID           uint64    `db:"id"`
	Username     string    `db:"username"`
	Email        string    `db:"email"`
	PasswordHash string    `db:"password_hash"`
	NamaLengkap  string    `db:"nama_lengkap"`
	KelasID      uint16    `db:"kelas_id"`
	JurusanID    uint16    `db:"jurusan_id"`
	MustChangePW bool      `db:"must_change_pw"`
	Aktif        bool      `db:"aktif"` // 0 = deactivated: cannot sign in
	CreatedAt    time.Time `db:"created_at"`
}

// Session is a row of `sessions` (source of truth for auth; mirror TTL 5 min).
type Session struct {
	ID        string    `db:"id"`
	UserID    uint64    `db:"user_id"`
	Role      string    `db:"role"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
}

// Quiz is a row of `quizzes`.
type Quiz struct {
	ID                 uint64         `db:"id"`
	Code               string         `db:"code"`
	Judul              string         `db:"judul"`
	Deskripsi          sql.NullString `db:"deskripsi"`
	TimerType          string         `db:"timer_type"` // global | per_soal | tanpa_timer
	TimerOn            bool           `db:"timer_on"`
	Status             string         `db:"status"` // nonaktif | aktif | berjalan | selesai
	JoinMode           string         `db:"join_mode"`
	TotalSeconds       uint32         `db:"total_seconds"`
	PerQuestionSeconds uint16         `db:"per_question_seconds"`
	StartedAt          sql.NullTime   `db:"started_at"`
	ShuffleOptions     bool           `db:"shuffle_options"`
	ShuffleQuestions   bool           `db:"shuffle_questions"`
	ShowCorrectWrong   bool           `db:"show_correct_wrong"`
	ShowFinalScore     bool           `db:"show_final_score"`
	RankingLive        bool           `db:"ranking_live"`
	MaxAttempts        uint8          `db:"max_attempts"`
	QuestionReview     string         `db:"question_review"` // none | text | full
	CreatedAt          time.Time      `db:"created_at"`
}

// Question is a row of the question bank `questions`.
type Question struct {
	ID        uint64         `db:"id"`
	Teks      string         `db:"teks"`
	Type      string         `db:"type"` // pg | multi | essay
	Options   sql.NullString `db:"options"`
	Correct   sql.NullString `db:"correct"`
	CreatedAt time.Time      `db:"created_at"`
}

// QuizQuestion is a row of `quiz_questions` (a quiz composing bank questions).
type QuizQuestion struct {
	ID         uint64 `db:"id"`
	QuizID     uint64 `db:"quiz_id"`
	QuestionID uint64 `db:"question_id"`
	Seq        uint16 `db:"seq"`
}

// Participant is a row of `participants` (one per attempt).
type Participant struct {
	ID               uint64          `db:"id"`
	QuizID           uint64          `db:"quiz_id"`
	UserID           uint64          `db:"user_id"`
	AttemptNo        uint8           `db:"attempt_no"`
	Status           string          `db:"status"` // pending | registered | started | selesai | dikeluarkan
	QOrder           sql.NullString  `db:"qorder"`
	StartedAt        sql.NullTime    `db:"started_at"`
	EndsAt           sql.NullTime    `db:"ends_at"`
	RemainingSeconds uint32          `db:"remaining_seconds"`
	FinishedAt       sql.NullTime    `db:"finished_at"`
	ScoreAuto        sql.NullFloat64 `db:"score_auto"`
	EssayScore       sql.NullFloat64 `db:"essay_score"`
	FinalScore       sql.NullFloat64 `db:"final_score"`
	Cheating         bool            `db:"cheating"`
	CurrentQ         sql.NullInt64   `db:"current_q"`
	CurrentQSince    sql.NullTime    `db:"current_q_since"`
}

// Answer is a row of `answers` (idempotent upsert on participant+question).
type Answer struct {
	ID            uint64          `db:"id"`
	ParticipantID uint64          `db:"participant_id"`
	QuestionID    uint64          `db:"question_id"`
	Answer        sql.NullString  `db:"answer"`
	IsCorrect     sql.NullBool    `db:"is_correct"`
	Score         sql.NullFloat64 `db:"score"`
	AnsweredAt    time.Time       `db:"answered_at"`
}

// AntiCheatEvent is a row of `anti_cheat_events`.
type AntiCheatEvent struct {
	ID            uint64    `db:"id"`
	ParticipantID uint64    `db:"participant_id"`
	Kind          string    `db:"kind"` // blur | minimize | switch | sleep
	CreatedAt     time.Time `db:"created_at"`
}

// PasswordReset is a row of `password_resets`.
type PasswordReset struct {
	ID            uint64    `db:"id"`
	UserID        uint64    `db:"user_id"`
	InputUsername string    `db:"input_username"`
	InputNama     string    `db:"input_nama"`
	Status        string    `db:"status"` // pending | disetujui | ditolak | selesai
	CreatedAt     time.Time `db:"created_at"`
}

// RefKelas is a row of `ref_kelas` (class dropdown).
type RefKelas struct {
	ID   uint16 `db:"id"`
	Nama string `db:"nama"`
}

// RefJurusan is a row of `ref_jurusan` (major dropdown).
type RefJurusan struct {
	ID   uint16 `db:"id"`
	Nama string `db:"nama"`
}
