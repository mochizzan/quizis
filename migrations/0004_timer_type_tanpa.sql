-- timer_type gains a third kind: tanpa_timer — the per-question flow
-- (student-started, free navigation, teacher close) without any countdown.
-- The timer_on column stays but is now derived at write time from timer_type
-- (tanpa_timer → 0); the settings form no longer reads it.
ALTER TABLE quizzes
    MODIFY timer_type ENUM('global','per_soal','tanpa_timer') NOT NULL;
