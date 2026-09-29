package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/labstack/echo/v5"
)

// wantHash is the fingerprint the implementation must produce: first 12 hex
// chars of the SHA-256 of the content.
func wantHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:12]
}

func TestFingerprints(t *testing.T) {
	fsys := fstest.MapFS{
		"ui.js":     {Data: []byte("console.log(1)")},
		"sub/a.css": {Data: []byte("body{color:red}")},
	}
	assets, err := Fingerprints(fsys, "/js")
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("entries = %v, want exactly the 2 files (directories skipped)", assets)
	}
	if got, want := assets["/js/ui.js"], wantHash("console.log(1)"); got != want {
		t.Errorf("assets[/js/ui.js] = %q, want sha256[:12] = %q", got, want)
	}
	if got, want := assets["/js/sub/a.css"], wantHash("body{color:red}"); got != want {
		t.Errorf("assets[/js/sub/a.css] = %q, want sha256[:12] = %q", got, want)
	}
	for k, v := range assets {
		if len(v) != assetHashLen {
			t.Errorf("hash %q for %s has %d chars, want %d", v, k, len(v), assetHashLen)
		}
	}
	if _, ok := assets["/js/missing.js"]; ok {
		t.Error("path absent from the FS must be absent from the registry")
	}

	// different content → different fingerprint (cache actually busts)
	other := fstest.MapFS{"ui.js": {Data: []byte("console.log(2)")}}
	otherHashes, err := Fingerprints(other, "/js")
	if err != nil {
		t.Fatalf("Fingerprints(other): %v", err)
	}
	if otherHashes["/js/ui.js"] == assets["/js/ui.js"] {
		t.Error("different content produced the same fingerprint")
	}
}

func TestAssetURL(t *testing.T) {
	assets := map[string]string{"/js/ui.js": "abc123def456"}
	cases := []struct {
		name string
		path string
		want string
	}{
		{"known asset gets ?v=", "/js/ui.js", "/js/ui.js?v=abc123def456"},
		{"unknown asset path passes through", "/js/other.js", "/js/other.js"},
		{"dynamic route passes through", "/teacher", "/teacher"},
		{"external url passes through", "https://cdn.example/x.js", "https://cdn.example/x.js"},
		{"empty path passes through", "", ""},
	}
	for _, tc := range cases {
		if got := AssetURL(assets, tc.path); got != tc.want {
			t.Errorf("%s: AssetURL(%q) = %q, want %q", tc.name, tc.path, got, tc.want)
		}
	}
	// a nil registry is a total passthrough — never a panic
	if got := AssetURL(nil, "/js/ui.js"); got != "/js/ui.js" {
		t.Errorf("AssetURL(nil, …) = %q, want passthrough", got)
	}
}

func TestStaticCache(t *testing.T) {
	mounts := []StaticMount{{Path: "/css", FS: fstest.MapFS{"app.css": {Data: []byte("body{}")}}}}
	assets, err := Fingerprints(mounts[0].FS, "/css")
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	hash := assets["/css/app.css"]
	etag := `"` + hash + `"`

	e := echo.New()
	e.Use(StaticCache(mounts, assets))
	e.StaticFS("/css", mounts[0].FS)
	e.GET("/login", func(c *echo.Context) error {
		return c.String(http.StatusOK, "page")
	})

	cases := []struct {
		name        string
		target      string
		ifNoneMatch string
		wantStatus  int
		wantCC      string
		wantETag    string
		wantBody    string // asserted unless skipBody
		skipBody    bool
	}{
		{name: "matched v → immutable", target: "/css/app.css?v=" + hash,
			wantStatus: 200, wantCC: "public, max-age=31536000, immutable", wantBody: "body{}"},
		{name: "missing v → no-cache + ETag", target: "/css/app.css",
			wantStatus: 200, wantCC: "no-cache", wantETag: etag, wantBody: "body{}"},
		{name: "wrong v → no-cache, not immutable", target: "/css/app.css?v=deadbeef0000",
			wantStatus: 200, wantCC: "no-cache", wantETag: etag, wantBody: "body{}"},
		{name: "If-None-Match hit → body-less 304", target: "/css/app.css", ifNoneMatch: etag,
			wantStatus: 304, wantCC: "no-cache", wantETag: etag, wantBody: ""},
		{name: "If-None-Match miss → served", target: "/css/app.css", ifNoneMatch: `"ffffffffffff"`,
			wantStatus: 200, wantCC: "no-cache", wantETag: etag, wantBody: "body{}"},
		{name: "unknown static path → no-cache, no ETag", target: "/css/missing.css",
			wantStatus: 404, wantCC: "no-cache", skipBody: true},
		{name: "non-static path → no-store", target: "/login",
			wantStatus: 200, wantCC: "no-store", wantBody: "page"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.ifNoneMatch != "" {
				req.Header.Set("If-None-Match", tc.ifNoneMatch)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != tc.wantCC {
				t.Errorf("Cache-Control = %q, want %q", cc, tc.wantCC)
			}
			if tag := rec.Header().Get("ETag"); tag != tc.wantETag {
				t.Errorf("ETag = %q, want %q", tag, tc.wantETag)
			}
			if !tc.skipBody && rec.Body.String() != tc.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}
