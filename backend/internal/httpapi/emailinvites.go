package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"typsteditor/backend/internal/auth"
	"typsteditor/backend/internal/mail"
)

// Invitaciones a un proyecto por correo: el propietario (o un admin) comparte
// con un correo que no tiene cuenta. Se guarda en project_email_invites y se
// envía una URL firmada (#/unirse/<token>, SignURLToken(PurposeJoin, …)) de
// un solo uso. Quien la abre crea su cuenta (con ese correo, ya comprobado
// porque recibió el enlace) o entra con la que tenga, y queda como miembro.
// Mientras no se acepta aparece como pendiente en «Compartir», donde se puede
// reenviar o anular.

const emailInviteTTL = 14 * 24 * time.Hour

type emailInvite struct {
	ID          int64
	ProjectID   int64
	Email       string
	Nonce       string
	InvitedBy   sql.NullInt64
	InviterName sql.NullString
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Accepted    sql.NullTime
}

const emailInviteCols = `e.id, e.project_id, e.email, e.nonce, e.invited_by, u.name, e.created_at, e.expires_at, e.accepted_at`

func scanEmailInvite(row interface{ Scan(...any) error }) (*emailInvite, error) {
	var e emailInvite
	err := row.Scan(&e.ID, &e.ProjectID, &e.Email, &e.Nonce, &e.InvitedBy, &e.InviterName, &e.CreatedAt, &e.ExpiresAt, &e.Accepted)
	return &e, err
}

func (s *Server) emailInviteByID(ctx context.Context, id int64) (*emailInvite, error) {
	return scanEmailInvite(s.db.QueryRowContext(ctx, `SELECT `+emailInviteCols+`
FROM project_email_invites e LEFT JOIN users u ON u.id = e.invited_by WHERE e.id = ?`, id))
}

func emailInviteJSON(e *emailInvite) map[string]any {
	return map[string]any{
		"id": e.ID, "email": e.Email, "invited_by": e.InviterName.String,
		"created_at": e.CreatedAt, "expires_at": e.ExpiresAt, "expired": !time.Now().Before(e.ExpiresAt),
	}
}

// sendEmailInvite renueva el nonce y la caducidad de la invitación y la envía.
func (s *Server) sendEmailInvite(ctx context.Context, e *emailInvite, p *Project, by *User) (map[string]any, error) {
	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(emailInviteTTL)
	nonce := newNonce()
	if _, err := s.db.ExecContext(ctx, `UPDATE project_email_invites SET nonce = ?, expires_at = ?, invited_by = ? WHERE id = ?`,
		nonce, exp, by.ID, e.ID); err != nil {
		return nil, err
	}
	e.Nonce, e.ExpiresAt, e.InviterName = nonce, exp, sql.NullString{String: by.Name, Valid: true}
	link := s.appLink("unirse/" + s.tokens.SignURLToken(auth.PurposeJoin, e.ID, nonce, exp))
	err := s.deliver(ctx, mail.ProjectInvite(e.Email, by.Name, p.Name, link, exp))
	out := mailResult(err, link)
	out["invited"] = emailInviteJSON(e)
	return out, nil
}

// inviteByEmail: addMember con un correo sin cuenta.
func (s *Server) inviteByEmail(w http.ResponseWriter, r *http.Request, p *Project, me *User, email string) {
	if !s.limiter.allow("email-invite:"+strconv.FormatInt(me.ID, 10), 30, time.Hour) {
		writeError(w, http.StatusTooManyRequests, "demasiadas invitaciones en poco tiempo; espera un rato")
		return
	}
	// Una sola invitación pendiente por correo y proyecto: se reutiliza.
	var id int64
	err := s.db.QueryRowContext(r.Context(), `SELECT id FROM project_email_invites
WHERE project_id = ? AND email = ? AND accepted_at IS NULL ORDER BY id DESC LIMIT 1`, p.ID, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC().Truncate(time.Second)
		res, err := s.db.ExecContext(r.Context(), `INSERT INTO project_email_invites (project_id, email, nonce, invited_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, p.ID, email, newNonce(), me.ID, now, now.Add(emailInviteTTL))
		if err != nil {
			internalError(w, err)
			return
		}
		id, _ = res.LastInsertId()
	} else if err != nil {
		internalError(w, err)
		return
	}
	e, err := s.emailInviteByID(r.Context(), id)
	if err != nil {
		internalError(w, err)
		return
	}
	out, err := s.sendEmailInvite(r.Context(), e, p, me)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}

func (s *Server) manageEmailInvite(w http.ResponseWriter, r *http.Request) (*Project, *emailInvite, bool) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return nil, nil, false
	}
	if !canManage(currentUser(r), p) {
		writeError(w, http.StatusForbidden, "solo el propietario o un administrador puede gestionar las invitaciones")
		return nil, nil, false
	}
	id, err := strconv.ParseInt(r.PathValue("inviteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return nil, nil, false
	}
	e, err := s.emailInviteByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (e.ProjectID != p.ID || e.Accepted.Valid)) {
		writeError(w, http.StatusNotFound, "invitación no encontrada")
		return nil, nil, false
	}
	if err != nil {
		internalError(w, err)
		return nil, nil, false
	}
	return p, e, true
}

// GET /api/projects/{id}/email-invites: invitaciones por correo sin aceptar.
func (s *Server) listEmailInvites(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if !canManage(currentUser(r), p) {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	// Las caducadas hace más de 30 días ya no se muestran.
	rows, err := s.db.QueryContext(r.Context(), `SELECT `+emailInviteCols+`
FROM project_email_invites e LEFT JOIN users u ON u.id = e.invited_by
WHERE e.project_id = ? AND e.accepted_at IS NULL AND e.expires_at > ?
ORDER BY e.created_at DESC`, p.ID, time.Now().AddDate(0, 0, -30).UTC())
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		e, err := scanEmailInvite(rows)
		if err != nil {
			internalError(w, err)
			return
		}
		out = append(out, emailInviteJSON(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// DELETE /api/projects/{id}/email-invites/{inviteId}: anula la invitación.
func (s *Server) deleteEmailInvite(w http.ResponseWriter, r *http.Request) {
	_, e, ok := s.manageEmailInvite(w, r)
	if !ok {
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM project_email_invites WHERE id = ?`, e.ID); err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/projects/{id}/email-invites/{inviteId}/resend: nuevo enlace (el anterior deja de valer).
func (s *Server) resendEmailInvite(w http.ResponseWriter, r *http.Request) {
	p, e, ok := s.manageEmailInvite(w, r)
	if !ok {
		return
	}
	me := currentUser(r)
	if !s.limiter.allow("email-invite:"+strconv.FormatInt(me.ID, 10), 30, time.Hour) {
		writeError(w, http.StatusTooManyRequests, "demasiadas invitaciones en poco tiempo; espera un rato")
		return
	}
	out, err := s.sendEmailInvite(r.Context(), e, p, me)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- quien recibe la invitación ----------

var errJoinInvalid = errf(http.StatusNotFound, "la invitación no es válida, ya se usó o caducó; pide una nueva a quien te invitó")

// joinInviteFrom valida la firma y la fila (vigente y sin aceptar).
func (s *Server) joinInviteFrom(ctx context.Context, token string) (*emailInvite, error) {
	id, nonce, _, err := s.tokens.ParseURLToken(auth.PurposeJoin, token, time.Now())
	if err != nil {
		return nil, errJoinInvalid
	}
	e, err := s.emailInviteByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (e.Nonce != nonce || e.Accepted.Valid || !time.Now().Before(e.ExpiresAt))) {
		return nil, errJoinInvalid
	}
	return e, err
}

// GET /api/join/{token}: lo que ve el invitado antes de aceptar.
func (s *Server) joinInfo(w http.ResponseWriter, r *http.Request) {
	e, err := s.joinInviteFrom(r.Context(), r.PathValue("token"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	var name, owner string
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT p.name, u.name FROM projects p JOIN users u ON u.id = p.owner_id WHERE p.id = ?`, e.ProjectID).Scan(&name, &owner); err != nil {
		writeAPIError(w, errJoinInvalid)
		return
	}
	var n int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE email = ?`, e.Email).Scan(&n)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"project_name": name, "owner_name": owner, "inviter_name": e.InviterName.String,
		"email": e.Email, "expires_at": e.ExpiresAt, "has_account": n > 0,
		"suggested_username": s.usernameFromEmail(r.Context(), e.Email),
	})
}

// claimJoin marca la invitación como aceptada (solo una vez) y agrega al usuario.
func (s *Server) claimJoin(ctx context.Context, e *emailInvite, uid int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE project_email_invites SET accepted_at = ?, accepted_by = ? WHERE id = ? AND accepted_at IS NULL`,
		time.Now().UTC(), uid, e.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errJoinInvalid
	}
	var p Project
	if err := s.db.QueryRowContext(ctx, `SELECT id, name, owner_id FROM projects WHERE id = ?`, e.ProjectID).Scan(&p.ID, &p.Name, &p.OwnerID); err != nil {
		return err
	}
	if uid == p.OwnerID {
		return nil
	}
	by := &User{ID: e.InvitedBy.Int64, Name: e.InviterName.String}
	if !e.InvitedBy.Valid {
		by.ID = p.OwnerID
	}
	_, err = s.db.ExecContext(ctx, s.dialect.InsertIgnore()+` INTO project_members (project_id, user_id, invited_by) VALUES (?, ?, ?)`,
		p.ID, uid, by.ID)
	if err == nil {
		s.hub.NotifyMembers(p.ID)
	}
	return err
}

// POST /api/join/{token}/accept: un usuario con sesión acepta la invitación.
func (s *Server) joinAccept(w http.ResponseWriter, r *http.Request) {
	e, err := s.joinInviteFrom(r.Context(), r.PathValue("token"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if err := s.claimJoin(r.Context(), e, currentUser(r).ID); err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project_id": e.ProjectID})
}

// POST /api/join/{token}/register {name, username, password}: crea la cuenta
// con el correo de la invitación, la agrega al proyecto y abre la sesión.
func (s *Server) joinRegister(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("join-register:"+clientIP(r), 20, 10*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "demasiados intentos; espera unos minutos")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	e, err := s.joinInviteFrom(r.Context(), r.PathValue("token"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	in.Name, in.Username = strings.TrimSpace(in.Name), strings.TrimSpace(in.Username)
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 128:
		writeError(w, http.StatusBadRequest, "nombre requerido (máx. 128)")
		return
	case !usernameRe.MatchString(in.Username):
		writeError(w, http.StatusBadRequest, "usuario: 3-64 caracteres (letras, números, . _ -)")
		return
	case !validPassword(in.Password):
		writeError(w, http.StatusBadRequest, "contraseña: entre 8 y 72 caracteres")
		return
	}
	var n int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE email = ?`, e.Email).Scan(&n); err != nil {
		internalError(w, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "ya existe una cuenta con este correo: inicia sesión para aceptar la invitación")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(w, err)
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO users (username, email, name, password_hash, role) VALUES (?, ?, ?, ?, 'user')`,
		in.Username, e.Email, in.Name, string(hash))
	if s.dialect.IsDuplicate(err) {
		writeError(w, http.StatusConflict, "ese nombre de usuario ya está ocupado; elige otro")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	uid, _ := res.LastInsertId()
	if err := s.claimJoin(r.Context(), e, uid); err != nil {
		// Otra pestaña la aceptó a la vez: la cuenta queda creada igualmente.
		var ae *apiError
		if !errors.As(err, &ae) {
			internalError(w, err)
			return
		}
	}
	s.writeSessionWith(w, r, uid, map[string]any{"project_id": e.ProjectID})
}

// writeSessionWith: como writeSession, con campos adicionales en la respuesta.
func (s *Server) writeSessionWith(w http.ResponseWriter, r *http.Request, id int64, extra map[string]any) {
	u, err := s.userByID(r.Context(), id)
	if err != nil {
		internalError(w, err)
		return
	}
	tok, exp, err := s.tokens.Issue(u.ID, u.Role)
	if err != nil {
		internalError(w, err)
		return
	}
	out := map[string]any{"token": tok, "expires_at": exp, "user": u}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}
