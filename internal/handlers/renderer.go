package handlers

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v5"

	mw "quiz/internal/middleware"
)

// Renderer renders html/template pages for Echo. All *.html files under the
// views directory are parsed exactly once at boot — a parse error fails the
// boot instead of the first request.
type Renderer struct {
	t *template.Template
}

// NewRenderer walks dir (template.ParseGlob cannot cross '/'), collects every
// *.html file, and parses them into one template set. assets is the
// fingerprint registry (final URL path → hash12) backing the `asset`
// template func for ?v= cache busting; a nil/empty registry makes every
// asset call a passthrough.
func NewRenderer(dir string, assets map[string]string) (*Renderer, error) {
	t := template.New("").Funcs(template.FuncMap{
		"fmtScore": func(v float64) string { return fmt.Sprintf("%.2f", v) },
		// asset: content-hash fingerprint for static asset URLs
		// ({{asset "/js/ui.js"}} → /js/ui.js?v=<hash12>); unknown paths
		// pass through unchanged.
		"asset": func(path string) string { return mw.AssetURL(assets, path) },
		"join":  strings.Join,
		"add":   func(a, b int) int { return a + b },
		"sub":   func(a, b int) int { return a - b },
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires alternating key/value pairs")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key must be a string, got %T", values[i])
				}
				m[k] = values[i+1]
			}
			return m, nil
		},
	})

	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".html") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk views %s: %w", dir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .html templates found in %s", dir)
	}

	t, err = t.ParseFiles(files...)
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Renderer{t: t}, nil
}

// Render executes the named template definition (e.g. "page-login").
//
// Every map payload is decorated with the request-scoped navigation context
// before execution: Role (the session role, "" when anonymous) and Nav (the
// active-nav key derived from the URL path). Templates branch on those two
// keys for the role-isolated menus, so no page can show another role's links
// just because its handler forgot to pass them.
func (r *Renderer) Render(c *echo.Context, w io.Writer, name string, data any) error {
	if m, ok := data.(map[string]any); ok && c != nil {
		data = withNavContext(c, m)
	}
	return r.t.ExecuteTemplate(w, name, data)
}

// withNavContext copies data and fills Role/Nav unless the handler already
// set them. The copy matters: some payloads (quiz lists) are shared with the
// cache mirror and must not be mutated. It also fills "Crumbs" with the
// path-derived route hierarchy (Breadcrumbs) when the handler left it out —
// quiz/attempt pages pass the real title instead.
func withNavContext(c *echo.Context, data map[string]any) map[string]any {
	out := make(map[string]any, len(data)+3)
	for k, v := range data {
		out[k] = v
	}
	if _, ok := out["Role"]; !ok {
		role := ""
		if s := mw.SessionFrom(c); s != nil {
			role = s.Role
		}
		out["Role"] = role
	}
	if _, ok := out["Nav"]; !ok {
		out["Nav"] = navKey(c.Request().URL.Path)
	}
	if _, ok := out["Crumbs"]; !ok {
		out["Crumbs"] = Breadcrumbs(c.Request().URL.Path)
	}
	return out
}

// navKey maps a request path to the active-nav key used by the sidebar and
// the landing navbar. Sub-routes inherit their section's key (quiz detail,
// results, grading and monitor all light up "quizzes").
func navKey(path string) string {
	switch {
	case path == "/" || path == "/student":
		return "home"
	case path == "/join":
		return "join"
	case path == "/about":
		return "about"
	case path == "/profile":
		return "profile"
	case strings.HasPrefix(path, "/history"):
		return "history"
	case path == "/teacher":
		return "dashboard"
	case strings.HasPrefix(path, "/teacher/questions"):
		return "questions"
	case strings.HasPrefix(path, "/teacher/classes"):
		return "classes"
	case strings.HasPrefix(path, "/teacher/majors"):
		return "majors"
	case strings.HasPrefix(path, "/teacher/students"):
		return "students"
	case strings.HasPrefix(path, "/teacher/password-resets"):
		return "resets"
	case strings.HasPrefix(path, "/teacher/quiz"):
		return "quizzes"
	}
	return ""
}
