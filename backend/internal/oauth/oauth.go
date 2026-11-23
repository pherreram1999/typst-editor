// Package oauth implementa el servidor de autorización OAuth 2.1 mínimo que
// protege el endpoint MCP: clientes públicos registrados dinámicamente (RFC
// 7591), códigos de autorización con PKCE S256, tokens de acceso y de
// refresco rotativos, y tokens personales para uso local. Los tokens son
// opacos y en la base de datos solo se guarda su SHA-256.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
)

// Scopes del recurso MCP. Escribir implica poder leer.
const (
	ScopeRead  = "typst:read"
	ScopeWrite = "typst:write"
)

// AllScopes son los scopes que anuncia el servidor.
var AllScopes = []string{ScopeRead, ScopeWrite}

// Prefijos de los tokens (ayudan a reconocerlos y a detectarlos si se filtran).
const (
	PrefixPersonal = "tse_"
	PrefixAccess   = "tsa_"
	PrefixRefresh  = "tsr_"
	prefixCode     = "tsc_"
)

// NewToken genera un token aleatorio de 32 bytes con el prefijo dado.
func NewToken(prefix string) string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return prefix + base64.RawURLEncoding.EncodeToString(b[:])
}

// Hash es lo que se guarda de un token o código.
func Hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// NewClientID genera el identificador de un cliente registrado.
func NewClientID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "tsclient_" + hex.EncodeToString(b[:])
}

// VerifyPKCE comprueba code_verifier contra code_challenge (método S256).
func VerifyPKCE(verifier, challenge string) bool {
	// RFC 7636 §4.1: 43–128 caracteres no reservados.
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, r := range verifier {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// ValidChallenge comprueba el formato de un code_challenge S256.
func ValidChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

// ValidRedirectURI acepta URIs https y, para clientes locales (Claude Code,
// MCP Inspector…), http en loopback con cualquier puerto. Se rechazan
// fragmentos y esquemas que permitirían ejecutar código (javascript:, data:).
// También se aceptan esquemas propios de aplicaciones nativas (p. ej.
// cursor://), que el sistema operativo entrega a la aplicación instalada.
func ValidRedirectURI(raw string) error {
	if len(raw) > 2048 {
		return errors.New("redirect_uri demasiado larga")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Fragment != "" || u.User != nil {
		return errors.New("redirect_uri inválida")
	}
	switch u.Scheme {
	case "https":
		if u.Host == "" {
			return errors.New("redirect_uri inválida")
		}
		return nil
	case "http":
		if IsLoopback(u.Hostname()) {
			return nil
		}
		return errors.New("redirect_uri http solo se permite en localhost")
	case "javascript", "data", "file", "vbscript", "blob", "about", "ftp", "ws", "wss":
		return errors.New("redirect_uri con esquema no permitido")
	}
	// Esquema privado de una aplicación nativa (RFC 8252 §7.1): debe llevar un punto
	// o ser un nombre razonable.
	for _, r := range u.Scheme {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '+' || r == '-') {
			return errors.New("redirect_uri inválida")
		}
	}
	return nil
}

// IsLoopback indica si host es localhost o una IP de loopback.
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RedirectMatches compara una redirect_uri pedida con las registradas. Para
// loopback http el puerto puede variar (RFC 8252 §7.3).
func RedirectMatches(registered []string, got string) bool {
	if slices.Contains(registered, got) {
		return true
	}
	g, err := url.Parse(got)
	if err != nil || g.Scheme != "http" || !IsLoopback(g.Hostname()) {
		return false
	}
	for _, r := range registered {
		u, err := url.Parse(r)
		if err == nil && u.Scheme == "http" && u.Hostname() == g.Hostname() && u.Path == g.Path && u.RawQuery == g.RawQuery {
			return true
		}
	}
	return false
}

// ParseScopes normaliza una lista de scopes separada por espacios: descarta
// los desconocidos y añade lectura si se pide escritura. Vacío = todos.
func ParseScopes(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return slices.Clone(AllScopes)
	}
	var out []string
	if slices.Contains(f, ScopeRead) || slices.Contains(f, ScopeWrite) {
		out = append(out, ScopeRead)
	}
	if slices.Contains(f, ScopeWrite) {
		out = append(out, ScopeWrite)
	}
	return out
}

// Intersect devuelve los scopes de want que también están en allowed.
func Intersect(want, allowed []string) []string {
	var out []string
	for _, s := range want {
		if slices.Contains(allowed, s) {
			out = append(out, s)
		}
	}
	return out
}
