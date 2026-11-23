package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"typsteditor/backend/internal/typst"
)

// Las plantillas son proyectos marcados con is_template. Cualquier usuario
// puede verlas en la galería, ver su miniatura y copiarlas; editarlas sigue
// requiriendo acceso normal al proyecto.

// readOnlyAccess marca el acceso de quien solo puede ver la plantilla.
func readOnlyAccess(u *User, p *Project) {
	if p.Access == "admin" && u.Role != "admin" {
		p.Access = "template"
	}
}

// projectForCopy carga un proyecto al que el usuario tiene acceso o que es plantilla.
func (s *Server) projectForCopy(w http.ResponseWriter, r *http.Request) (*Project, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return nil, false
	}
	u := currentUser(r)
	p, err := scanProject(s.db.QueryRowContext(r.Context(),
		s.projectSelect+` WHERE p.id = ? AND (`+projectAccess+` OR p.is_template = 1)`, selectArgs(u, id, u.ID, u.Role)...))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "proyecto no encontrado")
		return nil, false
	}
	if err != nil {
		internalError(w, err)
		return nil, false
	}
	readOnlyAccess(u, p)
	return p, true
}

// listTemplates devuelve todas las plantillas, por categoría y nombre.
func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	rows, err := s.db.QueryContext(r.Context(),
		s.projectSelect+` WHERE p.is_template = 1 ORDER BY p.template_category = '', p.template_category, p.name`,
		selectArgs(u)...)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()
	out := []*Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			internalError(w, err)
			return
		}
		readOnlyAccess(u, p)
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

// copyTree copia los archivos de trabajo (sin .git ni ocultos ni enlaces).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil // enlaces y otros tipos se ignoran
	})
}

// cloneProject crea un proyecto nuevo del usuario actual con una copia de los
// archivos de otro (propio, compartido o plantilla). La copia tiene su propio
// historial git y no hereda remoto, cuenta de GitHub, miembros ni marca de plantilla.
// POST /api/projects/{id}/clone {"name": "..."}
func (s *Server) cloneProject(w http.ResponseWriter, r *http.Request) {
	src, ok := s.projectForCopy(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Copia de " + src.Name
		if utf8.RuneCountInString(name) > 128 {
			name = string([]rune(name)[:128])
		}
	}
	if len(name) > 128*4 || utf8.RuneCountInString(name) > 128 {
		writeError(w, http.StatusBadRequest, "nombre demasiado largo (máx. 128)")
		return
	}
	longRequest(w, 2*time.Minute)
	p, err := s.cloneFrom(r.Context(), currentUser(r), src, name)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// cloneFrom crea un proyecto del usuario con una copia de los archivos de src.
func (s *Server) cloneFrom(ctx context.Context, u *User, src *Project, name string) (*Project, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (name, owner_id, main_file, git_branch) VALUES (?, ?, ?, 'main')`, name, u.ID, src.MainFile)
	if err != nil {
		return nil, internalErr(err)
	}
	id, _ := res.LastInsertId()
	dir := s.projectDir(id)
	fail := func(msg string) error {
		_ = os.RemoveAll(dir)
		_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `DELETE FROM projects WHERE id = ?`, id)
		return errf(http.StatusInternalServerError, msg)
	}

	// Copiar con los documentos abiertos guardados y sin operaciones git en curso.
	mu := s.projectLock(src.ID)
	mu.Lock()
	s.hub.FlushProject(src.ID)
	err = copyTree(s.projectDir(src.ID), dir)
	mu.Unlock()
	if err != nil {
		return nil, fail("no se pudieron copiar los archivos")
	}
	if err := s.git.Init(ctx, dir, "main"); err != nil {
		return nil, fail("git init: " + err.Error())
	}
	p, err := s.projectByID(ctx, u, id)
	if err != nil {
		return nil, internalErr(err)
	}
	return p, nil
}

// newBlankProject crea un proyecto vacío del usuario con la plantilla por
// defecto (main.typ) y un repositorio git propio, sin remoto.
func (s *Server) newBlankProject(ctx context.Context, u *User, name string) (*Project, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (name, owner_id, git_branch) VALUES (?, ?, 'main')`, name, u.ID)
	if err != nil {
		return nil, internalErr(err)
	}
	id, _ := res.LastInsertId()
	dir := s.projectDir(id)
	fail := func(msg string) error {
		_ = os.RemoveAll(dir)
		_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `DELETE FROM projects WHERE id = ?`, id)
		return errf(http.StatusInternalServerError, msg)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fail("no se pudo crear el directorio del proyecto")
	}
	if err := s.git.Init(ctx, dir, "main"); err != nil {
		return nil, fail("git init: " + err.Error())
	}
	if err := os.WriteFile(filepath.Join(dir, "main.typ"), []byte(projectTemplate(name)), 0o644); err != nil {
		return nil, fail("no se pudo crear main.typ")
	}
	p, err := s.projectByID(ctx, u, id)
	if err != nil {
		return nil, internalErr(err)
	}
	return p, nil
}

// thumbnail devuelve la primera página del documento como PNG (para la galería).
func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectForCopy(w, r)
	if !ok {
		return
	}
	longRequest(w, 2*time.Minute)
	c := s.cacheFor(p.ID)
	c.mu.Lock()
	defer c.mu.Unlock()
	rev := s.hub.Rev(p.ID)
	file := filepath.Join(s.buildDir(p.ID), "thumb.png")
	if !(c.thumbValid && c.thumbRev == rev) {
		mu := s.projectLock(p.ID)
		mu.Lock()
		s.hub.FlushProject(p.ID)
		_, err := s.typst.Thumbnail(r.Context(), s.projectDir(p.ID), p.MainFile, s.buildDir(p.ID))
		mu.Unlock()
		var ce *typst.CompileError
		if errors.As(err, &ce) {
			writeError(w, http.StatusUnprocessableEntity, "la plantilla no compila")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "no se pudo ejecutar typst")
			return
		}
		c.thumbRev, c.thumbValid = rev, true
	}
	f, err := os.Open(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "miniatura no disponible")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "thumb.png", time.Time{}, f)
}
