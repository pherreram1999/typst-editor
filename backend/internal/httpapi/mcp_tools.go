package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"typsteditor/backend/internal/collab"
	"typsteditor/backend/internal/typst"
)

// Herramientas MCP. Todas pasan por s.project (permisos, ver mcp.go) y las
// que modifican texto por hub.EditText (colaboración en vivo).

const (
	readFileMaxLines = 2000
	embedMaxBytes    = 5 << 20  // binarios incrustados en la respuesta
	pdfEmbedMax      = 10 << 20 // PDF incrustado
	textMaxBytes     = 2 << 20  // texto devuelto por download_file/read_file
)

func (s *Server) newMCPServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "nullpointer-editor", Title: "nullpointer editor", Version: "1.0.0"},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	s.addMCPTools(srv)
	return srv
}

// tool registra una herramienta: identifica al que llama antes de ejecutarla.
func tool[In, Out any](s *Server, srv *mcp.Server, t *mcp.Tool, h func(context.Context, *mcpCaller, In) (*mcp.CallToolResult, Out, error)) {
	mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		c, err := s.caller(ctx, req)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return h(ctx, c, in)
	})
}

func ptr[T any](v T) *T { return &v }

// readOnly / writes: anotaciones para que el cliente sepa qué pide confirmación.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func writes(title string, destructive, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: ptr(destructive), IdempotentHint: idempotent, OpenWorldHint: ptr(false)}
}

func textResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

// apiMessage convierte un error compartido con la API HTTP en un error de herramienta.
func apiMessage(err error) error {
	var ae *apiError
	if errors.As(err, &ae) {
		return errors.New(ae.msg)
	}
	return err
}

// ---------- tipos de entrada y salida ----------

type projectArg struct {
	ProjectID int64 `json:"project_id" jsonschema:"ID del proyecto (ver list_projects)"`
}

type fileArg struct {
	ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto (ver list_projects)"`
	Path      string `json:"path" jsonschema:"ruta relativa a la raíz del proyecto, con /, p. ej. capitulos/intro.typ"`
}

type projectSummary struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	MainFile   string    `json:"main_file"`
	Access     string    `json:"access" jsonschema:"owner (propio) o member (compartido contigo)"`
	Owner      string    `json:"owner"`
	Members    []string  `json:"members"`
	Editing    []string  `json:"editing_now" jsonschema:"otros usuarios con el proyecto abierto ahora"`
	IsTemplate bool      `json:"is_template"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func summarize(p *Project, editors []string) projectSummary {
	if editors == nil {
		editors = []string{}
	}
	return projectSummary{ID: p.ID, Name: p.Name, MainFile: p.MainFile, Access: p.Access, Owner: p.OwnerName,
		Members: p.MemberNames, Editing: editors, IsTemplate: p.IsTemplate, UpdatedAt: p.UpdatedAt}
}

// ---------- registro ----------

func (s *Server) addMCPTools(srv *mcp.Server) {
	s.addProjectTools(srv)
	s.addFileTools(srv)
	s.addTransferTools(srv)
	s.addCompileTools(srv)
}

func (s *Server) addProjectTools(srv *mcp.Server) {
	type whoami struct {
		ID          int64    `json:"id"`
		Username    string   `json:"username"`
		Name        string   `json:"name"`
		Role        string   `json:"role"`
		Scopes      []string `json:"scopes"`
		AllProjects bool     `json:"all_projects" jsonschema:"true si este acceso incluye todos tus proyectos"`
		Projects    []int64  `json:"projects" jsonschema:"si no, los proyectos a los que está limitado"`
	}
	tool(s, srv, &mcp.Tool{Name: "whoami", Description: "Usuario autenticado y alcance de este acceso (permisos y proyectos).",
		Annotations: readOnly("Quién soy")},
		func(ctx context.Context, c *mcpCaller, _ struct{}) (*mcp.CallToolResult, whoami, error) {
			return nil, whoami{ID: c.user.ID, Username: c.user.Username, Name: c.user.Name, Role: c.realRole,
				Scopes: c.grant.Scopes, AllProjects: c.grant.AllProjects, Projects: nonNilIDs(c.grant.Projects)}, nil
		})

	type listIn struct {
		Query string `json:"query,omitempty" jsonschema:"filtra por nombre (sin distinguir mayúsculas)"`
	}
	type listOut struct {
		Projects []projectSummary `json:"projects"`
	}
	tool(s, srv, &mcp.Tool{Name: "list_projects",
		Description: "Lista los proyectos a los que tienes acceso (propios y compartidos), destacados y más recientes primero.",
		Annotations: readOnly("Listar proyectos")},
		func(ctx context.Context, c *mcpCaller, in listIn) (*mcp.CallToolResult, listOut, error) {
			ps, err := s.mcpProjects(ctx, c)
			if err != nil {
				return nil, listOut{}, err
			}
			editors := s.hub.ActiveEditors(c.user.ID)
			out := listOut{Projects: []projectSummary{}}
			q := strings.ToLower(strings.TrimSpace(in.Query))
			for _, p := range ps {
				if q == "" || strings.Contains(strings.ToLower(p.Name), q) {
					out.Projects = append(out.Projects, summarize(p, editors[p.ID]))
				}
			}
			return nil, out, nil
		})

	type projectOut struct {
		projectSummary
		GitRemote string `json:"git_remote"`
		GitBranch string `json:"git_branch"`
		Files     int    `json:"files"`
		CanManage bool   `json:"can_manage" jsonschema:"si puedes cambiar nombre o archivo principal (update_project)"`
	}
	tool(s, srv, &mcp.Tool{Name: "get_project", Description: "Detalles de un proyecto: archivo principal, miembros, remoto git, número de archivos.",
		Annotations: readOnly("Ver proyecto")},
		func(ctx context.Context, c *mcpCaller, in projectArg) (*mcp.CallToolResult, projectOut, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, projectOut{}, err
			}
			files, _ := s.projectFiles(p.ID)
			return nil, projectOut{projectSummary: summarize(p, s.hub.ActiveEditors(c.user.ID)[p.ID]),
				GitRemote: p.GitRemote, GitBranch: p.GitBranch, Files: len(files), CanManage: canManage(c.user, p)}, nil
		})

	type template struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Description string `json:"description"`
	}
	type templatesOut struct {
		Templates []template `json:"templates"`
	}
	tool(s, srv, &mcp.Tool{Name: "list_templates", Description: "Plantillas publicadas (usables con create_project template_id).",
		Annotations: readOnly("Listar plantillas")},
		func(ctx context.Context, c *mcpCaller, _ struct{}) (*mcp.CallToolResult, templatesOut, error) {
			rows, err := s.db.QueryContext(ctx, `SELECT id, name, template_category, template_description FROM projects
  WHERE is_template = 1 ORDER BY template_category = '', template_category, name`)
			if err != nil {
				return nil, templatesOut{}, errors.New("error interno")
			}
			defer rows.Close()
			out := templatesOut{Templates: []template{}}
			for rows.Next() {
				var t template
				if err := rows.Scan(&t.ID, &t.Name, &t.Category, &t.Description); err != nil {
					return nil, templatesOut{}, errors.New("error interno")
				}
				out.Templates = append(out.Templates, t)
			}
			return nil, out, nil
		})

	type createIn struct {
		Name       string `json:"name" jsonschema:"nombre del proyecto (máx. 128)"`
		TemplateID int64  `json:"template_id,omitempty" jsonschema:"copiar los archivos de esta plantilla o de uno de tus proyectos; sin él se crea con un main.typ de ejemplo"`
	}
	tool(s, srv, &mcp.Tool{Name: "create_project",
		Description: "Crea un proyecto tuyo, vacío (main.typ de ejemplo) o como copia de una plantilla o de otro proyecto al que tengas acceso.",
		Annotations: writes("Crear proyecto", false, false)},
		func(ctx context.Context, c *mcpCaller, in createIn) (*mcp.CallToolResult, projectSummary, error) {
			if !c.canWrite() {
				return nil, projectSummary{}, errReadOnly
			}
			name := strings.TrimSpace(in.Name)
			if name == "" || utf8.RuneCountInString(name) > 128 {
				return nil, projectSummary{}, errors.New("nombre requerido (máx. 128 caracteres)")
			}
			var p *Project
			var err error
			if in.TemplateID != 0 {
				src, err := s.copySource(ctx, c, in.TemplateID)
				if err != nil {
					return nil, projectSummary{}, err
				}
				p, err = s.cloneFrom(ctx, c.user, src, name)
				if err != nil {
					return nil, projectSummary{}, apiMessage(err)
				}
			} else if p, err = s.newBlankProject(ctx, c.user, name); err != nil {
				return nil, projectSummary{}, apiMessage(err)
			}
			// Un acceso limitado a ciertos proyectos incluye los que crea.
			if !c.grant.AllProjects {
				if err := s.oauth.AddProject(ctx, c.grant.GrantID, p.ID); err != nil {
					return nil, projectSummary{}, errors.New("error interno")
				}
			}
			return nil, summarize(p, nil), nil
		})

	type updateIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Name      string `json:"name,omitempty" jsonschema:"nuevo nombre"`
		MainFile  string `json:"main_file,omitempty" jsonschema:"nuevo archivo principal (un .typ existente)"`
	}
	tool(s, srv, &mcp.Tool{Name: "update_project", Description: "Cambia el nombre o el archivo principal de un proyecto (solo el propietario).",
		Annotations: writes("Ajustes del proyecto", false, true)},
		func(ctx context.Context, c *mcpCaller, in updateIn) (*mcp.CallToolResult, projectSummary, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, projectSummary{}, err
			}
			if !canManage(c.user, p) {
				return nil, projectSummary{}, errors.New("solo el propietario puede cambiar los ajustes del proyecto")
			}
			if n := strings.TrimSpace(in.Name); n != "" {
				if utf8.RuneCountInString(n) > 128 {
					return nil, projectSummary{}, errors.New("nombre demasiado largo (máx. 128)")
				}
				p.Name = n
			}
			if in.MainFile != "" {
				mf, err := cleanPath(in.MainFile)
				if err != nil || path.Ext(mf) != ".typ" {
					return nil, projectSummary{}, errors.New("el archivo principal debe ser un .typ del proyecto")
				}
				if _, _, err := s.regularFile(p.ID, mf); err != nil {
					return nil, projectSummary{}, errors.New("el archivo principal no existe")
				}
				p.MainFile = mf
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE projects SET name = ?, main_file = ? WHERE id = ?`, p.Name, p.MainFile, p.ID); err != nil {
				return nil, projectSummary{}, errors.New("error interno")
			}
			s.hub.BumpRev(p.ID, true)
			return nil, summarize(p, nil), nil
		})

	tool(s, srv, &mcp.Tool{Name: "git_status",
		Description: "Estado git local del proyecto: archivos cambiados sin publicar, último commit y commits por delante/detrás del remoto (solo el propietario). Pull y push se hacen desde la web.",
		Annotations: readOnly("Estado git")},
		func(ctx context.Context, c *mcpCaller, in projectArg) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, nil, err
			}
			if !isOwner(c.user, p) {
				return nil, nil, errors.New("solo el propietario del proyecto puede ver el estado git")
			}
			mu := s.projectLock(p.ID)
			mu.Lock()
			defer mu.Unlock()
			s.hub.FlushProject(p.ID)
			st, err := s.git.GetStatus(ctx, s.projectDir(p.ID), p.GitBranch)
			if err != nil {
				return nil, nil, fmt.Errorf("git: %v", err)
			}
			return nil, map[string]any{"remote": p.GitRemote, "branch": p.GitBranch, "status": st}, nil
		})
}

func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// mcpProjects lista los proyectos accesibles por MCP (propios y compartidos,
// filtrados por la concesión).
func (s *Server) mcpProjects(ctx context.Context, c *mcpCaller) ([]*Project, error) {
	u := c.user
	rows, err := s.db.QueryContext(ctx, s.projectSelect+` WHERE `+projectAccess+projectOrder, selectArgs(u, u.ID, u.Role)...)
	if err != nil {
		return nil, errors.New("error interno")
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, errors.New("error interno")
		}
		if c.allows(p.ID) {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// copySource: proyecto del que copiar (accesible por MCP, o una plantilla).
func (s *Server) copySource(ctx context.Context, c *mcpCaller, id int64) (*Project, error) {
	if p, err := s.project(ctx, c, id, false); err == nil {
		return p, nil
	}
	u := c.user
	p, err := scanProject(s.db.QueryRowContext(ctx, s.projectSelect+` WHERE p.id = ? AND p.is_template = 1`, selectArgs(u, id)...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("plantilla o proyecto %d no encontrado (ver list_templates)", id)
	}
	if err != nil {
		return nil, errors.New("error interno")
	}
	return p, nil
}

// ---------- archivos de texto ----------

// filePath valida una ruta de archivo del proyecto.
func filePath(raw string) (string, error) {
	rel, err := cleanPath(raw)
	if err != nil {
		return "", fmt.Errorf("%v: %q", err, raw)
	}
	return rel, nil
}

// readText devuelve el contenido actual de un archivo de texto (lo que hay en
// el editor si está abierto, que puede ir por delante del disco).
func (s *Server) readText(p *Project, rel string) (string, error) {
	if t, ok := s.hub.Text(p.ID, rel); ok {
		return t, nil
	}
	abs, fi, err := s.regularFile(p.ID, rel)
	if err != nil {
		return "", fmt.Errorf("archivo no encontrado: %s (usa list_files)", rel)
	}
	if fi.Size() > collab.MaxDocSize {
		return "", errors.New("archivo demasiado grande para leerlo como texto; usa download_file")
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", errors.New("no se pudo leer el archivo")
	}
	if !utf8.Valid(b) || slices.Contains(b, 0) {
		return "", errors.New("no es un archivo de texto; usa view_image (imágenes) o download_file")
	}
	return strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n"), nil
}

// editText modifica un archivo editable a través del hub. Con create, el
// archivo puede no existir.
func (s *Server) editText(p *Project, rel string, create bool, fn func(string) (string, error)) (string, bool, error) {
	if !isEditable(rel) {
		return "", false, errors.New("solo se editan archivos de texto (.typ, .bib, .yml, .toml, .txt, .md, .csv, .json, .xml, .tex, .csl); para otros usa upload_file")
	}
	abs := filepath.Join(s.projectDir(p.ID), filepath.FromSlash(rel))
	if fi, err := os.Lstat(abs); err == nil && !fi.Mode().IsRegular() {
		return "", false, errors.New("ya existe una entrada con ese nombre que no es un archivo")
	} else if err != nil && !create {
		return "", false, fmt.Errorf("archivo no encontrado: %s (usa list_files, o write_file para crearlo)", rel)
	}
	text, created, err := s.hub.EditText(p.ID, rel, abs, create, fn)
	switch {
	case errors.Is(err, collab.ErrTooLarge):
		return "", false, errors.New("el documento superaría el tamaño máximo (5 M caracteres)")
	case errors.Is(err, collab.ErrNotText):
		return "", false, errors.New("el archivo no es texto UTF-8")
	}
	return text, created, err
}

func normalizeNL(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// lineOf devuelve el número de línea (desde 1) del desplazamiento off.
func lineOf(text string, off int) int { return strings.Count(text[:off], "\n") + 1 }

// numbered devuelve las líneas [from, to] (desde 1) con su número delante.
func numbered(lines []string, from int) string {
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%6d\t%s\n", from+i, l)
	}
	return b.String()
}

// snippet muestra unas líneas alrededor de [l0, l1] del texto final.
func snippet(text string, l0, l1 int) string {
	lines := strings.Split(text, "\n")
	from := max(1, l0-2)
	to := min(len(lines), l1+2)
	return numbered(lines[from-1:to], from)
}

func (s *Server) addFileTools(srv *mcp.Server) {
	type listIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Dir       string `json:"dir,omitempty" jsonschema:"solo los archivos bajo esta carpeta"`
	}
	type listOut struct {
		MainFile string      `json:"main_file"`
		Files    []FileEntry `json:"files" jsonschema:"editable: se puede leer y editar como texto"`
	}
	tool(s, srv, &mcp.Tool{Name: "list_files", Description: "Lista los archivos del proyecto con su tamaño en bytes.",
		Annotations: readOnly("Listar archivos")},
		func(ctx context.Context, c *mcpCaller, in listIn) (*mcp.CallToolResult, listOut, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, listOut{}, err
			}
			files, err := s.projectFiles(p.ID)
			if err != nil {
				return nil, listOut{}, errors.New("no se pudo leer el proyecto")
			}
			if d := strings.Trim(in.Dir, "/ "); d != "" && d != "." {
				files = slices.DeleteFunc(files, func(f FileEntry) bool { return !strings.HasPrefix(f.Path, d+"/") })
			}
			return nil, listOut{MainFile: p.MainFile, Files: files}, nil
		})

	type readIn struct {
		ProjectID   int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path        string `json:"path" jsonschema:"ruta del archivo"`
		Offset      int    `json:"offset,omitempty" jsonschema:"primera línea a leer (desde 1)"`
		Limit       int    `json:"limit,omitempty" jsonschema:"número máximo de líneas (por defecto 2000)"`
		LineNumbers *bool  `json:"line_numbers,omitempty" jsonschema:"anteponer el número de línea (por defecto true)"`
	}
	tool(s, srv, &mcp.Tool{Name: "read_file",
		Description: "Lee un archivo de texto del proyecto (el contenido actual, incluido lo que otros estén escribiendo). Devuelve las líneas numeradas; usa offset/limit en archivos largos.",
		Annotations: readOnly("Leer archivo")},
		func(ctx context.Context, c *mcpCaller, in readIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			text, err := s.readText(p, rel)
			if err != nil {
				return nil, nil, err
			}
			lines := strings.Split(text, "\n")
			from := max(1, in.Offset)
			limit := in.Limit
			if limit <= 0 {
				limit = readFileMaxLines
			}
			if from > len(lines) {
				return textResult("%s tiene %d líneas; offset %d está fuera del archivo.", rel, len(lines), from), nil, nil
			}
			to := min(len(lines), from+limit-1)
			var body string
			if in.LineNumbers == nil || *in.LineNumbers {
				body = numbered(lines[from-1:to], from)
			} else {
				body = strings.Join(lines[from-1:to], "\n")
			}
			if len(body) > textMaxBytes {
				return nil, nil, errors.New("el fragmento es demasiado grande; usa un limit menor")
			}
			head := fmt.Sprintf("%s — líneas %d-%d de %d\n", rel, from, to, len(lines))
			if to < len(lines) {
				head += fmt.Sprintf("(hay más: continúa con offset %d)\n", to+1)
			}
			return textResult("%s\n%s", head, body), nil, nil
		})

	type searchIn struct {
		ProjectID     int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Pattern       string `json:"pattern" jsonschema:"texto a buscar (o expresión regular RE2 si regex es true)"`
		Regex         bool   `json:"regex,omitempty" jsonschema:"interpretar pattern como expresión regular"`
		CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"distinguir mayúsculas"`
		Dir           string `json:"dir,omitempty" jsonschema:"buscar solo bajo esta carpeta"`
		MaxResults    int    `json:"max_results,omitempty" jsonschema:"máximo de coincidencias (por defecto 100)"`
	}
	tool(s, srv, &mcp.Tool{Name: "search_files", Description: "Busca texto en los archivos de texto del proyecto; devuelve archivo:línea: contenido.",
		Annotations: readOnly("Buscar en archivos")},
		func(ctx context.Context, c *mcpCaller, in searchIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, nil, err
			}
			if in.Pattern == "" {
				return nil, nil, errors.New("pattern requerido")
			}
			expr := in.Pattern
			if !in.Regex {
				expr = regexp.QuoteMeta(expr)
			}
			if !in.CaseSensitive {
				expr = "(?i)" + expr
			}
			re, err := regexp.Compile(expr)
			if err != nil {
				return nil, nil, fmt.Errorf("expresión regular inválida: %v", err)
			}
			maxN := in.MaxResults
			if maxN <= 0 || maxN > 1000 {
				maxN = 100
			}
			files, err := s.projectFiles(p.ID)
			if err != nil {
				return nil, nil, errors.New("no se pudo leer el proyecto")
			}
			dir := strings.Trim(in.Dir, "/ ")
			var b strings.Builder
			n := 0
			for _, f := range files {
				if !f.Editable || (dir != "" && dir != "." && !strings.HasPrefix(f.Path, dir+"/")) {
					continue
				}
				text, err := s.readText(p, f.Path)
				if err != nil {
					continue
				}
				for i, line := range strings.Split(text, "\n") {
					if re.MatchString(line) {
						if n == maxN {
							fmt.Fprintf(&b, "… (más de %d coincidencias; afina la búsqueda)\n", maxN)
							return textResult("%s", b.String()), nil, nil
						}
						if len(line) > 300 {
							line = line[:300] + "…"
						}
						fmt.Fprintf(&b, "%s:%d: %s\n", f.Path, i+1, line)
						n++
					}
				}
			}
			if n == 0 {
				return textResult("Sin coincidencias."), nil, nil
			}
			return textResult("%d coincidencias:\n%s", n, b.String()), nil, nil
		})

	type writeIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path      string `json:"path" jsonschema:"ruta del archivo (se crean las carpetas que falten)"`
		Content   string `json:"content" jsonschema:"contenido completo del archivo"`
	}
	tool(s, srv, &mcp.Tool{Name: "write_file",
		Description: "Crea un archivo de texto o reemplaza todo su contenido. Para cambios parciales usa edit_file.",
		Annotations: writes("Escribir archivo", true, true)},
		func(ctx context.Context, c *mcpCaller, in writeIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			text, created, err := s.editText(p, rel, true, func(string) (string, error) { return in.Content, nil })
			if err != nil {
				return nil, nil, err
			}
			verb := "Actualizado"
			if created {
				verb = "Creado"
			}
			return textResult("%s %s (%d líneas).", verb, rel, strings.Count(text, "\n")+1), nil, nil
		})

	type editIn struct {
		ProjectID  int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path       string `json:"path" jsonschema:"ruta del archivo"`
		OldString  string `json:"old_string" jsonschema:"texto exacto a reemplazar (incluye contexto suficiente para que sea único)"`
		NewString  string `json:"new_string" jsonschema:"texto nuevo"`
		ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"reemplazar todas las apariciones"`
	}
	tool(s, srv, &mcp.Tool{Name: "edit_file",
		Description: "Reemplaza un fragmento exacto de un archivo de texto. Falla si old_string no aparece o aparece varias veces (salvo replace_all). Copia old_string de read_file sin los números de línea.",
		Annotations: writes("Editar archivo", true, false)},
		func(ctx context.Context, c *mcpCaller, in editIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			oldS, newS := normalizeNL(in.OldString), normalizeNL(in.NewString)
			if oldS == "" {
				return nil, nil, errors.New("old_string no puede estar vacío (usa insert_text o write_file)")
			}
			if oldS == newS {
				return nil, nil, errors.New("old_string y new_string son iguales")
			}
			var count, first int
			text, _, err := s.editText(p, rel, false, func(cur string) (string, error) {
				count = strings.Count(cur, oldS)
				switch {
				case count == 0:
					return "", fmt.Errorf("old_string no aparece en %s (revisa espacios y saltos de línea con read_file)", rel)
				case count > 1 && !in.ReplaceAll:
					return "", fmt.Errorf("old_string aparece %d veces en %s; añade contexto para que sea único o usa replace_all", count, rel)
				}
				first = strings.Index(cur, oldS)
				if in.ReplaceAll {
					return strings.ReplaceAll(cur, oldS, newS), nil
				}
				return strings.Replace(cur, oldS, newS, 1), nil
			})
			if err != nil {
				return nil, nil, err
			}
			l0 := lineOf(text, first)
			l1 := l0 + strings.Count(newS, "\n")
			msg := fmt.Sprintf("Editado %s", rel)
			if count > 1 {
				msg += fmt.Sprintf(" (%d reemplazos)", count)
			}
			return textResult("%s. Resultado alrededor del primer cambio:\n%s", msg, snippet(text, l0, l1)), nil, nil
		})

	type insertIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path      string `json:"path" jsonschema:"ruta del archivo"`
		Line      int    `json:"line" jsonschema:"insertar después de esta línea (0 = al principio; -1 = al final)"`
		Text      string `json:"text" jsonschema:"texto a insertar (una o varias líneas)"`
	}
	tool(s, srv, &mcp.Tool{Name: "insert_text", Description: "Inserta líneas en un archivo de texto después de la línea indicada.",
		Annotations: writes("Insertar texto", false, false)},
		func(ctx context.Context, c *mcpCaller, in insertIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			ins := strings.TrimSuffix(normalizeNL(in.Text), "\n")
			at := 0
			text, _, err := s.editText(p, rel, false, func(cur string) (string, error) {
				lines := strings.Split(cur, "\n")
				at = in.Line
				if at < 0 || at > len(lines) {
					at = len(lines)
				}
				if cur == "" {
					return ins, nil
				}
				out := append(append(slices.Clone(lines[:at]), strings.Split(ins, "\n")...), lines[at:]...)
				return strings.Join(out, "\n"), nil
			})
			if err != nil {
				return nil, nil, err
			}
			return textResult("Insertado en %s después de la línea %d:\n%s", rel, at, snippet(text, at+1, at+1+strings.Count(ins, "\n"))), nil, nil
		})

	type renameIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		From      string `json:"from" jsonschema:"ruta actual"`
		To        string `json:"to" jsonschema:"ruta nueva (se crean las carpetas que falten)"`
	}
	tool(s, srv, &mcp.Tool{Name: "rename_file",
		Description: "Renombra o mueve un archivo. Recuerda actualizar los #include/#import/image(…) que lo referencien (search_files).",
		Annotations: writes("Renombrar archivo", true, false)},
		func(ctx context.Context, c *mcpCaller, in renameIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, nil, err
			}
			from, err := filePath(in.From)
			if err != nil {
				return nil, nil, err
			}
			to, err := filePath(in.To)
			if err != nil {
				return nil, nil, err
			}
			if from == to {
				return nil, nil, errors.New("las rutas son iguales")
			}
			if from == p.MainFile && !canManage(c.user, p) {
				return nil, nil, errors.New("solo el propietario puede renombrar el archivo principal")
			}
			if from == p.MainFile && path.Ext(to) != ".typ" {
				return nil, nil, errors.New("el archivo principal debe seguir siendo un .typ")
			}
			if err := s.renameFile(ctx, p, from, to); err != nil {
				return nil, nil, apiMessage(err)
			}
			return textResult("Renombrado %s → %s.", from, to), nil, nil
		})

	tool(s, srv, &mcp.Tool{Name: "delete_file", Description: "Elimina un archivo del proyecto (no el principal). No se puede deshacer salvo con git.",
		Annotations: writes("Eliminar archivo", true, true)},
		func(ctx context.Context, c *mcpCaller, in fileArg) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			if err := s.removeFile(p, rel); err != nil {
				return nil, nil, apiMessage(err)
			}
			return textResult("Eliminado %s.", rel), nil, nil
		})
}

// renameFile mueve un archivo dentro del proyecto. Si está abierto se guarda
// y se cierra (los editores vuelven a abrirlo con la ruta nueva).
func (s *Server) renameFile(ctx context.Context, p *Project, from, to string) error {
	srcAbs, _, err := s.regularFile(p.ID, from)
	if err != nil {
		return errf(http.StatusNotFound, "archivo no encontrado: "+from)
	}
	dstAbs := filepath.Join(s.projectDir(p.ID), filepath.FromSlash(to))
	if _, err := os.Lstat(dstAbs); err == nil {
		return errf(http.StatusConflict, "ya existe "+to)
	}
	mu := s.projectLock(p.ID)
	mu.Lock()
	defer mu.Unlock()
	s.hub.SaveAndClose(p.ID, from, "el archivo se ha renombrado a "+to)
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return errf(http.StatusBadRequest, "no se pudo crear la carpeta")
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		return errf(http.StatusInternalServerError, "no se pudo renombrar")
	}
	s.pruneEmptyDirs(p.ID, filepath.Dir(srcAbs))
	if from == p.MainFile {
		if _, err := s.db.ExecContext(ctx, `UPDATE projects SET main_file = ? WHERE id = ?`, to, p.ID); err != nil {
			return errf(http.StatusInternalServerError, "renombrado, pero no se pudo actualizar el archivo principal")
		}
		p.MainFile = to
	}
	s.hub.BumpRev(p.ID, true)
	return nil
}

// ---------- subir y descargar ----------

func mimeOf(rel string) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(rel))); t != "" {
		return t
	}
	return "application/octet-stream"
}

func (s *Server) addTransferTools(srv *mcp.Server) {
	type uploadIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path      string `json:"path" jsonschema:"ruta de destino, p. ej. imagenes/figura.png"`
		Content   string `json:"content_base64" jsonschema:"contenido del archivo en base64 (máx. 20 MB)"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"reemplazar si ya existe"`
		Optimize  bool   `json:"optimize,omitempty" jsonschema:"optimizar sin pérdida PNG/JPEG"`
	}
	tool(s, srv, &mcp.Tool{Name: "upload_file",
		Description: "Sube un archivo (imagen, fuente, datos, .bib…) con su contenido en base64. Para archivos grandes que tengas en disco usa create_upload_url.",
		Annotations: writes("Subir archivo", true, true)},
		func(ctx context.Context, c *mcpCaller, in uploadIn) (*mcp.CallToolResult, *uploadResult, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			data, err := decodeBase64(in.Content)
			if err != nil {
				return nil, nil, err
			}
			return s.mcpStore(p, rel, data, in.Overwrite, in.Optimize)
		})

	type uploadURLIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path      string `json:"path" jsonschema:"ruta de destino en el proyecto"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"permitir reemplazar un archivo existente"`
	}
	type uploadURLOut struct {
		URL       string `json:"url"`
		Method    string `json:"method"`
		ExpiresIn int    `json:"expires_in" jsonschema:"segundos"`
		Example   string `json:"example"`
	}
	tool(s, srv, &mcp.Tool{Name: "create_upload_url",
		Description: "Crea un enlace de un solo uso (15 min) para subir un archivo sin pasarlo por la conversación: PUT con el contenido en el cuerpo (curl -T archivo URL) o POST multipart con el campo «file».",
		Annotations: writes("Enlace de subida", false, false)},
		func(ctx context.Context, c *mcpCaller, in uploadURLIn) (*mcp.CallToolResult, uploadURLOut, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, uploadURLOut{}, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, uploadURLOut{}, err
			}
			if !in.Overwrite {
				if _, _, err := s.regularFile(p.ID, rel); err == nil {
					return nil, uploadURLOut{}, fmt.Errorf("%s ya existe (usa overwrite: true para reemplazarlo)", rel)
				}
			}
			u := s.mcpLink(c, p, "upload", rel, in.Overwrite)
			return nil, uploadURLOut{URL: u, Method: "PUT", ExpiresIn: int(mcpUploadTTL.Seconds()),
				Example: fmt.Sprintf("curl -fsS -T %s '%s'", shellQuote(path.Base(rel)), u)}, nil
		})

	type fromURLIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		URL       string `json:"url" jsonschema:"URL https pública del archivo"`
		Path      string `json:"path" jsonschema:"ruta de destino en el proyecto"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"reemplazar si ya existe"`
		Optimize  bool   `json:"optimize,omitempty" jsonschema:"optimizar sin pérdida PNG/JPEG"`
	}
	tool(s, srv, &mcp.Tool{Name: "upload_from_url",
		Description: "Descarga un archivo desde una URL https pública (máx. 20 MB) y lo guarda en el proyecto.",
		Annotations: &mcp.ToolAnnotations{Title: "Subir desde URL", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(true)}},
		func(ctx context.Context, c *mcpCaller, in fromURLIn) (*mcp.CallToolResult, *uploadResult, error) {
			p, err := s.project(ctx, c, in.ProjectID, true)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			if !in.Overwrite {
				if _, _, err := s.regularFile(p.ID, rel); err == nil {
					return nil, nil, fmt.Errorf("%s ya existe (usa overwrite: true para reemplazarlo)", rel)
				}
			}
			data, _, err := fetchURL(ctx, in.URL)
			if err != nil {
				return nil, nil, err
			}
			return s.mcpStore(p, rel, data, in.Overwrite, in.Optimize)
		})

	type downloadIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path      string `json:"path" jsonschema:"ruta del archivo"`
		Embed     *bool  `json:"embed,omitempty" jsonschema:"incluir el contenido en la respuesta (por defecto true; máx. 5 MB) además del enlace"`
	}
	tool(s, srv, &mcp.Tool{Name: "download_file",
		Description: "Obtiene cualquier archivo del proyecto: su contenido (texto, o binario como recurso incrustado) y un enlace de descarga válido 10 min (p. ej. para curl -o).",
		Annotations: readOnly("Descargar archivo")},
		func(ctx context.Context, c *mcpCaller, in downloadIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			s.hub.FlushProject(p.ID)
			abs, fi, err := s.regularFile(p.ID, rel)
			if err != nil {
				return nil, nil, fmt.Errorf("archivo no encontrado: %s (usa list_files)", rel)
			}
			link := s.mcpLink(c, p, "file", rel, false)
			res := textResult("%s (%d bytes, %s)\nEnlace de descarga (10 min): %s", rel, fi.Size(), mimeOf(rel), link)
			res.Content = append(res.Content, &mcp.ResourceLink{URI: link, Name: path.Base(rel), MIMEType: mimeOf(rel), Size: ptr(fi.Size())})
			if in.Embed != nil && !*in.Embed {
				return res, nil, nil
			}
			if fi.Size() > embedMaxBytes {
				res.Content = append(res.Content, &mcp.TextContent{Text: "El archivo supera 5 MB: no se incluye su contenido, usa el enlace."})
				return res, nil, nil
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, nil, errors.New("no se pudo leer el archivo")
			}
			uri := fmt.Sprintf("typst-editor://projects/%d/files/%s", p.ID, rel)
			if utf8.Valid(data) && !slices.Contains(data, 0) && len(data) <= textMaxBytes {
				res.Content = append(res.Content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: uri, MIMEType: mimeOf(rel), Text: string(data)}})
			} else {
				res.Content = append(res.Content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: uri, MIMEType: mimeOf(rel), Blob: data}})
			}
			return res, nil, nil
		})

	type viewIn struct {
		ProjectID int64  `json:"project_id" jsonschema:"ID del proyecto"`
		Path      string `json:"path" jsonschema:"ruta de la imagen PNG o JPEG (o SVG)"`
		MaxSize   int    `json:"max_size,omitempty" jsonschema:"lado mayor máximo en píxeles (por defecto 1568)"`
	}
	tool(s, srv, &mcp.Tool{Name: "view_image", Description: "Muestra una imagen del proyecto (reducida si es muy grande) para verla.",
		Annotations: readOnly("Ver imagen")},
		func(ctx context.Context, c *mcpCaller, in viewIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filePath(in.Path)
			if err != nil {
				return nil, nil, err
			}
			abs, fi, err := s.regularFile(p.ID, rel)
			if err != nil {
				return nil, nil, fmt.Errorf("archivo no encontrado: %s", rel)
			}
			if fi.Size() > maxUpload*2 {
				return nil, nil, errors.New("imagen demasiado grande")
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, nil, errors.New("no se pudo leer el archivo")
			}
			if strings.EqualFold(path.Ext(rel), ".svg") {
				if len(data) > textMaxBytes {
					return nil, nil, errors.New("SVG demasiado grande")
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: "image/svg+xml"}}}, nil, nil
			}
			side := in.MaxSize
			if side <= 0 || side > 4096 {
				side = 1568
			}
			img, mt, err := typst.ViewImage(data, side)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("%s (%d bytes en el proyecto)", rel, fi.Size())},
				&mcp.ImageContent{Data: img, MIMEType: mt},
			}}, nil, nil
		})

	type exportOut struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	tool(s, srv, &mcp.Tool{Name: "export_project", Description: "Enlace (10 min) para descargar el proyecto completo en .zip (sin la carpeta .git).",
		Annotations: readOnly("Exportar .zip")},
		func(ctx context.Context, c *mcpCaller, in projectArg) (*mcp.CallToolResult, exportOut, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, exportOut{}, err
			}
			return nil, exportOut{URL: s.mcpLink(c, p, "zip", "", false), ExpiresIn: int(mcpDownloadTTL.Seconds())}, nil
		})
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// mcpStore guarda un archivo subido por una herramienta.
func (s *Server) mcpStore(p *Project, rel string, data []byte, overwrite, optimize bool) (*mcp.CallToolResult, *uploadResult, error) {
	if !overwrite {
		if _, _, err := s.regularFile(p.ID, rel); err == nil {
			return nil, nil, fmt.Errorf("%s ya existe (usa overwrite: true para reemplazarlo)", rel)
		}
	}
	res, err := s.storeFile(p, rel, data, optimize, false)
	if err != nil {
		return nil, nil, apiMessage(err)
	}
	return nil, res, nil
}

// ---------- compilación y vista ----------

type diagnosticsOut struct {
	OK          bool               `json:"ok"`
	Pages       int                `json:"pages,omitempty"`
	Revision    int64              `json:"revision"`
	Diagnostics []typst.Diagnostic `json:"diagnostics" jsonschema:"errores y advertencias con archivo, línea y columna"`
	Output      string             `json:"output,omitempty" jsonschema:"salida de typst si falló"`
}

// compileProject compila el PDF (en caché por revisión) y resume el resultado.
func (s *Server) compileProject(ctx context.Context, p *Project) (*pdfResult, diagnosticsOut, error) {
	c := s.cacheFor(p.ID)
	c.mu.Lock()
	defer c.mu.Unlock()
	res, err := s.compilePDFLocked(ctx, p, c)
	out := diagnosticsOut{Revision: s.hub.Rev(p.ID), Diagnostics: []typst.Diagnostic{}}
	if res != nil {
		out.Revision, out.Diagnostics = res.Rev, res.Diagnostics
	}
	var ce *typst.CompileError
	if errors.As(err, &ce) {
		out.Output = ce.Output
		return res, out, nil
	}
	if err != nil {
		return nil, out, apiMessage(err)
	}
	out.OK = true
	out.Pages = typst.PDFPageCount(res.File)
	return res, out, nil
}

func (s *Server) addCompileTools(srv *mcp.Server) {
	tool(s, srv, &mcp.Tool{Name: "compile",
		Description: "Compila el proyecto con typst y devuelve si compila, el número de páginas y los errores/advertencias con archivo:línea.",
		Annotations: readOnly("Compilar")},
		func(ctx context.Context, c *mcpCaller, in projectArg) (*mcp.CallToolResult, diagnosticsOut, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, diagnosticsOut{}, err
			}
			_, out, err := s.compileProject(ctx, p)
			return nil, out, err
		})

	type renderIn struct {
		ProjectID int64 `json:"project_id" jsonschema:"ID del proyecto"`
		Pages     []int `json:"pages" jsonschema:"números de página (desde 1), máximo 4 por llamada"`
		PPI       int   `json:"ppi,omitempty" jsonschema:"resolución, 30–200 (por defecto 100)"`
	}
	tool(s, srv, &mcp.Tool{Name: "render_pages",
		Description: "Renderiza páginas del documento como imágenes PNG para revisar cómo queda (misma paginación que el PDF).",
		Annotations: readOnly("Ver páginas")},
		func(ctx context.Context, c *mcpCaller, in renderIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, nil, err
			}
			if len(in.Pages) == 0 || len(in.Pages) > 4 {
				return nil, nil, errors.New("indica entre 1 y 4 páginas")
			}
			for _, n := range in.Pages {
				if n < 1 {
					return nil, nil, errors.New("las páginas empiezan en 1")
				}
			}
			ppi := in.PPI
			if ppi == 0 {
				ppi = 100
			}
			ppi = min(max(ppi, 30), 200)
			mu := s.projectLock(p.ID)
			mu.Lock()
			s.hub.FlushProject(p.ID)
			imgs, diags, err := s.typst.RenderPNG(ctx, s.projectDir(p.ID), p.MainFile, s.buildDir(p.ID), in.Pages, ppi)
			mu.Unlock()
			var ce *typst.CompileError
			if errors.As(err, &ce) {
				return nil, map[string]any{"ok": false, "output": ce.Output, "diagnostics": diags}, nil
			}
			if err != nil {
				return nil, nil, fmt.Errorf("no se pudo ejecutar typst: %v", err)
			}
			res := &mcp.CallToolResult{}
			for i, n := range in.Pages {
				if imgs[i] == nil {
					res.Content = append(res.Content, &mcp.TextContent{Text: fmt.Sprintf("La página %d no existe.", n)})
					continue
				}
				res.Content = append(res.Content, &mcp.TextContent{Text: fmt.Sprintf("Página %d:", n)},
					&mcp.ImageContent{Data: imgs[i], MIMEType: "image/png"})
			}
			return res, nil, nil
		})

	type sourceIn struct {
		ProjectID int64 `json:"project_id" jsonschema:"ID del proyecto"`
		Page      int   `json:"page" jsonschema:"número de página (desde 1)"`
	}
	type sourceBlock struct {
		Y    float64 `json:"y_pt" jsonschema:"posición vertical en la página, en pt desde arriba"`
		File string  `json:"file"`
		Line int     `json:"line"`
		Text string  `json:"source" jsonschema:"línea de código"`
	}
	type sourceOut struct {
		Page   int           `json:"page"`
		Blocks []sourceBlock `json:"blocks"`
	}
	tool(s, srv, &mcp.Tool{Name: "page_source",
		Description: "Para una página del documento, qué archivo y línea del código genera cada bloque (párrafos, títulos, figuras, tablas…), de arriba abajo.",
		Annotations: readOnly("Código de una página")},
		func(ctx context.Context, c *mcpCaller, in sourceIn) (*mcp.CallToolResult, sourceOut, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, sourceOut{}, err
			}
			pts, _, err := s.syncPoints(ctx, p)
			if err != nil {
				return nil, sourceOut{}, apiMessage(err)
			}
			out := sourceOut{Page: in.Page, Blocks: []sourceBlock{}}
			texts := map[string][]string{}
			for _, pt := range pts {
				if pt.Page != in.Page {
					continue
				}
				lines, ok := texts[pt.File]
				if !ok {
					t, _ := s.readText(p, pt.File)
					lines = strings.Split(t, "\n")
					texts[pt.File] = lines
				}
				src := ""
				if pt.Line >= 1 && pt.Line <= len(lines) {
					src = lines[pt.Line-1]
					if len(src) > 200 {
						src = src[:200] + "…"
					}
				}
				out.Blocks = append(out.Blocks, sourceBlock{Y: pt.Y, File: pt.File, Line: pt.Line, Text: src})
			}
			if len(out.Blocks) == 0 {
				return textResult("No hay bloques localizados en la página %d (puede no existir o tener solo contenido generado).", in.Page), out, nil
			}
			return nil, out, nil
		})

	type pdfIn struct {
		ProjectID int64 `json:"project_id" jsonschema:"ID del proyecto"`
		Embed     bool  `json:"embed,omitempty" jsonschema:"incluir el PDF en la respuesta (máx. 10 MB)"`
	}
	tool(s, srv, &mcp.Tool{Name: "get_pdf",
		Description: "Compila el PDF final (con las imágenes originales) y devuelve un enlace de descarga válido 10 min; con embed, también el PDF incrustado.",
		Annotations: readOnly("Obtener PDF")},
		func(ctx context.Context, c *mcpCaller, in pdfIn) (*mcp.CallToolResult, any, error) {
			p, err := s.project(ctx, c, in.ProjectID, false)
			if err != nil {
				return nil, nil, err
			}
			res, out, err := s.compileProject(ctx, p)
			if err != nil {
				return nil, nil, err
			}
			if !out.OK {
				return nil, out, nil
			}
			link := s.mcpLink(c, p, "pdf", "", false)
			name := strings.TrimSuffix(path.Base(p.MainFile), ".typ") + ".pdf"
			fi, _ := os.Stat(res.File)
			var size int64
			if fi != nil {
				size = fi.Size()
			}
			r := textResult("PDF de %d páginas (%d bytes, %d advertencias).\nEnlace de descarga (10 min): %s", out.Pages, size, len(out.Diagnostics), link)
			r.Content = append(r.Content, &mcp.ResourceLink{URI: link, Name: name, MIMEType: "application/pdf", Size: ptr(size)})
			if in.Embed {
				if size > pdfEmbedMax {
					r.Content = append(r.Content, &mcp.TextContent{Text: "El PDF supera 10 MB: no se incluye, usa el enlace."})
				} else if data, err := os.ReadFile(res.File); err == nil {
					r.Content = append(r.Content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
						URI: fmt.Sprintf("typst-editor://projects/%d/%s", p.ID, name), MIMEType: "application/pdf", Blob: data}})
				}
			}
			return r, nil, nil
		})
}

// decodeBase64 acepta base64 estándar o URL, con o sin relleno, y data: URLs.
func decodeBase64(s string) ([]byte, error) {
	if i := strings.Index(s, ";base64,"); strings.HasPrefix(s, "data:") && i >= 0 {
		s = s[i+len(";base64,"):]
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if len(s) > (maxUpload/3+1)*4+8 {
		return nil, errors.New("archivo demasiado grande (máx. 20 MB)")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("content_base64 no es base64 válido")
}
