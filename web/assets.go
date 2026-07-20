// Package web embeds Victus's static assets (compiled CSS, vendored htmx) into the binary.
package web

import "embed"

// StaticFS holds the compiled Tailwind CSS and vendored htmx served under /static.
//
//go:embed static
var StaticFS embed.FS
