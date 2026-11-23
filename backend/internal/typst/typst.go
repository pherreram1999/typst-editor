// Package typst invoca el binario typst del sistema.
package typst

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
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

type Compiler struct {
	Bin   string
	nice  string        // `nice` del sistema (si existe): typst corre con prioridad baja
	slots chan struct{} // procesos typst simultáneos en todo el servidor

	// Carpetas de fuentes adicionales (las mismas de TYPST_FONT_PATHS): si
	// cambia su contenido se rehace el catálogo del navegador.
	FontDirs []string

	fontsMu  sync.Mutex
	fonts    *FontCatalog // ver Fonts
	fontsSig string       // firma de FontDirs con la que se hizo fonts
}

// New prepara el compilador. Cada typst ya usa todos los núcleos, así que
// ejecutar muchos a la vez no acelera nada y multiplica la memoria (un
// documento grande puede necesitar varios GB): se limitan a la mitad de los
// núcleos. Se lanzan con `nice` para que el servidor, el editor y el resto del
// equipo sigan respondiendo mientras compilan.
func New(bin string, fontDirs ...string) *Compiler {
	c := &Compiler{Bin: bin, FontDirs: fontDirs, slots: make(chan struct{}, max(1, runtime.NumCPU()/2))}
	if p, err := exec.LookPath("nice"); err == nil {
		c.nice = p
	}
	return c
}

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
	args := append([]string{"compile", "--root", root, "--diagnostic-format", "short"}, extra...)
	args = append(args, filepath.Join(root, filepath.FromSlash(main)), output)
	_, diags, err := c.exec(ctx, root, args)
	return diags, err
}

// exec ejecuta typst con prioridad baja y dentro del límite de procesos
// simultáneos; devuelve la salida estándar y los diagnósticos (de stderr).
func (c *Compiler) exec(ctx context.Context, root string, args []string) ([]byte, []Diagnostic, error) {
	if c.slots != nil {
		select {
		case c.slots <- struct{}{}:
			defer func() { <-c.slots }()
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if c.nice != "" {
		cmd = exec.CommandContext(ctx, c.nice, append([]string{"-n", "10", c.Bin}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, c.Bin, args...)
	}
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	// Las rutas de los diagnósticos se muestran relativas al proyecto.
	text := strings.ReplaceAll(stdout.String()+stderr.String(), root+string(filepath.Separator), "")
	if args[0] == "eval" {
		text = strings.ReplaceAll(stderr.String(), root+string(filepath.Separator), "")
	}
	diags := parseDiagnostics(text)
	if ctx.Err() == context.DeadlineExceeded {
		return nil, diags, &CompileError{Output: "tiempo de compilación agotado", Diagnostics: diags}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, diags, &CompileError{Output: strings.TrimSpace(text), Diagnostics: diags}
	}
	return stdout.Bytes(), diags, err
}

// PDF compila el proyecto (con las imágenes originales) a outDir/document.pdf.
func (c *Compiler) PDF(ctx context.Context, root, main, outDir string) (string, []Diagnostic, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", nil, err
	}
	out := filepath.Join(outDir, "document.pdf")
	diags, err := c.run(ctx, root, main, out)
	return out, diags, err
}

// ---------- vista previa ----------

// Page es una página de la vista previa. El SVG se guarda en
// outDir/pages/<Hash>.svg: una página que no cambia entre compilaciones
// conserva su hash y el navegador no la vuelve a descargar.
type Page struct {
	Hash   string  `json:"hash"`
	Width  float64 `json:"width"` // pt
	Height float64 `json:"height"`
}

// previewRoot es el espejo del proyecto con imágenes reducidas (mirror.go).
func previewRoot(outDir string) (string, error) {
	return filepath.Abs(filepath.Join(outDir, "preview"))
}

// Preview compila la vista previa: una SVG por página a partir del espejo con
// imágenes reducidas. Devuelve solo los metadatos de cada página.
func (c *Compiler) Preview(ctx context.Context, root, main, outDir string) ([]Page, []Diagnostic, error) {
	mirror, err := previewRoot(outDir)
	if err != nil {
		return nil, nil, err
	}
	if err := syncMirror(root, mirror, main); err != nil {
		return nil, nil, err
	}
	tmp := filepath.Join(outDir, "svg")
	store := filepath.Join(outDir, "pages")
	if err := os.RemoveAll(tmp); err != nil {
		return nil, nil, err
	}
	for _, d := range []string{tmp, store} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, nil, err
		}
	}
	diags, err := c.run(ctx, mirror, main, filepath.Join(tmp, "page-{0p}.svg"), "--format", "svg")
	if err != nil {
		return nil, diags, err
	}
	files, err := filepath.Glob(filepath.Join(tmp, "page-*.svg"))
	if err != nil {
		return nil, diags, err
	}
	sort.Strings(files) // {0p} rellena con ceros: el orden léxico es el numérico
	pages := make([]Page, 0, len(files))
	for _, f := range files {
		pg, err := storePage(f, store)
		if err != nil {
			return nil, diags, err
		}
		pages = append(pages, pg)
	}
	return pages, diags, nil
}

// storePage mueve una página al almacén por contenido.
func storePage(file, store string) (Page, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return Page{}, err
	}
	sum := sha256.Sum256(b)
	pg := Page{Hash: hex.EncodeToString(sum[:16])}
	pg.Width, pg.Height = svgSize(b)
	dst := filepath.Join(store, pg.Hash+".svg")
	if _, err := os.Stat(dst); err == nil {
		return pg, os.Remove(file)
	}
	return pg, os.Rename(file, dst)
}

var viewBoxRe = regexp.MustCompile(`viewBox="\s*[-\d.]+[\s,]+[-\d.]+[\s,]+([\d.]+)[\s,]+([\d.]+)\s*"`)

// svgSize lee el tamaño de la página (pt) del viewBox de la raíz.
func svgSize(b []byte) (float64, float64) {
	head := b[:min(len(b), 1024)]
	if m := viewBoxRe.FindSubmatch(head); m != nil {
		w, err1 := strconv.ParseFloat(string(m[1]), 64)
		h, err2 := strconv.ParseFloat(string(m[2]), 64)
		if err1 == nil && err2 == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	return 595.28, 841.89 // A4
}

var hashRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// PagePath devuelve el archivo de una página guardada, si existe.
func PagePath(outDir, hash string) (string, bool) {
	if !hashRe.MatchString(hash) {
		return "", false
	}
	p := filepath.Join(outDir, "pages", hash+".svg")
	fi, err := os.Stat(p)
	return p, err == nil && fi.Mode().IsRegular()
}

// PrunePages borra del almacén las páginas que no están en ninguna de las
// listas indicadas (se conservan las de la compilación anterior para los
// clientes que aún la estén mostrando).
func PrunePages(outDir string, keep ...[]Page) {
	live := map[string]bool{}
	for _, ps := range keep {
		for _, p := range ps {
			live[p.Hash+".svg"] = true
		}
	}
	store := filepath.Join(outDir, "pages")
	entries, err := os.ReadDir(store)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !live[e.Name()] {
			_ = os.Remove(filepath.Join(store, e.Name()))
		}
	}
}

// Thumbnail renderiza la primera página como PNG en outDir/thumb.png (con las
// imágenes reducidas de la vista previa: a 48 ppi no se nota).
func (c *Compiler) Thumbnail(ctx context.Context, root, main, outDir string) (string, error) {
	mirror, err := previewRoot(outDir)
	if err != nil {
		return "", err
	}
	if err := syncMirror(root, mirror, main); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, "thumb.png")
	_, err = c.run(ctx, mirror, main, out, "--format", "png", "--pages", "1", "--ppi", "48")
	return out, err
}

// pagesRe encuentra el árbol de páginas de un PDF de typst («/Type/Pages/Count N»).
var pagesRe = regexp.MustCompile(`/Type\s*/Pages\b[^>]*?/Count\s+(\d+)`)

// PDFPageCount devuelve el número de páginas de un PDF generado por typst
// (0 si no se puede saber).
func PDFPageCount(file string) int {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range pagesRe.FindAllSubmatch(b, -1) {
		if v, _ := strconv.Atoi(string(m[1])); v > n {
			n = v // la raíz del árbol cuenta todas
		}
	}
	return n
}

// RenderPNG renderiza las páginas indicadas (desde 1) como PNG a la
// resolución dada, sobre el espejo de la vista previa (imágenes reducidas,
// misma paginación que el PDF). Devuelve el contenido de cada página pedida,
// o nil si el documento no tiene esa página.
func (c *Compiler) RenderPNG(ctx context.Context, root, main, outDir string, pages []int, ppi int) ([][]byte, []Diagnostic, error) {
	mirror, err := previewRoot(outDir)
	if err != nil {
		return nil, nil, err
	}
	if err := syncMirror(root, mirror, main); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp(outDir, "png-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	sel := make([]string, len(pages))
	for i, p := range pages {
		sel[i] = strconv.Itoa(p)
	}
	diags, err := c.run(ctx, mirror, main, filepath.Join(tmp, "page-{p}.png"),
		"--format", "png", "--pages", strings.Join(sel, ","), "--ppi", strconv.Itoa(ppi))
	if err != nil {
		return nil, diags, err
	}
	out := make([][]byte, len(pages))
	for i, p := range pages {
		if b, err := os.ReadFile(filepath.Join(tmp, "page-"+strconv.Itoa(p)+".png")); err == nil {
			out[i] = b
		}
	}
	return out, diags, nil
}
