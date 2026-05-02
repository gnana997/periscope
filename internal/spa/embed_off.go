//go:build !embed

package spa

import "io/fs"

// FS returns nil in dev builds. Callers should detect nil and skip
// registering the SPA handler — the Vite dev server handles SPA
// routing on :5173.
func FS() fs.FS { return nil }
