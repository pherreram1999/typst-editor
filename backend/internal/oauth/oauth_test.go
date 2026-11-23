package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
)

func TestVerifyPKCE(t *testing.T) {
	verifier := strings.Repeat("a1-._~", 8) // 48 caracteres válidos
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if !ValidChallenge(challenge) {
		t.Fatal("challenge válido rechazado")
	}
	if !VerifyPKCE(verifier, challenge) {
		t.Fatal("PKCE correcto rechazado")
	}
	if VerifyPKCE(verifier+"x", challenge) {
		t.Fatal("verifier distinto aceptado")
	}
	if VerifyPKCE("corto", challenge) {
		t.Fatal("verifier demasiado corto aceptado")
	}
	if VerifyPKCE(strings.Repeat("a", 42)+"!", challenge) {
		t.Fatal("verifier con caracteres inválidos aceptado")
	}
	if ValidChallenge("plain") {
		t.Fatal("challenge inválido aceptado")
	}
}

func TestValidRedirectURI(t *testing.T) {
	ok := []string{
		"https://claude.ai/api/mcp/auth_callback",
		"https://chatgpt.com/connector_platform_oauth_redirect",
		"http://localhost:6274/oauth/callback",
		"http://127.0.0.1:33418/callback",
		"http://[::1]:8000/cb",
		"cursor://anysphere.cursor-retrieval/oauth/callback",
	}
	bad := []string{
		"http://example.com/cb",
		"https://claude.ai/cb#frag",
		"javascript:alert(1)",
		"data:text/html,hola",
		"https://user:pass@evil.com/",
		"no es una url",
		"",
	}
	for _, u := range ok {
		if err := ValidRedirectURI(u); err != nil {
			t.Errorf("%q rechazada: %v", u, err)
		}
	}
	for _, u := range bad {
		if ValidRedirectURI(u) == nil {
			t.Errorf("%q aceptada", u)
		}
	}
}

func TestRedirectMatches(t *testing.T) {
	reg := []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:3000/callback"}
	if !RedirectMatches(reg, "https://claude.ai/api/mcp/auth_callback") {
		t.Error("coincidencia exacta rechazada")
	}
	if !RedirectMatches(reg, "http://localhost:51234/callback") {
		t.Error("loopback con otro puerto rechazado")
	}
	if RedirectMatches(reg, "http://localhost:51234/otro") {
		t.Error("loopback con otra ruta aceptado")
	}
	if RedirectMatches(reg, "https://claude.ai/api/mcp/auth_callback2") {
		t.Error("https distinta aceptada")
	}
}

func TestParseScopes(t *testing.T) {
	cases := map[string][]string{
		"":                        {ScopeRead, ScopeWrite},
		"typst:read":              {ScopeRead},
		"typst:write":             {ScopeRead, ScopeWrite},
		"openid typst:read otros": {ScopeRead},
		"openid":                  nil,
	}
	for in, want := range cases {
		if got := ParseScopes(in); !slices.Equal(got, want) {
			t.Errorf("ParseScopes(%q) = %v; quiero %v", in, got, want)
		}
	}
}

func TestTokens(t *testing.T) {
	a, b := NewToken(PrefixAccess), NewToken(PrefixAccess)
	if a == b || !strings.HasPrefix(a, PrefixAccess) || len(a) != len(PrefixAccess)+43 {
		t.Fatalf("tokens: %q %q", a, b)
	}
	if Hash(a) == Hash(b) || len(Hash(a)) != 64 {
		t.Fatal("hash")
	}
}
