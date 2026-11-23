package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"typsteditor/backend/internal/mail"
)

// userCols: Pending sale de password_hash vacío (alta pendiente de definir la contraseña).
const userCols = `id, username, COALESCE(email, ''), name, role, active, created_at,
  CASE WHEN password_hash = '' THEN 1 ELSE 0 END, must_change_password`

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,64}$`)

// dummyHash equalizes login timing when the user does not exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.Role, &u.Active, &u.CreatedAt, &u.Pending, &u.MustChangePassword)
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
	// Se entra con el nombre de usuario o con el correo.
	login := strings.TrimSpace(in.Username)
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, password_hash, active, role FROM users WHERE username = ? OR email = ?`, login, strings.ToLower(login)).
		Scan(&id, &hash, &active, &role)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
		writeError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	// hash vacío: alta pendiente (aún no definió su contraseña con el enlace del
	// correo). Mismo mensaje que una contraseña incorrecta, para no revelar
	// qué cuentas existen; la pantalla ofrece «¿Olvidaste tu contraseña?».
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
		writeError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || !active {
		writeError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	tok, exp, err := s.tokens.Issue(id, role)
	if err != nil {
		internalError(w, err)
		return
	}
	u, err := s.userByID(r.Context(), id)
	if err != nil {
		internalError(w, err)
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
		internalError(w, err)
		return
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			internalError(w, err)
			return
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}

func validRole(r string) bool { return r == "admin" || r == "user" }

// createUser (admin): crea la cuenta solo con el correo (sin contraseña) y le
// envía el enlace para definirla; sin correo configurado, le asigna una
// contraseña temporal que se muestra una vez y debe cambiar al entrar. Usuario y nombre son opcionales: se
// proponen a partir del correo y el usuario puede cambiar el nombre al
// activar la cuenta. Sin correo configurado, la respuesta trae el enlace.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Role     string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// El correo es obligatorio también con el envío desactivado: solo cambia
	// cómo se activa la cuenta (contraseña temporal en vez de enlace).
	email, ok := mail.NormalizeEmail(in.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "correo inválido")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" {
		in.Username = s.usernameFromEmail(r.Context(), email)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name, _, _ = strings.Cut(email, "@")
	}
	if in.Role == "" {
		in.Role = "user"
	}
	switch {
	case !usernameRe.MatchString(in.Username):
		writeError(w, http.StatusBadRequest, "usuario: 3-64 caracteres (letras, números, . _ -)")
		return
	case len(in.Name) > 128:
		writeError(w, http.StatusBadRequest, "nombre demasiado largo (máx. 128)")
		return
	case !validRole(in.Role):
		writeError(w, http.StatusBadRequest, "rol inválido")
		return
	}
	var taken int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE email = ?`, email).Scan(&taken); err != nil {
		internalError(w, err)
		return
	}
	if taken > 0 {
		writeError(w, http.StatusConflict, "ya existe una cuenta con ese correo")
		return
	}
	res, err := s.db.ExecContext(r.Context(),
		`INSERT INTO users (username, email, name, password_hash, role) VALUES (?, ?, ?, '', ?)`,
		in.Username, email, in.Name, in.Role)
	if s.dialect.IsDuplicate(err) {
		writeError(w, http.StatusConflict, "el usuario o el correo ya existen")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	id, _ := res.LastInsertId()
	u, err := s.userByID(r.Context(), id)
	if err != nil {
		internalError(w, err)
		return
	}
	out, err := s.sendAccessLink(r.Context(), u, currentUser(r).Name)
	if err != nil {
		internalError(w, err)
		return
	}
	// Releído: con contraseña temporal ya no está pendiente y debe cambiarla.
	if nu, err := s.userByID(r.Context(), id); err == nil {
		u = nu
	}
	out["user"] = u
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var in struct {
		Name     *string `json:"name"`
		Email    *string `json:"email"` // "" lo quita
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
	if in.Email != nil {
		var email any // NULL
		if strings.TrimSpace(*in.Email) != "" {
			e, ok := mail.NormalizeEmail(*in.Email)
			if !ok {
				writeError(w, http.StatusBadRequest, "correo inválido")
				return
			}
			email = e
		}
		sets, args = append(sets, "email=?"), append(args, email)
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
			internalError(w, err)
			return
		}
		sets, args = append(sets, "password_hash=?"), append(args, string(hash))
	}
	if len(sets) > 0 {
		args = append(args, id)
		if _, err := s.db.ExecContext(r.Context(),
			`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); s.dialect.IsDuplicate(err) {
			writeError(w, http.StatusConflict, "ese correo ya lo usa otra cuenta")
			return
		} else if err != nil {
			internalError(w, err)
			return
		}
	}
	u, _ := s.userByID(r.Context(), id)
	writeJSON(w, http.StatusOK, u)
}
