// Package web embeds static assets: vendor/ (served at /assets), css/
// (served at /css) and js/ (served at /js). Templates under views/ are
// read from disk at boot instead.
package web

import "embed"

// FS contains the vendor, css and js trees.
//
//go:embed vendor css js
var FS embed.FS
