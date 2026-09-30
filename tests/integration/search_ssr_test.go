package integration

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Search-bars → SSR conversion pins (task: unified server-side search with
// debounced live updates). ?q= (toolbar/roster), ?qq= (composed-questions
// tab) and ?bq= (bank modal) each narrow only their OWN list, in memory via
// list.go matchSearch; the GET forms echo the active params back so the
// no-JS full-page submit and the debounced fetch round-trip the same
// state. Client-side DOM filtering is gone — the server response is the
// sole source of rows.

// searchSSRFixture builds one quiz with two composed questions (qq targets),
// two bank-only questions (bq targets) and two participants (q targets).
func searchSSRFixture(t *testing.T) (*httptest.Server, *sql.DB, *http.Cookie, uint64) {
	t.Helper()
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	qid := createQuiz(t, ts, ck, quizForm("SSR search quiz"))
	composedTarget := addQuestion(t, ts, pool, ck, "Algebra basics intro", "pg",
		[]string{"a", "b"}, []int{0})
	composedOther := addQuestion(t, ts, pool, ck, "History trivia question", "pg",
		[]string{"a", "b"}, []int{0})
	compose(t, ts, ck, qid, composedTarget, composedOther)
	addQuestion(t, ts, pool, ck, "Algebra advanced drill", "essay", nil, nil)
	addQuestion(t, ts, pool, ck, "Chemistry lab safety", "essay", nil, nil)
	for _, username := range []string{"albert", "beatrix"} {
		seedUser(t, pool, username, false)
		resp, body := postJSON(t,
			fmt.Sprintf("%s/teacher/quiz/%d/participants/add", ts.URL, qid),
			map[string]string{"username": username}, ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("add participant %s = %d: %s", username, resp.StatusCode, body)
		}
	}
	return ts, pool, ck, qid
}

// composedRows extracts the questions-table body and counts its data-seq
// rows. Scoping matters: the same texts also appear in the bank modal.
func composedRows(body string) (string, int) {
	const open = `<tbody id="q-body">`
	i := strings.Index(body, open)
	if i < 0 {
		return "", -1
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, "</tbody>")
	if j < 0 {
		return "", -1
	}
	seg := rest[:j]
	return seg, strings.Count(seg, "data-seq=")
}

// bankList extracts the bank modal's list segment (from #bank-list up to
// #bank-no-match) and counts its data-add-q buttons; ok=false means the
// server rendered no list at all (search matched nothing).
func bankList(body string) (string, int, bool) {
	i := strings.Index(body, `id="bank-list"`)
	if i < 0 {
		return "", 0, false
	}
	j := strings.Index(body[i:], `id="bank-no-match"`)
	if j < 0 {
		return "", -1, false
	}
	seg := body[i : i+j]
	return seg, strings.Count(seg, "data-add-q="), true
}

// elementHidden reports the d-none state of the <p> carrying the id.
// found=false means the element is not rendered at all.
func elementHidden(body, id string) (hidden, found bool) {
	i := strings.Index(body, `id="`+id+`"`)
	if i < 0 {
		return false, false
	}
	open := strings.LastIndex(body[:i], "<p ")
	if open < 0 {
		return false, false
	}
	gt := strings.Index(body[open:], ">")
	if gt < 0 {
		return false, false
	}
	tag := body[open : open+gt+1]
	if !strings.Contains(tag, `id="`+id+`"`) {
		return false, false
	}
	return strings.Contains(tag, "d-none"), true
}

func TestSearchSSRComposedQuestionsTab(t *testing.T) {
	ts, _, ck, qid := searchSSRFixture(t)
	get := func(rawQuery string) string {
		t.Helper()
		resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d%s", ts.URL, qid, rawQuery), ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /teacher/quiz/%d%s = %d: %s", qid, rawQuery, resp.StatusCode, body)
		}
		return body
	}

	// absent param → every composed row, no-match state hidden
	_, n := composedRows(get(""))
	if n != 2 {
		t.Errorf("default composed rows = %d, want 2", n)
	}
	if hidden, found := elementHidden(get(""), "q-empty-search"); !found || !hidden {
		t.Errorf("default no-match state hidden=%v found=%v, want hidden", hidden, found)
	}

	// matching ?qq= → only matching rows; count reflects the filtered total
	seg, n := composedRows(get("?qq=Algebra"))
	if n != 1 {
		t.Errorf("qq=Algebra rows = %d, want 1", n)
	}
	if !strings.Contains(seg, "Algebra basics intro") {
		t.Error("qq=Algebra missing the matching composed question")
	}
	if strings.Contains(seg, "History trivia question") {
		t.Error("qq=Algebra still lists the non-matching composed question")
	}
	if hidden, _ := elementHidden(get("?qq=Algebra"), "q-empty-search"); !hidden {
		t.Error("qq=Algebra must keep the no-match state hidden")
	}
	if !strings.Contains(get("?qq=Algebra"), `<span id="q-count">1</span>`) {
		t.Error("q-count does not reflect the filtered total (want 1)")
	}

	// matching ?qq= → the full page still shows the OTHER tab data unfiltered:
	// the bank modal keeps every bank question (bq is absent)
	if !strings.Contains(get("?qq=Algebra"), "Chemistry lab safety") {
		t.Error("qq= must not filter the bank modal (bq absent)")
	}

	// no-match ?qq= → zero rows + SSR no-match state visible, while the
	// "no questions yet" empty state stays hidden (questions DO exist)
	body := get("?qq=zzz-no-match")
	seg, n = composedRows(body)
	if n != 0 {
		t.Errorf("qq=<nomatch> rows = %d, want 0", n)
	}
	if strings.Contains(seg, "data-seq=") {
		t.Error("qq=<nomatch> rendered a question row")
	}
	if hidden, found := elementHidden(body, "q-empty-search"); !found || hidden {
		t.Errorf("qq=<nomatch> no-match state hidden=%v found=%v, want visible", hidden, found)
	}
	if hidden, found := elementHidden(body, "q-empty"); !found || !hidden {
		t.Errorf("qq=<nomatch> 'no questions yet' state hidden=%v found=%v, want hidden", hidden, found)
	}
	if !strings.Contains(body, `<span id="q-count">0</span>`) {
		t.Error("q-count does not reflect the filtered total (want 0)")
	}

	// explicit empty ?qq= → same as absent: full list
	if _, n := composedRows(get("?qq=")); n != 2 {
		t.Errorf("qq= rows = %d, want 2", n)
	}

	// param echo: the form carries the active search back
	if !strings.Contains(get("?qq=Algebra"), `name="qq" value="Algebra"`) {
		t.Error("qq value not echoed back into the search input")
	}
}

func TestSearchSSRBankModal(t *testing.T) {
	ts, _, ck, qid := searchSSRFixture(t)
	get := func(rawQuery string) string {
		t.Helper()
		resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d%s", ts.URL, qid, rawQuery), ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /teacher/quiz/%d%s = %d: %s", qid, rawQuery, resp.StatusCode, body)
		}
		return body
	}

	// absent/empty bq → the full bank (2 composed + 2 bank-only), no-match hidden
	for _, q := range []string{"", "?bq="} {
		_, n, ok := bankList(get(q))
		if !ok || n != 4 {
			t.Errorf("bq=%q bank rows = %d ok=%v, want 4 (list present)", q, n, ok)
		}
		if hidden, found := elementHidden(get(q), "bank-no-match"); !found || !hidden {
			t.Errorf("bq=%q bank no-match hidden=%v found=%v, want hidden", q, hidden, found)
		}
	}

	// matching ?bq= → only matching bank items, no-match stays hidden
	seg, n, ok := bankList(get("?bq=Algebra"))
	if !ok || n != 2 {
		t.Fatalf("bq=Algebra bank rows = %d ok=%v, want 2", n, ok)
	}
	if !strings.Contains(seg, "Algebra basics intro") || !strings.Contains(seg, "Algebra advanced drill") {
		t.Error("bq=Algebra missing a matching bank question")
	}
	if strings.Contains(seg, "Chemistry lab safety") {
		t.Error("bq=Algebra still lists the non-matching bank question")
	}
	if hidden, _ := elementHidden(get("?bq=Algebra"), "bank-no-match"); !hidden {
		t.Error("bq=Algebra must keep the bank no-match state hidden")
	}

	// no-match ?bq= → no list rendered + SSR no-match state visible
	body := get("?bq=zzz-no-match")
	if _, _, ok := bankList(body); ok {
		t.Error("bq=<nomatch> still renders #bank-list")
	}
	if hidden, found := elementHidden(body, "bank-no-match"); !found || hidden {
		t.Errorf("bq=<nomatch> no-match hidden=%v found=%v, want visible", hidden, found)
	}
	// ...but the composed-questions tab is untouched by bq
	if _, n := composedRows(body); n != 2 {
		t.Errorf("bq= must not filter the questions tab (rows = %d, want 2)", n)
	}

	// param echo: the bank form carries the active search back
	if !strings.Contains(get("?bq=Chem"), `name="bq" value="Chem"`) {
		t.Error("bq value not echoed back into the bank search input")
	}
}

func TestSearchSSRCrossContamination(t *testing.T) {
	ts, _, ck, qid := searchSSRFixture(t)
	url := fmt.Sprintf("%s/teacher/quiz/%d?q=albert&qq=Algebra", ts.URL, qid)
	resp, body := getWith(t, url, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET cross params = %d: %s", resp.StatusCode, body)
	}

	// roster rows respond to q= (their OWN param): albert in, beatrix out
	if !strings.Contains(body, "albert") {
		t.Error("q=albert missing the matching participant")
	}
	if strings.Contains(body, "beatrix") {
		t.Error("q=albert still lists beatrix")
	}
	// q= must not touch the questions tab: with qq=Algebra the composed
	// table still shows its OWN match (it would be 0 rows if q leaked in)
	seg, n := composedRows(body)
	if n != 1 {
		t.Errorf("composed rows with q=albert&qq=Algebra = %d, want 1", n)
	}
	if !strings.Contains(seg, "Algebra basics intro") {
		t.Error("questions tab lost its qq=Algebra match under q=")
	}
	if strings.Contains(seg, "History trivia question") {
		t.Error("questions tab leaked a non-matching row under q=")
	}
	// qq= must not touch the roster: albert's row survives (it would be
	// empty if qq leaked in) and both search forms round-trip their params
	if !strings.Contains(body, `name="q" value="albert"`) {
		t.Error("toolbar q value not echoed back")
	}
	if !strings.Contains(body, `name="qq" value="Algebra"`) {
		t.Error("questions qq value not echoed back")
	}
	// hidden preservation: each form keeps the OTHER tab's params
	if !strings.Contains(body, `<input type="hidden" name="q" value="albert">`) {
		t.Error("questions/bank search form does not preserve q")
	}
	if !strings.Contains(body, `<input type="hidden" name="qq" value="Algebra">`) {
		t.Error("bank search form does not preserve qq")
	}
}

func TestSearchSSRHostileInputs(t *testing.T) {
	ts, _, ck, qid := searchSSRFixture(t)
	get := func(rawQuery string) (int, string) {
		t.Helper()
		resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d%s", ts.URL, qid, rawQuery), ck)
		return resp.StatusCode, body
	}

	// hostile ?qq= payloads: 200, escaped echo, no unescaped <script> in
	// the body, and the "empty as appropriate" no-match state
	script := "<script>alert(1)</script>"
	cases := []struct {
		raw     string // already query-escaped
		value   string // expected escaped echo inside value="…"
		wantRaw bool   // raw payload must NOT appear in the body
	}{
		{"%3Cscript%3Ealert(1)%3C/script%3E", "&lt;script&gt;alert(1)&lt;/script&gt;", true},
		{"%27", "&#39;", false},
		{"%22", "&#34;", false},
	}
	for _, c := range cases {
		code, body := get("?qq=" + c.raw)
		if code != http.StatusOK {
			t.Errorf("qq=%s status = %d, want 200", c.raw, code)
			continue
		}
		if !strings.Contains(body, `name="qq" value="`+c.value+`"`) {
			t.Errorf("qq=%s escaped echo %q missing", c.raw, c.value)
		}
		if c.wantRaw && strings.Contains(body, script) {
			t.Errorf("qq=%s leaked an unescaped <script> tag into the body", c.raw)
		}
		if hidden, found := elementHidden(body, "q-empty-search"); !found || hidden {
			t.Errorf("qq=%s no-match hidden=%v found=%v, want visible", c.raw, hidden, found)
		}
		if _, n := composedRows(body); n != 0 {
			t.Errorf("qq=%s rows = %d, want 0", c.raw, n)
		}
	}

	// hostile ?q= on the roster: 200, escaped echo into the toolbar input,
	// the roster empties out while the questions tab stays full
	code, body := get("?q=" + url.QueryEscape("' OR '1'='1'"))
	if code != http.StatusOK {
		t.Fatalf("q=<injection> status = %d, want 200", code)
	}
	if !strings.Contains(body, `name="q" value="&#39; OR &#39;1&#39;=&#39;1&#39;"`) {
		t.Error("toolbar q value not echoed back escaped")
	}
	if strings.Contains(body, `value="' OR '1'='1'"`) {
		t.Error("toolbar q echoed an unescaped quote payload")
	}
	if !strings.Contains(body, "Tidak ada hasil yang cocok dengan pencarian Anda.") {
		t.Error("roster did not empty out for a non-matching q")
	}
	if _, n := composedRows(body); n != 2 {
		t.Errorf("hostile q leaked into the questions tab (rows = %d, want 2)", n)
	}
}
