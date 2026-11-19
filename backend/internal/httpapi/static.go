package httpapi

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// staticHandler sirve el frontend compilado (frontend/dist) desde dir.
// Las rutas que no corresponden a un archivo devuelven index.html para que
// la SPA resuelva el enrutado. Devuelve nil si dir no contiene index.html.
func staticHandler(dir string) http.Handler {
	if dir == "" {
		return nil
	}
	fsys := os.DirFS(dir)
	if st, err := fs.Stat(fsys, "index.html"); err != nil || st.IsDir() {
		return nil
	}
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		// Archivos ocultos y directorios nunca se sirven tal cual (sin listados).
		serveFile := false
		if name != "" && !hiddenPath(name) {
			if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
				serveFile = true
			}
		}
		if !serveFile || name == "index.html" {
			// index.html nunca se cachea: siempre apunta a los assets de la última compilación.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			// Vite añade un hash al nombre de los assets: se pueden cachear indefinidamente.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + name
		r2.URL.RawPath = ""
		files.ServeHTTP(w, r2)
	})
}

func hiddenPath(name string) bool {
	for seg := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}
