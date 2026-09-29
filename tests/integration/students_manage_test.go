package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"quiz/internal/handlers"
)

// Manage Akun Murid: guru-only page, full edit (username, full name, email,
// class, major), deactivate (blocks login + kills live sessions), reactivate,
// and cascade delete. Delete and deactivate confirm through the Bootstrap
// modal (data-confirm) — never a browser confirm().
func TestManageStudentAccounts(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	guru := guruLogin(t, ts)

	// --- access control: guru only ---------------------------------------
	if resp, _ := getWith(t, ts.URL+"/teacher/students"); resp.StatusCode != http.StatusFound {
		t.Errorf("anonymous students page = %d, want 302", resp.StatusCode)
	}
	stu := studentCookie(t, pool, "manage-probe")
	if resp, _ := getWith(t, ts.URL+"/teacher/students", stu); resp.StatusCode != http.StatusFound {
		t.Errorf("murid students page = %d, want 302", resp.StatusCode)
	}

	// --- seed an account with a real password + a quiz attempt ------------
	reg := registerUser(t, ts, "soni", "soni@example.test", "Soni Murid", "secret123")
	uid := userOf(t, ts, reg)
	qid := createQuiz(t, ts, guru, quizForm("Manage quiz"))
	if resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/participants/add", ts.URL, qid),
		map[string]string{"username": "soni"}, guru); resp.StatusCode != http.StatusOK {
		t.Fatalf("add participant = %d: %s", resp.StatusCode, body)
	}

	// --- page renders the account, the menu and modal-confirm actions -----
	resp, body := getWith(t, ts.URL+"/teacher/students", guru)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("students page = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		"Manage Akun Murid", "soni", "Soni Murid", "soni@example.test",
		`href="/teacher/students" aria-current="page"`,
		`data-post="`, `/delete"`, `data-confirm="Hapus akun soni`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("students page missing %q", want)
		}
	}
	if strings.Contains(body, "confirm(") {
		t.Error("students page uses browser confirm()")
	}

	editForm := func(username, nama, email, kelas, jurusan string) url.Values {
		return url.Values{
			"username": {username}, "nama_lengkap": {nama},
			"email": {email}, "kelas_id": {kelas}, "jurusan_id": {jurusan},
		}
	}
	editURL := fmt.Sprintf("%s/teacher/students/%d/edit", ts.URL, uid)

	// --- edit updates every managed field ---------------------------------
	resp, body = postForm(t, editURL,
		editForm("soni2", "Soni Baru", "soni2@example.test", "2", "2"), guru)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("edit = %d %s, want 200 ok", resp.StatusCode, body)
	}
	var username, nama, email string
	var kelasID, jurusanID int
	if err := pool.QueryRow(`SELECT username, nama_lengkap, email, kelas_id, jurusan_id
		FROM users WHERE id = ?`, uid).
		Scan(&username, &nama, &email, &kelasID, &jurusanID); err != nil {
		t.Fatalf("reread user: %v", err)
	}
	if username != "soni2" || nama != "Soni Baru" || email != "soni2@example.test" ||
		kelasID != 2 || jurusanID != 2 {
		t.Errorf("after edit: %s / %s / %s / %d / %d", username, nama, email, kelasID, jurusanID)
	}

	// --- edit guards: duplicate, unknown ref, unknown id -------------------
	registerUser(t, ts, "rival", "rival@example.test", "Rival Murid", "secret123")
	resp, body = postForm(t, editURL,
		editForm("rival", "Soni Baru", "soni2@example.test", "2", "2"), guru)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "CONFLICT") {
		t.Errorf("duplicate username = %d %s, want 409 CONFLICT", resp.StatusCode, body)
	}
	resp, body = postForm(t, editURL,
		editForm("soni2", "Soni Baru", "soni2@example.test", "9999", "2"), guru)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "VALIDATION") {
		t.Errorf("unknown class = %d %s, want 400 VALIDATION", resp.StatusCode, body)
	}
	resp, _ = postForm(t, fmt.Sprintf("%s/teacher/students/999999/edit", ts.URL),
		editForm("ghost", "Ghost", "ghost@example.test", "1", "1"), guru)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("edit unknown id = %d, want 404", resp.StatusCode)
	}

	// --- deactivate: login blocked, live session dies ----------------------
	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/students/%d/deactivate", ts.URL, uid), url.Values{}, guru)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("deactivate = %d %s, want 200 ok", resp.StatusCode, body)
	}
	resp, body = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"soni2"}, "password": {"secret123"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login as deactivated = %d, want 401", resp.StatusCode)
	}
	if msg := banner(t, body); msg != handlers.MsgLoginInactive {
		t.Errorf("deactivated login message = %q, want %q", msg, handlers.MsgLoginInactive)
	}
	if _, body := do(t, ts.URL+"/me", reg.Value); body != "anon" {
		t.Errorf("deactivated live session /me = %q, want anon", body)
	}
	resp, body = getWith(t, ts.URL+"/teacher/students", guru)
	if !strings.Contains(body, "Nonaktif") || !strings.Contains(body, "/activate") {
		t.Error("deactivated row missing Nonaktif badge or activate action")
	}

	// --- reactivate restores login -----------------------------------------
	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/students/%d/activate", ts.URL, uid), url.Values{}, guru)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("activate = %d %s, want 200 ok", resp.StatusCode, body)
	}
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"soni2"}, "password": {"secret123"}})
	if resp.StatusCode != http.StatusFound {
		t.Errorf("login after reactivate = %d, want 302", resp.StatusCode)
	}

	// --- cascade delete: user, attempts and reset rows go away -------------
	if _, err := pool.Exec(`INSERT INTO password_resets (user_id, input_username, input_nama)
		VALUES (?, 'soni2', 'Soni Baru')`, uid); err != nil {
		t.Fatalf("seed reset row: %v", err)
	}
	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/students/%d/delete", ts.URL, uid), url.Values{}, guru)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("delete = %d %s, want 200 ok", resp.StatusCode, body)
	}
	for _, q := range []struct {
		what string
		sql  string
	}{
		{"users", fmt.Sprintf(`SELECT COUNT(*) FROM users WHERE id = %d`, uid)},
		{"participants", fmt.Sprintf(`SELECT COUNT(*) FROM participants WHERE user_id = %d`, uid)},
		{"password_resets", fmt.Sprintf(`SELECT COUNT(*) FROM password_resets WHERE user_id = %d`, uid)},
		{"sessions", fmt.Sprintf(`SELECT COUNT(*) FROM sessions WHERE user_id = %d`, uid)},
	} {
		var n int
		if err := pool.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.what, err)
		}
		if n != 0 {
			t.Errorf("after delete: %s rows = %d, want 0", q.what, n)
		}
	}
	// answers of the deleted account's attempts are gone too
	var answers int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM answers a
		JOIN participants p ON p.id = a.participant_id WHERE p.user_id = ?`, uid).
		Scan(&answers); err != nil {
		t.Fatalf("count answers: %v", err)
	}
	if answers != 0 {
		t.Errorf("after delete: orphan answers = %d, want 0", answers)
	}
	resp, body = getWith(t, ts.URL+"/teacher/students", guru)
	if strings.Contains(body, "soni2") {
		t.Error("deleted account still listed")
	}
	resp, _ = getWith(t, ts.URL+"/teacher/students/999999/delete", guru) // nolint: body unused
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete unknown id = %d, want 404", resp.StatusCode)
	}
}

// The account edit form was split out of the list into its own page
// (GET /teacher/students/:id/edit): the list keeps a per-row edit link and
// the data-post row actions, but must no longer render the modal markup.
func TestStudentsListHasEditLinkWithoutModal(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	guru := guruLogin(t, ts)
	reg := registerUser(t, ts, "splitkid", "splitkid@example.test", "Split Kid", "secret123")
	uid := userOf(t, ts, reg)

	resp, body := getWith(t, ts.URL+"/teacher/students", guru)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("students page = %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, `id="student-edit-modal"`) {
		t.Error("students list still renders the edit modal")
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/teacher/students/%d/edit"`, uid)) {
		t.Errorf("students list missing the per-row edit link for user %d", uid)
	}
	if !strings.Contains(body, fmt.Sprintf(`data-post="/teacher/students/%d/delete"`, uid)) {
		t.Errorf("students list missing the row delete action for user %d", uid)
	}
}
