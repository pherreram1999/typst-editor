package auth

import (
	"strings"
	"testing"
	"time"
)

func TestInviteSignature(t *testing.T) {
	iss := NewIssuer([]byte(strings.Repeat("s", 40)), time.Hour)
	now := time.Now()
	exp := now.Add(48 * time.Hour).Truncate(time.Second)
	tok := iss.SignInvite(42, "abcDEF_-123", exp)

	id, nonce, gotExp, err := iss.ParseInvite(tok, now)
	if err != nil || id != 42 || nonce != "abcDEF_-123" || !gotExp.Equal(exp) {
		t.Fatalf("ParseInvite = %d %q %v %v", id, nonce, gotExp, err)
	}
	// Caducado.
	if _, _, _, err := iss.ParseInvite(tok, exp); err == nil {
		t.Fatal("un enlace caducado no debe validar")
	}
	// Alargar la caducidad o cambiar el id invalida la firma.
	parts := strings.Split(tok, ".")
	for _, bad := range []string{
		parts[0] + "." + "9999999999" + "." + parts[2] + "." + parts[3],
		"43." + parts[1] + "." + parts[2] + "." + parts[3],
		parts[0] + "." + parts[1] + ".otro." + parts[3],
		tok + "x",
		"",
		"1.2.3",
	} {
		if _, _, _, err := iss.ParseInvite(bad, now); err == nil {
			t.Fatalf("enlace manipulado aceptado: %q", bad)
		}
	}
	// El mismo token no vale para otro propósito.
	if _, _, _, err := iss.ParseURLToken(PurposeAccount, tok, now); err == nil {
		t.Fatal("token de invitación aceptado como token de cuenta")
	}
	// Otra clave.
	other := NewIssuer([]byte(strings.Repeat("t", 40)), time.Hour)
	if _, _, _, err := other.ParseInvite(tok, now); err == nil {
		t.Fatal("firma con otra clave aceptada")
	}
}

func TestGuestTokens(t *testing.T) {
	iss := NewIssuer([]byte(strings.Repeat("s", 40)), time.Hour)
	g := Guest{ID: -12345, InviteID: 7, ProjectID: 3, Name: "Colibrí curioso"}
	tok, err := iss.IssueGuest(g, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, err := iss.ParseGuest(tok)
	if err != nil || *got != g {
		t.Fatalf("ParseGuest = %+v, %v", got, err)
	}
	// Un token de invitado no es una sesión de usuario, ni al revés.
	if _, err := iss.Parse(tok); err == nil {
		t.Fatal("Parse aceptó un token de invitado")
	}
	utok, _, _ := iss.Issue(1, "admin")
	if _, err := iss.ParseGuest(utok); err == nil {
		t.Fatal("ParseGuest aceptó un token de usuario")
	}
	// Caducado.
	old, _ := iss.IssueGuest(g, time.Now().Add(-time.Minute))
	if _, err := iss.ParseGuest(old); err == nil {
		t.Fatal("token de invitado caducado aceptado")
	}
}
