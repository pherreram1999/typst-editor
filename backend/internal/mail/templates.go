package mail

import (
	"html"
	"strings"
	"time"
)

// Plantillas de los correos. Todo el contenido variable (nombres, proyectos)
// se escapa: lo escriben los usuarios.

type layout struct {
	Preheader string   // resumen que muestran los clientes de correo junto al asunto
	Title     string   // encabezado
	Paras     []string // párrafos (texto plano; se escapan)
	Button    string   // texto del botón
	URL       string   // destino del botón
	Note      string   // nota final en gris (texto plano)
}

func render(l layout) (htmlOut, text string) {
	var b strings.Builder
	e := html.EscapeString
	b.WriteString(`<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`)
	b.WriteString(e(l.Title))
	b.WriteString(`</title></head><body style="margin:0;padding:0;background:#f1f5f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#0f172a">`)
	b.WriteString(`<div style="display:none;max-height:0;overflow:hidden;opacity:0">` + e(l.Preheader) + `</div>`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f1f5f9;padding:32px 12px"><tr><td align="center">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px">`)
	b.WriteString(`<tr><td style="padding:0 4px 16px;font-size:17px;font-weight:700;color:#1e3a8a">nullpointer editor</td></tr>`)
	b.WriteString(`<tr><td style="background:#ffffff;border:1px solid #e2e8f0;border-radius:12px;padding:32px 28px">`)
	b.WriteString(`<h1 style="margin:0 0 16px;font-size:21px;line-height:1.3;color:#0f172a">` + e(l.Title) + `</h1>`)
	for _, p := range l.Paras {
		b.WriteString(`<p style="margin:0 0 14px;font-size:15px;line-height:1.6;color:#334155">` + e(p) + `</p>`)
	}
	if l.URL != "" {
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" style="margin:22px 0"><tr><td style="border-radius:8px;background:#1e40af">`)
		b.WriteString(`<a href="` + e(l.URL) + `" style="display:inline-block;padding:12px 22px;font-size:15px;font-weight:600;color:#ffffff;text-decoration:none;border-radius:8px">` + e(l.Button) + `</a>`)
		b.WriteString(`</td></tr></table>`)
		b.WriteString(`<p style="margin:0 0 6px;font-size:12px;color:#64748b">Si el botón no funciona, copia este enlace en el navegador:</p>`)
		b.WriteString(`<p style="margin:0;font-size:12px;word-break:break-all"><a href="` + e(l.URL) + `" style="color:#1e40af">` + e(l.URL) + `</a></p>`)
	}
	if l.Note != "" {
		b.WriteString(`<p style="margin:22px 0 0;padding-top:16px;border-top:1px solid #e2e8f0;font-size:12px;line-height:1.5;color:#64748b">` + e(l.Note) + `</p>`)
	}
	b.WriteString(`</td></tr></table></td></tr></table></body></html>`)

	var t strings.Builder
	t.WriteString(l.Title + "\n\n")
	for _, p := range l.Paras {
		t.WriteString(p + "\n\n")
	}
	if l.URL != "" {
		t.WriteString(l.Button + ": " + l.URL + "\n\n")
	}
	if l.Note != "" {
		t.WriteString("—\n" + l.Note + "\n")
	}
	return b.String(), t.String()
}

func message(to, subject, tag string, l layout) Message {
	h, t := render(l)
	return Message{To: to, Subject: subject, HTML: h, Text: t, Tag: tag}
}

// FormatDate: fecha legible en español (UTC, sin depender de la zona del servidor).
func FormatDate(t time.Time) string {
	months := []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto",
		"septiembre", "octubre", "noviembre", "diciembre"}
	t = t.UTC()
	return t.Format("2") + " de " + months[t.Month()-1] + " de " + t.Format("2006, 15:04") + " (UTC)"
}

// Welcome: cuenta nueva creada por un administrador; el enlace define la contraseña.
func Welcome(to, name, username, inviter, url string, exp time.Time) Message {
	paras := []string{"Hola, " + name + "."}
	if inviter != "" {
		paras = append(paras, inviter+" te creó una cuenta en nullpointer editor, el editor colaborativo de documentos Typst.")
	} else {
		paras = append(paras, "Se creó una cuenta para ti en nullpointer editor, el editor colaborativo de documentos Typst.")
	}
	paras = append(paras, "Tu usuario es «"+username+"». También puedes iniciar sesión con este correo. Para activar la cuenta, define tu contraseña:")
	return message(to, "Activa tu cuenta de nullpointer editor", "alta", layout{
		Preheader: "Define tu contraseña para empezar a usar nullpointer editor.",
		Title:     "Bienvenido a nullpointer editor",
		Paras:     paras,
		Button:    "Definir mi contraseña",
		URL:       url,
		Note:      "El enlace es personal, de un solo uso y vence el " + FormatDate(exp) + ". Si no esperabas este correo, puedes ignorarlo.",
	})
}

// ResetPassword: enlace para elegir una contraseña nueva.
func ResetPassword(to, name, url string, exp time.Time) Message {
	return message(to, "Restablece tu contraseña de nullpointer editor", "clave", layout{
		Preheader: "Elige una contraseña nueva para tu cuenta.",
		Title:     "Restablecer la contraseña",
		Paras: []string{
			"Hola, " + name + ".",
			"Recibimos una solicitud para restablecer la contraseña de tu cuenta. Elige una nueva con este enlace:",
		},
		Button: "Elegir una contraseña nueva",
		URL:    url,
		Note:   "El enlace es de un solo uso y vence el " + FormatDate(exp) + ". Si no lo pediste, ignora este correo: tu contraseña no cambia.",
	})
}

// VerifyEmail: confirmar el correo que un usuario añadió a su cuenta.
func VerifyEmail(to, name, url string, exp time.Time) Message {
	return message(to, "Confirma tu correo en nullpointer editor", "correo", layout{
		Preheader: "Confirma que este correo es tuyo.",
		Title:     "Confirma tu correo",
		Paras: []string{
			"Hola, " + name + ".",
			"Confirma que quieres usar esta dirección en tu cuenta de nullpointer editor. Con ella podrás iniciar sesión, recuperar tu contraseña y recibir avisos de los proyectos que te compartan.",
		},
		Button: "Confirmar mi correo",
		URL:    url,
		Note:   "El enlace vence el " + FormatDate(exp) + ". Si no fuiste tú, ignora este correo.",
	})
}

// ProjectShared: aviso a un usuario registrado al que agregaron a un proyecto.
func ProjectShared(to, name, inviter, project, url string) Message {
	return message(to, inviter+" compartió «"+project+"» contigo", "proyecto", layout{
		Preheader: "Ya puedes editar «" + project + "» en nullpointer editor.",
		Title:     "Te compartieron un proyecto",
		Paras: []string{
			"Hola, " + name + ".",
			inviter + " te agregó como colaborador del proyecto «" + project + "». Ya puedes abrirlo, editarlo y compilarlo junto con el resto del equipo.",
		},
		Button: "Abrir el proyecto",
		URL:    url,
		Note:   "Recibes este aviso porque tienes una cuenta en nullpointer editor con este correo.",
	})
}

// ProjectInvite: invitación a un correo sin cuenta; el enlace crea la cuenta y da acceso.
func ProjectInvite(to, inviter, project, url string, exp time.Time) Message {
	return message(to, inviter+" te invitó a colaborar en «"+project+"»", "invitacion", layout{
		Preheader: "Crea tu cuenta para colaborar en «" + project + "».",
		Title:     "Te invitaron a colaborar",
		Paras: []string{
			"Hola.",
			inviter + " te invitó a colaborar en el proyecto «" + project + "» de nullpointer editor, el editor colaborativo de documentos Typst.",
			"Acepta la invitación para crear tu cuenta (o entrar con la que ya tengas) y abrir el proyecto:",
		},
		Button: "Aceptar la invitación",
		URL:    url,
		Note:   "La invitación es de un solo uso y vence el " + FormatDate(exp) + ". Si no esperabas este correo, puedes ignorarlo.",
	})
}
