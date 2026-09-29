// Package bootstrap embeds the offline Bootstrap 5.3 distribution kept in
// bootstrap/css and bootstrap/js (including the JS bundle with Popper), so the
// app serves Bootstrap locally with zero CDN, npm, or network fetches.
package bootstrap

import "embed"

// FS contains the css/ and js/ trees of the offline Bootstrap distribution.
//
//go:embed css js
var FS embed.FS
