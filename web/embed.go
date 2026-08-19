// Package webassets embeds the production Vue build into the Go server.
package webassets

import "embed"

// Dist contains the Vite production build.
//
//go:embed dist
var Dist embed.FS
