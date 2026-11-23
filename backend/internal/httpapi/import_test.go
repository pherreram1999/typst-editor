package httpapi

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func buildZip(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestExtractZipStripsTopFolderAndHidden(t *testing.T) {
	dir := t.TempDir()
	zr := buildZip(t, map[string]string{
		"tesis/main.typ":            "= Hola",
		"tesis/cap/uno.typ":         "x",
		"tesis/img/a.png":           "png",
		"tesis/.git/config":         "secreto",
		"__MACOSX/tesis/._main.typ": "basura",
	})
	if err := extractZip(zr, dir, &importLimits{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"main.typ", "cap/uno.typ", "img/a.png"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("falta %s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error(".git no debería extraerse")
	}
	if m, err := pickMainFile(dir); err != nil || m != "main.typ" {
		t.Errorf("principal = %q, %v", m, err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	zr := buildZip(t, map[string]string{"../fuera.typ": "x"})
	if err := extractZip(zr, t.TempDir(), &importLimits{}); err == nil {
		t.Fatal("se esperaba error con ../")
	}
}

func TestPickMainFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a-capitulo.typ"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "tesis.typ"), []byte(`#include "a-capitulo.typ"`), 0o644)
	if m, _ := pickMainFile(dir); m != "tesis.typ" {
		t.Errorf("principal = %q, se esperaba tesis.typ", m)
	}
	empty := t.TempDir()
	os.WriteFile(filepath.Join(empty, "a.png"), []byte("x"), 0o644)
	if _, err := pickMainFile(empty); err == nil {
		t.Error("se esperaba error sin .typ")
	}
}

func TestDecodeCP437(t *testing.T) {
	if len(cp437High) != 128 {
		t.Fatalf("tabla cp437 con %d caracteres", len(cp437High))
	}
	if got := decodeCP437("Dise\xa4o/a\xa2n.typ"); got != "Diseño/aón.typ" {
		t.Errorf("got %q", got)
	}
}
