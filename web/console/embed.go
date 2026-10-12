package consoleweb

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist
var bundle embed.FS

func Handler() http.Handler {
	assets, err := fs.Sub(bundle, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/console/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/console/")
		if name == "" {
			name = "index.html"
		}
		if strings.HasPrefix(name, "api/") || name == "api" || name == "mockup.html" || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(assets, name)
		if err != nil || info.IsDir() {
			if path.Ext(name) != "" || (name == "assets" || strings.HasPrefix(name, "assets/")) || !strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		copy := r.Clone(r.Context())
		copy.URL.Path = "/" + name
		if name == "index.html" {
			copy.URL.Path = "/"
		}
		files.ServeHTTP(w, copy)
	})
}
