package httpapi

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
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
			if gz := gzipped(fsys, name); gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				h := w.Header()
				h.Set("Content-Encoding", "gzip")
				h.Set("Vary", "Accept-Encoding")
				if ct := contentTypes[path.Ext(name)]; ct != "" {
					h.Set("Content-Type", ct)
				}
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(gz))
				return
			}
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

var contentTypes = map[string]string{
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".wasm": "application/wasm",
	".svg":  "image/svg+xml",
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8", // diccionario ortográfico (~2 MB → ~270 KB)
}

// gzipCache guarda los assets comprimidos (nombre -> []byte, nil si no se
// comprimen). Los assets llevan hash en el nombre: su contenido no cambia.
var gzipCache sync.Map

// gzipped devuelve el asset comprimido con gzip, o nil si no conviene (tipos
// ya comprimidos o archivos pequeños). Se comprime la primera vez que se pide
// (el compilador WASM de typst pasa de 28 MB a unos 11).
func gzipped(fsys fs.FS, name string) []byte {
	if v, ok := gzipCache.Load(name); ok {
		return v.([]byte)
	}
	var out []byte
	if contentTypes[path.Ext(name)] != "" {
		if data, err := fs.ReadFile(fsys, name); err == nil && len(data) > 1024 {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
			zw.Write(data)
			if zw.Close() == nil && buf.Len() < len(data) {
				out = buf.Bytes()
			}
		}
	}
	gzipCache.Store(name, out)
	return out
}
