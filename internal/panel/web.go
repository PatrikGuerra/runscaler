package panel

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// web/dist holds the Vite build. Only .gitkeep is committed; the image build
// runs `npm run build` before `go build`.
//
//go:embed all:web/dist
var webDist embed.FS

// spaHandler serves built files and falls back to index.html for client-side routes.
func spaHandler() http.Handler {
	dist, _ := fs.Sub(webDist, "web/dist")
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(dist, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r) // never answer a missing script with index.html
				return
			}
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "panel UI not built (run npm run build in internal/panel/web)", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
