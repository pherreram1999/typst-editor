package typst

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func hasFont(cat *FontCatalog, path string) bool {
	for _, f := range cat.Files {
		if f.path == path {
			return true
		}
	}
	return false
}

// Una carpeta de FontDirs entra en el catálogo, y copiar en ella una fuente
// nueva lo rehace sin reiniciar.
func TestFontDirsCatalog(t *testing.T) {
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst no está instalado")
	}
	src := "../../../fonts/roboto"
	regular, err := os.ReadFile(filepath.Join(src, "Roboto-Regular.ttf"))
	if err != nil {
		t.Skip("sin fonts/roboto:", err)
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "Roboto-Regular.ttf")
	if err := os.WriteFile(a, regular, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPST_FONT_PATHS", dir)
	c := New("typst", dir)
	cat, err := c.Fonts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFont(cat, a) {
		t.Fatalf("el catálogo no incluye %s", a)
	}
	if again, _ := c.Fonts(context.Background()); again != cat {
		t.Fatal("sin cambios el catálogo debe reutilizarse")
	}
	sig := c.FontsSignature()

	bold, err := os.ReadFile(filepath.Join(src, "Roboto-Bold.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "Roboto-Bold.ttf")
	if err := os.WriteFile(b, bold, 0o644); err != nil {
		t.Fatal(err)
	}
	if c.FontsSignature() == sig {
		t.Fatal("la firma no cambió al añadir una fuente")
	}
	cat2, err := c.Fonts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFont(cat2, b) || cat2.ETag == cat.ETag {
		t.Fatal("el catálogo no se rehízo con la fuente nueva")
	}
}
