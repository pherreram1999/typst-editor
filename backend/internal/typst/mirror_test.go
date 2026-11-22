package typst

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeSum resume el contenido de un árbol para comprobar que no cambia.
func treeSum(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	m := map[string][32]byte{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			b, _ := os.ReadFile(p)
			m[p] = sha256.Sum256(b)
		}
		return err
	})
	return m
}

func TestSyncMirror(t *testing.T) {
	src, mirror := t.TempDir(), filepath.Join(t.TempDir(), "preview")
	bigPNG := withChunks(t, gradient(2000, 1000, false), physChunk(300))
	writeFile(t, src, "main.typ", []byte("= Hola\n#include \"cap/uno.typ\"\n"))
	writeFile(t, src, "cap/uno.typ", []byte("Texto"))
	writeFile(t, src, "media/grande.png", bigPNG)
	writeFile(t, src, "media/foto.jpg", []byte("no es una imagen"))
	writeFile(t, src, "datos.csv", []byte("a,b\n"))
	writeFile(t, src, ".git/config", []byte("[core]"))
	before := treeSum(t, src)

	if err := syncMirror(src, mirror, "main.typ"); err != nil {
		t.Fatal(err)
	}
	kind := func(rel string) string {
		fi, err := os.Lstat(filepath.Join(mirror, filepath.FromSlash(rel)))
		switch {
		case err != nil:
			return "no existe"
		case fi.Mode()&fs.ModeSymlink != 0:
			return "enlace"
		case fi.IsDir():
			return "directorio"
		}
		return "archivo"
	}
	expect := map[string]string{
		"main.typ":         "archivo", // typst exige que la entrada esté dentro de --root
		"cap":              "directorio",
		"cap/uno.typ":      "enlace",
		"media/grande.png": "archivo", // copia reducida
		"media/foto.jpg":   "enlace",  // no se pudo leer: se usa el original
		"datos.csv":        "enlace",
		".git":             "no existe",
	}
	for rel, want := range expect {
		if got := kind(rel); got != want {
			t.Errorf("%s: %s, quiero %s", rel, got, want)
		}
	}
	proxyInfo, _ := os.Stat(filepath.Join(mirror, "media/grande.png"))

	// Sin cambios, la copia reducida no se regenera.
	time.Sleep(10 * time.Millisecond)
	if err := syncMirror(src, mirror, "main.typ"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(mirror, "media/grande.png")); !fi.ModTime().Equal(proxyInfo.ModTime()) {
		t.Error("la copia reducida se regeneró sin que cambiara el original")
	}

	// Cambios: nuevo principal, imagen reemplazada por una pequeña, archivo
	// y directorio borrados, archivo nuevo.
	writeFile(t, src, "otro.typ", []byte("= Otro"))
	writeFile(t, src, "media/grande.png", withChunks(t, gradient(100, 100, false)))
	os.RemoveAll(filepath.Join(src, "cap"))
	os.Remove(filepath.Join(src, "datos.csv"))
	writeFile(t, src, "main.typ", []byte("= Cambiado"))
	before = treeSum(t, src)
	if err := syncMirror(src, mirror, "otro.typ"); err != nil {
		t.Fatal(err)
	}
	expect = map[string]string{
		"main.typ":         "enlace",
		"otro.typ":         "archivo",
		"media/grande.png": "enlace",
		"cap":              "no existe",
		"datos.csv":        "no existe",
	}
	for rel, want := range expect {
		if got := kind(rel); got != want {
			t.Errorf("tras los cambios, %s: %s, quiero %s", rel, got, want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(mirror, "main.typ")); string(b) != "= Cambiado" {
		t.Errorf("main.typ en el espejo: %q", b)
	}

	// Volver a convertir en archivo principal un enlace no debe escribir a
	// través del enlace (sobre el original).
	if err := syncMirror(src, mirror, "main.typ"); err != nil {
		t.Fatal(err)
	}
	after := treeSum(t, src)
	for p, sum := range before {
		if after[p] != sum {
			t.Errorf("el espejo modificó el original %s", p)
		}
	}
	if len(after) != len(before) {
		t.Errorf("el espejo creó o borró archivos en el proyecto")
	}
}

func TestPreviewPages(t *testing.T) {
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst no está en el PATH")
	}
	root, out := t.TempDir(), t.TempDir()
	doc := func(last string) []byte {
		return []byte("#set page(width: 10cm, height: 5cm)\nUno\n#pagebreak()\nDos\n#pagebreak()\n" + last + "\n")
	}
	writeFile(t, root, "main.typ", doc("Tres"))
	c := New("typst")
	ctx := context.Background()
	first, _, err := c.Preview(ctx, root, "main.typ", out)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("%d páginas, quiero 3", len(first))
	}
	if w, h := first[0].Width, first[0].Height; w < 283 || w > 284 || h < 141 || h > 142 {
		t.Errorf("tamaño de página %.2f×%.2f pt, quiero 283,46×141,73", w, h)
	}

	// Cambiar solo la última página conserva el hash de las demás.
	writeFile(t, root, "main.typ", doc("Tres, cambiada"))
	second, _, err := c.Preview(ctx, root, "main.typ", out)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Hash != first[0].Hash || second[1].Hash != first[1].Hash || second[2].Hash == first[2].Hash {
		t.Errorf("hashes: %v → %v", first, second)
	}
	for _, p := range append(first, second...) {
		if _, ok := PagePath(out, p.Hash); !ok {
			t.Errorf("falta la página %s", p.Hash)
		}
	}
	PrunePages(out, second)
	if _, ok := PagePath(out, first[2].Hash); ok {
		t.Error("PrunePages no borró la página que ya no se usa")
	}
	if _, ok := PagePath(out, second[2].Hash); !ok {
		t.Error("PrunePages borró una página en uso")
	}
	for _, bad := range []string{"../main", "", strings.Repeat("g", 32)} {
		if _, ok := PagePath(out, bad); ok {
			t.Errorf("PagePath aceptó %q", bad)
		}
	}

	// Un error de compilación se informa con rutas relativas al proyecto, no
	// al espejo.
	writeFile(t, root, "cap.typ", []byte("#nada"))
	writeFile(t, root, "main.typ", []byte("#include \"cap.typ\"\n"))
	_, diags, err := c.Preview(ctx, root, "main.typ", out)
	var ce *CompileError
	if err == nil || !errors.As(err, &ce) || len(diags) == 0 || diags[0].File != "cap.typ" {
		t.Fatalf("err=%v diags=%+v", err, diags)
	}
	if strings.Contains(ce.Output, out) {
		t.Errorf("la salida menciona el directorio interno: %s", ce.Output)
	}
}
