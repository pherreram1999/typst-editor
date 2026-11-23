package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"typsteditor/backend/internal/auth"
	"typsteditor/backend/internal/collab"
	"typsteditor/backend/internal/database"
)

// Enlaces de invitación: el propietario (o un admin) genera URL firmadas
// (#/i/<token>, ver auth/guest.go) que caducan en horas, días o meses. Quien
// abre una entra al proyecto como invitado, sin cuenta, con el nombre que
// elija (o uno aleatorio). Cada enlace puede ser de edición o de solo lectura.
//
// Un invitado se autentica con un JWT propio (IssueGuest) y en cada petición
// se revalida el enlace en la BD: revocarlo o que caduque corta el acceso al
// momento (y revokeInvite cierra sus WebSocket). Solo llega a las rutas
// registradas con «shared» en Routes —el proyecto, sus archivos, la vista
// previa, el PDF y la edición—, y loadProject solo le deja ver el proyecto
// del enlace. Las rutas nuevas quedan, por omisión, cerradas a invitados.

// guestSession: invitado autenticado de la petición actual.
type guestSession struct {
	auth.Guest
	CanEdit   bool
	ExpiresAt time.Time
}

type guestKey struct{}

func currentGuest(r *http.Request) *guestSession {
	g, _ := r.Context().Value(guestKey{}).(*guestSession)
	return g
}

// Límites de la caducidad elegida al crear un enlace.
var inviteUnits = map[string]struct {
	max int
	add func(time.Time, int) time.Time
}{
	"hours":  {720, func(t time.Time, n int) time.Time { return t.Add(time.Duration(n) * time.Hour) }},
	"days":   {365, func(t time.Time, n int) time.Time { return t.AddDate(0, 0, n) }},
	"months": {12, func(t time.Time, n int) time.Time { return t.AddDate(0, n, 0) }},
}

type inviteRow struct {
	ID         int64
	ProjectID  int64
	Nonce      string
	Label      string
	Permission string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Joins      int
	LastUsedAt database.NullTime
	CreatedBy  sql.NullString
}

const inviteCols = `i.id, i.project_id, i.nonce, i.label, i.permission, i.created_at, i.expires_at, i.joins, i.last_used_at, u.name`

func scanInvite(row interface{ Scan(...any) error }) (*inviteRow, error) {
	var iv inviteRow
	err := row.Scan(&iv.ID, &iv.ProjectID, &iv.Nonce, &iv.Label, &iv.Permission, &iv.CreatedAt, &iv.ExpiresAt,
		&iv.Joins, &iv.LastUsedAt, &iv.CreatedBy)
	return &iv, err
}

// inviteByID carga un enlace vigente (sql.ErrNoRows si no existe o ya caducó).
func (s *Server) inviteByID(ctx context.Context, id int64) (*inviteRow, error) {
	iv, err := scanInvite(s.db.QueryRowContext(ctx, `SELECT `+inviteCols+`
FROM project_invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.id = ?`, id))
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(iv.ExpiresAt) {
		return nil, sql.ErrNoRows
	}
	return iv, nil
}

// inviteFromToken valida la firma del enlace y lo busca en la BD.
func (s *Server) inviteFromToken(ctx context.Context, token string) (*inviteRow, error) {
	id, nonce, _, err := s.tokens.ParseInvite(token, time.Now())
	if err != nil {
		return nil, sql.ErrNoRows
	}
	iv, err := s.inviteByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if iv.Nonce != nonce {
		return nil, sql.ErrNoRows
	}
	return iv, nil
}

// guestFromToken valida una sesión de invitado contra su enlace (vigente y del mismo proyecto).
func (s *Server) guestFromToken(ctx context.Context, tok string) (*guestSession, error) {
	g, err := s.tokens.ParseGuest(tok)
	if err != nil {
		return nil, err
	}
	iv, err := s.inviteByID(ctx, g.InviteID)
	if err != nil {
		return nil, err
	}
	if iv.ProjectID != g.ProjectID {
		return nil, errors.New("enlace de otro proyecto")
	}
	return &guestSession{Guest: *g, CanEdit: iv.Permission == "edit", ExpiresAt: iv.ExpiresAt}, nil
}

// guestUser: usuario sintético de un invitado (rol «guest», ID negativo).
func guestUser(g *guestSession) *User {
	return &User{ID: g.ID, Name: g.Name, Role: "guest", Active: true}
}

// requireWrite responde 403 si la petición es de un invitado de solo lectura.
func requireWrite(w http.ResponseWriter, r *http.Request) bool {
	if g := currentGuest(r); g != nil && !g.CanEdit {
		writeError(w, http.StatusForbidden, "este enlace de invitado es de solo lectura")
		return false
	}
	return true
}

// guestProject carga el proyecto de un invitado (solo el de su enlace).
func (s *Server) guestProject(ctx context.Context, g *guestSession, id int64) (*Project, error) {
	if id != g.ProjectID {
		return nil, sql.ErrNoRows
	}
	p, err := scanProject(s.db.QueryRowContext(ctx, s.projectSelect+` WHERE p.id = ?`, selectArgs(guestUser(g), id)...))
	if err != nil {
		return nil, err
	}
	// Nada de la configuración de GitHub para quien no tiene cuenta.
	p.Access, p.ReadOnly, p.Starred, p.FolderID = "guest", !g.CanEdit, false, nil
	p.GitRemote, p.GitBranch, p.GitHubAccountID, p.GitHubLogin, p.GitHubMine, p.HasToken = "", "", nil, "", false, false
	return p, nil
}

// ---------- gestión (propietario o admin) ----------

func (s *Server) inviteJSON(iv *inviteRow) map[string]any {
	return map[string]any{
		"id":           iv.ID,
		"label":        iv.Label,
		"permission":   iv.Permission,
		"created_at":   iv.CreatedAt,
		"created_by":   iv.CreatedBy.String,
		"expires_at":   iv.ExpiresAt,
		"expired":      !time.Now().Before(iv.ExpiresAt),
		"joins":        iv.Joins,
		"last_used_at": nullTimeJSON(iv.LastUsedAt),
		// El token se puede volver a calcular: el propietario copia el enlace cuando quiera.
		"token": s.tokens.SignInvite(iv.ID, iv.Nonce, iv.ExpiresAt),
	}
}

func nullTimeJSON(t database.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func (s *Server) manageInvites(w http.ResponseWriter, r *http.Request) (*Project, bool) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return nil, false
	}
	if !canManage(currentUser(r), p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede gestionar los enlaces de invitado")
		return nil, false
	}
	return p, true
}

// GET /api/projects/{id}/invites: enlaces del proyecto (los caducados hace
// más de 30 días se borran).
func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	p, ok := s.manageInvites(w, r)
	if !ok {
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM project_invites WHERE project_id = ? AND expires_at < ?`,
		p.ID, time.Now().AddDate(0, 0, -30).UTC())
	rows, err := s.db.QueryContext(r.Context(), `SELECT `+inviteCols+`
FROM project_invites i LEFT JOIN users u ON u.id = i.created_by
WHERE i.project_id = ? ORDER BY i.created_at DESC, i.id DESC`, p.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		iv, err := scanInvite(rows)
		if err != nil {
			internalError(w, err)
			return
		}
		out = append(out, s.inviteJSON(iv))
	}
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/projects/{id}/invites {label, permission: edit|view, amount, unit: hours|days|months}
func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.manageInvites(w, r)
	if !ok {
		return
	}
	var in struct {
		Label      string `json:"label"`
		Permission string `json:"permission"`
		Amount     int    `json:"amount"`
		Unit       string `json:"unit"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Label = strings.TrimSpace(in.Label)
	if utf8.RuneCountInString(in.Label) > 100 {
		writeError(w, http.StatusBadRequest, "la descripción admite como máximo 100 caracteres")
		return
	}
	if in.Permission == "" {
		in.Permission = "edit"
	}
	if in.Permission != "edit" && in.Permission != "view" {
		writeError(w, http.StatusBadRequest, "permiso inválido (edit o view)")
		return
	}
	unit, ok := inviteUnits[in.Unit]
	if !ok {
		writeError(w, http.StatusBadRequest, "unidad inválida (hours, days o months)")
		return
	}
	if in.Amount < 1 || in.Amount > unit.max {
		writeError(w, http.StatusBadRequest, "duración inválida: entre 1 y "+strconv.Itoa(unit.max))
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	exp := unit.add(now, in.Amount)
	var nb [16]byte
	_, _ = rand.Read(nb[:])
	nonce := base64.RawURLEncoding.EncodeToString(nb[:])
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO project_invites
(project_id, nonce, label, permission, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, nonce, in.Label, in.Permission, currentUser(r).ID, now, exp)
	if err != nil {
		internalError(w, err)
		return
	}
	id, _ := res.LastInsertId()
	iv, err := s.inviteByID(r.Context(), id)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.inviteJSON(iv))
}

// DELETE /api/projects/{id}/invites/{inviteId}: revoca el enlace y expulsa a
// los invitados que entraron con él.
func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.manageInvites(w, r)
	if !ok {
		return
	}
	iid, err := strconv.ParseInt(r.PathValue("inviteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM project_invites WHERE id = ? AND project_id = ?`, iid, p.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "enlace no encontrado")
		return
	}
	s.hub.KickPeers(p.ID, func(peer *collab.Peer) bool { return peer.Invite == iid },
		"el enlace de invitado se ha revocado")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- públicas (sin sesión) ----------

// GET /api/invite/{token}: lo que ve el invitado antes de entrar.
func (s *Server) inviteInfo(w http.ResponseWriter, r *http.Request) {
	iv, err := s.inviteFromToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "el enlace no es válido o ha caducado")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	var name, owner string
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT p.name, u.name FROM projects p JOIN users u ON u.id = p.owner_id WHERE p.id = ?`, iv.ProjectID).Scan(&name, &owner); err != nil {
		writeError(w, http.StatusNotFound, "el enlace no es válido o ha caducado")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":   iv.ProjectID,
		"project_name": name,
		"owner_name":   owner,
		"label":        iv.Label,
		"permission":   iv.Permission,
		"expires_at":   iv.ExpiresAt,
		"editors":      s.hub.ActiveEditors(0)[iv.ProjectID],
	})
}

// cleanGuestName valida el nombre visible de un invitado (1–40 caracteres).
func cleanGuestName(n string) (string, bool) {
	n = strings.Join(strings.Fields(n), " ")
	if n == "" || utf8.RuneCountInString(n) > 40 {
		return "", false
	}
	for _, c := range n {
		if unicode.IsControl(c) || c == 0x200b || c == 0xfeff {
			return "", false
		}
	}
	return n, true
}

// POST /api/invite/{token}/join {name}: abre una sesión de invitado.
func (s *Server) joinInvite(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("join:"+clientIP(r), 60, 10*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "demasiados intentos; espera unos minutos")
		return
	}
	iv, err := s.inviteFromToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "el enlace no es válido o ha caducado")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, ok := cleanGuestName(in.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "escribe un nombre de 1 a 40 caracteres")
		return
	}
	// ID negativo y aleatorio dentro de los enteros exactos de JavaScript (2^52).
	var b [8]byte
	_, _ = rand.Read(b[:])
	gid := -int64(binary.BigEndian.Uint64(b[:])>>12) - 1
	g := &guestSession{Guest: auth.Guest{ID: gid, InviteID: iv.ID, ProjectID: iv.ProjectID, Name: name},
		CanEdit: iv.Permission == "edit", ExpiresAt: iv.ExpiresAt}
	tok, err := s.tokens.IssueGuest(g.Guest, iv.ExpiresAt)
	if err != nil {
		internalError(w, err)
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE project_invites SET joins = joins + 1, last_used_at = ? WHERE id = ?`,
		time.Now().UTC(), iv.ID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"user":       guestUser(g),
		"project_id": iv.ProjectID,
		"permission": iv.Permission,
		"expires_at": iv.ExpiresAt,
	})
}
