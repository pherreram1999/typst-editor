package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"typsteditor/backend/internal/auth"
	"typsteditor/backend/internal/mail"
)

// Cuentas por correo. Un administrador crea la cuenta solo con el correo (sin
// contraseña: password_hash vacío, «pendiente») y el usuario recibe una URL
// firmada (#/cuenta/<token>) de un solo uso para definirla; hasta entonces no
// puede iniciar sesión. El mismo mecanismo sirve para restablecer la
// contraseña («olvidé mi contraseña») y para confirmar un correo nuevo.
//
// Los tokens son SignURLToken(PurposeAccount, id, nonce, caducidad): la firma
// se comprueba antes de ir a la BD y la fila de account_tokens (con el mismo
// nonce y used_at NULL) los hace de un solo uso y revocables.

const (
	purposeSetup = "setup" // definir la contraseña de una cuenta nueva
	purposeReset = "reset" // restablecer la contraseña
	purposeEmail = "email" // confirmar un correo nuevo (columna email)

	setupTTL = 7 * 24 * time.Hour
	resetTTL = 2 * time.Hour
	emailTTL = 48 * time.Hour
)

func newNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// appLink: URL absoluta de una ruta de la SPA (PUBLIC_URL/#/<ruta>).
func (s *Server) appLink(route string) string {
	return s.opts.PublicURL + "/#/" + route
}

// deliver envía un correo y registra el fallo (sin clave: mail.ErrDisabled).
func (s *Server) deliver(ctx context.Context, m mail.Message) error {
	err := s.mail.Send(ctx, m)
	if err != nil && !errors.Is(err, mail.ErrDisabled) {
		log.Printf("correo «%s» a %s: %v", m.Subject, m.To, err)
	}
	return err
}

// deliverAsync envía un aviso sin hacer esperar a la petición.
func (s *Server) deliverAsync(m mail.Message) {
	if !s.mail.Enabled() || m.To == "" {
		return
	}
	go func() { _ = s.deliver(context.Background(), m) }()
}

// mailResult: resultado de un envío para quien lo provocó. Sin correo
// configurado (o si falló) incluye el enlace, para copiarlo y enviarlo a mano.
func mailResult(err error, link string) map[string]any {
	out := map[string]any{"mail_sent": err == nil}
	if err != nil {
		out["link"] = link
		if errors.Is(err, mail.ErrDisabled) {
			out["mail_error"] = "el envío de correos no está configurado (falta RESEND)"
		} else {
			out["mail_error"] = "no se pudo enviar el correo"
		}
	}
	return out
}

// issueAccountToken crea un enlace de cuenta (y anula los anteriores sin usar
// del mismo tipo para ese usuario). Devuelve la URL y su caducidad.
func (s *Server) issueAccountToken(ctx context.Context, userID int64, purpose, email string) (string, time.Time, error) {
	ttl := map[string]time.Duration{purposeSetup: setupTTL, purposeReset: resetTTL, purposeEmail: emailTTL}[purpose]
	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(ttl)
	nonce := newNonce()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM account_tokens WHERE user_id = ? AND purpose = ? AND used_at IS NULL`, userID, purpose)
	var em any
	if email != "" {
		em = email
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO account_tokens (user_id, purpose, nonce, email, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, userID, purpose, nonce, em, now, exp)
	if err != nil {
		return "", time.Time{}, err
	}
	id, _ := res.LastInsertId()
	return s.appLink("cuenta/" + s.tokens.SignURLToken(auth.PurposeAccount, id, nonce, exp)), exp, nil
}

type accountToken struct {
	ID      int64
	Purpose string
	Email   string // purposeEmail: el correo a confirmar
	User    *User
}

var errTokenInvalid = errors.New("el enlace no es válido, ya se usó o caducó")

// accountTokenFrom valida la firma y la fila (vigente, sin usar, usuario activo).
func (s *Server) accountTokenFrom(ctx context.Context, token string) (*accountToken, error) {
	id, nonce, _, err := s.tokens.ParseURLToken(auth.PurposeAccount, token, time.Now())
	if err != nil {
		return nil, errTokenInvalid
	}
	var (
		t       accountToken
		dbNonce string
		email   sql.NullString
		uid     int64
		exp     time.Time
		used    sql.NullTime
	)
	err = s.db.QueryRowContext(ctx, `SELECT purpose, nonce, email, user_id, expires_at, used_at FROM account_tokens WHERE id = ?`, id).
		Scan(&t.Purpose, &dbNonce, &email, &uid, &exp, &used)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (dbNonce != nonce || used.Valid || !time.Now().Before(exp))) {
		return nil, errTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	u, err := s.userByID(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !u.Active) {
		return nil, errTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	t.ID, t.Email, t.User = id, email.String, u
	return &t, nil
}

// sendAccessLink envía (o devuelve, sin correo) el enlace para entrar: alta si
// la cuenta está pendiente, restablecer la contraseña si no. inviter: quien
// creó la cuenta (solo en el correo de bienvenida).
func (s *Server) sendAccessLink(ctx context.Context, u *User, inviter string) (map[string]any, error) {
	// Sin correo: contraseña temporal en lugar de enlace.
	if !s.mail.Enabled() {
		return s.setTempPassword(ctx, u)
	}
	purpose := purposeReset
	if u.Pending {
		purpose = purposeSetup
	}
	link, exp, err := s.issueAccountToken(ctx, u.ID, purpose, "")
	if err != nil {
		return nil, err
	}
	var sendErr error = mail.ErrDisabled
	if u.Email != "" {
		var m mail.Message
		if purpose == purposeSetup {
			m = mail.Welcome(u.Email, u.Name, u.Username, inviter, link, exp)
		} else {
			m = mail.ResetPassword(u.Email, u.Name, link, exp)
		}
		sendErr = s.deliver(ctx, m)
	}
	out := mailResult(sendErr, link)
	if u.Email == "" {
		out["mail_error"] = "el usuario no tiene correo"
	}
	out["purpose"] = purpose
	out["expires_at"] = exp
	return out, nil
}

func validPassword(p string) bool { return len(p) >= 8 && len(p) <= 72 }

// ---------- handlers ----------

// GET /api/auth/config: lo que la pantalla de inicio necesita saber.
func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"mail_enabled": s.mail.Enabled()})
}

// GET /api/auth/account/{token}: datos del enlace (para la pantalla de la contraseña).
func (s *Server) accountInfo(w http.ResponseWriter, r *http.Request) {
	t, err := s.accountTokenFrom(r.Context(), r.PathValue("token"))
	if errors.Is(err, errTokenInvalid) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"purpose": t.Purpose, "name": t.User.Name, "username": t.User.Username, "email": t.User.Email}
	if t.Purpose == purposeEmail {
		out["email"] = t.Email
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/auth/account/{token}/password {password, name?}: define o
// restablece la contraseña y abre la sesión.
func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("account:"+clientIP(r), 30, 10*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "demasiados intentos; espera unos minutos")
		return
	}
	var in struct {
		Password string  `json:"password"`
		Name     *string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.accountTokenFrom(r.Context(), r.PathValue("token"))
	if err == nil && t.Purpose == purposeEmail {
		err = errTokenInvalid
	}
	if errors.Is(err, errTokenInvalid) {
		writeError(w, http.StatusNotFound, errTokenInvalid.Error())
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if !validPassword(in.Password) {
		writeError(w, http.StatusBadRequest, "contraseña: entre 8 y 72 caracteres")
		return
	}
	name := t.User.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > 128 {
			writeError(w, http.StatusBadRequest, "nombre requerido (máx. 128)")
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(w, err)
		return
	}
	// Marcar el enlace como usado solo si nadie lo usó antes (dos pestañas a la vez).
	res, err := s.db.ExecContext(r.Context(), `UPDATE account_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`, time.Now().UTC(), t.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, errTokenInvalid.Error())
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE users SET password_hash = ?, name = ?, must_change_password = 0 WHERE id = ?`, string(hash), name, t.User.ID); err != nil {
		internalError(w, err)
		return
	}
	// Los demás enlaces de contraseña pendientes ya no hacen falta.
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM account_tokens WHERE user_id = ? AND purpose IN ('setup', 'reset') AND used_at IS NULL`, t.User.ID)
	s.writeSession(w, r, t.User.ID)
}

// writeSession emite el JWT de un usuario y responde como /auth/login.
func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, id int64) {
	s.writeSessionWith(w, r, id, nil)
}

// POST /api/auth/account/{token}/email: confirma un correo nuevo.
func (s *Server) accountEmail(w http.ResponseWriter, r *http.Request) {
	t, err := s.accountTokenFrom(r.Context(), r.PathValue("token"))
	if err == nil && t.Purpose != purposeEmail {
		err = errTokenInvalid
	}
	if errors.Is(err, errTokenInvalid) {
		writeError(w, http.StatusNotFound, errTokenInvalid.Error())
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE users SET email = ? WHERE id = ?`, t.Email, t.User.ID); s.dialect.IsDuplicate(err) {
		writeError(w, http.StatusConflict, "ese correo ya lo usa otra cuenta")
		return
	} else if err != nil {
		internalError(w, err)
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE account_tokens SET used_at = ? WHERE id = ?`, time.Now().UTC(), t.ID)
	u, _ := s.userByID(r.Context(), t.User.ID)
	writeJSON(w, http.StatusOK, u)
}

// POST /api/auth/forgot {login}: envía el enlace para restablecer la
// contraseña (o para definirla, si la cuenta está pendiente). Responde igual
// exista o no la cuenta, para no revelar qué usuarios o correos hay.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login string `json:"login"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.mail.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "el envío de correos no está configurado; pide al administrador que restablezca tu acceso")
		return
	}
	if !s.limiter.allow("forgot:"+clientIP(r), 10, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "demasiadas solicitudes; espera unos minutos")
		return
	}
	login := strings.TrimSpace(in.Login)
	var id int64
	err := s.db.QueryRowContext(r.Context(), `SELECT id FROM users WHERE (username = ? OR email = ?) AND active = 1`,
		login, strings.ToLower(login)).Scan(&id)
	if err == nil && s.limiter.allow("forgot-user:"+strconv.FormatInt(id, 10), 1, time.Minute) {
		if u, err := s.userByID(r.Context(), id); err == nil && u.Email != "" {
			// En segundo plano: el tiempo de respuesta tampoco debe delatar si existe.
			go func() {
				if _, err := s.sendAccessLink(context.Background(), u, ""); err != nil {
					log.Printf("enlace de contraseña para %d: %v", u.ID, err)
				}
			}()
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/auth/me/email {email}: el usuario añade o cambia su correo; se
// guarda al confirmarlo con el enlace que recibe en esa dirección.
func (s *Server) changeEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	email, ok := mail.NormalizeEmail(in.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "correo inválido")
		return
	}
	if !s.mail.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "el envío de correos no está configurado; pide al administrador que registre tu correo")
		return
	}
	u := currentUser(r)
	if !s.limiter.allow("email-change:"+strconv.FormatInt(u.ID, 10), 5, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "demasiadas solicitudes; espera unos minutos")
		return
	}
	if email == u.Email {
		writeError(w, http.StatusBadRequest, "ese ya es tu correo")
		return
	}
	var other int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM users WHERE email = ?`, email).Scan(&other); err == nil {
		writeError(w, http.StatusConflict, "ese correo ya lo usa otra cuenta")
		return
	}
	link, exp, err := s.issueAccountToken(r.Context(), u.ID, purposeEmail, email)
	if err != nil {
		internalError(w, err)
		return
	}
	if err := s.deliver(r.Context(), mail.VerifyEmail(email, u.Name, link, exp)); err != nil {
		writeError(w, http.StatusBadGateway, "no se pudo enviar el correo de confirmación; inténtalo más tarde")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sent_to": email, "expires_at": exp})
}

// POST /api/users/{id}/access-link (admin): reenvía el enlace de alta o de
// restablecimiento. Sin correo configurado devuelve el enlace para copiarlo.
func (s *Server) userAccessLink(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	u, err := s.userByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "usuario no encontrado")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if !u.Active {
		writeError(w, http.StatusBadRequest, "la cuenta está desactivada")
		return
	}
	out, err := s.sendAccessLink(r.Context(), u, currentUser(r).Name)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// usernameFromEmail propone un nombre de usuario libre a partir del correo
// («ana.perez@x.com» → «ana.perez», «ana.perez2»…).
func (s *Server) usernameFromEmail(ctx context.Context, email string) string {
	local, _, _ := strings.Cut(email, "@")
	base := regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(strings.ToLower(local), "")
	base = strings.Trim(base, "._-")
	if len(base) > 56 {
		base = base[:56]
	}
	for len(base) < 3 {
		base += "x"
	}
	cand := base
	for i := 2; i < 1000; i++ {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username = ?`, cand).Scan(&n); err != nil || n == 0 {
			return cand
		}
		cand = base + strconv.Itoa(i)
	}
	return base + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
}

// ---------- contraseñas temporales (sin correo) ----------

// Alfabeto sin caracteres que se confunden al dictarlos o copiarlos (0/O, 1/l/I).
const tempAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// tempPassword: 3 grupos de 4 (≈ 69 bits), p. ej. «k7Rm-wQ3x-Pz9a».
func tempPassword() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	var out strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(tempAlphabet[int(c)%len(tempAlphabet)])
	}
	return out.String()
}

// setTempPassword asigna una contraseña temporal (que se devuelve una sola
// vez, nunca se guarda en claro) y obliga a cambiarla al iniciar sesión.
func (s *Server) setTempPassword(ctx context.Context, u *User) (map[string]any, error) {
	pw := tempPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, must_change_password = 1 WHERE id = ?`, string(hash), u.ID); err != nil {
		return nil, err
	}
	// Los enlaces de contraseña pendientes ya no deben servir.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM account_tokens WHERE user_id = ? AND purpose IN ('setup', 'reset') AND used_at IS NULL`, u.ID)
	return map[string]any{"mail_sent": false, "purpose": "temp", "temp_password": pw}, nil
}

// POST /api/auth/password {current_password, new_password}: el usuario cambia
// su contraseña (obligatorio si tiene una temporal).
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(r)
	if !s.limiter.allow("password:"+strconv.FormatInt(u.ID, 10), 10, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "demasiados intentos; espera unos minutos")
		return
	}
	var hash string
	if err := s.db.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		internalError(w, err)
		return
	}
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Current)) != nil {
		writeError(w, http.StatusBadRequest, "la contraseña actual no es correcta")
		return
	}
	if !validPassword(in.New) {
		writeError(w, http.StatusBadRequest, "contraseña nueva: entre 8 y 72 caracteres")
		return
	}
	if in.New == in.Current {
		writeError(w, http.StatusBadRequest, "la contraseña nueva debe ser distinta de la actual")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(in.New), bcrypt.DefaultCost)
	if err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?`, string(newHash), u.ID); err != nil {
		internalError(w, err)
		return
	}
	nu, _ := s.userByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, nu)
}
