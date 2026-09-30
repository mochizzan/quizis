package handlers

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gosqlmysql "github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"quiz/internal/cache"
	"quiz/internal/config"
	"quiz/internal/db"
	mw "quiz/internal/middleware"
	"quiz/internal/quizengine"
)

// User-visible auth messages (English, spec §1). MsgLoginFailed is returned
// verbatim for BOTH unknown-user and wrong-password so the two cases are
// indistinguishable (spec §8).
const (
	MsgLoginFailed      = "Username/email atau kata sandi tidak valid."
	MsgForgotNoMatch    = "Tidak ada akun yang cocok dengan data tersebut."
	MsgTaken            = "Username atau email sudah terdaftar."
	MsgShortPassword    = "Kata sandi minimal 8 karakter."
	MsgRequired         = "Silakan isi semua kolom."
	MsgPasswordMismatch = "Kata sandi tidak sama."
	MsgForgotSent       = "Permintaan dikirim. Guru Anda akan meninjaunya."
	MsgRegistered       = "Akun dibuat. Masuk untuk melanjutkan."
	// MsgLoginInactive is only reachable AFTER the password matched (or the
	// forced-change flag skipped it): a stranger still sees MsgLoginFailed.
	MsgLoginInactive = "Akun ini dinonaktifkan. Hubungi guru Anda."
)

// Auth serves the auth routes; methods are split across auth.go and
// password_resets.go.
type Auth struct {
	DB    *sql.DB
	Store *cache.Store
	Cfg   *config.Config
}

// --- GET /login -----------------------------------------------------------

func (a *Auth) LoginPage(c *echo.Context) error {
	data := loginData("", "")
	if c.QueryParam("registered") == "1" {
		data["Flash"] = flash("success", MsgRegistered)
	}
	return c.Render(200, "page-login", data)
}

func loginData(identity, errMsg string) map[string]any {
	m := map[string]any{"Title": "Masuk", "Identity": identity}
	if errMsg != "" {
		m["Flash"] = flash("danger", errMsg)
	}
	return m
}

// --- POST /login ----------------------------------------------------------
// One form for both roles: username OR email + password. The guru account
// comes from .env (user_id 0, role "guru"); students come from the users
// table. must_change_pw=1 skips password verification entirely and lands on
// /change-password.
func (a *Auth) Login(c *echo.Context) error {
	identity := strings.TrimSpace(c.FormValue("identity"))
	password := c.FormValue("password")
	ctx := c.Request().Context()

	// Guru: .env credentials, constant-time compare. A mismatch falls
	// through to the DB lookup so the failure stays generic.
	if identity == a.Cfg.GuruUser &&
		subtle.ConstantTimeCompare([]byte(password), []byte(a.Cfg.GuruPass)) == 1 {
		return a.startSession(c, 0, "guru", "/teacher")
	}

	var (
		uid   uint64
		hash  string
		must  bool
		aktif bool
	)
	err := a.DB.QueryRowContext(
		ctx,
		`SELECT id, password_hash, must_change_pw, aktif FROM users WHERE username = ?`, identity,
	).Scan(&uid, &hash, &must, &aktif)
	if err == sql.ErrNoRows {
		err = a.DB.QueryRowContext(
			ctx,
			`SELECT id, password_hash, must_change_pw, aktif FROM users WHERE email = ?`, identity,
		).Scan(&uid, &hash, &must, &aktif)
	}
	switch {
	case err == sql.ErrNoRows:
		return c.Render(http.StatusUnauthorized, "page-login", loginData(identity, MsgLoginFailed))
	case err != nil:
		return err
	}

	if !must && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return c.Render(http.StatusUnauthorized, "page-login", loginData(identity, MsgLoginFailed))
	}
	// deactivated accounts cannot sign in — checked only once the password
	// is known-good, so the status never becomes an account oracle
	if !aktif {
		return c.Render(http.StatusUnauthorized, "page-login", loginData(identity, MsgLoginInactive))
	}

	// lazy GC of expired session rows (spec: boot + on login)
	_ = db.DeleteExpired(ctx, a.DB, time.Now())

	// the landing is chosen from the session this login just created:
	// murid → dashboard (/student); the pending-join override in
	// startSession may still reroute it to /join?code=...
	target := "/student"
	if must {
		target = "/change-password"
	}
	return a.startSession(c, uid, "murid", target)
}

// PendingJoinCookie carries a join code typed on the public /join page
// across the sign-in hop: JS sets it before redirecting to /login, and
// startSession consumes it (cookie cleared, code moved into the URL) so the
// murid lands back on /join?code=... and the join completes itself.
const PendingJoinCookie = "pending_join"

// startSession inserts a fresh session, sets the cookie, and redirects.
// For a murid landing on the dashboard with a valid pending join code,
// the redirect becomes /join?code=... — sign-in (or a forced password
// change) never loses the code the student already typed. Registration
// no longer comes through here: it redirects to /login without touching
// the cookie, and the code rides along at the manual sign-in that follows.
func (a *Auth) startSession(c *echo.Context, userID uint64, role, redirectTo string) error {
	sid, err := db.NewSessionID()
	if err != nil {
		return err
	}
	if err := db.InsertSession(c.Request().Context(), a.DB, sid, userID, role,
		mw.SessionExpiresAt(time.Now())); err != nil {
		return err
	}
	mw.SetSessionCookie(c, sid)
	if role == "murid" && redirectTo == "/student" {
		if ck, err := c.Cookie(PendingJoinCookie); err == nil && quizengine.ValidJoinCode(ck.Value) {
			code := strings.ToUpper(ck.Value)
			c.SetCookie(&http.Cookie{
				Name: PendingJoinCookie, Value: "", Path: "/", MaxAge: -1,
				SameSite: http.SameSiteLaxMode,
			})
			redirectTo = "/join?code=" + url.QueryEscape(code)
		}
	}
	return c.Redirect(http.StatusFound, redirectTo)
}

// --- POST /logout ---------------------------------------------------------
// Double logout is a safe no-op. Order: mirror entry first, then DB row.
func (a *Auth) Logout(c *echo.Context) error {
	if ck, err := c.Cookie(mw.CookieName); err == nil && ck.Value != "" {
		if err := mw.RevokeSession(c.Request().Context(), a.DB, a.Store, ck.Value); err != nil {
			return err
		}
	}
	mw.ClearSessionCookie(c)
	return c.Redirect(http.StatusFound, "/login")
}

// --- GET /register --------------------------------------------------------

type refOption struct {
	ID   uint16
	Nama string
}

func (a *Auth) refOptions(c *echo.Context, table string) ([]refOption, error) {
	if table != "ref_kelas" && table != "ref_jurusan" {
		return nil, fmt.Errorf("invalid ref table %q", table)
	}
	rows, err := a.DB.QueryContext(c.Request().Context(),
		"SELECT id, nama FROM "+table+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []refOption
	for rows.Next() {
		var o refOption
		if err := rows.Scan(&o.ID, &o.Nama); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (a *Auth) RegisterPage(c *echo.Context) error {
	return a.registerForm(c, http.StatusOK, "", "", "", "")
}

func (a *Auth) registerForm(c *echo.Context, status int, errMsg, nama, username, email string) error {
	classes, err := a.refOptions(c, "ref_kelas")
	if err != nil {
		return err
	}
	majors, err := a.refOptions(c, "ref_jurusan")
	if err != nil {
		return err
	}
	data := map[string]any{
		"Title": "Daftar", "Classes": classes, "Majors": majors,
		"Nama": nama, "Username": username, "Email": email,
	}
	if errMsg != "" {
		data["Flash"] = flash("danger", errMsg)
	}
	return c.Render(status, "page-register", data)
}

// --- POST /register -------------------------------------------------------
// Unique username/email (race-safe via duplicate-key detection) → 409;
// password < 8 → 400; inserts a bcrypt hash and redirects to
// /login?registered=1 with a success flash. NO session is created — the
// student signs in manually (any pending join cookie stays untouched and
// is consumed at that sign-in).
func (a *Auth) Register(c *echo.Context) error {
	nama := strings.TrimSpace(c.FormValue("nama_lengkap"))
	username := strings.TrimSpace(c.FormValue("username"))
	email := strings.TrimSpace(c.FormValue("email"))
	password := c.FormValue("password")
	kelasStr := strings.TrimSpace(c.FormValue("kelas_id"))
	jurusanStr := strings.TrimSpace(c.FormValue("jurusan_id"))

	bad := func(status int, msg string) error {
		return a.registerForm(c, status, msg, nama, username, email)
	}
	if nama == "" || username == "" || email == "" || password == "" ||
		kelasStr == "" || jurusanStr == "" {
		return bad(http.StatusBadRequest, MsgRequired)
	}
	if !quizengine.PasswordMeetsPolicy(password) {
		return bad(http.StatusBadRequest, MsgShortPassword)
	}
	kelasID, err1 := strconv.ParseUint(kelasStr, 10, 16)
	jurusanID, err2 := strconv.ParseUint(jurusanStr, 10, 16)
	if err1 != nil || err2 != nil {
		return bad(http.StatusBadRequest, MsgRequired)
	}
	// the .env teacher identity is reserved (guru is not a users row)
	if username == a.Cfg.GuruUser {
		return bad(http.StatusConflict, MsgTaken)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := a.DB.ExecContext(c.Request().Context(),
		`INSERT INTO users (username, email, password_hash, nama_lengkap, kelas_id, jurusan_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		username, email, string(hash), nama, kelasID, jurusanID)
	if err != nil {
		if isDuplicateKey(err) {
			return bad(http.StatusConflict, MsgTaken)
		}
		return err
	}
	if _, err := res.LastInsertId(); err != nil {
		return err
	}
	return c.Redirect(http.StatusFound, "/login?registered=1")
}

func isDuplicateKey(err error) bool {
	var me *gosqlmysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// --- GET /forgot-password -------------------------------------------------

func (a *Auth) ForgotPage(c *echo.Context) error {
	data := map[string]any{"Title": "Lupa kata sandi"}
	if c.QueryParam("sent") == "1" {
		data["Success"] = MsgForgotSent // hides the form
		data["Flash"] = flash("success", MsgForgotSent)
	}
	return c.Render(200, "page-forgot", data)
}

// --- POST /forgot-password ------------------------------------------------
// Account identity AND full name must match the SAME user, otherwise one
// generic message for every failure kind. One active pending request only.
func (a *Auth) Forgot(c *echo.Context) error {
	identity := strings.TrimSpace(c.FormValue("identity"))
	nama := strings.TrimSpace(c.FormValue("nama"))

	var uid uint64
	err := a.DB.QueryRowContext(
		c.Request().Context(),
		`SELECT id FROM users WHERE (username = ? OR email = ?) AND nama_lengkap = ?`,
		identity, identity, nama,
	).Scan(&uid)
	if err == sql.ErrNoRows {
		return c.Render(http.StatusBadRequest, "page-forgot",
			map[string]any{"Title": "Lupa kata sandi", "Flash": flash("danger", MsgForgotNoMatch)})
	}
	if err != nil {
		return err
	}

	var pending int
	if err := a.DB.QueryRowContext(
		c.Request().Context(),
		`SELECT COUNT(*) FROM password_resets WHERE user_id = ? AND status = 'pending'`,
		uid,
	).Scan(&pending); err != nil {
		return err
	}
	if pending == 0 {
		if _, err := a.DB.ExecContext(
			c.Request().Context(),
			`INSERT INTO password_resets (user_id, input_username, input_nama) VALUES (?, ?, ?)`,
			uid, identity, nama,
		); err != nil {
			return err
		}
	}
	return c.Redirect(http.StatusFound, "/forgot-password?sent=1")
}

// --- GET|POST /change-password -------------------------------------------
// Gated by ForceChangePassword middleware (must_change_pw=1).
func (a *Auth) ChangePasswordPage(c *echo.Context) error {
	s := mw.SessionFrom(c)
	if s == nil {
		return c.Redirect(http.StatusFound, "/login")
	}
	if s.Role != "murid" || !s.MustChangePW {
		return c.Redirect(http.StatusFound, "/")
	}
	return c.Render(200, "page-change-password", map[string]any{"Title": "Ubah kata sandi"})
}

func (a *Auth) ChangePassword(c *echo.Context) error {
	s := mw.SessionFrom(c)
	if s == nil {
		return c.Redirect(http.StatusFound, "/login")
	}
	if s.Role != "murid" {
		return c.Render(http.StatusForbidden, "page-change-password",
			map[string]any{"Title": "Ubah kata sandi", "Flash": flash("danger", "Anda tidak diizinkan mengubah kata sandi ini.")})
	}
	data := func(msg string) map[string]any {
		return map[string]any{"Title": "Ubah kata sandi", "Flash": flash("danger", msg)}
	}
	two := c.FormValue("password")
	confirm := c.FormValue("confirm")
	if !quizengine.PasswordMeetsPolicy(two) {
		return c.Render(http.StatusBadRequest, "page-change-password", data(MsgShortPassword))
	}
	if two != confirm {
		return c.Render(http.StatusBadRequest, "page-change-password", data(MsgPasswordMismatch))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(two), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_pw = 0 WHERE id = ?`,
		string(hash), s.UserID)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return c.Render(http.StatusForbidden, "page-change-password",
			data("Akun sudah tidak ada."))
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE password_resets SET status = 'selesai' WHERE user_id = ? AND status = 'disetujui'`,
		s.UserID,
	); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// revoke every old session (mirror keys first, then rows), then issue a
	// fresh session so this device stays logged in — landing on the murid
	// dashboard like every other successful sign-in.
	if err := mw.RevokeUserSessions(ctx, a.DB, a.Store, s.UserID); err != nil {
		return err
	}
	return a.startSession(c, s.UserID, "murid", "/student")
}
