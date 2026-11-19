package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"typsteditor/backend/internal/auth"
	"typsteditor/backend/internal/collab"
	"typsteditor/backend/internal/github"
	"typsteditor/backend/internal/gitsync"
	"typsteditor/backend/internal/typst"
)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

type Options struct {
	DataDir   string
	TypstBin  string
	GitBin    string
	GitHubAPI string
}

type Server struct {
	db        *sql.DB
	tokens    *auth.Issuer
	opts      Options
	hub       *collab.Hub
	typst     *typst.Compiler
	git       *gitsync.Git
	gh        *github.Client
	locks     sync.Map // projectID -> *sync.Mutex
	cache     sync.Map // projectID -> *compileCache
	repoCache sync.Map // githubAccountID -> *repoCacheEntry
}

func New(db *sql.DB, tokens *auth.Issuer, opts Options) *Server {
	s := &Server{db: db, tokens: tokens, opts: opts,
		typst: &typst.Compiler{Bin: opts.TypstBin}, git: &gitsync.Git{Bin: opts.GitBin}, gh: github.New(opts.GitHubAPI)}
	s.hub = collab.NewHub(s.projectDir)
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
	mux.Handle("GET /api/auth/me", s.requireAuth(http.HandlerFunc(s.me)))
	mux.Handle("GET /api/users", s.requireAdmin(http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/users", s.requireAdmin(http.HandlerFunc(s.createUser)))
	mux.Handle("PUT /api/users/{id}", s.requireAdmin(http.HandlerFunc(s.updateUser)))

	authed := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAuth(h)) }
	authed("GET /api/projects", s.listProjects)
	authed("POST /api/projects", s.createProject)
	authed("GET /api/projects/{id}", s.getProject)
	authed("PUT /api/projects/{id}", s.updateProject)
	authed("DELETE /api/projects/{id}", s.deleteProject)
	authed("GET /api/projects/{id}/files", s.listFiles)
	authed("POST /api/projects/{id}/files", s.createFile)
	authed("DELETE /api/projects/{id}/files", s.deleteFile)
	authed("GET /api/projects/{id}/raw", s.rawFile)
	authed("POST /api/projects/{id}/upload", s.uploadFile)
	authed("GET /api/projects/{id}/preview", s.preview)
	authed("GET /api/projects/{id}/pdf", s.pdf)
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
	authed("GET /api/projects/{id}/git/status", s.gitStatus)
	authed("POST /api/projects/{id}/git/pull", s.gitPull)
	authed("POST /api/projects/{id}/git/push", s.gitPush)
	// El WebSocket se autentica con ?token= (ver ws.go).
	mux.HandleFunc("GET /api/projects/{id}/ws", s.projectWS)
	return logRequests(limitBody(mux))
}

// ---------- middleware ----------

type ctxKey struct{}

func currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || tok == "" {
			writeError(w, http.StatusUnauthorized, "token requerido")
			return
		}
		id, err := s.tokens.Parse(tok)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "token inválido o expirado")
			return
		}
		// Se consulta la BD en cada petición: desactivar o cambiar el rol tiene efecto inmediato.
		u, err := s.userByID(r.Context(), id)
		if err != nil || !u.Active {
			writeError(w, http.StatusUnauthorized, "sesión no válida")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
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
		if !strings.HasSuffix(r.URL.Path, "/upload") {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("JSON inválido")
	}
	return nil
}
