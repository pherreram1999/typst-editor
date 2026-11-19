package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

const userCols = `id, username, name, role, active, created_at`

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,64}$`)

// dummyHash equalizes login timing when the user does not exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Active, &u.CreatedAt)
	return u, err
}

func (s *Server) userByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

// ---------- auth ----------

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		id     int64
		hash   string
		active bool
		role   string
	)
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, password_hash, active, role FROM users WHERE username=?`, strings.TrimSpace(in.Username)).
		Scan(&id, &hash, &active, &role)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
		writeError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || !active {
		writeError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	tok, exp, err := s.tokens.Issue(id, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	u, err := s.userByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_at": exp, "user": u})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

// ---------- users (admin) ----------

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}

func validRole(r string) bool { return r == "admin" || r == "user" }

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case !usernameRe.MatchString(in.Username):
		writeError(w, http.StatusBadRequest, "usuario: 3-64 caracteres (letras, números, . _ -)")
		return
	case in.Name == "" || len(in.Name) > 128:
		writeError(w, http.StatusBadRequest, "nombre requerido (máx. 128)")
		return
	case len(in.Password) < 8 || len(in.Password) > 72:
		writeError(w, http.StatusBadRequest, "contraseña: entre 8 y 72 caracteres")
		return
	case !validRole(in.Role):
		writeError(w, http.StatusBadRequest, "rol inválido")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	res, err := s.db.ExecContext(r.Context(),
		`INSERT INTO users (username, name, password_hash, role) VALUES (?, ?, ?, ?)`,
		in.Username, in.Name, string(hash), in.Role)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		writeError(w, http.StatusConflict, "el usuario ya existe")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	id, _ := res.LastInsertId()
	u, _ := s.userByID(r.Context(), id)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var in struct {
		Name     *string `json:"name"`
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Active   *bool   `json:"active"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.userByID(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "usuario no encontrado")
		return
	}
	self := currentUser(r).ID == id
	if self && ((in.Role != nil && *in.Role != "admin") || (in.Active != nil && !*in.Active)) {
		writeError(w, http.StatusBadRequest, "no puedes quitarte el rol de administrador ni desactivarte")
		return
	}

	sets, args := []string{}, []any{}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 128 {
			writeError(w, http.StatusBadRequest, "nombre requerido (máx. 128)")
			return
		}
		sets, args = append(sets, "name=?"), append(args, n)
	}
	if in.Role != nil {
		if !validRole(*in.Role) {
			writeError(w, http.StatusBadRequest, "rol inválido")
			return
		}
		sets, args = append(sets, "role=?"), append(args, *in.Role)
	}
	if in.Active != nil {
		sets, args = append(sets, "active=?"), append(args, *in.Active)
	}
	if in.Password != nil && *in.Password != "" {
		if len(*in.Password) < 8 || len(*in.Password) > 72 {
			writeError(w, http.StatusBadRequest, "contraseña: entre 8 y 72 caracteres")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
		sets, args = append(sets, "password_hash=?"), append(args, string(hash))
	}
	if len(sets) > 0 {
		args = append(args, id)
		if _, err := s.db.ExecContext(r.Context(),
			`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
	}
	u, _ := s.userByID(r.Context(), id)
	writeJSON(w, http.StatusOK, u)
}
