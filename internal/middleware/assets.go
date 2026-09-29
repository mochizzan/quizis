package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

// assetHashLen is the fingerprint length embedded in ?v= URLs and ETags:
// the first 12 hex chars of the file's SHA-256 (48 bits — collision-safe
// for cache busting, short enough to stay readable in URLs).
const assetHashLen = 12

// StaticMount binds a URL prefix to the FS mounted there. cmd/server keeps
// the single list of these and feeds it to both the mount calls and
// StaticCache so the prefix sets cannot drift apart.
type StaticMount struct {
	Path string // URL prefix, e.g. "/css"
	FS   fs.FS
}

// Fingerprints walks fsys once at boot and maps every regular file's final
// URL path (urlPrefix + "/" + relative path) to the first 12 hex chars of
// its SHA-256. Directories are skipped.
func Fingerprints(fsys fs.FS, urlPrefix string) (map[string]string, error) {
	out := make(map[string]string)
	prefix := strings.TrimSuffix(urlPrefix, "/")
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[prefix+"/"+p] = hex.EncodeToString(sum[:])[:assetHashLen]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AssetURL appends the content fingerprint as ?v= to a known asset path so
// templates bust the browser cache exactly when the file content changes.
// Unknown paths (dynamic routes, external URLs, unregistered files) pass
// through unchanged — never an error, never a panic. Safe on a nil map.
func AssetURL(assets map[string]string, path string) string {
	if h, ok := assets[path]; ok && h != "" {
		return path + "?v=" + h
	}
	return path
}

// StaticCache is the cache policy for static assets plus no-store for
// everything SSR:
//   - under a mount with matching ?v= → immutable for a year (the
//     fingerprint changes when content changes, so a hit is never stale)
//   - under a mount with missing/wrong ?v= (this covers CSS-internal
//     relative font URLs) → no-cache + ETag; a matching If-None-Match
//     short-circuits a 304 here — the registry entry proves the file exists
//   - under a mount but not in the registry → no-cache without ETag (the
//     static handler 404s)
//   - everything else → no-store, set before next, so SSR HTML/JSON/SSE is
//     always fresh
func StaticCache(mounts []StaticMount, assets map[string]string) echo.MiddlewareFunc {
	prefixes := make([]string, len(mounts))
	for i, m := range mounts {
		prefixes[i] = strings.TrimSuffix(m.Path, "/")
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			path := c.Request().URL.Path
			if !underMount(path, prefixes) {
				c.Response().Header().Set("Cache-Control", "no-store")
				return next(c)
			}
			h, known := assets[path]
			if !known {
				c.Response().Header().Set("Cache-Control", "no-cache")
				return next(c)
			}
			if c.QueryParam("v") == h {
				c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				return next(c)
			}
			etag := `"` + h + `"`
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("ETag", etag)
			if ETagMatches(c.Request().Header.Get("If-None-Match"), etag) {
				return c.NoContent(http.StatusNotModified)
			}
			return next(c)
		}
	}
}

// underMount reports whether path sits at or below one of the prefixes.
func underMount(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// ETagMatches evaluates an If-None-Match header against the current ETag —
// the RFC 9110 matcher shared with the media endpoint: comma-separated
// list, "*" wildcards and weak-validator prefixes are all accepted.
func ETagMatches(header, etag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		v = strings.TrimPrefix(v, "W/")
		if v == "*" || strings.EqualFold(v, etag) {
			return true
		}
	}
	return false
}
