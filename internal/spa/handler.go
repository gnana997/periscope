package spa

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler returns an http.Handler that serves the SPA from the
// embedded bundle, with index.html as the SPA-routing fallback for
// any path that isn't a real file.
//
// Returns nil when no bundle is embedded (dev mode). Callers should
// guard against this.
func Handler() http.Handler {
	root := FS()
	if root == nil {
		return nil
	}
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := strings.TrimPrefix(r.URL.Path, "/")
		if upath == "" {
			upath = "index.html"
		}
		// Hit the embedded FS to see whether the requested path is a
		// real file. If not, serve index.html so React Router can
		// resolve the route on the client.
		if !fileExists(root, upath) {
			serveIndex(w, r, root)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func fileExists(root fs.FS, p string) bool {
	// Reject directory-traversal paths up front. Anything that begins
	// with "/" or contains ".." is not a real file we want to serve.
	if strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return false
	}
	clean := path.Clean(p)
	f, err := root.Open(clean)
	if err != nil {
		return false
	}
	stat, err := f.Stat()
	_ = f.Close()
	if err != nil || stat.IsDir() {
		return false
	}
	return true
}

func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	f, err := root.Open("index.html")
	if err != nil {
		http.Error(w, "index.html missing from SPA bundle", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.Copy(w, f)
}
