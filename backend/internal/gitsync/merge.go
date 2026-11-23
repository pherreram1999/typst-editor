package gitsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Fusión con conflictos al hacer pull. Pull aborta la fusión y devuelve un
// *ConflictError con, para cada archivo en conflicto, sus tres versiones
// (base común, la local y la del remoto). El commit remoto queda guardado en
// pullRef para que Resolve pueda rehacer la fusión con las resoluciones del
// usuario aunque FETCH_HEAD cambie.

const pullRef = "refs/typsteditor/pull"

// maxMergeText: archivos más grandes se resuelven eligiendo un lado entero.
const maxMergeText = 1 << 20

// ConflictFile es un archivo en conflicto. Base/Ours/Theirs son nil si el
// archivo no existe en esa versión (p. ej. borrado en un lado y modificado en
// el otro). Binary: no es texto editable (o es muy grande): solo se puede
// elegir una versión entera. OursBlob identifica la versión local con la que
// se resolvió: si cambia antes de aplicar la resolución, hay que repetir.
type ConflictFile struct {
	Path     string  `json:"path"`
	Base     *string `json:"base"`
	Ours     *string `json:"ours"`
	Theirs   *string `json:"theirs"`
	Binary   bool    `json:"binary"`
	HasBase  bool    `json:"has_base"`
	HasOurs  bool    `json:"has_ours"`
	HasTheir bool    `json:"has_theirs"`
	OursBlob string  `json:"ours_blob"`
}

type ConflictError struct {
	Branch string         `json:"branch"`
	Theirs string         `json:"theirs"` // commit remoto (sha)
	Files  []ConflictFile `json:"files"`
}

func (e *ConflictError) Error() string {
	names := make([]string, len(e.Files))
	for i, f := range e.Files {
		names[i] = f.Path
	}
	return "conflicto al fusionar con el remoto en: " + strings.Join(names, ", ")
}

// Resolution es la versión elegida para un archivo en conflicto: Content
// (texto final) o Delete; Take = "ours" | "theirs" toma una versión entera
// (archivos binarios).
type Resolution struct {
	Path     string  `json:"path"`
	OursBlob string  `json:"ours_blob"`
	Content  *string `json:"content"`
	Delete   bool    `json:"delete"`
	Take     string  `json:"take"`
}

// raw ejecuta git y devuelve la salida estándar sin recortar (contenido de blobs).
func (g *Git) raw(ctx context.Context, dir string, args ...string) ([]byte, error) {
	base := []string{"-c", "core.quotepath=off", "-c", "core.hooksPath=/dev/null"}
	cmd := exec.CommandContext(ctx, g.Bin, append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// unmerged lee el índice en conflicto: ruta -> etapa (1 base, 2 local, 3 remoto) -> blob.
func (g *Git) unmerged(ctx context.Context, dir string) (map[string]map[int]string, []string, error) {
	out, err := g.raw(ctx, dir, "ls-files", "-u", "-z")
	if err != nil {
		return nil, nil, err
	}
	m := map[string]map[int]string{}
	var order []string
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			continue
		}
		stage := int(f[2][0] - '0')
		if m[p] == nil {
			m[p] = map[int]string{}
			order = append(order, p)
		}
		m[p][stage] = f[1]
	}
	return m, order, nil
}

func isText(b []byte) bool {
	return len(b) <= maxMergeText && utf8.Valid(b) && !bytes.ContainsRune(b, 0)
}

// collectConflicts se llama con la fusión detenida por conflictos.
func (g *Git) collectConflicts(ctx context.Context, dir, branch string) (*ConflictError, error) {
	stages, order, err := g.unmerged(ctx, dir)
	if err != nil || len(order) == 0 {
		return nil, err
	}
	theirs, err := g.cmd(ctx, dir, nil, nil, "rev-parse", "MERGE_HEAD")
	if err != nil {
		return nil, err
	}
	if _, err := g.cmd(ctx, dir, nil, nil, "update-ref", pullRef, theirs); err != nil {
		return nil, err
	}
	ce := &ConflictError{Branch: branch, Theirs: theirs}
	for _, p := range order {
		st := stages[p]
		cf := ConflictFile{Path: p, OursBlob: st[2], HasBase: st[1] != "", HasOurs: st[2] != "", HasTheir: st[3] != ""}
		var vals [4][]byte
		for i := 1; i <= 3; i++ {
			if st[i] == "" {
				continue
			}
			b, err := g.raw(ctx, dir, "cat-file", "blob", st[i])
			if err != nil {
				return nil, err
			}
			vals[i] = b
			if !isText(b) {
				cf.Binary = true
			}
		}
		if !cf.Binary {
			str := func(i int) *string {
				if st[i] == "" {
					return nil
				}
				s := string(vals[i])
				return &s
			}
			cf.Base, cf.Ours, cf.Theirs = str(1), str(2), str(3)
		}
		ce.Files = append(ce.Files, cf)
	}
	return ce, nil
}

// Resolve completa un pull con conflictos: confirma los cambios locales
// nuevos, vuelve a fusionar el commit remoto guardado y aplica las
// resoluciones. Si aparece un conflicto sin resolver o la versión local de un
// archivo cambió desde que se resolvió, cancela la fusión y lo indica.
func (g *Git) Resolve(ctx context.Context, dir string, a *Author, theirs string, res []Resolution) (string, error) {
	ctx, cancel := ctxT(ctx)
	defer cancel()
	saved, err := g.cmd(ctx, dir, nil, nil, "rev-parse", "--verify", "-q", pullRef)
	if err != nil || saved != theirs {
		return "", &UserError{Msg: "la fusión pendiente ya no es válida; vuelve a hacer pull"}
	}
	if _, err := g.commitAll(ctx, dir, a, "Cambios locales antes de fusionar (Typst Extend)"); err != nil {
		return "", err
	}
	byPath := map[string]Resolution{}
	for _, r := range res {
		byPath[r.Path] = r
	}
	_, mergeErr := g.cmd(ctx, dir, nil, a, "merge", "--no-ff", "--no-commit", "--allow-unrelated-histories", theirs)
	abort := func(e error) (string, error) {
		_, _ = g.cmd(ctx, dir, nil, nil, "merge", "--abort")
		return "", e
	}
	stages, order, err := g.unmerged(ctx, dir)
	if err != nil {
		return abort(err)
	}
	if mergeErr != nil && len(order) == 0 {
		if strings.Contains(mergeErr.Error(), "Already up to date") {
			return "Ya estaba actualizado", nil
		}
		return abort(mergeErr)
	}
	for _, p := range order {
		r, ok := byPath[p]
		if !ok {
			return abort(&UserError{Msg: "hay un conflicto nuevo en «" + p + "»; vuelve a hacer pull"})
		}
		if r.OursBlob != stages[p][2] {
			return abort(&UserError{Msg: "«" + p + "» cambió mientras resolvías el conflicto; vuelve a hacer pull"})
		}
		if err := g.applyResolution(ctx, dir, p, r, stages[p]); err != nil {
			return abort(err)
		}
	}
	if _, err := g.cmd(ctx, dir, nil, a, "commit", "-q", "--no-edit"); err != nil {
		return abort(err)
	}
	_, _ = g.cmd(ctx, dir, nil, nil, "update-ref", "-d", pullRef)
	return "Fusión completada: cambios del remoto incorporados", nil
}

func (g *Git) applyResolution(ctx context.Context, dir, p string, r Resolution, st map[int]string) error {
	clean := path.Clean(p)
	if clean != p || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, ".git/") {
		return &UserError{Msg: "ruta inválida en la fusión: " + p}
	}
	abs := filepath.Join(dir, filepath.FromSlash(p))
	switch {
	case r.Take == "ours" || r.Take == "theirs":
		stage := map[string]int{"ours": 2, "theirs": 3}[r.Take]
		if st[stage] == "" { // esa versión no existe: borrar
			return g.removePath(ctx, dir, p, abs)
		}
		b, err := g.raw(ctx, dir, "cat-file", "blob", st[stage])
		if err != nil {
			return err
		}
		return g.writePath(ctx, dir, p, abs, b)
	case r.Delete:
		return g.removePath(ctx, dir, p, abs)
	case r.Content != nil:
		return g.writePath(ctx, dir, p, abs, []byte(*r.Content))
	}
	return &UserError{Msg: "falta la resolución de «" + p + "»"}
}

func (g *Git) writePath(ctx context.Context, dir, p, abs string, b []byte) error {
	if fi, err := os.Lstat(abs); err == nil && !fi.Mode().IsRegular() {
		return &UserError{Msg: "«" + p + "» no es un archivo normal"}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, b, 0o644); err != nil {
		return err
	}
	_, err := g.cmd(ctx, dir, nil, nil, "add", "--", p)
	return err
}

func (g *Git) removePath(ctx context.Context, dir, p, abs string) error {
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := g.cmd(ctx, dir, nil, nil, "rm", "-q", "--cached", "--ignore-unmatch", "--", p)
	return err
}
