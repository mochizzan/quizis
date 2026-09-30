package handlers

import (
	"strconv"
	"strings"
)

// Crumb is one breadcrumb item for the Bootstrap breadcrumb on every
// dashboard page. URL is empty on the current (last) page — the template
// then marks it .active with aria-current="page" — and on the static root
// "Dashboard" label, which must never link to "/" (the landing page).
type Crumb struct {
	Label string
	URL   string
}

// Breadcrumbs returns the route hierarchy for a dashboard path (guru and
// murid), or nil outside the dashboard shell. It runs from the URL path the
// same way navKey does, so no page shows a wrong hierarchy just because its
// handler forgot to pass one. Handlers that know the resource title (quiz
// pages, attempt detail) set their own "Crumbs" payload instead — the
// renderer only fills the default when the key is absent.
func Breadcrumbs(path string) []Crumb {
	switch {
	case path == "/teacher" || strings.HasPrefix(path, "/teacher/"):
		return guruCrumbs(path)
	case path == "/student" || path == "/profile" ||
		path == "/history" || strings.HasPrefix(path, "/history/"):
		return muridCrumbs(path)
	}
	return nil
}

// QuizCrumbs is the /teacher/quiz/<id> hierarchy with the real quiz title:
// Dashboard / Guru / Quiz / <judul> [/ section]. An empty section means the
// detail page itself — the title is then the current crumb.
func QuizCrumbs(id uint64, judul, section string) []Crumb {
	crumbs := []Crumb{
		{Label: "Dasbor"},
		{Label: "Guru", URL: "/teacher"},
		{Label: "Kuis", URL: "/teacher/quiz"},
		{Label: judul, URL: "/teacher/quiz/" + strconv.FormatUint(id, 10)},
	}
	if section == "" {
		crumbs[len(crumbs)-1].URL = ""
		return crumbs
	}
	return append(crumbs, Crumb{Label: section})
}

// AttemptCrumbs is the /history/:id hierarchy with the quiz title.
func AttemptCrumbs(judul string) []Crumb {
	return []Crumb{
		{Label: "Dasbor"},
		{Label: "Beranda", URL: "/student"},
		{Label: "Riwayat", URL: "/history"},
		{Label: judul},
	}
}

// guruCrumbs walks /teacher/<segments>: Dashboard / Guru / section…, the
// last crumb current (no link), every parent linking to its own route.
func guruCrumbs(path string) []Crumb {
	crumbs := []Crumb{{Label: "Dasbor"}, {Label: "Guru", URL: "/teacher"}}
	rest := strings.Trim(strings.TrimPrefix(path, "/teacher"), "/")
	if rest == "" { // /teacher itself — Guru is the current page
		crumbs[len(crumbs)-1].URL = ""
		return crumbs
	}
	segs := strings.Split(rest, "/")
	for i, seg := range segs {
		crumbs = append(crumbs, Crumb{
			Label: guruSection(seg, segs, i),
			URL:   "/teacher/" + strings.Join(segs[:i+1], "/"),
		})
	}
	crumbs[len(crumbs)-1].URL = ""
	return crumbs
}

// guruSection labels one /teacher path segment (sidebar vocabulary — the
// menu and its breadcrumb always share a name).
func guruSection(seg string, segs []string, i int) string {
	switch seg {
	case "students":
		return "Kelola akun murid"
	case "questions":
		return "Bank pertanyaan"
	case "classes":
		return "Kelas"
	case "majors":
		return "Jurusan"
	case "password-resets":
		return "Permintaan ganti kata sandi"
	case "quiz":
		return "Kuis"
	case "new":
		if i > 0 {
			switch segs[i-1] {
			case "questions":
				return "Pertanyaan baru"
			case "classes":
				return "Kelas baru"
			case "majors":
				return "Jurusan baru"
			}
		}
		return "Kuis baru"
	case "edit":
		if i > 0 && segs[i-1] == "questions" {
			return "Ubah pertanyaan"
		}
		return "Ubah"
	case "results":
		return "Hasil"
	case "grading":
		return "Penilaian"
	case "monitor":
		return "Pemantauan"
	}
	// /teacher/quiz/<id> — the four quiz pages override this with the title
	if i > 0 && segs[i-1] == "quiz" && isDigits(seg) {
		return "Kuis #" + seg
	}
	return seg
}

// muridCrumbs: Dashboard / Beranda [/ section…]. /student itself leaves
// Beranda current; /history/:id defaults to a generic tail for the (title-
// overridden) attempt page.
func muridCrumbs(path string) []Crumb {
	crumbs := []Crumb{{Label: "Dasbor"}, {Label: "Beranda", URL: "/student"}}
	switch {
	case path == "/student":
		crumbs[1].URL = "" // Beranda is the current page
	case path == "/profile":
		crumbs = append(crumbs, Crumb{Label: "Profil"})
	case path == "/history":
		crumbs = append(crumbs, Crumb{Label: "Riwayat"})
	default: // /history/:id without a handler override
		crumbs = append(crumbs,
			Crumb{Label: "Riwayat", URL: "/history"}, Crumb{Label: "Detail"})
	}
	return crumbs
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
