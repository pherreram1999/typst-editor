// Package gitsync ejecuta el binario git del sistema para sincronizar un
// proyecto con un repositorio remoto (GitHub) por HTTPS usando un token.
// El token nunca se escribe en la URL ni en la configuración del repositorio:
// se pasa en cada llamada como cabecera HTTP.
package gitsync

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const timeout = 2 * time.Minute

type Git struct {
	Bin string
	// allowLocal permite remotos locales (solo en tests).
	allowLocal bool
}

// Remote es la configuración de sincronización de un proyecto.
type Remote struct {
	URL    string
	Branch string
	Token  string
}

// Author identifica a quien hace el commit.
type Author struct{ Name, Email string }

// UserError es un error con un mensaje apto para mostrar al usuario.
type UserError struct {
	Msg string
	// BranchMissing: la rama no existe en el remoto (p. ej. repositorio vacío).
	BranchMissing bool
}

func (e *UserError) Error() string { return e.Msg }

var branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`)

// ValidateRemote comprueba la URL (solo https) y el nombre de rama.
func ValidateRemote(rawURL, branch string) error {
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return &UserError{Msg: "la URL del repositorio debe ser https://… sin credenciales"}
		}
	}
	if !branchRe.MatchString(branch) || strings.Contains(branch, "..") || strings.HasPrefix(branch, "-") ||
		strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".lock") {
		return &UserError{Msg: "nombre de rama inválido"}
	}
	return nil
}

func (g *Git) cmd(ctx context.Context, dir string, r *Remote, a *Author, args ...string) (string, error) {
	// core.symlinks=false: los enlaces simbólicos del remoto se materializan como
	// archivos de texto, así nadie puede apuntar fuera del directorio del proyecto.
	base := []string{"-c", "credential.helper=", "-c", "core.quotepath=off", "-c", "core.symlinks=false",
		"-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always"}
	if g.allowLocal {
		base = append(base, "-c", "protocol.file.allow=always")
	}
	if r != nil && r.Token != "" {
		auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + r.Token))
		base = append(base, "-c", "http.extraHeader=Authorization: Basic "+auth)
	}
	if a != nil {
		base = append(base, "-c", "user.name="+a.Name, "-c", "user.email="+a.Email)
	}
	cmd := exec.CommandContext(ctx, g.Bin, append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if r != nil && r.Token != "" {
		text = strings.ReplaceAll(text, r.Token, "***")
	}
	if err != nil {
		return text, fmt.Errorf("git %s: %w: %s", args[0], err, text)
	}
	return text, nil
}

func ctxT(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

// Init crea un repositorio vacío en dir con la rama indicada.
func (g *Git) Init(ctx context.Context, dir, branch string) error {
	ctx, cancel := ctxT(ctx)
	defer cancel()
	_, err := g.cmd(ctx, dir, nil, nil, "init", "-q", "-b", branch)
	return err
}

// Clone clona el remoto en dir (que no debe existir).
func (g *Git) Clone(ctx context.Context, dir string, r *Remote) error {
	ctx, cancel := ctxT(ctx)
	defer cancel()
	_, err := g.cmd(ctx, "", r, nil, "clone", "-q", "--branch", r.Branch, "--", r.URL, dir)
	if err != nil {
		return friendly(err, r)
	}
	return nil
}

func (g *Git) ensureRemote(ctx context.Context, dir string, r *Remote) error {
	if r.URL == "" {
		return &UserError{Msg: "configura primero la URL del repositorio en los ajustes del proyecto"}
	}
	if _, err := g.cmd(ctx, dir, nil, nil, "remote", "get-url", "origin"); err != nil {
		_, err = g.cmd(ctx, dir, nil, nil, "remote", "add", "origin", r.URL)
		return err
	}
	_, err := g.cmd(ctx, dir, nil, nil, "remote", "set-url", "origin", r.URL)
	return err
}

func (g *Git) hasHead(ctx context.Context, dir string) bool {
	_, err := g.cmd(ctx, dir, nil, nil, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// commitAll hace commit de todos los cambios del árbol de trabajo. Devuelve
// false si no había nada que confirmar.
func (g *Git) commitAll(ctx context.Context, dir string, a *Author, msg string) (bool, error) {
	if _, err := g.cmd(ctx, dir, nil, nil, "add", "-A"); err != nil {
		return false, err
	}
	st, err := g.cmd(ctx, dir, nil, nil, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if st == "" {
		return false, nil
	}
	_, err = g.cmd(ctx, dir, nil, a, "commit", "-q", "-m", msg)
	return err == nil, err
}

// Pull confirma los cambios locales y fusiona la rama remota. Si hay
// conflictos aborta la fusión y lo indica.
func (g *Git) Pull(ctx context.Context, dir string, r *Remote, a *Author) (string, error) {
	ctx, cancel := ctxT(ctx)
	defer cancel()
	if err := g.ensureRemote(ctx, dir, r); err != nil {
		return "", err
	}
	if _, err := g.commitAll(ctx, dir, a, "Cambios locales antes de pull (nullpointer editor)"); err != nil {
		return "", err
	}
	if _, err := g.cmd(ctx, dir, r, nil, "fetch", "-q", "origin", r.Branch); err != nil {
		return "", friendly(err, r)
	}
	if !g.hasHead(ctx, dir) {
		_, err := g.cmd(ctx, dir, nil, nil, "reset", "-q", "--hard", "FETCH_HEAD")
		return "Proyecto actualizado desde el remoto", err
	}
	out, err := g.cmd(ctx, dir, nil, a, "merge", "--no-edit", "--allow-unrelated-histories", "FETCH_HEAD")
	if err != nil {
		// Con conflictos se recogen las tres versiones de cada archivo y se
		// aborta: el árbol queda limpio (los demás siguen editando) y el
		// usuario resuelve en el editor de fusión; luego Resolve rehace la fusión.
		ce, cerr := g.collectConflicts(ctx, dir, r.Branch)
		_, _ = g.cmd(ctx, dir, nil, nil, "merge", "--abort")
		if cerr != nil {
			return "", cerr
		}
		if ce != nil {
			return "", ce
		}
		return "", err
	}
	if strings.Contains(out, "Already up to date") {
		return "Ya estaba actualizado", nil
	}
	return "Cambios del remoto incorporados", nil
}

// Push confirma los cambios locales y los envía a la rama remota.
func (g *Git) Push(ctx context.Context, dir string, r *Remote, a *Author, msg string) (string, error) {
	ctx, cancel := ctxT(ctx)
	defer cancel()
	if err := g.ensureRemote(ctx, dir, r); err != nil {
		return "", err
	}
	committed, err := g.commitAll(ctx, dir, a, msg)
	if err != nil {
		return "", err
	}
	if !g.hasHead(ctx, dir) {
		return "", &UserError{Msg: "no hay nada que enviar todavía"}
	}
	if _, err := g.cmd(ctx, dir, r, nil, "push", "-q", "origin", "HEAD:refs/heads/"+r.Branch); err != nil {
		return "", friendly(err, r)
	}
	if committed {
		return "Commit creado y enviado a " + r.Branch, nil
	}
	return "Enviado a " + r.Branch + " (sin cambios nuevos que confirmar)", nil
}

type Status struct {
	Changes    []string `json:"changes"`
	LastCommit string   `json:"last_commit"`
	Ahead      int      `json:"ahead"`
	Behind     int      `json:"behind"`
	Tracking   bool     `json:"tracking"`
}

// GetStatus resume el estado local (no contacta con el remoto).
func (g *Git) GetStatus(ctx context.Context, dir, branch string) (*Status, error) {
	ctx, cancel := ctxT(ctx)
	defer cancel()
	st := &Status{Changes: []string{}}
	out, err := g.cmd(ctx, dir, nil, nil, "status", "--porcelain", "-uall")
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			st.Changes = append(st.Changes, l)
		}
	}
	if g.hasHead(ctx, dir) {
		st.LastCommit, _ = g.cmd(ctx, dir, nil, nil, "log", "-1", "--format=%h %s (%an, %cr)")
		if c, err := g.cmd(ctx, dir, nil, nil, "rev-list", "--left-right", "--count", "HEAD...refs/remotes/origin/"+branch); err == nil {
			if f := strings.Fields(c); len(f) == 2 {
				st.Ahead, _ = strconv.Atoi(f[0])
				st.Behind, _ = strconv.Atoi(f[1])
				st.Tracking = true
			}
		}
	}
	return st, nil
}

// friendly traduce errores comunes de red/autenticación.
func friendly(err error, r *Remote) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "couldn't find remote ref") || strings.Contains(msg, "Remote branch") && strings.Contains(msg, "not found"):
		return &UserError{Msg: "la rama «" + r.Branch + "» no existe en el remoto (haz push para crearla)", BranchMissing: true}
	case strings.Contains(msg, "Authentication failed") || strings.Contains(msg, "could not read Username") ||
		strings.Contains(msg, "403") || strings.Contains(msg, "401"):
		return &UserError{Msg: "autenticación rechazada por el remoto: revisa el token (necesita permiso de escritura en «Contents»)"}
	case strings.Contains(msg, "Repository not found") || strings.Contains(msg, "not found"):
		return &UserError{Msg: "repositorio no encontrado (o el token no tiene acceso)"}
	case strings.Contains(msg, "[rejected]") || strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "fetch first"):
		return &UserError{Msg: "el remoto tiene commits que no tienes: haz pull antes de push"}
	case strings.Contains(msg, "Could not resolve host") || strings.Contains(msg, "unable to access"):
		return &UserError{Msg: "no se pudo conectar con el remoto"}
	}
	var ue *UserError
	if errors.As(err, &ue) {
		return err
	}
	return err
}
