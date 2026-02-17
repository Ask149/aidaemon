// Package web provides the embedded chat SPA static files.
package web

import "embed"

//go:embed *.html *.css *.js
var FS embed.FS
