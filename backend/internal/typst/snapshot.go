package typst

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Instantánea para revisión: una compilación guardada que se muestra a
// quien tenga el enlace sin volver a compilar. Cada proyecto tiene como mucho
// una; se escribe entera en una carpeta temporal y se cambia por la anterior
// de golpe, así que un lector nunca ve una mezcla de dos compilaciones.
//
//   <dir>/meta.json            SnapshotMeta
//   <dir>/document.pdf         con las imágenes originales (descarga)
//   <dir>/pages/page-NNNN.svg  con las imágenes reducidas de la vista previa

type SnapshotPage struct {
	Width  float64 `json:"width"` // pt
	Height float64 `json:"height"`
}

type SnapshotMeta struct {
	Pages      []SnapshotPage `json:"pages"`
	PDFSize    int64          `json:"pdf_size"`
	CompiledAt time.Time      `json:"compiled_at"`
}

// Snapshot compila el proyecto en dir (reemplazando la instantánea anterior
// solo si la compilación termina bien). buildDir es la carpeta de compilación
// del proyecto, donde vive el espejo de la vista previa.
func (c *Compiler) Snapshot(ctx context.Context, root, main, buildDir, dir string) (*SnapshotMeta, []Diagnostic, error) {
	mirror, err := previewRoot(buildDir)
	if err != nil {
		return nil, nil, err
	}
	if err := syncMirror(root, mirror, main); err != nil {
		return nil, nil, err
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := dir + ".tmp-" + hex.EncodeToString(rnd[:])
	if err := os.MkdirAll(filepath.Join(tmp, "pages"), 0o755); err != nil {
		return nil, nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmp)
		}
	}()

	diags, err := c.run(ctx, mirror, main, filepath.Join(tmp, "pages", "page-{0p}.svg"), "--format", "svg")
	if err != nil {
		return nil, diags, err
	}
	pdf := filepath.Join(tmp, "document.pdf")
	if _, err := c.run(ctx, root, main, pdf); err != nil {
		return nil, diags, err
	}

	files, err := filepath.Glob(filepath.Join(tmp, "pages", "page-*.svg"))
	if err != nil {
		return nil, diags, err
	}
	sort.Strings(files)
	meta := &SnapshotMeta{CompiledAt: time.Now().UTC(), Pages: make([]SnapshotPage, 0, len(files))}
	for i, f := range files {
		// Nombres fijos 1..N: las páginas se piden por número.
		if err := os.Rename(f, filepath.Join(tmp, "pages", pageName(i+1))); err != nil {
			return nil, diags, err
		}
		b, err := os.ReadFile(filepath.Join(tmp, "pages", pageName(i+1)))
		if err != nil {
			return nil, diags, err
		}
		w, h := svgSize(b)
		meta.Pages = append(meta.Pages, SnapshotPage{Width: w, Height: h})
	}
	if fi, err := os.Stat(pdf); err == nil {
		meta.PDFSize = fi.Size()
	}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(tmp, "meta.json"), data, 0o644); err != nil {
		return nil, diags, err
	}

	// Cambiar la carpeta: la anterior se aparta y se borra después.
	old := dir + ".old-" + hex.EncodeToString(rnd[:])
	if err := os.Rename(dir, old); err != nil && !os.IsNotExist(err) {
		return nil, diags, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.Rename(old, dir)
		return nil, diags, err
	}
	ok = true
	_ = os.RemoveAll(old)
	return meta, diags, nil
}

func pageName(n int) string { return fmt.Sprintf("page-%04d.svg", n) }

// SnapshotPagePath devuelve el SVG de la página n (desde 1) de la instantánea.
func SnapshotPagePath(dir string, n int) (string, bool) {
	if n < 1 {
		return "", false
	}
	p := filepath.Join(dir, "pages", pageName(n))
	fi, err := os.Stat(p)
	return p, err == nil && fi.Mode().IsRegular()
}

// ReadSnapshot lee los metadatos de la instantánea (nil si no hay).
func ReadSnapshot(dir string) (*SnapshotMeta, error) {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m SnapshotMeta
	return &m, json.Unmarshal(b, &m)
}
