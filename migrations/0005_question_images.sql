-- One optional image per bank question. Separate table: questions without a
-- row stay valid and existing rows are untouched (forward-only CREATE).
-- question_id is a LOGICAL reference — no FOREIGN KEY (spec §5: integrity is
-- enforced in Go; tests/db/db_test.go requires zero FKs schema-wide).
-- The UNIQUE key enforces one image per question (1:1) and is the lookup index.
CREATE TABLE question_images (
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    question_id INT UNSIGNED NOT NULL,        -- logical ref → questions.id (FK in Go)
    filename VARCHAR(255) NOT NULL,           -- stored filename on disk
    path VARCHAR(500) NOT NULL,               -- relative path under the uploads root
    byte_size INT UNSIGNED NOT NULL,          -- file size in bytes
    mime_type VARCHAR(100) NOT NULL,          -- image/png | image/jpeg | image/webp | image/gif
    sha256 CHAR(64) NOT NULL,                 -- lowercase hex digest (integrity)
    active TINYINT(1) NOT NULL DEFAULT 1,     -- 0 = hidden, file kept; never indexed
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uq_qi_question (question_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
