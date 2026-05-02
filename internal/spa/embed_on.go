//go:build embed

// Package spa exposes the built SPA bundle to the Go HTTP layer. Two
// build paths:
//
//   go build              → embed_off.go is included; FS() returns nil.
//                           Use this for `go run ./cmd/periscope` during
//                           local development; the Vite dev server
//                           handles SPA routing on :5173.
//
//   go build -tags embed  → embed_on.go is included; FS() returns the
//                           embedded SPA bundle. Used by the Docker
//                           build, which copies web/dist into
//                           internal/spa/dist before compiling.
//
// Splitting into two files keeps `//go:embed` from blowing up when the
// dist directory hasn't been built yet (a common dev-time annoyance).
package spa

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the SPA bundle rooted at "/", or nil if no bundle is
// embedded (dev mode). Callers must handle the nil case — typically by
// not registering the SPA handler at all.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	return sub
}
