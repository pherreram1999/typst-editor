package httpapi

// Recursos para el compilador de typst que corre en el navegador (vista
// previa en vivo): las fuentes del servidor, los paquetes y los archivos del
// espejo de la vista previa. El PDF, las miniaturas y el mapa de
// sincronización se siguen compilando en el servidor.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"typsteditor/backend/internal/typst"
)

const immutable = "private, max-age=31536000, immutable"

// typstFonts devuelve el catálogo de fuentes del servidor: versión de typst,
// etiqueta del catálogo y, en orden de carga, el ID y tamaño de cada archivo.
func (s *Server) typstFonts(w http.ResponseWriter, r *http.Request) {
	longRequest(w, time.Minute)
	cat, err := s.typst.Fonts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo leer el catálogo de fuentes: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"typst": cat.Version, "etag": cat.ETag, "fonts": cat.Files})
}

// typstFontsMeta sirve los esqueletos de todas las fuentes (ver
// typst.FontCatalog.Meta), ya comprimidos con gzip.
func (s *Server) typstFontsMeta(w http.ResponseWriter, r *http.Request) {
	longRequest(w, time.Minute)
	cat, err := s.typst.Fonts(r.Context())
	if err == nil {
		var meta []byte
		if meta, err = cat.Meta(); err == nil {
			h := w.Header()
			h.Set("Content-Type", "application/octet-stream")
			h.Set("ETag", `"`+cat.ETag+`"`)
			h.Set("Cache-Control", "private, no-cache")
			if r.Header.Get("If-None-Match") == `"`+cat.ETag+`"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				h.Set("Content-Encoding", "gzip")
				h.Set("Content-Length", strconv.Itoa(len(meta)))
				_, _ = w.Write(meta)
				return
			}
			gz, err := gzip.NewReader(bytes.NewReader(meta))
			if err == nil {
				_, _ = io.Copy(w, gz)
			}
			return
		}
	}
	writeError(w, http.StatusInternalServerError, "no se pudieron preparar las fuentes: "+err.Error())
}

// typstFont sirve una fuente completa por su ID (SHA-256 del contenido).
func (s *Server) typstFont(w http.ResponseWriter, r *http.Request) {
	cat, err := s.typst.Fonts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo leer el catálogo de fuentes")
		return
	}
	f := cat.Font(r.PathValue("id"))
	if f == nil {
		writeError(w, http.StatusNotFound, "fuente no encontrada")
		return
	}
	rd, err := f.Open()
	if err != nil {
		writeError(w, http.StatusNotFound, "fuente no disponible")
		return
	}
	defer rd.Close()
	longRequest(w, 5*time.Minute) // hay colecciones CJK de decenas de MB
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Cache-Control", immutable)
	http.ServeContent(w, r, "", time.Time{}, rd)
}

// ---------- paquetes ----------

var (
	pkgNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	pkgVersionRe = regexp.MustCompile(`^\d{1,9}\.\d{1,9}\.\d{1,9}$`)
	pkgMu        sync.Mutex // descargas de paquetes (una a la vez)
)

// packageDirs son los directorios donde el typst del servidor busca paquetes:
// los locales y los ya descargados (las mismas variables y valores por
// omisión que typst).
func packageDirs() []string {
	home, _ := os.UserHomeDir()
	env := func(name, xdg, def string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		if v := os.Getenv(xdg); v != "" {
			return filepath.Join(v, "typst", "packages")
		}
		return filepath.Join(home, def, "typst", "packages")
	}
	return []string{
		env("TYPST_PACKAGE_PATH", "XDG_DATA_HOME", filepath.Join(".local", "share")),
		env("TYPST_PACKAGE_CACHE_PATH", "XDG_CACHE_HOME", ".cache"),
	}
}

// typstPackage sirve un paquete como .tar.gz: el que ya tiene el typst del
// servidor (local o descargado) o, para @preview, el de packages.typst.org,
// que se guarda en DATA_DIR/packages.
func (s *Server) typstPackage(w http.ResponseWriter, r *http.Request) {
	ns, name, ver := r.PathValue("ns"), r.PathValue("name"), r.PathValue("version")
	if !pkgNameRe.MatchString(ns) || !pkgNameRe.MatchString(name) || !pkgVersionRe.MatchString(ver) {
		writeError(w, http.StatusBadRequest, "paquete inválido")
		return
	}
	longRequest(w, 2*time.Minute)
	for _, base := range packageDirs() {
		dir := filepath.Join(base, ns, name, ver)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Cache-Control", "private, no-cache")
			if err := tarGz(w, dir); err != nil {
				writeError(w, http.StatusInternalServerError, "no se pudo empaquetar "+name)
			}
			return
		}
	}
	if ns != "preview" {
		writeError(w, http.StatusNotFound, "paquete no encontrado")
		return
	}
	file := filepath.Join(s.opts.DataDir, "packages", ns, name+"-"+ver+".tar.gz")
	if _, err := os.Stat(file); err != nil {
		if err := downloadPackage(r, file, "https://packages.typst.org/"+ns+"/"+name+"-"+ver+".tar.gz"); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
	}
	f, err := os.Open(file)
	if err != nil {
		writeError(w, http.StatusNotFound, "paquete no encontrado")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", immutable) // una versión publicada no cambia
	http.ServeContent(w, r, "", time.Time{}, f)
}

func downloadPackage(r *http.Request, file, url string) error {
	pkgMu.Lock()
	defer pkgMu.Unlock()
	if _, err := os.Stat(file); err == nil {
		return nil // lo descargó otra petición mientras esperaba
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "typsteditor")
	res, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return errors.New("no se pudo descargar el paquete")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("paquete no encontrado")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".pkg-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, io.LimitReader(res.Body, 64<<20)); err != nil {
		tmp.Close()
		return errors.New("no se pudo descargar el paquete")
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// tarGz escribe dir como .tar.gz (solo archivos regulares y directorios).
func tarGz(w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ---------- archivos del proyecto ----------

// previewFiles lista los archivos del espejo de la vista previa (con las
// imágenes reducidas) para que el navegador descargue los que le faltan.
func (s *Server) previewFiles(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	longRequest(w, 5*time.Minute) // la primera vez se reducen las imágenes
	mu := s.projectLock(p.ID)
	mu.Lock()
	s.hub.FlushProject(p.ID)
	_, files, err := s.typst.PreviewFiles(s.projectDir(p.ID), p.MainFile, s.buildDir(p.ID))
	mu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo preparar la vista previa: "+err.Error())
		return
	}
	if files == nil {
		files = []typst.MirrorFile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rev": s.hub.Rev(p.ID), "main": p.MainFile, "files": files})
}

// previewFile sirve un archivo del espejo. Con ?v= (de previewFiles) la
// respuesta se puede guardar en caché: otro contenido tendría otra v.
func (s *Server) previewFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	rel, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mirror, err := filepath.Abs(filepath.Join(s.buildDir(p.ID), "preview"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	// Las entradas del espejo son copias o enlaces a archivos regulares del
	// propio proyecto (que nunca contiene enlaces).
	abs := filepath.Join(mirror, filepath.FromSlash(rel))
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "archivo no encontrado")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "archivo no encontrado")
		return
	}
	longRequest(w, 2*time.Minute)
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	if r.URL.Query().Get("v") != "" {
		h.Set("Cache-Control", immutable)
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, path.Base(rel), fi.ModTime(), f)
}
