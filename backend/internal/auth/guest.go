package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// URL firmadas (enlaces de invitado, de cuenta y de invitación por correo) y
// sesiones de invitado.
//
// Un enlace es «<id>.<caducidad unix>.<nonce>.<firma>», con la firma
// HMAC-SHA256 de los tres primeros campos con una clave derivada de
// JWT_SECRET. La firma se comprueba antes de consultar la BD (un enlace
// manipulado o inventado se rechaza sin más) y la caducidad va dentro de lo
// firmado, así que no se puede alargar editando la URL. La fila de la BD
// (con el mismo nonce) permite además revocarlo.

// ErrInvite: enlace firmado mal formado, con firma inválida o caducado.
var ErrInvite = errors.New("enlace de invitación inválido o caducado")

const guestAudience = "typsteditor-guest"

// Propósitos de las URL firmadas: cada uno tiene su propia clave, así que un
// token de un tipo nunca vale como otro.
const (
	PurposeInvite  = "invite"  // enlace de invitado (httpapi/invites.go)
	PurposeAccount = "account" // definir o restablecer la contraseña, confirmar un correo
	PurposeJoin    = "join"    // invitación a un proyecto enviada por correo
)

func (i *Issuer) signedKey(purpose string) []byte {
	m := hmac.New(sha256.New, i.secret)
	m.Write([]byte("typsteditor/" + purpose + "/v1"))
	return m.Sum(nil)
}

func (i *Issuer) signature(purpose, payload string) string {
	m := hmac.New(sha256.New, i.signedKey(purpose))
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SignURLToken devuelve «<id>.<caducidad unix>.<nonce>.<firma>» para purpose.
func (i *Issuer) SignURLToken(purpose string, id int64, nonce string, exp time.Time) string {
	payload := strconv.FormatInt(id, 10) + "." + strconv.FormatInt(exp.Unix(), 10) + "." + nonce
	return payload + "." + i.signature(purpose, payload)
}

// ParseURLToken comprueba la firma (de purpose) y la caducidad, y devuelve el
// id y el nonce, que el llamador compara con los de la BD.
func (i *Issuer) ParseURLToken(purpose, token string, now time.Time) (id int64, nonce string, exp time.Time, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || len(token) > 200 {
		return 0, "", time.Time{}, ErrInvite
	}
	payload := parts[0] + "." + parts[1] + "." + parts[2]
	if !hmac.Equal([]byte(parts[3]), []byte(i.signature(purpose, payload))) {
		return 0, "", time.Time{}, ErrInvite
	}
	id, err1 := strconv.ParseInt(parts[0], 10, 64)
	unix, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || id <= 0 {
		return 0, "", time.Time{}, ErrInvite
	}
	exp = time.Unix(unix, 0)
	if !now.Before(exp) {
		return 0, "", time.Time{}, ErrInvite
	}
	return id, parts[2], exp, nil
}

// SignInvite devuelve el token firmado de un enlace de invitación.
func (i *Issuer) SignInvite(id int64, nonce string, exp time.Time) string {
	return i.SignURLToken(PurposeInvite, id, nonce, exp)
}

// ParseInvite valida un enlace de invitación (ver ParseURLToken).
func (i *Issuer) ParseInvite(token string, now time.Time) (int64, string, time.Time, error) {
	return i.ParseURLToken(PurposeInvite, token, now)
}

// Guest es la identidad de una sesión de invitado.
type Guest struct {
	ID        int64  // negativo, distinto por sesión (presencia y colores del editor)
	InviteID  int64  // enlace con el que entró (se revalida en cada petición)
	ProjectID int64  // único proyecto al que tiene acceso
	Name      string // nombre elegido o aleatorio
}

type guestClaims struct {
	Invite  int64  `json:"inv"`
	Project int64  `json:"prj"`
	Name    string `json:"name"`
	jwt.RegisteredClaims
}

// IssueGuest firma el token de una sesión de invitado, válido hasta exp.
// Lleva otra audiencia y un sujeto no numérico: Parse (sesiones de usuario)
// nunca lo acepta, ni ParseGuest acepta un token de usuario.
func (i *Issuer) IssueGuest(g Guest, exp time.Time) (string, error) {
	c := guestClaims{
		Invite:  g.InviteID,
		Project: g.ProjectID,
		Name:    g.Name,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "guest:" + strconv.FormatInt(-g.ID, 10),
			Audience:  jwt.ClaimStrings{guestAudience},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "typsteditor",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
}

// ParseGuest valida el token de una sesión de invitado.
func (i *Issuer) ParseGuest(token string) (*Guest, error) {
	c := &guestClaims{}
	_, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (any, error) {
		return i.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("typsteditor"),
		jwt.WithAudience(guestAudience), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	n, ok := strings.CutPrefix(c.Subject, "guest:")
	gid, err := strconv.ParseInt(n, 10, 64)
	if !ok || err != nil || gid <= 0 || c.Invite <= 0 || c.Project <= 0 || c.Name == "" {
		return nil, errors.New("sesión de invitado inválida")
	}
	return &Guest{ID: -gid, InviteID: c.Invite, ProjectID: c.Project, Name: c.Name}, nil
}
