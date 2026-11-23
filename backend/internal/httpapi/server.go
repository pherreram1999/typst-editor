package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"typsteditor/backend/internal/database"

	"typsteditor/backend/internal/auth"
	"typsteditor/backend/internal/collab"
	"typsteditor/backend/internal/github"
	"typsteditor/backend/internal/gitsync"
	"typsteditor/backend/internal/mail"
	"typsteditor/backend/internal/oauth"
	"typsteditor/backend/internal/typst"
)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"` // vacío: cuenta anterior sin correo
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	// Pending: aún no definió su contraseña (alta por correo); no puede iniciar sesión.
	Pending bool `json:"pending"`
	// MustChangePassword: entró con una contraseña temporal (alta o
	// restablecimiento sin correo) y debe cambiarla antes de usar la aplicación.
	MustChangePassword bool `json:"must_change_password"`
}

type Options struct {
	DataDir   string
	TypstBin  string
	GitBin    string
	GitHubAPI string
	StaticDir string   // frontend compilado (vacío o sin index.html: no se sirve)
	FontDirs  []string // carpetas de fuentes adicionales (ya en TYPST_FONT_PATHS)
	// URL pública (sin barra final): emisor OAuth y recurso MCP (PublicURL/mcp).
	PublicURL  string
	MCPEnabled bool
	// Correo saliente (Resend); nil o sin clave: no se envían correos.
	Mail mail.Sender
}

type Server struct {
	db      *sql.DB
	dialect database.Dialect
	// projectSelect según el dialecto (ver projects.go).
	projectSelect string
	tokens        *auth.Issuer
	opts          Options
	hub           *collab.Hub
	typst         *typst.Compiler
	git           *gitsync.Git
	gh            *github.Client
	locks         sync.Map     // projectID -> *sync.Mutex
	cache         sync.Map     // projectID -> *compileCache
	fontSig       atomic.Value // firma de las carpetas de fuentes con la que se llenó cache
	repoCache     sync.Map     // githubAccountID -> *repoCacheEntry
	tickets       ticketStore  // descargas de un solo uso (export.go) y enlaces de las herramientas MCP
	oauth         *oauth.Store // servidor de autorización del endpoint MCP (oauth.go)
	limiter       rateLimiter  // registro dinámico de clientes OAuth por IP; altas y correos
	mail          mail.Sender
}

func New(db *sql.DB, d database.Dialect, tokens *auth.Issuer, opts Options) *Server {
	if d == nil {
		d = database.MySQL
	}
	s := &Server{db: db, dialect: d, tokens: tokens, opts: opts, projectSelect: buildProjectSelect(d),
		typst: typst.New(opts.TypstBin, opts.FontDirs...), git: &gitsync.Git{Bin: opts.GitBin}, gh: github.New(opts.GitHubAPI)}
	s.hub = collab.NewHub(s.projectDir)
	s.oauth = &oauth.Store{DB: db, D: d}
	s.mail = opts.Mail
	if s.mail == nil {
		s.mail = mail.New("", "")
	}
	return s
}

// Shutdown guarda en disco los documentos abiertos.
func (s *Server) Shutdown() { s.hub.FlushAll() }

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/auth/login", s.login)
	// Cuentas por correo (accounts.go): enlaces de alta, restablecer la
	// contraseña y confirmar un correo; «olvidé mi contraseña».
	mux.HandleFunc("GET /api/auth/config", s.authConfig)
	mux.HandleFunc("POST /api/auth/forgot", s.forgotPassword)
	mux.HandleFunc("GET /api/auth/account/{token}", s.accountInfo)
	mux.HandleFunc("POST /api/auth/account/{token}/password", s.accountPassword)
	mux.HandleFunc("POST /api/auth/account/{token}/email", s.accountEmail)
	mux.Handle("PUT /api/auth/me/email", s.requireAuth(http.HandlerFunc(s.changeEmail)))
	mux.Handle("POST /api/auth/password", s.requireAuth(http.HandlerFunc(s.changePassword)))
	mux.Handle("POST /api/users/{id}/access-link", s.requireAdmin(http.HandlerFunc(s.userAccessLink)))
	// Invitaciones a un proyecto enviadas por correo (emailinvites.go).
	mux.HandleFunc("GET /api/join/{token}", s.joinInfo)
	mux.HandleFunc("POST /api/join/{token}/register", s.joinRegister)
	mux.Handle("POST /api/join/{token}/accept", s.requireAuth(http.HandlerFunc(s.joinAccept)))
	mux.Handle("GET /api/auth/me", s.requireAuth(http.HandlerFunc(s.me)))
	mux.Handle("GET /api/users", s.requireAdmin(http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/users", s.requireAdmin(http.HandlerFunc(s.createUser)))
	mux.Handle("PUT /api/users/{id}", s.requireAdmin(http.HandlerFunc(s.updateUser)))

	authed := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAuth(h)) }
	// shared: rutas abiertas también a los invitados de un enlace (invites.go);
	// el resto las rechaza con 403. Las de escritura comprueban requireWrite.
	shared := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireSession(h, true)) }
	authed("GET /api/projects", s.listProjects)
	authed("POST /api/projects", s.createProject)
	authed("POST /api/projects/import", s.importProject)
	shared("GET /api/projects/{id}", s.getProject)
	authed("PUT /api/projects/{id}", s.updateProject)
	authed("DELETE /api/projects/{id}", s.deleteProject)
	shared("GET /api/projects/{id}/files", s.listFiles)
	shared("POST /api/projects/{id}/files", s.createFile)
	shared("DELETE /api/projects/{id}/files", s.deleteFile)
	shared("GET /api/projects/{id}/raw", s.rawFile)
	shared("POST /api/projects/{id}/upload", s.uploadFile)
	shared("POST /api/projects/{id}/images/optimize", s.optimizeImage)
	shared("GET /api/projects/{id}/preview", s.preview)
	shared("GET /api/projects/{id}/preview/pages/{hash}", s.previewPage)
	shared("GET /api/projects/{id}/preview/sync", s.previewSync)
	shared("GET /api/projects/{id}/preview/files", s.previewFiles)
	shared("GET /api/projects/{id}/preview/file", s.previewFile)
	shared("GET /api/typst/fonts", s.typstFonts)
	shared("GET /api/typst/fonts/meta", s.typstFontsMeta)
	shared("GET /api/typst/fonts/{id}", s.typstFont)
	shared("GET /api/typst/packages/{ns}/{name}/{version}", s.typstPackage)
	shared("GET /api/projects/{id}/pdf", s.pdf)
	authed("POST /api/projects/{id}/export/ticket", s.exportTicket)
	authed("GET /api/users/search", s.searchUsers)
	authed("GET /api/templates", s.listTemplates)
	authed("POST /api/projects/{id}/clone", s.cloneProject)
	authed("GET /api/projects/{id}/thumbnail", s.thumbnail)
	authed("GET /api/folders", s.listFolders)
	authed("POST /api/folders", s.createFolder)
	authed("PUT /api/folders/{id}", s.renameFolder)
	authed("DELETE /api/folders/{id}", s.deleteFolder)
	authed("PUT /api/projects/{id}/prefs", s.updatePrefs)
	authed("GET /api/github/accounts", s.listAccounts)
	authed("POST /api/github/accounts", s.createAccount)
	authed("PUT /api/github/accounts/{id}", s.updateAccount)
	authed("DELETE /api/github/accounts/{id}", s.deleteAccount)
	authed("GET /api/github/accounts/{id}/repos", s.accountRepos)
	authed("GET /api/github/accounts/{id}/branches", s.accountBranches)
	authed("GET /api/projects/{id}/members", s.listMembers)
	authed("POST /api/projects/{id}/members", s.addMember)
	authed("DELETE /api/projects/{id}/members/{userId}", s.removeMember)
	authed("GET /api/projects/{id}/email-invites", s.listEmailInvites)
	authed("DELETE /api/projects/{id}/email-invites/{inviteId}", s.deleteEmailInvite)
	authed("POST /api/projects/{id}/email-invites/{inviteId}/resend", s.resendEmailInvite)
	// Enlaces de invitación: gestión y entrada pública con la URL firmada (invites.go).
	authed("GET /api/projects/{id}/invites", s.listInvites)
	authed("POST /api/projects/{id}/invites", s.createInvite)
	authed("DELETE /api/projects/{id}/invites/{inviteId}", s.revokeInvite)
	mux.HandleFunc("GET /api/invite/{token}", s.inviteInfo)
	mux.HandleFunc("POST /api/invite/{token}/join", s.joinInvite)
	authed("GET /api/projects/{id}/git/status", s.gitStatus)
	authed("POST /api/projects/{id}/git/pull", s.gitPull)
	authed("POST /api/projects/{id}/git/pull/resolve", s.gitResolve)
	authed("POST /api/projects/{id}/git/push", s.gitPush)
	// Descarga directa del navegador: se autentica con un ticket (export.go).
	mux.HandleFunc("GET /api/projects/{id}/export.zip", s.exportZip)
	// Enlace de revisión: gestión (autenticada) y lectura pública por token (review.go).
	authed("GET /api/projects/{id}/review", s.getReview)
	authed("POST /api/projects/{id}/review", s.publishReview)
	authed("PUT /api/projects/{id}/review", s.updateReview)
	authed("DELETE /api/projects/{id}/review", s.deleteReview)
	mux.HandleFunc("GET /api/review/{token}", s.reviewInfo)
	mux.HandleFunc("GET /api/review/{token}/pages/{n}", s.reviewPage)
	mux.HandleFunc("GET /api/review/{token}/document.pdf", s.reviewPDF)
	// Agentes: endpoint MCP, servidor OAuth 2.1 y gestión de accesos (mcp.go, oauth.go).
	if s.opts.MCPEnabled {
		s.mcpRoutes(mux, authed)
	}
	// El WebSocket se autentica con ?token= (ver ws.go).
	mux.HandleFunc("GET /api/projects/{id}/ws", s.projectWS)
	// Rutas /api desconocidas: 404 en JSON, nunca el index.html de la SPA.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "ruta no encontrada")
	})
	if h := staticHandler(s.opts.StaticDir); h != nil {
		mux.Handle("/", h)
	} else {
		log.Printf("aviso: no se encontró el frontend en %q (STATIC_DIR); solo se sirve la API", s.opts.StaticDir)
	}
	return logRequests(limitBody(mux))
}

// ---------- middleware ----------

type ctxKey struct{}

func currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

// requireAuth: rutas solo para usuarios con cuenta (un invitado recibe 403).
func (s *Server) requireAuth(next http.Handler) http.Handler { return s.requireSession(next, false) }

// requireSession autentica el Bearer: sesión de usuario o, si guests, también
// de invitado (con currentGuest en el contexto y un User sintético).
func (s *Server) requireSession(next http.Handler, guests bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || tok == "" {
			writeError(w, http.StatusUnauthorized, "token requerido")
			return
		}
		u, g, status, msg := s.authenticate(r.Context(), tok)
		if status != 0 {
			writeError(w, status, msg)
			return
		}
		if g != nil && !guests {
			writeError(w, http.StatusForbidden, "los invitados no tienen acceso a esta función")
			return
		}
		// Contraseña temporal: solo puede consultar su sesión y cambiarla.
		if u.MustChangePassword && !allowedWithTempPassword[r.Method+" "+r.URL.Path] {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "debes cambiar tu contraseña temporal antes de continuar", "code": "password_change_required"})
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, u)
		if g != nil {
			ctx = context.WithValue(ctx, guestKey{}, g)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// allowedWithTempPassword: rutas que puede usar quien aún tiene una
// contraseña temporal (MustChangePassword); el resto responde 403.
var allowedWithTempPassword = map[string]bool{
	"GET /api/auth/me":        true,
	"POST /api/auth/password": true,
}

// authenticate valida un token de usuario o de invitado. Se consulta la BD en
// cada petición: desactivar un usuario, cambiar su rol o revocar un enlace de
// invitado tiene efecto inmediato. Si falla, devuelve el estado y el mensaje.
func (s *Server) authenticate(ctx context.Context, tok string) (*User, *guestSession, int, string) {
	if id, err := s.tokens.Parse(tok); err == nil {
		u, err := s.userByID(ctx, id)
		if err != nil || !u.Active {
			return nil, nil, http.StatusUnauthorized, "sesión no válida"
		}
		return u, nil, 0, ""
	}
	if g, err := s.guestFromToken(ctx, tok); err == nil {
		return guestUser(g), g, 0, ""
	}
	return nil, nil, http.StatusUnauthorized, "token inválido o expirado"
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).Role != "admin" {
			writeError(w, http.StatusForbidden, "se requiere rol administrador")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Las subidas aplican su propio límite (ver uploadFile).
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload"), strings.HasPrefix(r.URL.Path, "/api/mcp/upload/"), r.URL.Path == "/api/projects/import":
		case r.URL.Path == "/mcp":
			// Herramientas MCP con archivos en base64 (20 MB → ~27 MB).
			r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		case strings.HasSuffix(r.URL.Path, "/git/pull/resolve"):
			// Resultado de una fusión: varios archivos de hasta 1 MiB cada uno.
			r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
		default:
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

// statusWriter recuerda el código de estado para el registro de peticiones.
// Unwrap deja que http.ResponseController (plazos, Flush, Hijack del
// WebSocket) llegue al ResponseWriter original.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			// Un pánico en un handler: se registra con la traza y se responde 500
			// (sin esto net/http corta la conexión y el cliente no sabe qué pasó).
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Printf("PÁNICO en %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
				if sw.status == 0 {
					writeError(sw, http.StatusInternalServerError, "error interno")
				}
			}
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, status, time.Since(start).Round(time.Millisecond))
		}()
		next.ServeHTTP(sw, r)
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	if status >= 500 {
		log.Printf("error %d: %s [%s]", status, msg, caller(2))
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

// internalError registra la causa real de un fallo (con archivo:línea) y
// responde 500 «error interno» sin exponer detalles al cliente.
func internalError(w http.ResponseWriter, err error) {
	log.Printf("error interno: %v [%s]", err, caller(2))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "error interno"})
}

// caller devuelve «archivo.go:línea» de quien llamó skip niveles más arriba.
func caller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "?"
	}
	return filepath.Base(file) + ":" + strconv.Itoa(line)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("JSON inválido")
	}
	return nil
}
