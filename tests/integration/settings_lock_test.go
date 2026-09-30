package integration

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// S3 — settings lock: the detail page renders the Settings tab disabled
// (and the pane read-only, no Save button) for every status but nonaktif,
// while EditQuiz's status guard rejects saves on the locked statuses with
// the pinned conflict message.

// settingsLockQuiz creates a quiz with one composed question, ready to
// activate.
func settingsLockQuiz(t *testing.T, ts *httptest.Server, pool *sql.DB,
	ck *http.Cookie, title string,
) uint64 {
	t.Helper()
	qid := createQuiz(t, ts, ck, quizForm(title))
	q := addQuestion(t, ts, pool, ck, "Settings lock question?", "pg",
		[]string{"A", "B"}, []int{0})
	compose(t, ts, ck, qid, q)
	return qid
}

// assertSettingsLockedEdit posts the settings form and pins the 409
// contract: code CONFLICT + the exact locked message (wording also pinned
// by quiz_crud_test.go, compose_remove_test.go and reorder_test.go —
// never reword it).
func assertSettingsLockedEdit(t *testing.T, ts *httptest.Server, ck *http.Cookie,
	qid uint64, stage string,
) {
	t.Helper()
	resp, body := postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/edit", ts.URL, qid),
		quizForm("Changed while "+stage), ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("edit %s = %d, want 409 (%s)", stage, resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if env.Error != "CONFLICT" {
		t.Errorf("error code = %q, want CONFLICT", env.Error)
	}
	const want = "Kuis memiliki peserta atau sedang aktif — pengubahan dikunci."
	if env.Message != want {
		t.Errorf("message = %q, want %q", env.Message, want)
	}
}

// E6: settings POST on a RUNNING quiz → 409 (aktif is already pinned by
// quiz_crud_test.go's TestEditLockedAfterActivation; berjalan was not).
func TestSettingsLockEditRejectedWhenRunning(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	qid := settingsLockQuiz(t, ts, pool, ck, "Running lock quiz")
	if resp, body := setStatus(t, ts, ck, qid, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, qid)
	if got := quizStatus(t, pool, qid); got != "berjalan" {
		t.Fatalf("quiz status = %s, want berjalan", got)
	}
	assertSettingsLockedEdit(t, ts, ck, qid, "running")
}

// E4/E6: settings POST on a FINISHED quiz → 409 (selesai was not pinned).
func TestSettingsLockEditRejectedWhenFinished(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	qid := settingsLockQuiz(t, ts, pool, ck, "Finished lock quiz")
	if resp, body := setStatus(t, ts, ck, qid, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, qid)
	resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/stop", ts.URL, qid),
		map[string]bool{"confirm": true}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop = %d: %s", resp.StatusCode, body)
	}
	if got := quizStatus(t, pool, qid); got != "selesai" {
		t.Fatalf("quiz status = %s, want selesai", got)
	}
	assertSettingsLockedEdit(t, ts, ck, qid, "finished")
}

// E5: the rendered detail page carries the disabled Settings-tab marker,
// the pane lock marker and no Save button for aktif/selesai — while a
// nonaktif quiz keeps the tab exactly as it was (fully interactive).
func TestSettingsLockRendersDisabledSettingsTab(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	qid := settingsLockQuiz(t, ts, pool, ck, "SSR lock quiz")

	const (
		disabledTab = `data-bs-target="#tab-settings" type="button" role="tab" disabled aria-disabled="true"`
		lockTitle   = `title="Pengaturan terkunci setelah kuis diaktifkan."`
		paneLocked  = `data-settings-locked="true"`
	)
	getDetail := func(stage string) string {
		t.Helper()
		resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d", ts.URL, qid), ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET detail (%s) = %d: %s", stage, resp.StatusCode, body)
		}
		return body
	}
	assertLocked := func(body, stage string) {
		t.Helper()
		if !contains(body, disabledTab) {
			t.Errorf("%s: disabled Settings tab marker missing", stage)
		}
		if !contains(body, lockTitle) {
			t.Errorf("%s: settings lock tooltip missing", stage)
		}
		if !contains(body, paneLocked) {
			t.Errorf("%s: data-settings-locked pane marker missing", stage)
		}
		if contains(body, "Simpan pengaturan") {
			t.Errorf("%s: Simpan pengaturan button still rendered", stage)
		}
	}

	// nonaktif: no lock anywhere, Save button present
	body := getDetail("nonaktif")
	if contains(body, disabledTab) || contains(body, paneLocked) {
		t.Errorf("nonaktif detail carries settings lock markers")
	}
	if !contains(body, "Simpan pengaturan") {
		t.Errorf("nonaktif detail missing the Simpan pengaturan button")
	}

	// activate → aktif: locked render
	if resp, b := setStatus(t, ts, ck, qid, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, b)
	}
	assertLocked(getDetail("aktif"), "aktif")

	// start → stop → selesai: locked render
	startGlobal(t, ts, ck, qid)
	resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/stop", ts.URL, qid),
		map[string]bool{"confirm": true}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop = %d: %s", resp.StatusCode, body)
	}
	if got := quizStatus(t, pool, qid); got != "selesai" {
		t.Fatalf("quiz status = %s, want selesai", got)
	}
	assertLocked(getDetail("selesai"), "selesai")
}
