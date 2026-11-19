package gitsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRemote(t *testing.T) {
	ok := [][2]string{{"https://github.com/o/r.git", "main"}, {"", "feature/x"}}
	bad := [][2]string{{"http://github.com/o/r", "main"}, {"ext::sh -c id", "main"}, {"https://u:p@github.com/o/r", "main"},
		{"https://github.com/o/r", "-x"}, {"https://github.com/o/r", "a..b"}, {"git@github.com:o/r.git", "main"}}
	for _, c := range ok {
		if err := ValidateRemote(c[0], c[1]); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
	for _, c := range bad {
		if ValidateRemote(c[0], c[1]) == nil {
			t.Errorf("%v: se esperaba error", c)
		}
	}
}

func TestPushPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no disponible")
	}
	ctx := context.Background()
	g := &Git{Bin: "git", allowLocal: true}
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	r := &Remote{URL: bare, Branch: "main"}
	a := &Author{Name: "Ana", Email: "ana@x"}

	// Clonar un repositorio vacío indica BranchMissing (se empieza con la plantilla).
	var ue *UserError
	if err := g.Clone(ctx, filepath.Join(tmp, "vacio"), r); !errors.As(err, &ue) || !ue.BranchMissing {
		t.Fatalf("clonar repo vacío: se esperaba BranchMissing, obtuve %v", err)
	}

	// Proyecto A: init + push.
	da := filepath.Join(tmp, "a")
	os.MkdirAll(da, 0o755)
	if err := g.Init(ctx, da, "main"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(da, "main.typ"), []byte("= Hola\n"), 0o644)
	if _, err := g.Push(ctx, da, r, a, "primer commit"); err != nil {
		t.Fatal(err)
	}

	// Proyecto B: clone, modificar, push.
	db := filepath.Join(tmp, "b")
	if err := g.Clone(ctx, db, r); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(db, "main.typ"), []byte("= Hola\n\nDesde B\n"), 0o644)
	if _, err := g.Push(ctx, db, r, a, "cambio B"); err != nil {
		t.Fatal(err)
	}

	// A cambia otro archivo: push rechazado hasta hacer pull.
	os.WriteFile(filepath.Join(da, "otro.typ"), []byte("x\n"), 0o644)
	_, err := g.Push(ctx, da, r, a, "cambio A")
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "pull") {
		t.Fatalf("se esperaba rechazo por pull pendiente, obtuve %v", err)
	}
	if _, err := g.Pull(ctx, da, r, a); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(da, "main.typ")); string(b) != "= Hola\n\nDesde B\n" {
		t.Fatalf("pull no trajo el cambio: %q", b)
	}
	if _, err := g.Push(ctx, da, r, a, "cambio A"); err != nil {
		t.Fatal(err)
	}

	// Conflicto: ambos editan la misma línea.
	os.WriteFile(filepath.Join(db, "main.typ"), []byte("= Conflicto B\n"), 0o644)
	if _, err := g.Pull(ctx, db, r, a); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(da, "main.typ"), []byte("= Conflicto A\n\nDesde B\n"), 0o644)
	if _, err := g.Push(ctx, da, r, a, "A"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(db, "main.typ"), []byte("= Otra B\n\nDesde B\n"), 0o644)
	_, err = g.Pull(ctx, db, r, a)
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "conflicto") {
		t.Fatalf("se esperaba conflicto, obtuve %v", err)
	}
	st, err := g.GetStatus(ctx, db, "main")
	if err != nil || len(st.Changes) != 0 {
		t.Fatalf("tras abortar, el árbol debe quedar limpio: %+v %v", st, err)
	}
}
