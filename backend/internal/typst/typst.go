// Package typst invoca el binario typst del sistema.
package typst

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const timeout = 90 * time.Second

type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// CompileError contiene la salida de typst cuando la compilación falla.
type CompileError struct {
	Output      string
	Diagnostics []Diagnostic
}

func (e *CompileError) Error() string { return "error de compilación" }

type Compiler struct{ Bin string }

// "main.typ:3:5: error: unknown variable: x"
var diagRe = regexp.MustCompile(`^(.+?):(\d+):(\d+): (error|warning): (.*)$`)

func parseDiagnostics(out string) []Diagnostic {
	var ds []Diagnostic
	for _, line := range strings.Split(out, "\n") {
		m := diagRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		l, _ := strconv.Atoi(m[2])
		c, _ := strconv.Atoi(m[3])
		ds = append(ds, Diagnostic{File: filepath.ToSlash(m[1]), Line: l, Column: c, Severity: m[4], Message: m[5]})
	}
	return ds
}

func (c *Compiler) run(ctx context.Context, root, main, output string, extra ...string) ([]Diagnostic, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string{"compile", "--root", root, "--diagnostic-format", "short"}, extra...)
	args = append(args, filepath.Join(root, filepath.FromSlash(main)), output)
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	// Las rutas de los diagnósticos se muestran relativas al proyecto.
	text := strings.ReplaceAll(out.String(), root+string(filepath.Separator), "")
	diags := parseDiagnostics(text)
	if ctx.Err() == context.DeadlineExceeded {
		return diags, &CompileError{Output: "tiempo de compilación agotado", Diagnostics: diags}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return diags, &CompileError{Output: strings.TrimSpace(text), Diagnostics: diags}
	}
	return diags, err
}

// PDF compila el proyecto a un PDF en outDir/document.pdf.
func (c *Compiler) PDF(ctx context.Context, root, main, outDir string) (string, []Diagnostic, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", nil, err
	}
	out := filepath.Join(outDir, "document.pdf")
	diags, err := c.run(ctx, root, main, out)
	return out, diags, err
}

// SVG compila el proyecto a una página SVG por página y devuelve su contenido.
func (c *Compiler) SVG(ctx context.Context, root, main, outDir string) ([]string, []Diagnostic, error) {
	dir := filepath.Join(outDir, "svg")
	if err := os.RemoveAll(dir); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	diags, err := c.run(ctx, root, main, filepath.Join(dir, "page-{0p}.svg"), "--format", "svg")
	if err != nil {
		return nil, diags, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "page-*.svg"))
	if err != nil {
		return nil, diags, err
	}
	sort.Strings(files) // {0p} rellena con ceros: el orden léxico es el numérico
	pages := make([]string, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, diags, err
		}
		pages = append(pages, string(b))
	}
	return pages, diags, nil
}

// Thumbnail renderiza la primera página como PNG en outDir/thumb.png.
func (c *Compiler) Thumbnail(ctx context.Context, root, main, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, "thumb.png")
	_, err := c.run(ctx, root, main, out, "--format", "png", "--pages", "1", "--ppi", "48")
	return out, err
}
