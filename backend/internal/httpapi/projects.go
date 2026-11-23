package httpapi

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"typsteditor/backend/internal/collab"
	"typsteditor/backend/internal/database"
	"typsteditor/backend/internal/gitsync"
	"typsteditor/backend/internal/imgopt"
	"typsteditor/backend/internal/typst"
)

type Project struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MainFile  string `json:"main_file"`
	OwnerID   int64  `json:"owner_id"`
	OwnerName string `json:"owner_name"`
	GitRemote string `json:"git_remote"`
	GitBranch string `json:"git_branch"`
	// Plantilla: visible en la galería para todos los usuarios, que pueden copiarla.
	IsTemplate          bool   `json:"is_template"`
	TemplateCategory    string `json:"template_category"`
	TemplateDescription string `json:"template_description"`
	// Cuenta de GitHub cuyas credenciales usa el proyecto (puede ser de otro usuario).
	GitHubAccountID *int64    `json:"github_account_id"`
	GitHubLogin     string    `json:"github_login"`
	GitHubMine      bool      `json:"github_account_mine"`
	HasToken        bool      `json:"has_token"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	// Access: relación del usuario actual con el proyecto: owner | member | admin.
	Access string `json:"access"`
	// Organización personal del usuario actual.
	Starred bool `json:"starred"`
	// Nombres de los colaboradores invitados (para los avatares) y usuarios
	// con el proyecto abierto ahora mismo (presencia del WebSocket).
	MemberNames []string `json:"member_names"`
	Editors     []string `json:"editors"`
	memberList  sql.NullString
	FolderID    *int64 `json:"folder_id"`
	Members     int    `json:"members"`
	// ReadOnly: invitado con un enlace de solo lectura (Access "guest").
	ReadOnly bool `json:"read_only,omitempty"`
	gitToken string
}

type FileEntry struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Editable bool   `json:"editable"`
}

const maxUpload = 20 << 20

// projectSelectTmpl (Server.projectSelect, construido por buildProjectSelect
// según el dialecto) calcula además el acceso del usuario actual y une la cuenta de
// GitHub del proyecto. Sus parámetros son selectArgs(u) y después los de la
// condición (todos los ? de projectSelect son el ID del usuario: selectArgs).
// projectAccess limita a los proyectos propios, a los compartidos
// con el usuario y, para admin, a todos (parámetros: userID, rol).
const (
	projectSelectTmpl = `SELECT p.id, p.name, p.main_file, p.owner_id, u.name, p.git_remote, p.git_branch,
  p.is_template, p.template_category, p.template_description,
  p.github_account_id, COALESCE(ga.login, ''), COALESCE(ga.token, ''), COALESCE(ga.user_id = ?, FALSE),
  p.created_at, p.updated_at,
  CASE WHEN p.owner_id = ? THEN 'owner' WHEN m.user_id IS NOT NULL THEN 'member' ELSE 'admin' END,
  (SELECT COUNT(*) FROM project_members pm WHERE pm.project_id = p.id),
  COALESCE(pr.starred, 0), pr.folder_id,
  (SELECT %s
   FROM project_members pm2 JOIN users u2 ON u2.id = pm2.user_id WHERE pm2.project_id = p.id)
FROM projects p
JOIN users u ON u.id = p.owner_id
LEFT JOIN github_accounts ga ON ga.id = p.github_account_id
LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = ?
LEFT JOIN project_user_prefs pr ON pr.project_id = p.id AND pr.user_id = ?`
	// Destacados primero (el último destacado arriba) y después por actividad.
	projectOrder  = ` ORDER BY COALESCE(pr.starred, 0) DESC, pr.starred_at DESC, p.updated_at DESC`
	projectAccess = `(p.owner_id = ? OR m.user_id IS NOT NULL OR ? = 'admin')`
)

// Extensiones que se abren en el editor colaborativo.
var textExts = map[string]bool{
	".typ": true, ".bib": true, ".yml": true, ".yaml": true, ".toml": true, ".txt": true,
	".md": true, ".csv": true, ".json": true, ".xml": true, ".tex": true, ".csl": true,
}

func isEditable(p string) bool { return textExts[strings.ToLower(path.Ext(p))] }

// buildProjectSelect completa projectSelectTmpl con la lista de miembros
// (separados por salto de línea; scanProject los ordena).
func buildProjectSelect(d database.Dialect) string {
	return fmt.Sprintf(projectSelectTmpl, d.GroupConcat("u2.name", "\n"))
}

// selectArgs devuelve los parámetros de projectSelect seguidos de extra.
func selectArgs(u *User, extra ...any) []any {
	n := strings.Count(projectSelectTmpl, "?")
	args := make([]any, 0, n+len(extra))
	for i := 0; i < n; i++ {
		args = append(args, u.ID)
	}
	return append(args, extra...)
}

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	p := &Project{}
	err := row.Scan(&p.ID, &p.Name, &p.MainFile, &p.OwnerID, &p.OwnerName, &p.GitRemote, &p.GitBranch,
		&p.IsTemplate, &p.TemplateCategory, &p.TemplateDescription,
		&p.GitHubAccountID, &p.GitHubLogin, &p.gitToken, &p.GitHubMine,
		&p.CreatedAt, &p.UpdatedAt, &p.Access, &p.Members, &p.Starred, &p.FolderID, &p.memberList)
	p.MemberNames = []string{}
	if p.memberList.Valid && p.memberList.String != "" {
		p.MemberNames = strings.Split(p.memberList.String, "\n")
		sort.Strings(p.MemberNames)
	}
	p.HasToken = p.gitToken != ""
	return p, err
}

func (s *Server) projectDir(id int64) string {
	return filepath.Join(s.opts.DataDir, "projects", strconv.FormatInt(id, 10))
}

func (s *Server) buildDir(id int64) string {
	return filepath.Join(s.opts.DataDir, "build", strconv.FormatInt(id, 10))
}

// projectLock serializa compilaciones y operaciones git de un proyecto.
func (s *Server) projectLock(id int64) *sync.Mutex {
	m, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// cleanPath valida una ruta relativa al proyecto y la normaliza con "/".
func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	p = path.Clean(p)
	if p == "." || len(p) > 255 || !filepath.IsLocal(filepath.FromSlash(p)) {
		return "", errors.New("ruta inválida")
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return "", errors.New("ruta inválida: no se permiten archivos ocultos")
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return "", errors.New("ruta inválida")
			}
		}
	}
	return p, nil
}

// regularFile devuelve la ruta absoluta si es un archivo regular (no enlace).
func (s *Server) regularFile(id int64, rel string) (string, fs.FileInfo, error) {
	abs := filepath.Join(s.projectDir(id), filepath.FromSlash(rel))
	fi, err := os.Lstat(abs)
	if err != nil {
		return "", nil, err
	}
	if !fi.Mode().IsRegular() {
		return "", nil, errors.New("no es un archivo regular")
	}
	return abs, fi, nil
}

// projectByID carga un proyecto si el usuario tiene acceso (sql.ErrNoRows si no).
func (s *Server) projectByID(ctx context.Context, u *User, id int64) (*Project, error) {
	return scanProject(s.db.QueryRowContext(ctx,
		s.projectSelect+` WHERE p.id = ? AND `+projectAccess, selectArgs(u, id, u.ID, u.Role)...))
}

// loadProject carga el proyecto de la ruta comprobando que el usuario actual
// es propietario, miembro o administrador (un invitado, solo el proyecto de su
// enlace: guestProject). Sin acceso responde 404 para no
// revelar qué proyectos existen.
func (s *Server) loadProject(w http.ResponseWriter, r *http.Request) (*Project, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return nil, false
	}
	var p *Project
	if g := currentGuest(r); g != nil {
		p, err = s.guestProject(r.Context(), g, id)
	} else {
		p, err = s.projectByID(r.Context(), currentUser(r), id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "proyecto no encontrado")
		return nil, false
	}
	if err != nil {
		internalError(w, err)
		return nil, false
	}
	return p, true
}

func canManage(u *User, p *Project) bool { return u.Role == "admin" || u.ID == p.OwnerID }

func longRequest(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

// isOwner: pull y push quedan reservados al propietario (ni miembros ni admins),
// porque usan las credenciales de GitHub del proyecto y deciden qué se publica.
func isOwner(u *User, p *Project) bool { return u.ID == p.OwnerID }

// requireOwner responde 403 si el usuario actual no es el propietario.
func requireOwner(w http.ResponseWriter, r *http.Request, p *Project) bool {
	if isOwner(currentUser(r), p) {
		return true
	}
	writeError(w, http.StatusForbidden, "solo el propietario del proyecto puede hacer pull y push")
	return false
}

func gitError(w http.ResponseWriter, err error) {
	// Pull con conflictos: 409 con las tres versiones de cada archivo para el editor de fusión.
	var ce *gitsync.ConflictError
	if errors.As(err, &ce) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": ce.Error(), "conflict": ce})
		return
	}
	var ue *gitsync.UserError
	if errors.As(err, &ue) {
		writeError(w, http.StatusBadRequest, ue.Msg)
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func (s *Server) author(u *User) *gitsync.Author {
	return &gitsync.Author{Name: u.Name, Email: u.Username + "@users.noreply.typsteditor"}
}

func (s *Server) remote(p *Project) *gitsync.Remote {
	return &gitsync.Remote{URL: p.GitRemote, Branch: p.GitBranch, Token: p.gitToken}
}

// ---------- proyectos ----------

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	rows, err := s.db.QueryContext(r.Context(),
		s.projectSelect+` WHERE `+projectAccess+projectOrder, selectArgs(u, u.ID, u.Role)...)
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
		out = append(out, p)
	}
	editors := s.hub.ActiveEditors(u.ID)
	for _, p := range out {
		p.Editors = editors[p.ID]
		if p.Editors == nil {
			p.Editors = []string{}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func projectTemplate(name string) string {
	title, _ := json.Marshal(name)
	return `#let title = ` + string(title) + `
#set document(title: title)
#set page(paper: "us-letter", margin: 2.5cm, numbering: "1")
#set text(lang: "es", region: "mx", size: 11pt)
#set heading(numbering: "1.")

#align(center, text(20pt, weight: "bold", title))

= Introducción

Escribe aquí tu documento. La vista previa se actualiza mientras escribes y
los cambios de otros usuarios aparecen en tiempo real.

== Ejemplo

Texto con *negrita*, _cursiva_ y una ecuación: $E = m c^2$.

$ sum_(k=1)^n k = (n(n+1)) / 2 $
`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		GitRemote string `json:"git_remote"`
		GitBranch string `json:"git_branch"`
		// Cuenta de GitHub propia con la que clonar y sincronizar.
		GitHubAccountID *int64 `json:"github_account_id"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Name, in.GitRemote, in.GitBranch = strings.TrimSpace(in.Name), strings.TrimSpace(in.GitRemote), strings.TrimSpace(in.GitBranch)
	if in.GitBranch == "" {
		in.GitBranch = "main"
	}
	if in.Name == "" || len(in.Name) > 128 {
		writeError(w, http.StatusBadRequest, "nombre requerido (máx. 128)")
		return
	}
	if err := gitsync.ValidateRemote(in.GitRemote, in.GitBranch); err != nil {
		gitError(w, err)
		return
	}
	u := currentUser(r)
	token := ""
	if in.GitHubAccountID != nil {
		a, err := s.ownAccount(r, *in.GitHubAccountID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cuenta de GitHub no válida")
			return
		}
		token = a.token
	}
	res, err := s.db.ExecContext(r.Context(),
		`INSERT INTO projects (name, owner_id, github_account_id, git_remote, git_branch) VALUES (?, ?, ?, ?, ?)`,
		in.Name, u.ID, in.GitHubAccountID, in.GitRemote, in.GitBranch)
	if err != nil {
		internalError(w, err)
		return
	}
	id, _ := res.LastInsertId()
	dir := s.projectDir(id)
	fail := func(status int, msg string) {
		_ = os.RemoveAll(dir)
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM projects WHERE id=?`, id)
		writeError(w, status, msg)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		fail(http.StatusInternalServerError, "no se pudo crear el directorio del proyecto")
		return
	}

	cloned := false
	if in.GitRemote != "" {
		longRequest(w, 3*time.Minute)
		err := s.git.Clone(r.Context(), dir, &gitsync.Remote{URL: in.GitRemote, Branch: in.GitBranch, Token: token})
		var ue *gitsync.UserError
		switch {
		case err == nil:
			cloned = true
		case errors.As(err, &ue) && ue.BranchMissing:
			// Repositorio vacío o rama nueva: se empieza con la plantilla y el
			// primer push creará la rama en GitHub.
			_ = os.RemoveAll(dir)
		case errors.As(err, &ue):
			fail(http.StatusBadRequest, ue.Msg)
			return
		default:
			fail(http.StatusBadGateway, err.Error())
			return
		}
	}
	if cloned {
		// Elegir el archivo principal: main.typ o el primer .typ de la raíz.
		if _, err := os.Stat(filepath.Join(dir, "main.typ")); err != nil {
			if m, _ := filepath.Glob(filepath.Join(dir, "*.typ")); len(m) > 0 {
				sort.Strings(m)
				_, _ = s.db.ExecContext(r.Context(), `UPDATE projects SET main_file=? WHERE id=?`, filepath.Base(m[0]), id)
			}
		}
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(http.StatusInternalServerError, "no se pudo crear el directorio del proyecto")
			return
		}
		if err := s.git.Init(r.Context(), dir, in.GitBranch); err != nil {
			fail(http.StatusInternalServerError, "git init: "+err.Error())
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "main.typ"), []byte(projectTemplate(in.Name)), 0o644); err != nil {
			fail(http.StatusInternalServerError, "no se pudo crear main.typ")
			return
		}
	}
	p, err := s.projectByID(r.Context(), u, id)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if !canManage(currentUser(r), p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede cambiar los ajustes")
		return
	}
	var in struct {
		Name      *string `json:"name"`
		MainFile  *string `json:"main_file"`
		GitRemote *string `json:"git_remote"`
		GitBranch *string `json:"git_branch"`
		// 0 = sin cuenta. Solo se pueden asignar cuentas propias.
		GitHubAccountID     *int64  `json:"github_account_id"`
		IsTemplate          *bool   `json:"is_template"`
		TemplateCategory    *string `json:"template_category"`
		TemplateDescription *string `json:"template_description"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
		if p.Name == "" || len(p.Name) > 128 {
			writeError(w, http.StatusBadRequest, "nombre requerido (máx. 128)")
			return
		}
	}
	if in.MainFile != nil {
		mf, err := cleanPath(*in.MainFile)
		if err != nil || path.Ext(mf) != ".typ" {
			writeError(w, http.StatusBadRequest, "el archivo principal debe ser un .typ del proyecto")
			return
		}
		if _, _, err := s.regularFile(p.ID, mf); err != nil {
			writeError(w, http.StatusBadRequest, "el archivo principal no existe")
			return
		}
		p.MainFile = mf
	}
	if in.GitRemote != nil {
		p.GitRemote = strings.TrimSpace(*in.GitRemote)
	}
	if in.GitBranch != nil {
		p.GitBranch = strings.TrimSpace(*in.GitBranch)
	}
	if err := gitsync.ValidateRemote(p.GitRemote, p.GitBranch); err != nil {
		gitError(w, err)
		return
	}
	if in.GitHubAccountID != nil {
		switch id := *in.GitHubAccountID; {
		case id == 0:
			p.GitHubAccountID = nil
		case p.GitHubAccountID != nil && *p.GitHubAccountID == id:
			// sin cambios (puede ser la cuenta de otro usuario, p. ej. si edita un admin)
		default:
			if _, err := s.ownAccount(r, id); err != nil {
				writeError(w, http.StatusBadRequest, "solo puedes asignar tus propias cuentas de GitHub")
				return
			}
			p.GitHubAccountID = &id
		}
	}
	if in.IsTemplate != nil {
		p.IsTemplate = *in.IsTemplate
	}
	if in.TemplateCategory != nil {
		p.TemplateCategory = strings.Join(strings.Fields(*in.TemplateCategory), " ")
		if utf8.RuneCountInString(p.TemplateCategory) > 64 {
			writeError(w, http.StatusBadRequest, "categoría demasiado larga (máx. 64)")
			return
		}
	}
	if in.TemplateDescription != nil {
		p.TemplateDescription = strings.TrimSpace(*in.TemplateDescription)
		if utf8.RuneCountInString(p.TemplateDescription) > 255 {
			writeError(w, http.StatusBadRequest, "descripción demasiado larga (máx. 255)")
			return
		}
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE projects SET name=?, main_file=?, git_remote=?, git_branch=?, github_account_id=?,
		 is_template=?, template_category=?, template_description=? WHERE id=?`,
		p.Name, p.MainFile, p.GitRemote, p.GitBranch, p.GitHubAccountID,
		p.IsTemplate, p.TemplateCategory, p.TemplateDescription, p.ID); err != nil {
		internalError(w, err)
		return
	}
	s.hub.BumpRev(p.ID, true)
	p, err := s.projectByID(r.Context(), currentUser(r), p.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if !canManage(currentUser(r), p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede eliminar el proyecto")
		return
	}
	mu := s.projectLock(p.ID)
	mu.Lock()
	defer mu.Unlock()
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM projects WHERE id=?`, p.ID); err != nil {
		internalError(w, err)
		return
	}
	s.hub.CloseProject(p.ID, "el proyecto se ha eliminado")
	_ = os.RemoveAll(s.projectDir(p.ID))
	_ = os.RemoveAll(s.buildDir(p.ID))
	_ = os.RemoveAll(s.reviewDir(p.ID))
	s.cache.Delete(p.ID)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- archivos ----------

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	out, err := s.projectFiles(p.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo leer el proyecto")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out, "rev": s.hub.Rev(p.ID)})
}

// projectFiles lista los archivos regulares del proyecto (sin ocultos), ordenados.
func (s *Server) projectFiles(id int64) ([]FileEntry, error) {
	root := s.projectDir(id)
	out := []FileEntry{}
	err := filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if abs == root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, abs)
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		out = append(out, FileEntry{Path: rel, Size: fi.Size(), Editable: isEditable(rel)})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func (s *Server) createFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if !requireWrite(w, r) {
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rel, err := cleanPath(in.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !isEditable(rel) {
		writeError(w, http.StatusBadRequest, "extensión no editable (usa .typ, .bib, .yml, .txt, …); para imágenes usa «Subir»")
		return
	}
	abs := filepath.Join(s.projectDir(p.ID), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		writeError(w, http.StatusBadRequest, "no se pudo crear la carpeta")
		return
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		writeError(w, http.StatusConflict, "el archivo ya existe")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "no se pudo crear el archivo")
		return
	}
	f.Close()
	s.hub.BumpRev(p.ID, true)
	writeJSON(w, http.StatusCreated, FileEntry{Path: rel, Editable: true})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if !requireWrite(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "archivo requerido (máx. 20 MB)")
		return
	}
	defer file.Close()
	// path: reemplazar un archivo existente (con otro nombre de origen);
	// si no, se guarda con su nombre en dir.
	name := path.Base(strings.ReplaceAll(hdr.Filename, `\`, "/"))
	replace := strings.TrimSpace(r.FormValue("path"))
	if replace != "" {
		name = replace
	} else if dir := strings.TrimSpace(r.FormValue("dir")); dir != "" {
		name = dir + "/" + name
	}
	rel, err := cleanPath(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil || len(data) > maxUpload {
		writeError(w, http.StatusBadRequest, "archivo demasiado grande (máx. 20 MB)")
		return
	}
	optimize := r.FormValue("optimize") == "1" && isImage(rel)
	if optimize {
		longRequest(w, 3*time.Minute)
	}
	res, err := s.storeFile(p, rel, data, optimize, replace != "")
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// storeFile guarda un archivo subido (nuevo o reemplazando otro; con
// mustExist, solo reemplazando). Si es de texto y está abierto en el editor,
// el contenido nuevo llega a los clientes como un cambio remoto.
func (s *Server) storeFile(p *Project, rel string, data []byte, optimize, mustExist bool) (*uploadResult, error) {
	abs := filepath.Join(s.projectDir(p.ID), filepath.FromSlash(rel))
	if mustExist {
		if _, _, err := s.regularFile(p.ID, rel); err != nil {
			return nil, errf(http.StatusNotFound, "el archivo a reemplazar no existe")
		}
	}
	if fi, err := os.Lstat(abs); err == nil && !fi.Mode().IsRegular() {
		return nil, errf(http.StatusBadRequest, "ya existe una entrada con ese nombre")
	}
	if len(data) > maxUpload {
		return nil, errf(http.StatusBadRequest, "archivo demasiado grande (máx. 20 MB)")
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, errf(http.StatusBadRequest, "no se pudo crear la carpeta")
	}
	var opt *imgopt.Result
	if optimize && isImage(rel) {
		var res imgopt.Result
		data, res = imgopt.Optimize(data)
		opt = &res
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return nil, errf(http.StatusInternalServerError, "no se pudo guardar el archivo")
	}
	// Si el archivo estaba abierto en el editor, se propaga como cambio remoto.
	if isEditable(rel) {
		if _, err := collab.LoadText(abs); err == nil {
			s.hub.ReplaceText(p.ID, rel, string(data))
		}
	}
	s.hub.BumpRev(p.ID, true)
	return &uploadResult{FileEntry{Path: rel, Size: int64(len(data)), Editable: isEditable(rel)}, opt}, nil
}

type uploadResult struct {
	FileEntry
	Optimized *imgopt.Result `json:"optimized,omitempty"`
}

func isImage(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

// optimizeImage reduce sin pérdida una imagen del proyecto (ver imgopt).
// Se procesa de una en una para que el cliente muestre el progreso.
func (s *Server) optimizeImage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if !requireWrite(w, r) {
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rel, err := cleanPath(in.Path)
	if err != nil || !isImage(rel) {
		writeError(w, http.StatusBadRequest, "solo se optimizan imágenes PNG o JPEG")
		return
	}
	longRequest(w, 3*time.Minute)
	abs, _, err := s.regularFile(p.ID, rel)
	if err != nil {
		writeError(w, http.StatusNotFound, "archivo no encontrado")
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo leer el archivo")
		return
	}
	out, res := imgopt.Optimize(data)
	if res.After < res.Before {
		// Bajo el candado del proyecto, para no cambiarla en plena compilación.
		mu := s.projectLock(p.ID)
		mu.Lock()
		err := writeFileAtomic(abs, out)
		mu.Unlock()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "no se pudo guardar el archivo")
			return
		}
		s.hub.BumpRev(p.ID, true)
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "result": res})
}

// writeFileAtomic escribe mediante un temporal en el mismo directorio.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *Server) rawFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	rel, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.hub.FlushProject(p.ID)
	abs, fi, err := s.regularFile(p.ID, rel)
	if err != nil {
		writeError(w, http.StatusNotFound, "archivo no encontrado")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "archivo no encontrado")
		return
	}
	defer f.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, path.Base(rel), fi.ModTime(), f)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if !requireWrite(w, r) {
		return
	}
	rel, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.removeFile(p, rel); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeFile borra un archivo (no el principal) y las carpetas que queden vacías.
func (s *Server) removeFile(p *Project, rel string) error {
	if rel == p.MainFile {
		return errf(http.StatusBadRequest, "no se puede borrar el archivo principal")
	}
	abs, _, err := s.regularFile(p.ID, rel)
	if err != nil {
		return errf(http.StatusNotFound, "archivo no encontrado")
	}
	s.hub.CloseDoc(p.ID, rel, "el archivo se ha eliminado")
	if err := os.Remove(abs); err != nil {
		return errf(http.StatusInternalServerError, "no se pudo eliminar")
	}
	s.pruneEmptyDirs(p.ID, filepath.Dir(abs))
	s.hub.BumpRev(p.ID, true)
	return nil
}

// pruneEmptyDirs elimina dir y sus padres mientras estén vacíos (sin llegar a la raíz).
func (s *Server) pruneEmptyDirs(id int64, dir string) {
	root := s.projectDir(id)
	for d := dir; d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break
		}
	}
}

// ---------- compilación ----------

type compileCache struct {
	mu         sync.Mutex
	svgRev     int64
	svgValid   bool
	svg        map[string]any // respuesta de /preview (solo metadatos)
	pages      []typst.Page   // páginas de la última compilación correcta
	prevPages  []typst.Page   // y de la anterior (aún se pueden pedir)
	pdfRev     int64
	pdfValid   bool
	pdfDiags   []typst.Diagnostic // advertencias de la compilación guardada
	thumbRev   int64
	thumbValid bool
	// Mapa de sincronización vista previa ↔ código (su propio mutex: no
	// debe bloquear las peticiones de /preview mientras se calcula).
	syncMu    sync.Mutex
	syncRev   int64
	syncValid bool
	sync      []typst.SyncPoint
}

func (s *Server) cacheFor(id int64) *compileCache {
	// Una fuente añadida o quitada cambia el resultado sin cambiar la revisión
	// del proyecto: se descartan las compilaciones guardadas.
	if sig := s.typst.FontsSignature(); s.fontSig.Swap(sig) != sig {
		s.cache.Clear()
	}
	c, _ := s.cache.LoadOrStore(id, &compileCache{})
	return c.(*compileCache)
}

// preview compila la vista previa y devuelve sus diagnósticos y, por página,
// su hash y tamaño. El contenido de cada página se pide aparte (previewPage):
// un documento de cientos de páginas ocupa cientos de MB en SVG y el
// navegador solo descarga las que se ven y las que cambiaron.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	// La primera vez se generan las copias reducidas de las imágenes.
	longRequest(w, 5*time.Minute)
	c := s.cacheFor(p.ID)
	c.mu.Lock()
	defer c.mu.Unlock()
	rev := s.hub.Rev(p.ID)
	if c.svgValid && c.svgRev == rev {
		writeJSON(w, http.StatusOK, c.svg)
		return
	}
	mu := s.projectLock(p.ID)
	mu.Lock()
	s.hub.FlushProject(p.ID)
	pages, diags, err := s.typst.Preview(r.Context(), s.projectDir(p.ID), p.MainFile, s.buildDir(p.ID))
	mu.Unlock()
	if diags == nil {
		diags = []typst.Diagnostic{}
	}
	var ce *typst.CompileError
	var out map[string]any
	switch {
	case errors.As(err, &ce):
		out = map[string]any{"ok": false, "rev": rev, "output": ce.Output, "diagnostics": diags}
	case err != nil:
		writeError(w, http.StatusInternalServerError, "no se pudo ejecutar typst: "+err.Error())
		return
	default:
		out = map[string]any{"ok": true, "rev": rev, "pages": pages, "diagnostics": diags}
		c.prevPages, c.pages = c.pages, pages
		typst.PrunePages(s.buildDir(p.ID), c.prevPages, c.pages)
	}
	c.svg, c.svgRev, c.svgValid = out, rev, true
	writeJSON(w, http.StatusOK, out)
}

// previewPage sirve el SVG de una página por su hash. El contenido nunca
// cambia para un hash dado, así que el navegador lo guarda en caché.
func (s *Server) previewPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	file, ok := typst.PagePath(s.buildDir(p.ID), r.PathValue("hash"))
	if !ok {
		writeError(w, http.StatusNotFound, "página no disponible; vuelve a compilar")
		return
	}
	f, err := os.Open(file)
	if err != nil {
		writeError(w, http.StatusNotFound, "página no disponible; vuelve a compilar")
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Vary", "Accept-Encoding")
	// Con gzip, el SVG de typst (trazos de glifos) ocupa aproximadamente la mitad.
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		_, _ = io.Copy(w, f)
		return
	}
	h.Set("Content-Encoding", "gzip")
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	_, _ = io.Copy(gz, f)
	_ = gz.Close()
}

// previewSync devuelve los puntos que relacionan posiciones de la vista
// previa con archivo y línea del código (ver typst/sync.go).
func (s *Server) previewSync(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	longRequest(w, 5*time.Minute)
	pts, rev, err := s.syncPoints(r.Context(), p)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rev": rev, "points": pts})
}

// syncPoints devuelve el mapa vista previa ↔ código de la revisión actual (en caché).
func (s *Server) syncPoints(ctx context.Context, p *Project) ([]typst.SyncPoint, int64, error) {
	c := s.cacheFor(p.ID)
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	rev := s.hub.Rev(p.ID)
	if !(c.syncValid && c.syncRev == rev) {
		mu := s.projectLock(p.ID)
		mu.Lock()
		s.hub.FlushProject(p.ID)
		pts, err := s.typst.SyncMap(ctx, s.projectDir(p.ID), p.MainFile, s.buildDir(p.ID))
		mu.Unlock()
		var ce *typst.CompileError
		if errors.As(err, &ce) {
			return nil, rev, errf(http.StatusUnprocessableEntity, "el documento no compila")
		}
		if err != nil {
			return nil, rev, errf(http.StatusInternalServerError, "no se pudo ejecutar typst: "+err.Error())
		}
		if pts == nil {
			pts = []typst.SyncPoint{}
		}
		c.sync, c.syncRev, c.syncValid = pts, rev, true
	}
	return c.sync, rev, nil
}

func (s *Server) pdf(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	longRequest(w, 2*time.Minute)
	// El PDF se lee con el candado de la caché: otra compilación no lo cambia a medias.
	c := s.cacheFor(p.ID)
	c.mu.Lock()
	defer c.mu.Unlock()
	res, err := s.compilePDFLocked(r.Context(), p, c)
	var ce *typst.CompileError
	if errors.As(err, &ce) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ce.Output, "diagnostics": res.Diagnostics})
		return
	}
	if err != nil {
		writeAPIError(w, err)
		return
	}
	rev := res.Rev
	f, err := os.Open(res.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PDF no disponible")
		return
	}
	defer f.Close()
	name := strings.TrimSuffix(path.Base(p.MainFile), ".typ") + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.Header().Set("X-Typst-Rev", strconv.FormatInt(rev, 10))
	http.ServeContent(w, r, name, time.Time{}, f)
}

// pdfResult es el resultado de compilar el PDF de un proyecto.
type pdfResult struct {
	Rev         int64
	File        string
	Diagnostics []typst.Diagnostic // de la última compilación (vacío si venía de caché)
	Cached      bool
}

// compilePDFLocked compila el PDF si la revisión cambió (con c.mu tomado).
// Si el documento no compila devuelve *typst.CompileError con los diagnósticos.
func (s *Server) compilePDFLocked(ctx context.Context, p *Project, c *compileCache) (*pdfResult, error) {
	rev := s.hub.Rev(p.ID)
	res := &pdfResult{Rev: rev, File: filepath.Join(s.buildDir(p.ID), "document.pdf"), Diagnostics: []typst.Diagnostic{}}
	if c.pdfValid && c.pdfRev == rev {
		res.Cached = true
		res.Diagnostics = c.pdfDiags
		return res, nil
	}
	mu := s.projectLock(p.ID)
	mu.Lock()
	s.hub.FlushProject(p.ID)
	_, diags, err := s.typst.PDF(ctx, s.projectDir(p.ID), p.MainFile, s.buildDir(p.ID))
	mu.Unlock()
	if diags != nil {
		res.Diagnostics = diags
	}
	var ce *typst.CompileError
	if errors.As(err, &ce) {
		return res, err
	}
	if err != nil {
		return res, errf(http.StatusInternalServerError, "no se pudo ejecutar typst: "+err.Error())
	}
	c.pdfRev, c.pdfValid, c.pdfDiags = rev, true, res.Diagnostics
	return res, nil
}

// ---------- git ----------

func (s *Server) gitStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok || !requireOwner(w, r, p) {
		return
	}
	mu := s.projectLock(p.ID)
	mu.Lock()
	defer mu.Unlock()
	s.hub.FlushProject(p.ID)
	st, err := s.git.GetStatus(r.Context(), s.projectDir(p.ID), p.GitBranch)
	if err != nil {
		gitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) gitPull(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok || !requireOwner(w, r, p) {
		return
	}
	longRequest(w, 3*time.Minute)
	mu := s.projectLock(p.ID)
	mu.Lock()
	defer mu.Unlock()
	s.hub.FlushProject(p.ID)
	dir := s.projectDir(p.ID)
	msg, err := s.git.Pull(r.Context(), dir, s.remote(p), s.author(currentUser(r)))
	if err != nil {
		gitError(w, err)
		return
	}
	s.reloadOpenDocs(p)
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// gitResolve: POST /git/pull/resolve {theirs, files: [Resolution]} completa
// un pull con conflictos con lo resuelto en el editor de fusión.
func (s *Server) gitResolve(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok || !requireOwner(w, r, p) {
		return
	}
	var in struct {
		Theirs string               `json:"theirs"`
		Files  []gitsync.Resolution `json:"files"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.Theirs) < 40 || len(in.Files) == 0 {
		writeError(w, http.StatusBadRequest, "faltan las resoluciones de la fusión")
		return
	}
	longRequest(w, 3*time.Minute)
	mu := s.projectLock(p.ID)
	mu.Lock()
	defer mu.Unlock()
	s.hub.FlushProject(p.ID)
	msg, err := s.git.Resolve(r.Context(), s.projectDir(p.ID), s.author(currentUser(r)), in.Theirs, in.Files)
	if err != nil {
		gitError(w, err)
		return
	}
	s.reloadOpenDocs(p)
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// reloadOpenDocs lleva a los editores abiertos el contenido del disco tras
// un cambio externo (pull, fusión) y avisa de la nueva revisión.
func (s *Server) reloadOpenDocs(p *Project) {
	for _, rel := range s.hub.OpenPaths(p.ID) {
		abs, _, err := s.regularFile(p.ID, rel)
		if err != nil {
			s.hub.CloseDoc(p.ID, rel, "el archivo se eliminó al hacer pull")
			continue
		}
		if b, err := os.ReadFile(abs); err == nil {
			s.hub.ReplaceText(p.ID, rel, string(b))
		}
	}
	s.hub.BumpRev(p.ID, true)
}

func (s *Server) gitPush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok || !requireOwner(w, r, p) {
		return
	}
	var in struct {
		Message string `json:"message"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Message = strings.TrimSpace(in.Message)
	if in.Message == "" {
		in.Message = "Actualización desde Typst Extend"
	}
	if len(in.Message) > 2000 {
		writeError(w, http.StatusBadRequest, "mensaje de commit demasiado largo")
		return
	}
	longRequest(w, 3*time.Minute)
	mu := s.projectLock(p.ID)
	mu.Lock()
	defer mu.Unlock()
	s.hub.FlushProject(p.ID)
	msg, err := s.git.Push(r.Context(), s.projectDir(p.ID), s.remote(p), s.author(currentUser(r)), in.Message)
	if err != nil {
		gitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}
