-- Quiz website initial schema — mirrors spec §5 exactly.
-- Flat schema: no foreign keys, no strict normalization, no deduplication —
-- integrity is enforced in Go. Engine InnoDB, charset utf8mb4.
-- (The schema_migrations bookkeeping table is created by the migrator, not here.)

-- References (teacher-managed dropdowns; cache TTL 60s)
CREATE TABLE ref_kelas (
    id SMALLINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    nama VARCHAR(50) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE ref_jurusan (
    id SMALLINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    nama VARCHAR(50) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Students
CREATE TABLE users (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    email VARCHAR(100) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    nama_lengkap VARCHAR(100) NOT NULL,      -- display name (English UI: "Full name")
    kelas_id SMALLINT UNSIGNED NOT NULL,
    jurusan_id SMALLINT UNSIGNED NOT NULL,
    must_change_pw TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Sessions (source of truth; in-memory mirror TTL 5 min)
CREATE TABLE sessions (
    id CHAR(43) PRIMARY KEY,                 -- crypto/rand base64url, HttpOnly cookie
    user_id INT UNSIGNED NOT NULL,
    role ENUM('guru','murid') NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_sessions_user (user_id),
    INDEX idx_sessions_exp (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Quiz
CREATE TABLE quizzes (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    code CHAR(6) NOT NULL UNIQUE,
    judul VARCHAR(150) NOT NULL,
    deskripsi TEXT NULL,
    timer_type ENUM('global','per_soal') NOT NULL,
    timer_on TINYINT(1) NOT NULL DEFAULT 1,         -- 0 = no time limit
    status ENUM('nonaktif','aktif','berjalan','selesai') NOT NULL DEFAULT 'nonaktif',
    join_mode ENUM('open','approve') NOT NULL DEFAULT 'open',
    total_seconds INT UNSIGNED NOT NULL DEFAULT 0,          -- global timeout = started_at + total
    per_question_seconds SMALLINT UNSIGNED NOT NULL DEFAULT 300,
    started_at DATETIME NULL,
    shuffle_options TINYINT(1) NOT NULL DEFAULT 0,
    shuffle_questions TINYINT(1) NOT NULL DEFAULT 0,
    show_correct_wrong TINYINT(1) NOT NULL DEFAULT 1,
    show_final_score TINYINT(1) NOT NULL DEFAULT 1,
    ranking_live TINYINT(1) NOT NULL DEFAULT 0,
    max_attempts TINYINT UNSIGNED NOT NULL DEFAULT 1,      -- global forced to 1 in code
    question_review ENUM('none','text','full') NOT NULL DEFAULT 'none',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_quiz_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Question bank (separate from quizzes → length filter + reuse)
CREATE TABLE questions (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    teks TEXT NOT NULL,
    type ENUM('pg','multi','essay') NOT NULL,
    options JSON NULL,               -- ["option text", ...]; NULL for essay
    correct JSON NULL,               -- pg: 1 (original option index); multi: [0,2]; essay: free-text key (never auto-graded)
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_q_type (type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE quiz_questions (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    quiz_id INT UNSIGNED NOT NULL,
    question_id INT UNSIGNED NOT NULL,
    seq SMALLINT UNSIGNED NOT NULL,                -- manual order
    UNIQUE KEY uq_qq (quiz_id, question_id),
    INDEX idx_qq_order (quiz_id, seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Participant per attempt (write-first → commit → broadcast)
CREATE TABLE participants (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    quiz_id INT UNSIGNED NOT NULL,
    user_id INT UNSIGNED NOT NULL,
    attempt_no TINYINT UNSIGNED NOT NULL DEFAULT 1,
    status ENUM('pending','registered','started','selesai','dikeluarkan')
               NOT NULL DEFAULT 'registered',
    qorder JSON NULL,               -- snapshot: question order + option order per student
    started_at DATETIME NULL,
    ends_at DATETIME NULL,          -- absolute per-participant clock (pause = freeze)
    remaining_seconds INT UNSIGNED NOT NULL DEFAULT 0,
    finished_at DATETIME NULL,
    score_auto DECIMAL(5,2) NULL,   -- set at finish/timeout/stop/removal
    essay_score DECIMAL(5,2) NULL,  -- NULL = awaiting teacher grading
    final_score DECIMAL(5,2) NULL,  -- = score_auto; + essay after grading
    cheating TINYINT(1) NOT NULL DEFAULT 0,        -- teacher toggle
    current_q SMALLINT UNSIGNED NULL,              -- live monitor
    current_q_since DATETIME NULL,                 -- dwell time
    UNIQUE KEY uq_part (quiz_id, user_id, attempt_no),
    INDEX idx_part_monitor (quiz_id, status),
    INDEX idx_part_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Answers (write-through per submit; idempotent upsert)
CREATE TABLE answers (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    participant_id INT UNSIGNED NOT NULL,
    question_id INT UNSIGNED NOT NULL,
    answer TEXT NULL,                -- pg/multi: JSON array of ORIGINAL option indexes e.g. [0,2]; essay: free text
    is_correct TINYINT(1) NULL,      -- NULL = essay / not yet graded
    score DECIMAL(5,2) NULL,         -- per-question (essay)
    answered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uq_ans (participant_id, question_id),
    INDEX idx_ans_part (participant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Anti-cheat log (always write DB → then push SSE)
CREATE TABLE anti_cheat_events (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    participant_id INT UNSIGNED NOT NULL,
    kind ENUM('blur','minimize','switch','sleep') NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_ac_part (participant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Student password reset (teacher-approved)
CREATE TABLE password_resets (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    user_id INT UNSIGNED NOT NULL,
    input_username VARCHAR(50) NOT NULL,
    input_nama VARCHAR(100) NOT NULL,
    status ENUM('pending','disetujui','ditolak','selesai') NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_pr_user (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
