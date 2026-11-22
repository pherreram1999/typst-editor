package httpapi

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Descarga del proyecto completo en .zip.
//
// El navegador descarga el archivo directamente (a disco, con su propia barra
// de progreso) en lugar de cargarlo entero en memoria con fetch: una tesis con
// imágenes grandes pesa cientos de MB. Como una descarga normal no puede
// llevar la cabecera Authorization, el cliente pide antes un ticket de un solo
// uso que caduca en un minuto (así el JWT nunca aparece en una URL).

const ticketTTL = time.Minute

type downloadTicket struct {
	userID    int64
	projectID int64
	expires   time.Time
}

type ticketStore struct {
	mu sync.Mutex
	m  map[string]downloadTicket
}

func (t *ticketStore) issue(userID, projectID int64) string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]downloadTicket{}
	}
	for k, v := range t.m { // limpiar caducados
		if now.After(v.expires) {
			delete(t.m, k)
		}
	}
	t.m[id] = downloadTicket{userID, projectID, now.Add(ticketTTL)}
	return id
}

// take consume el ticket: solo sirve una vez.
func (t *ticketStore) take(id string) (downloadTicket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.m[id]
	delete(t.m, id)
	if !ok || time.Now().After(v.expires) {
		return downloadTicket{}, false
	}
	return v, true
}

// exportTicket: POST /api/projects/{id}/export/ticket → {"url": "..."}.
func (s *Server) exportTicket(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	t := s.tickets.issue(currentUser(r).ID, p.ID)
	writeJSON(w, http.StatusOK, map[string]string{
		"url": "/api/projects/" + strconv.FormatInt(p.ID, 10) + "/export.zip?ticket=" + t,
	})
}

// exportZip: GET /api/projects/{id}/export.zip?ticket=<ticket>. Se autentica
// con el ticket (ver arriba) y vuelve a comprobar el usuario y su acceso.
func (s *Server) exportZip(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tickets.take(r.URL.Query().Get("ticket"))
	if !ok || strconv.FormatInt(t.projectID, 10) != r.PathValue("id") {
		writeError(w, http.StatusUnauthorized, "enlace de descarga inválido o caducado; vuelve a intentarlo")
		return
	}
	u, err := s.userByID(r.Context(), t.userID)
	if err != nil || !u.Active {
		writeError(w, http.StatusUnauthorized, "sesión no válida")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	// Lo que hay en memoria del editor puede ir por delante del disco.
	s.hub.FlushProject(p.ID)
	longRequest(w, 30*time.Minute)

	name := exportName(p.Name)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+asciiName(name)+`.zip"; filename*=UTF-8''`+url.PathEscape(name+".zip"))
	w.Header().Set("Cache-Control", "no-store")
	if err := writeProjectZip(w, s.projectDir(p.ID), name); err != nil {
		// Las cabeceras ya se enviaron: el navegador verá la descarga fallida.
		log.Printf("exportar proyecto %d: %v", p.ID, err)
	}
}

// writeProjectZip escribe los archivos de trabajo del proyecto (sin .git,
// ocultos ni enlaces, igual que copyTree) dentro de la carpeta `root`.
func writeProjectZip(out io.Writer, dir, root string) error {
	zw := zip.NewWriter(out)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // enlaces y otros tipos se ignoran
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = root + "/" + filepath.ToSlash(rel)
		if d.IsDir() {
			h.Name += "/"
			_, err = zw.CreateHeader(h)
			return err
		}
		h.Method = zip.Deflate
		if compressed(rel) {
			h.Method = zip.Store // ya comprimidos: deflate solo gastaría CPU
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, f)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func compressed(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip", ".gz", ".woff", ".woff2", ".mp4", ".mp3":
		return true
	}
	return false
}

// exportName convierte el nombre del proyecto en un nombre de archivo seguro.
func exportName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(s))
	s = strings.Trim(s, ". ")
	if s == "" {
		return "proyecto"
	}
	return s
}

// asciiName es el respaldo de filename= para clientes sin filename*.
func asciiName(s string) string {
	return strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, s)
}
