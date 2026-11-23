// Package mail envía los correos de la aplicación con Resend
// (github.com/resend/resend-go): alta de cuentas, restablecer la contraseña,
// confirmar un correo, invitaciones y avisos de proyectos compartidos.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"time"

	"github.com/resend/resend-go/v4"
)

// ErrDisabled: no hay clave de Resend (RESEND / RESEND_API_KEY).
var ErrDisabled = errors.New("el envío de correos no está configurado")

// Message es un correo listo para enviar (HTML y texto plano).
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
	Tag     string // categoría para las estadísticas de Resend (p. ej. "alta")
}

// Sender envía correos; Mailer es la implementación con Resend.
type Sender interface {
	Enabled() bool
	Send(ctx context.Context, m Message) error
}

type Mailer struct {
	client *resend.Client
	from   string
}

// New crea el cliente. Sin clave, Enabled() es false y Send devuelve ErrDisabled.
func New(apiKey, from string) *Mailer {
	m := &Mailer{from: from}
	if apiKey != "" {
		m.client = resend.NewClient(apiKey)
	}
	return m
}

func (m *Mailer) Enabled() bool { return m != nil && m.client != nil }

func (m *Mailer) Send(ctx context.Context, msg Message) error {
	if !m.Enabled() {
		return ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req := &resend.SendEmailRequest{
		From:    m.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		Html:    msg.HTML,
		Text:    msg.Text,
	}
	if msg.Tag != "" {
		req.Tags = []resend.Tag{{Name: "tipo", Value: msg.Tag}}
	}
	res, err := m.client.Emails.SendWithContext(ctx, req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	log.Printf("correo «%s» enviado a %s (id %s)", msg.Subject, msg.To, res.Id)
	return nil
}

// NormalizeEmail valida una dirección («Nombre <a@b.c>» no: solo la
// dirección) y la devuelve en minúsculas.
func NormalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " <>\"'") {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", false
	}
	at := strings.LastIndexByte(s, '@')
	if at < 1 || !strings.Contains(s[at+1:], ".") || strings.HasSuffix(s, ".") {
		return "", false
	}
	return s, true
}
