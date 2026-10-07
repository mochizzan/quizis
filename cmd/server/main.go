package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"quiz/bootstrap"
	"quiz/internal/cache"
	"quiz/internal/config"
	"quiz/internal/db"
	"quiz/internal/handlers"
	mw "quiz/internal/middleware"
	"quiz/internal/realtime"
	"quiz/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = ".env"
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	pool, err := db.Open(cfg.DSN(cfg.DBName))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(pool); err != nil {
		return err
	}

	// staticMounts is the single source for the static asset locations: the
	// mount calls below, the ?v= fingerprint registry and the cache
	// middleware all derive from it, so the prefix lists cannot drift apart.
	staticMounts := []mw.StaticMount{
		{Path: "/assets", FS: echo.MustSubFS(web.FS, "vendor")}, // vendor/ → /assets (theme.css, bootstrap-icons/, chartjs/ Chart.js v4.5.1 MIT vendored)
		{Path: "/css", FS: echo.MustSubFS(web.FS, "css")},       // css/ → /css (app.css)
		{Path: "/js", FS: echo.MustSubFS(web.FS, "js")},         // js/ → /js (ui.js, teacher.js, …)
		{Path: "/bootstrap", FS: bootstrap.FS},                  // bootstrap/ → /bootstrap (offline 5.3, no CDN/npm)
	}
	assets := make(map[string]string)
	for _, m := range staticMounts {
		h, err := mw.Fingerprints(m.FS, m.Path)
		if err != nil {
			return fmt.Errorf("fingerprint %s: %w", m.Path, err)
		}
		for k, v := range h {
			assets[k] = v
		}
	}

	renderer, err := handlers.NewRenderer("views", assets)
	if err != nil {
		return err
	}

	store := cache.New()
	if err := db.DeleteExpired(context.Background(), pool, time.Now()); err != nil {
		return fmt.Errorf("session GC: %w", err)
	}
	auth := &handlers.Auth{DB: pool, Store: store, Cfg: cfg}

	e := echo.New()
	e.Renderer = renderer
	e.Use(middleware.Recover())
	e.Use(mw.StaticCache(staticMounts, assets))
	e.Use(mw.LoadSession(pool, store))

	// static asset mounts (single source: staticMounts above) — served under
	// the mw.StaticCache policy: fingerprinted ?v= URLs are immutable,
	// everything else ends up no-store (spec: client-side caching).
	for _, m := range staticMounts {
		e.StaticFS(m.Path, m.FS)
		// echo registers GET only (StaticFS → e.Add(http.MethodGet, …)),
		// which made `curl -I` (HEAD) a 405. Mirror HEAD on the same mount
		// with echo's own exported handler — identical semantics, and
		// http.ServeContent already suppresses the HEAD body natively.
		// disablePathUnescaping=true matches StaticFS's default (!enable…
		// from echo.New).
		e.HEAD(m.Path+"*", echo.StaticDirectoryHandler(m.FS, true))
	}

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

	teach := &handlers.Teacher{DB: pool, Store: store}

	hub, err := realtime.New()
	if err != nil {
		return fmt.Errorf("realtime hub: %w", err)
	}
	defer hub.Close()
	// one live registry shared by the student streams, the monitor and the
	// watchdog: rankers + participant heartbeats
	live := handlers.NewLive(pool)
	streams := &handlers.Streams{DB: pool, Hub: hub, Live: live}
	// status route (activation + per-question close) needs this too
	globals := handlers.NewGlobal(pool, hub, store, live)

	// SSE streams (spec §6.3)
	e.GET("/quiz/:code/stream", streams.StudentStream)
	teacher.GET("/quiz/:id/monitor/stream", streams.TeacherMonitorStream)

	// question image serving — bare route, in-handler guru/murid gate (spec §6)
	e.GET("/media/question/:imageId", teach.ServeQuestionImage)

	// public pages (anonymous): landing + join + about — the navbar profile
	// icon routes by session role (/login, /student, /teacher)
	land := &handlers.Landing{}
	e.GET("/", land.Home)
	e.GET("/join", land.JoinPage)
	e.GET("/about", land.About)

	// student side (spec §7): dashboard + join + workspace + attempt endpoints
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
	// chunked image upload (spec §6): initiate → chunks → complete (+cancel)
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
	teacher.POST("/quiz/:id/status", globals.SetQuizStatus)
	teacher.POST("/quiz/:id/participants/add", teach.AddParticipant)
	teacher.POST("/quiz/:id/delete", teach.DeleteQuiz)
	teacher.GET("/quiz/:id/qr", teach.QuizQR)
	teacher.GET("/quiz/:id", teach.QuizDetailPage)

	// evaluation + export (spec §6.11); the per-question analysis is its
	// own route so the students page never pays for that query
	teacher.GET("/quiz/:id/results", teach.ResultsPage)
	teacher.GET("/quiz/:id/results/analysis", teach.ResultsAnalysisPage)
	teacher.GET("/quiz/:id/results/export", teach.ExportResults)
	teacher.GET("/quiz/:id/grading", teach.GrantingPage)
	teacher.POST("/quiz/:id/grading/:answerId", teach.GradeAnswer)

	// global-timer side (spec §6.6): monitor, START, STOP, roster actions
	teacher.GET("/quiz/:id/monitor", globals.MonitorPage)
	teacher.POST("/quiz/:id/start", globals.Start)
	teacher.POST("/quiz/:id/stop", globals.Stop)
	teacher.POST("/quiz/:id/participants/:pid/action", globals.ParticipantAction)

	// boot rehydrate before the first request, then the 1 s watchdog
	// (timeouts, disconnect freeze, all-finished probe — spec §6.3, §6.6)
	if err := globals.Rehydrate(context.Background()); err != nil {
		return fmt.Errorf("rehydrate: %w", err)
	}
	go globals.WatchLoop(context.Background())

	return e.Start(fmt.Sprintf(":%d", cfg.Port))
}
