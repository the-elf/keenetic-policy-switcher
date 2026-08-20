// Package web embeds the static frontend (index.html, app.js, style.css)
// into the binary so the app is a single self-contained executable.
package web

import "embed"

//go:embed index.html app.js style.css
var FS embed.FS
