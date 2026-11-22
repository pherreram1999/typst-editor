package typst

import (
	"context"
	"os/exec"
	"testing"
)

func TestTokenizeSource(t *testing.T) {
	toks := tokenizeSource("= Título\nUn *día* ñandú // comentario\nVer https://x.org/a 😀b")
	got := toks
	want := []token{
		{"título", 1, 3}, {"un", 2, 1}, {"día", 2, 5}, {"ñandú", 2, 10},
		{"ver", 3, 1}, {"https", 3, 5}, {"x", 3, 13}, {"org", 3, 15}, {"a", 3, 19}, {"b", 3, 23}, // el emoji ocupa 2 unidades UTF-16
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d: %v, quiero %v", i, got[i], want[i])
		}
	}
}

func TestMatchBlocksPrefersDocumentOrder(t *testing.T) {
	srcs := []source{
		{"a.typ", tokenizeSource("= Resumen\nTexto repetido aquí.\n\nOtro párrafo distinto.\n\nTexto repetido aquí.")},
	}
	blocks := []block{
		{1, 10, "Resumen"},
		{1, 30, "Texto repetido aquí."},
		{1, 50, "Otro párrafo distinto."},
		{2, 10, "Texto repetido aquí."},
		{2, 40, "Generado por código"},
	}
	got := matchBlocks(blocks, srcs)
	lines := []int{}
	for _, p := range got {
		lines = append(lines, p.Line)
	}
	if len(lines) != 4 || lines[0] != 1 || lines[1] != 2 || lines[2] != 4 || lines[3] != 6 {
		t.Errorf("líneas %v, quiero [1 2 4 6]", lines)
	}
}

func TestSyncMap(t *testing.T) {
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst no está en el PATH")
	}
	root, out := t.TempDir(), t.TempDir()
	writeFile(t, root, "main.typ", []byte("#set page(width: 10cm, height: 6cm)\n= Introducción\nPrimer párrafo con *negrita* y `código`.\n\n#include \"cap/uno.typ\"\n"))
	writeFile(t, root, "cap/uno.typ", []byte("#pagebreak()\n== Métodos\n\n- elemento de lista\n- otro\n\nÚltimo párrafo del capítulo.\n"))
	pts, err := New("typst").SyncMap(context.Background(), root, "main.typ", out)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		page int
		file string
		line int
	}{{1, "main.typ", 2}, {1, "main.typ", 3}, {2, "cap/uno.typ", 2}, {2, "cap/uno.typ", 4}, {2, "cap/uno.typ", 7}}
	if len(pts) != len(want) {
		t.Fatalf("puntos: %+v", pts)
	}
	for i, w := range want {
		p := pts[i]
		if p.Page != w.page || p.File != w.file || p.Line != w.line {
			t.Errorf("punto %d: %+v, quiero %+v", i, p, w)
		}
		if i > 0 && p.Page == pts[i-1].Page && p.Y <= pts[i-1].Y {
			t.Errorf("punto %d no avanza en la página: %+v", i, p)
		}
	}
}
