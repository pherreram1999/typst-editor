package httpapi

import (
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"typsteditor/backend/internal/typst"
)

// Enlace de revisión: el propietario (o un admin) guarda una compilación del
// proyecto —una sola por proyecto— y la comparte con un enlace público
// (#/r/<token>). Quien lo abre ve las páginas sin cuenta y sin que el servidor
// vuelva a compilar; la descarga del PDF se puede desactivar (entonces el PDF
// no se sirve en absoluto, solo las páginas). «Actualizar» recompila y
// conserva el enlace; «Nuevo enlace» invalida el anterior.

var reviewTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func newReviewToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (s *Server) reviewDir(id int64) string {
	return filepath.Join(s.opts.DataDir, "review", strconv.FormatInt(id, 10))
}

type reviewRow struct {
	Token         string
	AllowDownload bool
	CreatedAt     time.Time
	CompiledAt    sql.NullTime
	CompiledBy    sql.NullString
}

func (s *Server) loadReview(r *http.Request, projectID int64) (*reviewRow, error) {
	var rv reviewRow
	err := s.db.QueryRowContext(r.Context(), `
SELECT rv.token, rv.allow_download, rv.created_at, rv.compiled_at, u.name
FROM project_reviews rv LEFT JOIN users u ON u.id = rv.compiled_by
WHERE rv.project_id = ?`, projectID).Scan(&rv.Token, &rv.AllowDownload, &rv.CreatedAt, &rv.CompiledAt, &rv.CompiledBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &rv, err
}

// reviewJSON: estado del enlace para el diálogo «Compartir».
func (s *Server) reviewJSON(p *Project, rv *reviewRow, u *User) map[string]any {
	out := map[string]any{"enabled": rv != nil, "can_manage": canManage(u, p)}
	if rv == nil {
		return out
	}
	out["token"] = rv.Token
	out["allow_download"] = rv.AllowDownload
	out["created_at"] = rv.CreatedAt
	out["compiled_by"] = rv.CompiledBy.String
	if meta, err := typst.ReadSnapshot(s.reviewDir(p.ID)); err == nil && meta != nil {
		out["compiled_at"] = meta.CompiledAt
		out["pages"] = len(meta.Pages)
		out["pdf_size"] = meta.PDFSize
	}
	return out
}

// GET /api/projects/{id}/review
func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	rv, err := s.loadReview(r, p.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error al consultar el enlace de revisión")
		return
	}
	writeJSON(w, http.StatusOK, s.reviewJSON(p, rv, currentUser(r)))
}

// POST /api/projects/{id}/review {allow_download?}: compila y guarda la
// instantánea (reemplaza la anterior); crea el enlace si no existía.
func (s *Server) publishReview(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	u := currentUser(r)
	if !canManage(u, p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede compartir el documento para revisión")
		return
	}
	var req struct {
		AllowDownload *bool `json:"allow_download"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
	}
	longRequest(w, 10*time.Minute)
	dir := s.reviewDir(p.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo preparar la carpeta de revisión")
		return
	}
	mu := s.projectLock(p.ID)
	mu.Lock()
	s.hub.FlushProject(p.ID)
	_, diags, err := s.typst.Snapshot(r.Context(), s.projectDir(p.ID), p.MainFile, s.buildDir(p.ID), dir)
	mu.Unlock()
	var ce *typst.CompileError
	if errors.As(err, &ce) {
		if diags == nil {
			diags = []typst.Diagnostic{}
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "el documento tiene errores y no se pudo compilar; corrígelos y vuelve a intentarlo", "diagnostics": diags})
		return
	}
	if err != nil {
		log.Printf("instantánea de revisión del proyecto %d: %v", p.ID, err)
		writeError(w, http.StatusInternalServerError, "no se pudo compilar el documento: "+err.Error())
		return
	}
	allow := true
	if req.AllowDownload != nil {
		allow = *req.AllowDownload
	}
	d := s.dialect
	_, err = s.db.ExecContext(r.Context(), `
INSERT INTO project_reviews (project_id, token, allow_download, created_by, compiled_at, compiled_by)
VALUES (?, ?, ?, ?, ?, ?)
`+d.OnConflict("project_id")+` compiled_at = `+d.Excluded("compiled_at")+`, compiled_by = `+d.Excluded("compiled_by")+`,
  allow_download = CASE WHEN ? THEN `+d.Excluded("allow_download")+` ELSE allow_download END`,
		p.ID, newReviewToken(), allow, u.ID, time.Now(), u.ID, req.AllowDownload != nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo guardar el enlace de revisión")
		return
	}
	rv, err := s.loadReview(r, p.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error al consultar el enlace de revisión")
		return
	}
	writeJSON(w, http.StatusOK, s.reviewJSON(p, rv, u))
}

// PUT /api/projects/{id}/review {allow_download?, new_token?}
func (s *Server) updateReview(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	u := currentUser(r)
	if !canManage(u, p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede cambiar el enlace de revisión")
		return
	}
	var req struct {
		AllowDownload *bool `json:"allow_download"`
		NewToken      bool  `json:"new_token"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	rv, err := s.loadReview(r, p.ID)
	if err != nil || rv == nil {
		writeError(w, http.StatusNotFound, "el proyecto no tiene enlace de revisión")
		return
	}
	allow := rv.AllowDownload
	if req.AllowDownload != nil {
		allow = *req.AllowDownload
	}
	token := rv.Token
	if req.NewToken {
		token = newReviewToken()
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE project_reviews SET allow_download = ?, token = ? WHERE project_id = ?`, allow, token, p.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo actualizar el enlace de revisión")
		return
	}
	rv, _ = s.loadReview(r, p.ID)
	writeJSON(w, http.StatusOK, s.reviewJSON(p, rv, u))
}

// DELETE /api/projects/{id}/review: desactiva el enlace y borra la compilación.
func (s *Server) deleteReview(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	u := currentUser(r)
	if !canManage(u, p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede desactivar el enlace de revisión")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM project_reviews WHERE project_id = ?`, p.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo desactivar el enlace")
		return
	}
	mu := s.projectLock(p.ID)
	mu.Lock()
	_ = os.RemoveAll(s.reviewDir(p.ID))
	mu.Unlock()
	writeJSON(w, http.StatusOK, s.reviewJSON(p, nil, u))
}

// ---------- acceso público (sin cuenta) ----------

type publicReview struct {
	ProjectID     int64
	Name          string
	OwnerName     string
	AllowDownload bool
}

// reviewByToken resuelve el enlace; responde 404 si no existe o se revocó.
func (s *Server) reviewByToken(w http.ResponseWriter, r *http.Request) (*publicReview, bool) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "no-referrer")
	tok := r.PathValue("token")
	if !reviewTokenRe.MatchString(tok) {
		writeError(w, http.StatusNotFound, "enlace de revisión inválido o desactivado")
		return nil, false
	}
	var pr publicReview
	err := s.db.QueryRowContext(r.Context(), `
SELECT p.id, p.name, o.name, rv.allow_download
FROM project_reviews rv JOIN projects p ON p.id = rv.project_id JOIN users o ON o.id = p.owner_id
WHERE rv.token = ?`, tok).Scan(&pr.ProjectID, &pr.Name, &pr.OwnerName, &pr.AllowDownload)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("enlace de revisión: %v", err)
		}
		writeError(w, http.StatusNotFound, "enlace de revisión inválido o desactivado")
		return nil, false
	}
	return &pr, true
}

// GET /api/review/{token}
func (s *Server) reviewInfo(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.reviewByToken(w, r)
	if !ok {
		return
	}
	meta, err := typst.ReadSnapshot(s.reviewDir(pr.ProjectID))
	if err != nil || meta == nil {
		writeError(w, http.StatusNotFound, "la compilación de este enlace ya no está disponible")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"name":           pr.Name,
		"owner":          pr.OwnerName,
		"allow_download": pr.AllowDownload,
		"compiled_at":    meta.CompiledAt,
		"version":        meta.CompiledAt.UnixMilli(),
		"pages":          meta.Pages,
		"pdf_size":       meta.PDFSize,
	})
}

// GET /api/review/{token}/pages/{n}?v=<version>
func (s *Server) reviewPage(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.reviewByToken(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	file, ok := typst.SnapshotPagePath(s.reviewDir(pr.ProjectID), n)
	if !ok {
		writeError(w, http.StatusNotFound, "página no disponible")
		return
	}
	f, err := os.Open(file)
	if err != nil {
		writeError(w, http.StatusNotFound, "página no disponible")
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml")
	// La URL lleva la versión de la compilación: al actualizarla cambia.
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Vary", "Accept-Encoding")
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		_, _ = io.Copy(w, f)
		return
	}
	h.Set("Content-Encoding", "gzip")
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	_, _ = io.Copy(gz, f)
	_ = gz.Close()
}

// GET /api/review/{token}/document.pdf: solo si la descarga está permitida.
func (s *Server) reviewPDF(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.reviewByToken(w, r)
	if !ok {
		return
	}
	if !pr.AllowDownload {
		writeError(w, http.StatusForbidden, "la descarga de este documento está desactivada")
		return
	}
	f, err := os.Open(filepath.Join(s.reviewDir(pr.ProjectID), "document.pdf"))
	if err != nil {
		writeError(w, http.StatusNotFound, "PDF no disponible")
		return
	}
	defer f.Close()
	longRequest(w, 30*time.Minute)
	name := exportName(pr.Name) + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+asciiName(name)+`"; filename*=UTF-8''`+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Time{}, f)
}
