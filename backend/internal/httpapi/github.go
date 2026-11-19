package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"typsteditor/backend/internal/github"
)

// GitHubAccount es una cuenta de GitHub (usuario + token) de un usuario de la
// aplicación. El token nunca sale del servidor.
type GitHubAccount struct {
	ID        int64     `json:"id"`
	Label     string    `json:"label"`
	Login     string    `json:"login"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	token     string
}

const repoCacheTTL = 2 * time.Minute

type repoCacheEntry struct {
	at    time.Time
	repos []github.Repo
}

func ghError(w http.ResponseWriter, err error) {
	var ge *github.Error
	if errors.As(err, &ge) {
		status := ge.Status
		if status == http.StatusUnauthorized {
			status = http.StatusBadRequest // no es la sesión de la app la que caducó
		}
		writeError(w, status, ge.Msg)
		return
	}
	writeError(w, http.StatusBadGateway, "error al consultar GitHub")
}

// ownAccount carga una cuenta del usuario actual (sql.ErrNoRows si no es suya).
func (s *Server) ownAccount(r *http.Request, id int64) (*GitHubAccount, error) {
	a := &GitHubAccount{}
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, label, login, token, created_at, updated_at FROM github_accounts WHERE id = ? AND user_id = ?`,
		id, currentUser(r).ID).Scan(&a.ID, &a.Label, &a.Login, &a.token, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *Server) loadAccount(w http.ResponseWriter, r *http.Request) (*GitHubAccount, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return nil, false
	}
	a, err := s.ownAccount(r, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "cuenta de GitHub no encontrada")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return nil, false
	}
	return a, true
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, label, login, created_at, updated_at FROM github_accounts WHERE user_id = ? ORDER BY login`,
		currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	defer rows.Close()
	out := []GitHubAccount{}
	for rows.Next() {
		var a GitHubAccount
		if err := rows.Scan(&a.ID, &a.Label, &a.Login, &a.CreatedAt, &a.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}

// verifyToken comprueba el token contra GitHub y que pertenezca a login (si se indica).
func (s *Server) verifyToken(w http.ResponseWriter, r *http.Request, login, token string) (*github.User, bool) {
	if token == "" || len(token) > 512 || strings.ContainsAny(token, " \t\r\n") {
		writeError(w, http.StatusBadRequest, "token inválido")
		return nil, false
	}
	u, err := s.gh.User(r.Context(), token)
	if err != nil {
		ghError(w, err)
		return nil, false
	}
	if login != "" && !strings.EqualFold(login, u.Login) {
		writeError(w, http.StatusBadRequest, "el token pertenece al usuario de GitHub «"+u.Login+"», no a «"+login+"»")
		return nil, false
	}
	return u, true
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login string `json:"login"`
		Token string `json:"token"`
		Label string `json:"label"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Login, in.Token, in.Label = strings.TrimSpace(in.Login), strings.TrimSpace(in.Token), strings.TrimSpace(in.Label)
	if in.Login == "" {
		writeError(w, http.StatusBadRequest, "usuario de GitHub requerido")
		return
	}
	if len(in.Label) > 128 {
		writeError(w, http.StatusBadRequest, "descripción demasiado larga (máx. 128)")
		return
	}
	gu, ok := s.verifyToken(w, r, in.Login, in.Token)
	if !ok {
		return
	}
	res, err := s.db.ExecContext(r.Context(),
		`INSERT INTO github_accounts (user_id, label, login, token) VALUES (?, ?, ?, ?)`,
		currentUser(r).ID, in.Label, gu.Login, in.Token)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		writeError(w, http.StatusConflict, "ya tienes registrada la cuenta «"+gu.Login+"»; actualiza su token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	id, _ := res.LastInsertId()
	a, err := s.ownAccount(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadAccount(w, r)
	if !ok {
		return
	}
	var in struct {
		Label *string `json:"label"`
		Token *string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Label != nil {
		a.Label = strings.TrimSpace(*in.Label)
		if len(a.Label) > 128 {
			writeError(w, http.StatusBadRequest, "descripción demasiado larga (máx. 128)")
			return
		}
	}
	if in.Token != nil && strings.TrimSpace(*in.Token) != "" {
		tok := strings.TrimSpace(*in.Token)
		if _, ok := s.verifyToken(w, r, a.Login, tok); !ok {
			return
		}
		a.token = tok
		s.repoCache.Delete(a.ID)
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE github_accounts SET label = ?, token = ? WHERE id = ?`, a.Label, a.token, a.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	a, _ = s.ownAccount(r, a.ID)
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadAccount(w, r)
	if !ok {
		return
	}
	// Los proyectos que la usaban quedan sin credenciales (ON DELETE SET NULL).
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM github_accounts WHERE id = ?`, a.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	s.repoCache.Delete(a.ID)
	w.WriteHeader(http.StatusNoContent)
}

// accountRepos lista los repositorios de la cuenta (con caché breve; ?refresh=1 la ignora).
func (s *Server) accountRepos(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadAccount(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("refresh") == "" {
		if c, ok := s.repoCache.Load(a.ID); ok && time.Since(c.(*repoCacheEntry).at) < repoCacheTTL {
			writeJSON(w, http.StatusOK, c.(*repoCacheEntry).repos)
			return
		}
	}
	longRequest(w, time.Minute)
	repos, err := s.gh.Repos(r.Context(), a.token)
	if err != nil {
		ghError(w, err)
		return
	}
	s.repoCache.Store(a.ID, &repoCacheEntry{at: time.Now(), repos: repos})
	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) accountBranches(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadAccount(w, r)
	if !ok {
		return
	}
	branches, err := s.gh.Branches(r.Context(), a.token, r.URL.Query().Get("repo"))
	if err != nil {
		ghError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, branches)
}
