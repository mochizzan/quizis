package handlers

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// Landing serves the public, anonymous-safe pages (design_landingpage.md):
// GET / (the landing page), GET /join (the join-code form) and GET /about.
// The renderer injects the session role, which only decides where the
// navbar's profile icon points and whether a join form submission signs in
// first — POST /join itself still requires the murid session.
//
// The join page is anonymous, so it must never carry quiz data (title, code,
// participant count) in the page or any public response: it renders the code
// form and nothing else.
type Landing struct{}

// Home renders the landing page: hero, promo banner, capability strip.
func (l *Landing) Home(c *echo.Context) error {
	return c.Render(http.StatusOK, "page-landing", map[string]any{"Title": ""})
}

// JoinPage renders the public join page: the join-code form only.
func (l *Landing) JoinPage(c *echo.Context) error {
	return c.Render(http.StatusOK, "page-join", map[string]any{
		"Title": "Gabung kuis",
	})
}

// About renders the static About page (feature overview, both roles).
func (l *Landing) About(c *echo.Context) error {
	return c.Render(http.StatusOK, "page-about", map[string]any{"Title": "Tentang"})
}
