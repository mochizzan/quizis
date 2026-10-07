package integration

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"quiz/bootstrap"
	"quiz/internal/cache"
	"quiz/internal/config"
	"quiz/internal/handlers"
	mw "quiz/internal/middleware"
	"quiz/internal/realtime"
	"quiz/internal/testutil"
	"quiz/web"
)

// testAssets fingerprints the same embedded trees cmd/server mounts so
// rendered pages carry real ?v= fingerprints in tests (parity with
// production). The prefix list mirrors cmd/server's single-source
// staticMounts — keep the two in sync.
func testAssets(t *testing.T) map[string]string {
	t.Helper()
	mounts := []mw.StaticMount{
		{Path: "/assets", FS: echo.MustSubFS(web.FS, "vendor")},
		{Path: "/css", FS: echo.MustSubFS(web.FS, "css")},
		{Path: "/js", FS: echo.MustSubFS(web.FS, "js")},
		{Path: "/bootstrap", FS: bootstrap.FS},
	}
	assets := make(map[string]string)
	for _, m := range mounts {
		h, err := mw.Fingerprints(m.FS, m.Path)
		if err != nil {
			t.Fatalf("fingerprint %s: %v", m.Path, err)
		}
		for k, v := range h {
			assets[k] = v
		}
	}
	return assets
}

// newAuthServer wires the REAL auth routes + middleware (mirrors cmd/server).
func newAuthServer(t *testing.T, pool *sql.DB, store *cache.Store, cfg *config.Config) *httptest.Server {
	ts, _ := newServerWith(t, pool, store, cfg, &handlers.Teacher{DB: pool, Store: store})
	return ts
}

// newServerWith is newAuthServer with an injectable Teacher — the join-code
// seam the UNIQUE-collision test needs. The returned Global gives tests
// direct access to WatchOnce/Rehydrate (the loop only runs in cmd/server).
func newServerWith(t *testing.T, pool *sql.DB, store *cache.Store, cfg *config.Config, teach *handlers.Teacher) (*httptest.Server, *handlers.Global) {
	t.Helper()
	renderer, err := handlers.NewRenderer(filepath.Join("..", "..", "views"), testAssets(t))
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	e := echo.New()
	e.Renderer = renderer
	e.Use(mw.LoadSession(pool, store))

	auth := &handlers.Auth{DB: pool, Store: store, Cfg: cfg}
	e.GET("/login", auth.LoginPage)
	e.POST("/login", auth.Login)
	e.POST("/logout", auth.Logout)
	e.GET("/register", auth.RegisterPage)
	e.POST("/register", auth.Register)
	e.GET("/forgot-password", auth.ForgotPage)
	e.POST("/forgot-password", auth.Forgot)
	e.GET("/change-password", auth.ChangePasswordPage, mw.ForceChangePassword)
	e.POST("/change-password", auth.ChangePassword, mw.ForceChangePassword)

	teacher := e.Group("/teacher", mw.AuthTeacher)
	teacher.GET("/password-resets", auth.PasswordResetsPage)
	teacher.POST("/password-resets/:id/approve", auth.ApproveReset)
	teacher.POST("/password-resets/:id/reject", auth.RejectReset)

	e.GET("/teacher", teach.Dashboard, mw.AuthTeacher)
	teacher.GET("/api/overview", teach.Overview)
	teacher.GET("/classes", teach.ClassesPage)
	teacher.GET("/classes/new", teach.NewClassPage)
	teacher.POST("/classes", teach.CreateClass)
	teacher.POST("/classes/:id/delete", teach.DeleteClass)
	teacher.GET("/majors", teach.MajorsPage)
	teacher.GET("/majors/new", teach.NewMajorPage)
	teacher.POST("/majors", teach.CreateMajor)
	teacher.POST("/majors/:id/delete", teach.DeleteMajor)
	teacher.GET("/students", teach.StudentsPage)
	teacher.GET("/students/:id/edit", teach.StudentEditPage)
	teacher.POST("/students/:id/edit", teach.EditStudent)
	teacher.POST("/students/:id/activate", teach.ActivateStudent)
	teacher.POST("/students/:id/deactivate", teach.DeactivateStudent)
	teacher.POST("/students/:id/delete", teach.DeleteStudent)
	teacher.GET("/questions", teach.QuestionsPage)
	teacher.GET("/questions/new", teach.QuestionNewPage)
	teacher.POST("/questions", teach.CreateQuestion)
	teacher.GET("/questions/:id/edit", teach.QuestionEditPage)
	teacher.POST("/questions/:id/edit", teach.EditQuestion)
	teacher.POST("/questions/:id/delete", teach.DeleteQuestion)
	// chunked image upload — same wiring as cmd/server.
	teacher.POST("/uploads", teach.InitiateUpload)
	teacher.POST("/uploads/:id/chunks/:index", teach.UploadChunk)
	teacher.POST("/uploads/:id/complete", teach.CompleteUpload)
	teacher.POST("/uploads/:id/delete", teach.DeleteUpload)
	teacher.GET("/quiz", teach.QuizListPage)
	teacher.GET("/quiz/new", teach.QuizNewPage)
	teacher.POST("/quiz/new", teach.CreateQuiz)
	teacher.POST("/quiz/:id/edit", teach.EditQuiz)
	teacher.POST("/quiz/:id/questions", teach.ComposeQuestions)
	teacher.POST("/quiz/:id/questions/reorder", teach.ReorderQuestions)
	teacher.POST("/quiz/:id/questions/:qid/delete", teach.RemoveQuestion)
	teacher.POST("/quiz/:id/participants/add", teach.AddParticipant)
	teacher.POST("/quiz/:id/delete", teach.DeleteQuiz)
	teacher.GET("/quiz/:id/qr", teach.QuizQR)
	teacher.GET("/quiz/:id", teach.QuizDetailPage)
	// evaluation + export (spec §6.11)
	teacher.GET("/quiz/:id/results", teach.ResultsPage)
	teacher.GET("/quiz/:id/results/analysis", teach.ResultsAnalysisPage)
	teacher.GET("/quiz/:id/results/export", teach.ExportResults)
	teacher.GET("/quiz/:id/grading", teach.GrantingPage)
	teacher.POST("/quiz/:id/grading/:answerId", teach.GradeAnswer)

	// SSE streams — same wiring as cmd/server.
	hub, err := realtime.New()
	if err != nil {
		t.Fatalf("realtime hub: %v", err)
	}
	t.Cleanup(hub.Close)
	// one live registry shared by the student streams, the monitor and the
	// watchdog: rankers + participant heartbeats
	live := handlers.NewLive(pool)
	streams := &handlers.Streams{DB: pool, Hub: hub, Live: live}
	e.GET("/quiz/:code/stream", streams.StudentStream)
	teacher.GET("/quiz/:id/monitor/stream", streams.TeacherMonitorStream)

	// public landing + join/about — same wiring as cmd/server.
	land := &handlers.Landing{}
	e.GET("/", land.Home)
	e.GET("/join", land.JoinPage)
	e.GET("/about", land.About)

	// question image serving — bare route, in-handler gate (mirrors cmd/server).
	e.GET("/media/question/:imageId", teach.ServeQuestionImage)

	// student side — same wiring as cmd/server.
	student := &handlers.Student{DB: pool, Hub: hub, Store: store, Live: live}
	stuMW := []echo.MiddlewareFunc{mw.AuthStudent, mw.ForceChangePassword}
	e.GET("/student", student.HomePage, stuMW...)
	e.POST("/join", student.Join, stuMW...)
	e.GET("/quiz/:code", student.WorkspacePage, stuMW...)
	e.POST("/quiz/:code/start", student.StartAttempt, stuMW...)
	e.POST("/quiz/:code/answer", student.SubmitAnswer, stuMW...)
	e.POST("/quiz/:code/next", student.NextQuestion, stuMW...)
	e.POST("/quiz/:code/page", student.ReportPage, stuMW...)
	e.POST("/quiz/:code/finish", student.FinishAttempt, stuMW...)
	e.POST("/quiz/:code/visibility", student.ReportVisibility, stuMW...)
	e.GET("/history", student.HistoryPage, stuMW...)
	e.GET("/history/:id", student.HistoryDetail, stuMW...)
	e.GET("/profile", student.ProfilePage, stuMW...)
	e.POST("/profile/edit", student.EditProfile, stuMW...)

	// global-timer side — same wiring as cmd/server (watchdog/rehydrate are
	// driven explicitly by tests via WatchOnce/Rehydrate, not a loop).
	globals := handlers.NewGlobal(pool, hub, store, live)
	teacher.POST("/quiz/:id/status", globals.SetQuizStatus)
	teacher.GET("/quiz/:id/monitor", globals.MonitorPage)
	teacher.POST("/quiz/:id/start", globals.Start)
	teacher.POST("/quiz/:id/stop", globals.Stop)
	teacher.POST("/quiz/:id/participants/:pid/action", globals.ParticipantAction)

	e.GET("/me", func(c *echo.Context) error {
		s := mw.SessionFrom(c)
		if s == nil {
			return c.String(200, "anon")
		}
		return c.String(200, fmt.Sprintf("%d:%s", s.UserID, s.Role))
	})

	ts := httptest.NewServer(e)
	t.Cleanup(ts.Close)
	return ts, globals
}

// postForm issues an x-www-form-urlencoded POST without following redirects.
func postForm(t *testing.T, endpoint string, form url.Values, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == mw.CookieName && ck.Value != "" {
			return ck
		}
	}
	return nil
}

// banner extracts the server-rendered Toast message (the generic error
// message shown after a failed form post).
func banner(t *testing.T, html string) string {
	t.Helper()
	const open = `<div class="toast-body">`
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatalf("no toast message in %q", html)
	}
	rest := html[i+len(open):]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		t.Fatalf("unterminated toast body in %q", html)
	}
	return rest[:j]
}

func registerUser(t *testing.T, ts *httptest.Server, username, email, nama, password string) *http.Cookie {
	t.Helper()
	resp, body := postForm(t, ts.URL+"/register", url.Values{
		"nama_lengkap": {nama},
		"username":     {username},
		"email":        {email},
		"password":     {password},
		"kelas_id":     {"1"},
		"jurusan_id":   {"1"},
	})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("register %s = %d, want 302 (%s)", username, resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?registered=1" {
		t.Fatalf("register %s Location = %q, want /login?registered=1", username, loc)
	}
	if ck := sessionCookie(resp); ck != nil {
		t.Fatalf("register %s: session cookie set — register must not auto-login", username)
	}
	// Registration creates no session; callers get their authenticated
	// cookie through the mandated post-register sign-in.
	resp, body = postForm(t, ts.URL+"/login", url.Values{
		"identity": {username}, "password": {password},
	})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("post-register login %s = %d, want 302 (%s)", username, resp.StatusCode, body)
	}
	ck := sessionCookie(resp)
	if ck == nil {
		t.Fatalf("post-register login %s: no session cookie", username)
	}
	return ck
}

func authFixture(t *testing.T) (*httptest.Server, *config.Config) {
	t.Helper()
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users", "password_resets")
	store := cache.New()
	cfg := testutil.Config(t)
	return newAuthServer(t, pool, store, cfg), cfg
}

func TestRegisterLoginLogoutCycle(t *testing.T) {
	ts, _ := authFixture(t)

	// register page renders with seeded dropdowns
	_, body := do(t, ts.URL+"/register", "")
	if !strings.Contains(body, "Daftar") || !strings.Contains(body, "Grade 10") {
		t.Fatalf("register page missing expected content: %q", body)
	}

	c1 := registerUser(t, ts, "nina", "nina@example.test", "Nina Test", "secret123")

	// register itself never logged anyone in (registerUser pins that); the
	// session returned comes from the sign-in that follows
	if _, body := do(t, ts.URL+"/me", c1.Value); !strings.HasSuffix(body, ":murid") {
		t.Fatalf("after register /me = %q, want authenticated murid", body)
	}

	// logout revokes instantly
	resp, _ := postForm(t, ts.URL+"/logout", url.Values{}, c1)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Fatalf("logout = %d %q, want 302 /login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, body := do(t, ts.URL+"/me", c1.Value); body != "anon" {
		t.Fatalf("after logout /me = %q, want anon", body)
	}

	// login again with username
	resp, _ = postForm(t, ts.URL+"/login", url.Values{"identity": {"nina"}, "password": {"secret123"}})
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/student" {
		t.Fatalf("login = %d %q, want 302 /student", resp.StatusCode, resp.Header.Get("Location"))
	}
	c2 := sessionCookie(resp)
	if c2 == nil || c2.Value == c1.Value {
		t.Fatal("login did not issue a fresh session cookie")
	}
	if _, body := do(t, ts.URL+"/me", c2.Value); !strings.HasSuffix(body, ":murid") {
		t.Fatalf("after re-login /me = %q, want authenticated murid", body)
	}
}

// A code typed on the public /join page survives the sign-in hop: login
// consumes the pending_join cookie and lands on /join?code=...,
// clearing the cookie in the same response. Registration does NOT
// auto-login anymore: it redirects to /login?registered=1 with the cookie
// left intact, and the code is consumed at the manual sign-in that
// follows. A structurally invalid pending value is ignored (plain home
// redirect).
func TestPendingJoinCodeSurvivesSignIn(t *testing.T) {
	ts, _ := authFixture(t)
	registerUser(t, ts, "pendu", "pendu@example.test", "Pendu Test", "secret123")

	pending := &http.Cookie{Name: "pending_join", Value: "ABC234"}
	resp, _ := postForm(t, ts.URL+"/login",
		url.Values{"identity": {"pendu"}, "password": {"secret123"}}, pending)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/join?code=ABC234" {
		t.Fatalf("login Location = %q, want /join?code=ABC234", loc)
	}
	if !pendingJoinCleared(resp) {
		t.Error("login did not clear the pending_join cookie")
	}

	// structurally invalid pending code → ignored, plain home redirect
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"pendu"}, "password": {"secret123"}},
		&http.Cookie{Name: "pending_join", Value: "not a code!"})
	if loc := resp.Header.Get("Location"); loc != "/student" {
		t.Errorf("invalid pending code → Location = %q, want /student", loc)
	}

	// register leaves the cookie untouched (redirect to the login page,
	// no session) — the code survives to the manual sign-in
	resp, _ = postForm(t, ts.URL+"/register", url.Values{
		"nama_lengkap": {"Pendu Two"}, "username": {"pendu2"},
		"email": {"pendu2@example.test"}, "password": {"secret123"},
		"kelas_id": {"1"}, "jurusan_id": {"1"},
	}, pending)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("register = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?registered=1" {
		t.Errorf("register Location = %q, want /login?registered=1", loc)
	}
	if pendingJoinCleared(resp) {
		t.Error("register cleared the pending_join cookie")
	}

	// ... and the sign-in right after registration consumes it
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"pendu2"}, "password": {"secret123"}}, pending)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("post-register login = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/join?code=ABC234" {
		t.Errorf("post-register login Location = %q, want /join?code=ABC234", loc)
	}
	if !pendingJoinCleared(resp) {
		t.Error("post-register login did not clear the pending_join cookie")
	}
}

func pendingJoinCleared(resp *http.Response) bool {
	for _, ck := range resp.Cookies() {
		if ck.Name == "pending_join" && ck.Value == "" {
			return true
		}
	}
	return false
}

func TestLoginWithEmailInsteadOfUsername(t *testing.T) {
	ts, _ := authFixture(t)
	registerUser(t, ts, "omar", "omar@example.test", "Omar Test", "secret123")

	resp, _ := postForm(t, ts.URL+"/login",
		url.Values{"identity": {"omar@example.test"}, "password": {"secret123"}})
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/student" {
		t.Fatalf("email login = %d %q, want 302 /student", resp.StatusCode, resp.Header.Get("Location"))
	}
	if ck := sessionCookie(resp); ck == nil {
		t.Fatal("email login: no session cookie")
	}
}

func TestDuplicateRegistrationConflicts(t *testing.T) {
	ts, _ := authFixture(t)
	registerUser(t, ts, "rina", "rina@example.test", "Rina Test", "secret123")

	// duplicate username, different email
	resp, body := postForm(t, ts.URL+"/register", url.Values{
		"nama_lengkap": {"Other"}, "username": {"rina"},
		"email": {"other@example.test"}, "password": {"secret123"},
		"kelas_id": {"1"}, "jurusan_id": {"1"},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate username = %d, want 409", resp.StatusCode)
	}
	if !strings.Contains(body, handlers.MsgTaken) {
		t.Errorf("duplicate username banner missing %q", handlers.MsgTaken)
	}

	// duplicate email, different username
	resp, body = postForm(t, ts.URL+"/register", url.Values{
		"nama_lengkap": {"Other"}, "username": {"otherguy"},
		"email": {"rina@example.test"}, "password": {"secret123"},
		"kelas_id": {"1"}, "jurusan_id": {"1"},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate email = %d, want 409", resp.StatusCode)
	}
	if !strings.Contains(body, handlers.MsgTaken) {
		t.Errorf("duplicate email banner missing %q", handlers.MsgTaken)
	}
}

func TestLoginFailureIsOneGenericMessage(t *testing.T) {
	ts, _ := authFixture(t)
	registerUser(t, ts, "budi", "budi@example.test", "Budi Test", "secret123")

	respGhost, bodyGhost := postForm(t, ts.URL+"/login",
		url.Values{"identity": {"ghost"}, "password": {"whatever"}})
	respWrong, bodyWrong := postForm(t, ts.URL+"/login",
		url.Values{"identity": {"budi"}, "password": {"wrongpass"}})

	if respGhost.StatusCode != http.StatusUnauthorized || respWrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("statuses = %d/%d, want 401/401", respGhost.StatusCode, respWrong.StatusCode)
	}
	ghostMsg := banner(t, bodyGhost)
	wrongMsg := banner(t, bodyWrong)
	if ghostMsg != wrongMsg {
		t.Errorf("messages differ: unknown=%q wrong=%q", ghostMsg, wrongMsg)
	}
	if ghostMsg != handlers.MsgLoginFailed {
		t.Errorf("message = %q, want exactly %q", ghostMsg, handlers.MsgLoginFailed)
	}
}

func TestForgotPasswordFlow(t *testing.T) {
	ts, _ := authFixture(t)
	nama := "Sari Test"
	registerUser(t, ts, "sari", "sari@example.test", nama, "secret123")

	// mismatch kind 1: wrong full name
	resp, body := postForm(t, ts.URL+"/forgot-password",
		url.Values{"identity": {"sari"}, "nama": {"Someone Else"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong name = %d, want 400", resp.StatusCode)
	}
	msg1 := banner(t, body)

	// mismatch kind 2: unknown identity
	resp, body = postForm(t, ts.URL+"/forgot-password",
		url.Values{"identity": {"ghost"}, "nama": {"Whoever"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown identity = %d, want 400", resp.StatusCode)
	}
	msg2 := banner(t, body)

	if msg1 != msg2 {
		t.Errorf("failure messages differ: name-mismatch=%q unknown=%q", msg1, msg2)
	}
	if msg1 != handlers.MsgForgotNoMatch {
		t.Errorf("message = %q, want exactly %q", msg1, handlers.MsgForgotNoMatch)
	}

	// match → pending request created
	resp, _ = postForm(t, ts.URL+"/forgot-password",
		url.Values{"identity": {"sari"}, "nama": {nama}})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("match = %d, want 302", resp.StatusCode)
	}
	if n := pendingCount(t, ts); n != 1 {
		t.Fatalf("pending rows = %d, want 1", n)
	}

	// repeat → idempotent no-op (still one active request)
	resp, _ = postForm(t, ts.URL+"/forgot-password",
		url.Values{"identity": {"sari"}, "nama": {nama}})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("repeat match = %d, want 302", resp.StatusCode)
	}
	if n := pendingCount(t, ts); n != 1 {
		t.Fatalf("pending rows after repeat = %d, want 1", n)
	}

	// teacher sees it
	guru := guruLogin(t, ts)
	_, body = do(t, ts.URL+"/teacher/password-resets", guru.Value)
	if !strings.Contains(body, "sari") {
		t.Errorf("teacher reset list missing requester username: %q", body)
	}
}

func TestApproveResetForcesPasswordChangeAndRevokesSessions(t *testing.T) {
	ts, _ := authFixture(t)
	nama := "Tari Test"
	c1 := registerUser(t, ts, "tari", "tari@example.test", nama, "oldpass12")

	// request + approve
	postForm(t, ts.URL+"/forgot-password", url.Values{"identity": {"tari"}, "nama": {nama}})
	id := pendingID(t, ts)
	guru := guruLogin(t, ts)

	// missing temp password → 400 VALIDATION
	resp, body := postForm(t, ts.URL+"/teacher/password-resets/"+id+"/approve", url.Values{}, guru)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "VALIDATION") {
		t.Fatalf("missing temp approve = %d %q, want 400 VALIDATION", resp.StatusCode, body)
	}
	// short temp password → 400
	resp, body = postForm(t, ts.URL+"/teacher/password-resets/"+id+"/approve",
		url.Values{"temp_password": {"short"}}, guru)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short temp approve = %d %q, want 400", resp.StatusCode, body)
	}

	resp, body = postForm(t, ts.URL+"/teacher/password-resets/"+id+"/approve",
		url.Values{"temp_password": {"Temp1234"}}, guru)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("approve = %d %q, want 200 ok", resp.StatusCode, body)
	}

	// double approve → 409 CONFLICT (idempotent guard)
	resp, body = postForm(t, ts.URL+"/teacher/password-resets/"+id+"/approve",
		url.Values{"temp_password": {"Temp1234"}}, guru)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "CONFLICT") {
		t.Fatalf("double approve = %d %q, want 409 CONFLICT", resp.StatusCode, body)
	}

	// the pre-approve session is revoked by the approval itself
	if _, body := do(t, ts.URL+"/me", c1.Value); body != "anon" {
		t.Errorf("pre-approve session survived approval: /me = %q", body)
	}

	// wrong temp password → 401 (verification is never skipped)
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"tari"}, "password": {"totally-wrong"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-temp login = %d, want 401", resp.StatusCode)
	}

	// correct temp password → /change-password
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"tari"}, "password": {"Temp1234"}})
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/change-password" {
		t.Fatalf("temp login = %d %q, want 302 /change-password",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	c2 := sessionCookie(resp)
	if c2 == nil {
		t.Fatal("no session cookie on forced-change login")
	}

	// change password → fresh session, redirect home
	resp, body = postForm(t, ts.URL+"/change-password",
		url.Values{"password": {"newpass99"}, "confirm": {"newpass99"}}, c2)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/student" {
		t.Fatalf("change password = %d %q (%s), want 302 /student", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	c3 := sessionCookie(resp)
	if c3 == nil {
		t.Fatal("no fresh session cookie after password change")
	}

	// EVERY old session is dead immediately (c1 from the post-register
	// sign-in, c2 from the forced-change login)
	for name, ck := range map[string]*http.Cookie{"post-register-login-session": c1, "forced-login-session": c2} {
		if _, body := do(t, ts.URL+"/me", ck.Value); body != "anon" {
			t.Errorf("%s survived the password change: /me = %q", name, body)
		}
	}
	// the fresh session works
	if _, body := do(t, ts.URL+"/me", c3.Value); !strings.HasSuffix(body, ":murid") {
		t.Errorf("fresh session /me = %q, want authenticated murid", body)
	}

	// old password dead, new password live, no forced redirect anymore
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"tari"}, "password": {"oldpass12"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old password login = %d, want 401", resp.StatusCode)
	}
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"tari"}, "password": {"newpass99"}})
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/student" {
		t.Errorf("new password login = %d %q, want 302 /student", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestApproveResetKillsWarmSessions(t *testing.T) {
	ts, _ := authFixture(t)
	nama := "Sinta Test"
	c1 := registerUser(t, ts, "sinta", "sinta@example.test", nama, "warmold12")
	if _, body := do(t, ts.URL+"/me", c1.Value); !strings.HasSuffix(body, ":murid") {
		t.Fatalf("warm session /me = %q, want authenticated murid", body)
	}

	postForm(t, ts.URL+"/forgot-password", url.Values{"identity": {"sinta"}, "nama": {nama}})
	id := pendingID(t, ts)
	guru := guruLogin(t, ts)

	resp, body := postForm(t, ts.URL+"/teacher/password-resets/"+id+"/approve",
		url.Values{"temp_password": {"Temp1234"}}, guru)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("approve = %d %q, want 200 ok", resp.StatusCode, body)
	}

	// the warm pre-approve cookie is dead on the next request
	if _, body := do(t, ts.URL+"/me", c1.Value); body != "anon" {
		t.Errorf("warm session survived approval: /me = %q", body)
	}

	// temp-password login works and lands on /change-password
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"sinta"}, "password": {"Temp1234"}})
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/change-password" {
		t.Errorf("temp login = %d %q, want 302 /change-password",
			resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestRejectResetKeepsPasswordValidation(t *testing.T) {
	ts, _ := authFixture(t)
	nama := "Dodi Test"
	registerUser(t, ts, "dodi", "dodi@example.test", nama, "secret123")

	postForm(t, ts.URL+"/forgot-password", url.Values{"identity": {"dodi"}, "nama": {nama}})
	id := pendingID(t, ts)
	guru := guruLogin(t, ts)

	resp, body := postForm(t, ts.URL+"/teacher/password-resets/"+id+"/reject", url.Values{}, guru)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("reject = %d %q, want 200 ok", resp.StatusCode, body)
	}

	// a rejected request can no longer be approved
	resp, body = postForm(t, ts.URL+"/teacher/password-resets/"+id+"/approve",
		url.Values{"temp_password": {"Temp1234"}}, guru)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "CONFLICT") {
		t.Fatalf("approve after reject = %d %q, want 409 CONFLICT", resp.StatusCode, body)
	}

	// login still validates the password: wrong rejected, right accepted
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"dodi"}, "password": {"wrongpass"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password after reject = %d, want 401", resp.StatusCode)
	}
	resp, _ = postForm(t, ts.URL+"/login",
		url.Values{"identity": {"dodi"}, "password": {"secret123"}})
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/student" {
		t.Errorf("correct password after reject = %d %q, want 302 /student",
			resp.StatusCode, resp.Header.Get("Location"))
	}
}

// --- helpers needing the DB ---------------------------------------------

func pendingCount(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	pool := testutil.DB(t)
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM password_resets WHERE status = 'pending'`).
		Scan(&n); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return n
}

func pendingID(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	pool := testutil.DB(t)
	var id uint64
	if err := pool.QueryRow(`SELECT id FROM password_resets WHERE status = 'pending' ORDER BY id DESC LIMIT 1`).
		Scan(&id); err != nil {
		t.Fatalf("pending id: %v", err)
	}
	return strconv.FormatUint(id, 10)
}

func guruLogin(t *testing.T, ts *httptest.Server) *http.Cookie {
	t.Helper()
	cfg := testutil.Config(t)
	resp, body := postForm(t, ts.URL+"/login",
		url.Values{"identity": {cfg.GuruUser}, "password": {cfg.GuruPass}})
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/teacher" {
		t.Fatalf("guru login = %d %q, want 302 /teacher (%s)",
			resp.StatusCode, resp.Header.Get("Location"), body)
	}
	ck := sessionCookie(resp)
	if ck == nil {
		t.Fatal("guru login: no session cookie")
	}
	return ck
}
