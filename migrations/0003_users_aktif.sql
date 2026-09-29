-- Manage Akun Murid (teacher dashboard): a student account can be
-- deactivated. aktif=0 blocks sign-in (auth.go) and any live session
-- (middleware LoadSession); deactivation also revokes the user's sessions.
-- Default 1 keeps every existing account active.
ALTER TABLE users
    ADD COLUMN aktif TINYINT(1) NOT NULL DEFAULT 1 AFTER must_change_pw;
