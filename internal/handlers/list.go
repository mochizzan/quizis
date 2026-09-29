package handlers

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
)

// Shared search + filter + pagination for every table page.
//
// Contract: ?q= (search), the page's own filter params (e.g. ?length=,
// ?status=) and ?page= COMBINE — the search form preserves filters, filter
// links preserve q, pagination links preserve both. Only "q" and "page" are
// owned by the toolbar/pagination; everything else in the query string is
// round-tripped untouched.
//
// Data policy: table pages keep fetching their full list (cache mirror or
// one SQL scan — the app has no unbounded feeds) and narrow it in memory.
// One code path for every table, no duplicated WHERE clauses, and the DB
// stays the source of truth. Rows handed to templates are a sub-slice of
// that list — never mutated in place (mirror payloads are shared).

// TablePerPage is the row count of one table page.
const TablePerPage = 10

// TableUI is the shared table view-model, injected under the "Table" data
// key. Layout partials: views/layout/table_ui.html ("table-toolbar",
// "table-pagination").
type TableUI struct {
	Action      string      // page URL without query (search form action, reset link)
	Q           string      // current search term ("" = off)
	Placeholder string      // search input placeholder (set per page)
	Total       int         // rows matching search + filter (before paging)
	Page        int         // 1-based, clamped
	Pages       int         // total pages, >= 1
	From, To    int         // "Menampilkan From–To dari Total" (0 when empty)
	Params      [][2]string // preserved filter params, sorted by name
	Items       []PageItem  // pagination cells (windowed)
	PrevURL     string      // "" when on the first page
	NextURL     string      // "" when on the last page
}

// PageItem is one pagination cell: a link, the current page, or an ellipsis.
type PageItem struct {
	Label    string
	URL      string // "" for the current page and ellipses
	Current  bool
	Ellipsis bool
}

// tableReserved lists query params the toolbar/pagination own; all other
// params are preserved verbatim across search, filter and page links.
var tableReserved = map[string]bool{"q": true, "page": true}

// listQ returns the trimmed ?q= search term.
func listQ(c *echo.Context) string {
	return strings.TrimSpace(c.QueryParam("q"))
}

// listPage returns ?page= as a positive int (1 when missing or invalid).
func listPage(c *echo.Context) int {
	n, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// matchSearch reports whether any field contains q (case-insensitive
// substring). An empty q matches everything, so search layers on top of
// filters instead of replacing them.
func matchSearch(q string, fields ...string) bool {
	if q == "" {
		return true
	}
	ql := strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), ql) {
			return true
		}
	}
	return false
}

// paginate clamps page to the available window and returns the rows shown
// on it plus the clamped (page, pages). pages is at least 1 so templates can
// render "1 / 1" for an empty table.
func paginate[T any](rows []T, page int) (out []T, clampedPage, pages int) {
	pages = (len(rows) + TablePerPage - 1) / TablePerPage
	if pages < 1 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * TablePerPage
	end := start + TablePerPage
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], page, pages
}

// newTable builds the view-model for a table page. page must come from
// paginate() so the clamped window and the links agree.
func newTable(c *echo.Context, q string, total, page int) TableUI {
	t := TableUI{
		Action: c.Request().URL.Path,
		Q:      q,
		Total:  total,
		Page:   page,
	}
	t.Pages = (total + TablePerPage - 1) / TablePerPage
	if t.Pages < 1 {
		t.Pages = 1
	}
	if t.Page > t.Pages {
		t.Page = t.Pages
	}
	if total > 0 {
		t.From = (t.Page-1)*TablePerPage + 1
		t.To = min(t.Page*TablePerPage, total)
	}

	// preserve every non-q/page param (filters), sorted for stable output
	qs := c.Request().URL.Query()
	for k, vs := range qs {
		if tableReserved[k] {
			continue
		}
		if len(vs) > 0 && vs[0] != "" {
			t.Params = append(t.Params, [2]string{k, vs[0]})
		}
	}
	sort.Slice(t.Params, func(i, j int) bool { return t.Params[i][0] < t.Params[j][0] })

	if t.Page > 1 {
		t.PrevURL = t.pageURL(t.Page - 1)
	}
	if t.Page < t.Pages {
		t.NextURL = t.pageURL(t.Page + 1)
	}
	t.Items = t.window()
	return t
}

// pageURL builds action?page=N with q and all preserved filters.
func (t TableUI) pageURL(p int) string {
	v := url.Values{}
	if t.Q != "" {
		v.Set("q", t.Q)
	}
	for _, kv := range t.Params {
		v.Set(kv[0], kv[1])
	}
	if p > 1 {
		v.Set("page", strconv.Itoa(p))
	}
	if s := v.Encode(); s != "" {
		return t.Action + "?" + s
	}
	return t.Action
}

// window returns the pagination cells: every page when there are ≤ 7,
// otherwise first/last with the current neighbourhood and ellipses for gaps.
func (t TableUI) window() []PageItem {
	mark := func(i int) PageItem {
		if i == t.Page {
			return PageItem{Label: strconv.Itoa(i), Current: true}
		}
		return PageItem{Label: strconv.Itoa(i), URL: t.pageURL(i)}
	}
	var items []PageItem
	if t.Pages <= 7 {
		for i := 1; i <= t.Pages; i++ {
			items = append(items, mark(i))
		}
		return items
	}
	items = append(items, mark(1))
	lo, hi := t.Page-1, t.Page+1
	if lo > 2 {
		items = append(items, PageItem{Label: "…", Ellipsis: true})
	}
	for i := max(2, lo); i <= min(t.Pages-1, hi); i++ {
		items = append(items, mark(i))
	}
	if hi < t.Pages-1 {
		items = append(items, PageItem{Label: "…", Ellipsis: true})
	}
	return append(items, mark(t.Pages))
}
