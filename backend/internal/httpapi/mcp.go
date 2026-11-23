package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"typsteditor/backend/internal/oauth"
)

// Servidor MCP (Model Context Protocol) en /mcp, transporte Streamable HTTP.
//
// Vive dentro del mismo proceso que la API porque el hub de colaboración
// guarda en memoria los documentos abiertos: las herramientas escriben a
// través de hub.EditText, así que los usuarios con el archivo abierto ven los
// cambios del agente en tiempo real y nadie pisa a nadie.
//
// Autenticación: token de acceso OAuth (tsa_…, para conectores remotos como
// Claude o ChatGPT) o token personal (tse_…, para clientes locales como
// Claude Code o MCP Inspector), siempre en la cabecera Authorization. Los
// permisos se comprueban en cada llamada, en este orden:
//  1. el usuario se vuelve a leer (desactivado → fuera) y solo ve sus
//     proyectos propios o compartidos, igual que en la web;
//  2. por MCP el rol de administrador no da acceso a proyectos ajenos;
//  3. la concesión puede limitarse a ciertos proyectos;
//  4. las herramientas que modifican exigen el scope typst:write.

const mcpInstructions = `Servidor de Typst Extend: proyectos Typst colaborativos con vista previa y PDF.

Flujo habitual: list_projects → list_files → read_file → edit_file / write_file → compile (diagnósticos) → render_pages (ver cómo queda) → get_pdf.
- Las rutas son relativas a la raíz del proyecto, con "/" (p. ej. "capitulos/intro.typ").
- Las ediciones se aplican en vivo: si alguien tiene el archivo abierto en el navegador verá el cambio al instante.
- Prefiere edit_file (reemplazo exacto de un fragmento) a reescribir archivos enteros.
- Tras editar, usa compile para ver errores y advertencias con archivo:línea, y render_pages para revisar el resultado visual.
- page_source indica qué archivo y línea generan cada bloque de una página.
- Para subir archivos grandes desde tu máquina usa create_upload_url (y curl -T); para descargarlos, la URL de download_file.`

// mcpUser es el usuario con el que trabajan las herramientas: sin el rol de
// administrador, para que un agente no recorra todos los proyectos del servidor.
func mcpUser(u *User) *User {
	c := *u
	c.Role = "user"
	return &c
}

func (s *Server) mcpRoutes(mux *http.ServeMux, authed func(string, http.HandlerFunc)) {
	pub := s.opts.PublicURL
	prm := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               s.mcpResource(),
		AuthorizationServers:   []string{pub},
		ScopesSupported:        oauth.AllScopes,
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Typst Extend",
		ResourceDocumentation:  pub + "/#/mcp",
	})
	mux.Handle("GET /.well-known/oauth-protected-resource", prm)
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("/.well-known/oauth-authorization-server", cors("GET", s.asMetadata))
	mux.HandleFunc("/.well-known/openid-configuration", cors("GET", s.asMetadata))
	mux.HandleFunc("/oauth/register", cors("POST", s.oauthRegister))
	mux.HandleFunc("GET /oauth/authorize", s.oauthAuthorize)
	mux.HandleFunc("/oauth/token", cors("POST", s.oauthToken))
	mux.HandleFunc("/oauth/revoke", cors("POST", s.oauthRevoke))

	authed("GET /api/oauth/authorize/info", s.oauthAuthorizeInfo)
	authed("POST /api/oauth/authorize", s.oauthApprove)
	authed("GET /api/mcp/grants", s.listGrants)
	authed("POST /api/mcp/tokens", s.createPersonalToken)
	authed("PUT /api/mcp/grants/{id}", s.updateGrant)
	authed("DELETE /api/mcp/grants/{id}", s.deleteGrant)

	// Enlaces creados por las herramientas (se autentican con el ticket).
	mux.HandleFunc("GET /api/mcp/files/{ticket}", s.mcpDownload)
	mux.HandleFunc("PUT /api/mcp/upload/{ticket}", s.mcpUpload)
	mux.HandleFunc("POST /api/mcp/upload/{ticket}", s.mcpUpload)

	server := s.newMCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// Toda petición lleva un token: la protección contra DNS rebinding no
		// aporta nada y rompería el despliegue detrás de un proxy local.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        32 << 20,
	})
	protected := auth.RequireBearerToken(s.verifyMCPToken, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: pub + "/.well-known/oauth-protected-resource/mcp",
	})(handler)
	mux.Handle("/mcp", cors("GET, POST, DELETE", func(w http.ResponseWriter, r *http.Request) {
		// Compilar o renderizar un documento grande tarda más que el límite
		// general de 15 s; las subidas en base64 también.
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
		_ = rc.SetReadDeadline(time.Now().Add(5 * time.Minute))
		protected.ServeHTTP(w, r)
	}))
}

// verifyMCPToken valida el token de la cabecera Authorization del endpoint MCP.
func (s *Server) verifyMCPToken(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	v, err := s.oauth.Verify(ctx, token)
	if errors.Is(err, oauth.ErrInvalidGrant) {
		return nil, fmt.Errorf("%w: token inválido, revocado o caducado", auth.ErrInvalidToken)
	}
	if err != nil {
		return nil, err
	}
	// Un token OAuth solo vale para el recurso para el que se emitió.
	if v.Resource != "" && v.Resource != s.mcpResource() {
		return nil, fmt.Errorf("%w: token emitido para otro recurso", auth.ErrInvalidToken)
	}
	u, err := s.userByID(ctx, v.UserID)
	if err != nil || !u.Active || u.MustChangePassword {
		return nil, fmt.Errorf("%w: usuario no válido", auth.ErrInvalidToken)
	}
	exp := v.Expires
	if exp.IsZero() { // token personal sin caducidad
		exp = time.Now().Add(time.Hour)
	}
	return &auth.TokenInfo{
		Scopes: v.Scopes, Expiration: exp, UserID: strconv.FormatInt(v.UserID, 10),
		Extra: map[string]any{"grant": v},
	}, nil
}

// mcpCaller es quien llama a una herramienta.
type mcpCaller struct {
	user     *User // sin rol de administrador (mcpUser)
	realRole string
	grant    *oauth.Verified
}

// caller identifica al usuario de una llamada (releído de la base de datos).
func (s *Server) caller(ctx context.Context, req *mcp.CallToolRequest) (*mcpCaller, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil, errors.New("sin autenticación")
	}
	v, _ := req.Extra.TokenInfo.Extra["grant"].(*oauth.Verified)
	if v == nil {
		return nil, errors.New("sin autenticación")
	}
	return s.callerFor(ctx, v)
}

func (s *Server) callerFor(ctx context.Context, v *oauth.Verified) (*mcpCaller, error) {
	u, err := s.userByID(ctx, v.UserID)
	if err != nil || !u.Active || u.MustChangePassword {
		return nil, errors.New("el usuario ya no está activo o debe cambiar su contraseña")
	}
	return &mcpCaller{user: mcpUser(u), realRole: u.Role, grant: v}, nil
}

func (c *mcpCaller) canWrite() bool { return slices.Contains(c.grant.Scopes, oauth.ScopeWrite) }

// allows indica si la concesión incluye el proyecto.
func (c *mcpCaller) allows(projectID int64) bool {
	return c.grant.AllProjects || slices.Contains(c.grant.Projects, projectID)
}

var errReadOnly = errors.New("este acceso es de solo lectura (falta el permiso typst:write); el usuario puede ampliarlo en Typst Extend › Agentes (MCP)")

// project carga un proyecto con las comprobaciones de permisos de MCP.
func (s *Server) project(ctx context.Context, c *mcpCaller, id int64, write bool) (*Project, error) {
	if write && !c.canWrite() {
		return nil, errReadOnly
	}
	notFound := fmt.Errorf("proyecto %d no encontrado o sin acceso (usa list_projects)", id)
	if !c.allows(id) {
		return nil, notFound
	}
	p, err := s.projectByID(ctx, c.user, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound
	}
	if err != nil {
		return nil, errors.New("error interno")
	}
	return p, nil
}

// ---------- enlaces de descarga y subida ----------

const (
	mcpDownloadTTL = 10 * time.Minute
	mcpUploadTTL   = 15 * time.Minute
)

// mcpLink crea un enlace firmado por ticket para la concesión del que llama.
func (s *Server) mcpLink(c *mcpCaller, p *Project, kind, rel string, overwrite bool) string {
	t := downloadTicket{userID: c.user.ID, projectID: p.ID, kind: kind, path: rel, grantID: c.grant.GrantID, overwrite: overwrite}
	if kind == "upload" {
		return s.opts.PublicURL + "/api/mcp/upload/" + s.tickets.issueTicket(t, mcpUploadTTL)
	}
	t.multi = true
	return s.opts.PublicURL + "/api/mcp/files/" + s.tickets.issueTicket(t, mcpDownloadTTL)
}

// ticketProject vuelve a comprobar, al usar un enlace, que el usuario y la
// concesión siguen siendo válidos y dan acceso al proyecto.
func (s *Server) ticketProject(ctx context.Context, t downloadTicket, write bool) (*mcpCaller, *Project, error) {
	v, err := s.oauth.GrantAccess(ctx, t.grantID)
	if err != nil || v.UserID != t.userID {
		return nil, nil, errf(http.StatusUnauthorized, "el acceso con el que se creó el enlace se ha revocado")
	}
	c, err := s.callerFor(ctx, v)
	if err != nil {
		return nil, nil, errf(http.StatusUnauthorized, err.Error())
	}
	p, err := s.project(ctx, c, t.projectID, write)
	if err != nil {
		return nil, nil, errf(http.StatusForbidden, err.Error())
	}
	return c, p, nil
}

// mcpDownload: GET /api/mcp/files/{ticket} (archivo, PDF o .zip del proyecto).
func (s *Server) mcpDownload(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tickets.take(r.PathValue("ticket"))
	if !ok || t.kind == "" || t.kind == "upload" {
		writeError(w, http.StatusUnauthorized, "enlace inválido o caducado; pide uno nuevo")
		return
	}
	_, p, err := s.ticketProject(r.Context(), t, false)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch t.kind {
	case "file":
		s.hub.FlushProject(p.ID)
		abs, fi, err := s.regularFile(p.ID, t.path)
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
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Header().Set("Content-Disposition", attachment(path.Base(t.path)))
		http.ServeContent(w, r, path.Base(t.path), fi.ModTime(), f)
	case "pdf":
		longRequest(w, 2*time.Minute)
		c := s.cacheFor(p.ID)
		c.mu.Lock()
		defer c.mu.Unlock()
		res, err := s.compilePDFLocked(r.Context(), p, c)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "el documento no compila; usa la herramienta compile")
			return
		}
		f, err := os.Open(res.File)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "PDF no disponible")
			return
		}
		defer f.Close()
		name := strings.TrimSuffix(path.Base(p.MainFile), ".typ") + ".pdf"
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", attachment(name))
		http.ServeContent(w, r, name, time.Time{}, f)
	case "zip":
		s.hub.FlushProject(p.ID)
		longRequest(w, 30*time.Minute)
		name := exportName(p.Name)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", attachment(name+".zip"))
		if err := writeProjectZip(w, s.projectDir(p.ID), name); err != nil {
			log.Printf("exportar proyecto %d (MCP): %v", p.ID, err)
		}
	}
}

func attachment(name string) string {
	return `attachment; filename="` + asciiName(name) + `"; filename*=UTF-8''` + url.PathEscape(name)
}

// mcpUpload: PUT /api/mcp/upload/{ticket} con el contenido en el cuerpo (curl -T)
// o POST multipart con el campo «file».
func (s *Server) mcpUpload(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tickets.take(r.PathValue("ticket"))
	if !ok || t.kind != "upload" {
		writeError(w, http.StatusUnauthorized, "enlace de subida inválido, usado o caducado; pide uno nuevo con create_upload_url")
		return
	}
	_, p, err := s.ticketProject(r.Context(), t, true)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	longRequest(w, 5*time.Minute)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	var src io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		f, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "falta el campo «file»")
			return
		}
		defer f.Close()
		src = f
	}
	data, err := io.ReadAll(io.LimitReader(src, maxUpload+1))
	if err != nil || len(data) > maxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "archivo demasiado grande (máx. 20 MB)")
		return
	}
	if !t.overwrite {
		if _, _, err := s.regularFile(p.ID, t.path); err == nil {
			writeError(w, http.StatusConflict, "el archivo ya existe (crea el enlace con overwrite: true para reemplazarlo)")
			return
		}
	}
	res, err := s.storeFile(p, t.path, data, false, false)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// ---------- descarga desde una URL pública ----------

// errPrivateAddr: la URL apunta a la red interna (protección SSRF).
var errPrivateAddr = errors.New("la URL apunta a una dirección privada o local")

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		// 100.64.0.0/10 (CGNAT) y 0.0.0.0/8 tampoco son internet.
		(ip.To4() != nil && (ip.To4()[0] == 0 || ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64)))
}

// fetchClient descarga solo de direcciones públicas: la comprobación se hace
// sobre la IP a la que realmente se conecta (Control), así que tampoco vale
// un DNS que primero responde una IP pública y luego una privada.
var fetchClient = &http.Client{
	Timeout: 90 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
					return errPrivateAddr
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          4,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("demasiadas redirecciones")
		}
		if req.URL.Scheme != "https" {
			return errors.New("solo se siguen redirecciones https")
		}
		return nil
	},
}

// fetchURL descarga hasta maxUpload bytes de una URL https pública.
func fetchURL(ctx context.Context, raw string) ([]byte, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, "", errors.New("solo se admiten URLs https públicas")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "typst-editor-mcp/1.0")
	resp, err := fetchClient.Do(req)
	if err != nil {
		if errors.Is(err, errPrivateAddr) {
			return nil, "", errPrivateAddr
		}
		return nil, "", fmt.Errorf("no se pudo descargar: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("la URL respondió %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxUpload+1))
	if err != nil {
		return nil, "", fmt.Errorf("no se pudo descargar: %v", err)
	}
	if len(data) > maxUpload {
		return nil, "", errors.New("archivo demasiado grande (máx. 20 MB)")
	}
	return data, resp.Header.Get("Content-Type"), nil
}
