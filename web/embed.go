// Package web embeds the Mini App static assets served under /app/.
package web

import "embed"

// AppFS holds the Mini App files (index.html, app.js, style.css) under app/.
//
//go:embed app
var AppFS embed.FS

// AdminFS is the separate administrator Mini App; its API requires signed admin identity.
//go:embed admin
var AdminFS embed.FS
