package mail

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizeEmail(t *testing.T) {
	ok := map[string]string{
		"Ana@Ejemplo.com ":     "ana@ejemplo.com",
		"a.b+c@sub.dominio.mx": "a.b+c@sub.dominio.mx",
	}
	for in, want := range ok {
		if got, valid := NormalizeEmail(in); !valid || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v", in, got, valid)
		}
	}
	for _, in := range []string{"", "ana", "ana@", "@x.com", "ana@localhost", "Ana <ana@x.com>", "a b@x.com", "ana@x.com."} {
		if _, valid := NormalizeEmail(in); valid {
			t.Errorf("NormalizeEmail(%q) aceptado", in)
		}
	}
}

func TestTemplatesEscape(t *testing.T) {
	m := ProjectShared("a@b.co", "<b>Ana</b>", "Beto & Co", `Tesis "<script>"`, "https://x/#/p/1?a=1&b=2")
	if strings.Contains(m.HTML, "<script>") || strings.Contains(m.HTML, "<b>Ana") {
		t.Fatal("contenido sin escapar en el HTML")
	}
	if !strings.Contains(m.HTML, "a=1&amp;b=2") || !strings.Contains(m.Text, "https://x/#/p/1?a=1&b=2") {
		t.Fatal("enlace mal escrito")
	}
	w := Welcome("a@b.co", "Ana", "ana", "", "https://x/#/clave/t", time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC))
	if !strings.Contains(w.Text, "1 de diciembre de 2026") {
		t.Fatalf("fecha: %s", w.Text)
	}
}

// TestLiveSend envía de verdad un correo de cada tipo con Resend. Solo corre con
//
//	RESEND=re_… TYPSTEDITOR_MAIL_TO=destino@correo go test ./internal/mail -run Live -v
//
// (MAIL_FROM opcional; por defecto el remitente de pruebas de Resend).
func TestLiveSend(t *testing.T) {
	key, to := os.Getenv("RESEND"), os.Getenv("TYPSTEDITOR_MAIL_TO")
	if key == "" || to == "" {
		t.Skip("define RESEND y TYPSTEDITOR_MAIL_TO para enviar correos de prueba")
	}
	from := os.Getenv("MAIL_FROM")
	if from == "" {
		from = "Typst Extend <onboarding@resend.dev>"
	}
	m := New(key, from)
	exp := time.Now().Add(7 * 24 * time.Hour)
	for _, msg := range []Message{
		Welcome(to, "Prueba", "prueba", "Administrador", "https://ejemplo.com/#/cuenta/prueba", exp),
		ProjectInvite(to, "Administrador", "Tesis de prueba", "https://ejemplo.com/#/unirse/prueba", exp),
		ProjectShared(to, "Prueba", "Administrador", "Tesis de prueba", "https://ejemplo.com/#/p/1"),
	} {
		if err := m.Send(context.Background(), msg); err != nil {
			t.Fatalf("%s: %v", msg.Subject, err)
		}
	}
}
