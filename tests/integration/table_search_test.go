package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The quiz list is the exemplar for the shared table UX: ?q= (search),
// the page's filter params and ?page= COMBINE (list.go), pagination links
// preserve search+filter, and out-of-range pages clamp to the last one.

func TestQuizListSearchFilterPagination(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	// 12 quizzes → 2 pages at TablePerPage=10; 8 Alpha, 4 Bravo,
	// 3 Alpha flipped to aktif through the real status endpoint
	// (activation requires at least one question).
	var alpha []uint64
	for i := 1; i <= 8; i++ {
		alpha = append(alpha, createQuiz(t, ts, ck, quizForm(fmt.Sprintf("Alpha %02d", i))))
	}
	for i := 1; i <= 4; i++ {
		createQuiz(t, ts, ck, quizForm(fmt.Sprintf("Bravo %02d", i)))
	}
	for _, id := range alpha[:3] {
		qid := addQuestion(t, ts, pool, ck, "Soal pertama", "pg", []string{"A", "B"}, []int{0})
		compose(t, ts, ck, id, qid)
		resp, body := setStatus(t, ts, ck, id, "aktif")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("activate quiz %d = %d: %s", id, resp.StatusCode, body)
		}
	}

	get := func(rawQuery string) string {
		t.Helper()
		resp, body := getWith(t, ts.URL+"/teacher/quiz"+rawQuery, ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /teacher/quiz%s = %d: %s", rawQuery, resp.StatusCode, body)
		}
		return body
	}
	rows := func(body string) int { return strings.Count(body, `data-post="/teacher/quiz/`) }

	// default: first page only (10 of 12), count line, pagination links
	home := get("")
	if n := rows(home); n != 10 {
		t.Errorf("default page rows = %d, want 10", n)
	}
	if !strings.Contains(home, "Showing 1&#x2013;10 of 12 results") &&
		!strings.Contains(home, "Showing 1–10 of 12 results") {
		t.Errorf("count line missing: Showing 1–10 of 12 results")
	}
	if !strings.Contains(home, `class="pagination `) &&
		!strings.Contains(home, `class="pagination"`) {
		t.Error("pagination component missing")
	}
	if !strings.Contains(home, `href="/teacher/quiz?page=2"`) {
		t.Error("page 2 link missing")
	}

	// search narrows rows and the count line
	search := get("?q=Alpha")
	if n := rows(search); n != 8 {
		t.Errorf("q=Alpha rows = %d, want 8", n)
	}
	if !strings.Contains(search, "of 8 results") {
		t.Error("count line does not reflect the search (of 8 results)")
	}
	if strings.Contains(search, "Bravo") {
		t.Error("q=Alpha still lists Bravo quizzes")
	}

	// filter alone
	if n := rows(get("?status=aktif")); n != 3 {
		t.Errorf("status=aktif rows = %d, want 3", n)
	}

	// search + filter COMBINE — neither overwrites the other
	both := get("?q=Alpha&status=aktif")
	if n := rows(both); n != 3 {
		t.Errorf("q=Alpha&status=aktif rows = %d, want 3", n)
	}
	if !strings.Contains(both, "of 3 results") {
		t.Error("combined count line missing (of 3 results)")
	}
	if strings.Contains(both, "Bravo") {
		t.Error("combined query leaked Bravo quizzes")
	}

	// search form round-trips the active filter as a hidden input
	if !strings.Contains(both, `<input type="hidden" name="status" value="aktif">`) {
		t.Error("search form does not preserve the status filter")
	}

	// pagination links preserve the search term
	paged := get("?q=a")
	if !strings.Contains(paged, `href="/teacher/quiz?page=2&amp;q=a"`) &&
		!strings.Contains(paged, `href="/teacher/quiz?page=2&q=a"`) {
		t.Error("pagination link lost the q param")
	}

	// out-of-range page clamps to the last page instead of showing nothing
	last := get("?page=999")
	if n := rows(last); n != 2 {
		t.Errorf("page=999 rows = %d, want 2 (clamped to last page)", n)
	}
	if !strings.Contains(last, "Showing 11") {
		t.Error("page=999 did not clamp to the last page")
	}
}

// Part 1 + Part 2: deleting from the detail page must navigate back to the
// list (the old behavior reloaded the dead URL → 404), and the list gets its
// own row delete behind the shared Bootstrap confirm modal.
func TestQuizDeleteRedirectAndRowConfirm(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	id := createQuiz(t, ts, ck, quizForm("Hapus Saya"))
	idStr := fmt.Sprintf("%d", id)

	// detail: the delete form carries data-next → JS navigates to the list
	resp, detail := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d", ts.URL, id), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET detail = %d", resp.StatusCode)
	}
	want := `data-fetch data-confirm="Delete this quiz?" data-success="Quiz &quot;Hapus Saya&quot; deleted successfully." data-next="/teacher/quiz"`
	if !strings.Contains(detail, want) {
		t.Errorf("detail delete form missing %s", want)
	}

	// list: row-level delete behind the modal confirm, informative message
	resp, list := getWith(t, ts.URL+"/teacher/quiz", ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list = %d", resp.StatusCode)
	}
	for _, want := range []string{
		`data-post="/teacher/quiz/` + idStr + `/delete"`,
		`data-confirm="Hapus quiz &quot;Hapus Saya&quot;?`,
		`data-success="Quiz &quot;Hapus Saya&quot; deleted successfully."`,
	} {
		if !strings.Contains(list, want) {
			t.Errorf("list row delete missing %s", want)
		}
	}
	if strings.Contains(list, "confirm(") {
		t.Error("list uses browser confirm()")
	}

	// the delete itself still works from the list (server truth afterwards)
	resp, body := postForm(t, fmt.Sprintf("%s/teacher/quiz/%d/delete", ts.URL, id),
		url.Values{}, ck)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("POST delete = %d: %s", resp.StatusCode, body)
	}
	resp, _ = getWith(t, fmt.Sprintf("%s/teacher/quiz/%d", ts.URL, id), ck)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET deleted quiz = %d, want 404 (server truth)", resp.StatusCode)
	}
	resp, list = getWith(t, ts.URL+"/teacher/quiz", ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list after delete = %d", resp.StatusCode)
	}
	if strings.Contains(list, "Hapus Saya") {
		t.Error("deleted quiz still listed")
	}
}
