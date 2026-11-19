package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Member struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	Role      string    `json:"role"` // owner | member
	Active    bool      `json:"active"`
	InvitedBy *string   `json:"invited_by"`
	Since     time.Time `json:"since"`
}

// listMembers devuelve el propietario y los usuarios invitados.
func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT u.id, u.username, u.name, 'owner', u.active, NULL, p.created_at
FROM projects p JOIN users u ON u.id = p.owner_id WHERE p.id = ?
UNION ALL
SELECT u.id, u.username, u.name, 'member', u.active, i.name, m.created_at
FROM project_members m
JOIN users u ON u.id = m.user_id
LEFT JOIN users i ON i.id = m.invited_by
WHERE m.project_id = ?
ORDER BY 4 DESC, 3`, p.ID, p.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Username, &m.Name, &m.Role, &m.Active, &m.InvitedBy, &m.Since); err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

// addMember invita a un usuario registrado (por nombre de usuario).
func (s *Server) addMember(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	me := currentUser(r)
	if !canManage(me, p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede invitar usuarios")
		return
	}
	var in struct {
		Username string `json:"username"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		uid    int64
		active bool
	)
	err := s.db.QueryRowContext(r.Context(), `SELECT id, active FROM users WHERE username = ?`,
		strings.TrimSpace(in.Username)).Scan(&uid, &active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !active) {
		writeError(w, http.StatusNotFound, "no existe ningún usuario activo con ese nombre de usuario")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	if uid == p.OwnerID {
		writeError(w, http.StatusBadRequest, "ese usuario es el propietario del proyecto")
		return
	}
	_, err = s.db.ExecContext(r.Context(),
		`INSERT INTO project_members (project_id, user_id, invited_by) VALUES (?, ?, ?)`, p.ID, uid, me.ID)
	var me1062 *mysql.MySQLError
	if errors.As(err, &me1062) && me1062.Number == 1062 {
		writeError(w, http.StatusConflict, "el usuario ya es miembro del proyecto")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	s.hub.NotifyMembers(p.ID)
	w.WriteHeader(http.StatusCreated)
}

// removeMember quita a un miembro. El propietario o un admin pueden quitar a
// cualquiera; un miembro puede quitarse a sí mismo (abandonar el proyecto).
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	me := currentUser(r)
	if !canManage(me, p) && me.ID != uid {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede quitar miembros")
		return
	}
	if uid == p.OwnerID {
		writeError(w, http.StatusBadRequest, "no se puede quitar al propietario")
		return
	}
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, p.ID, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "ese usuario no es miembro del proyecto")
		return
	}
	// Su organización personal del proyecto (carpeta, destacado) deja de tener sentido.
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM project_user_prefs WHERE project_id = ? AND user_id = ?`, p.ID, uid)
	// Cerrar sus sesiones de edición abiertas (salvo que sea admin: sigue teniendo acceso).
	var role string
	if err := s.db.QueryRowContext(r.Context(), `SELECT role FROM users WHERE id = ?`, uid).Scan(&role); err != nil || role != "admin" {
		s.hub.KickUser(p.ID, uid, "ya no tienes acceso a este proyecto")
	}
	s.hub.NotifyMembers(p.ID)
	w.WriteHeader(http.StatusNoContent)
}

// searchUsers busca usuarios activos para invitarlos (máx. 10 resultados).
func (s *Server) searchUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []map[string]any{}
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
	rows, err := s.db.QueryContext(r.Context(), `
SELECT id, username, name FROM users
WHERE active = 1 AND id <> ? AND (username LIKE ? OR name LIKE ?)
ORDER BY username LIMIT 10`, currentUser(r).ID, like, like)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id             int64
			username, name string
		)
		if err := rows.Scan(&id, &username, &name); err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
		out = append(out, map[string]any{"id": id, "username": username, "name": name})
	}
	writeJSON(w, http.StatusOK, out)
}
