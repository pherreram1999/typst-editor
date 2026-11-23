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
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0].Path != "main.typ" {
		t.Fatalf("se esperaba conflicto en main.typ, obtuve %v", err)
	}
	f := ce.Files[0]
	if f.Ours == nil || *f.Ours != "= Otra B\n\nDesde B\n" || f.Theirs == nil || *f.Theirs != "= Conflicto A\n\nDesde B\n" || f.Base == nil || *f.Base != "= Hola\n\nDesde B\n" {
		t.Fatalf("versiones del conflicto inesperadas: base=%q ours=%q theirs=%q", *f.Base, *f.Ours, *f.Theirs)
	}
	st, err := g.GetStatus(ctx, db, "main")
	if err != nil || len(st.Changes) != 0 {
		t.Fatalf("tras abortar, el árbol debe quedar limpio: %+v %v", st, err)
	}

	// Resolver con una versión editada a mano (y otro archivo sin conflicto
	// que el remoto también cambió entra solo con la fusión).
	merged := "= Fusionado\n\nDesde B\n"
	if _, err := g.Resolve(ctx, db, a, "otro-sha", []Resolution{{Path: "main.typ", OursBlob: f.OursBlob, Content: &merged}}); err == nil {
		t.Fatal("Resolve con un commit remoto distinto del guardado debería fallar")
	}
	// Si la versión local cambia después del pull, hay que repetir.
	os.WriteFile(filepath.Join(db, "main.typ"), []byte("= Otra B editada\n\nDesde B\n"), 0o644)
	if _, err := g.Resolve(ctx, db, a, ce.Theirs, []Resolution{{Path: "main.typ", OursBlob: f.OursBlob, Content: &merged}}); !errors.As(err, &ue) || !strings.Contains(ue.Msg, "cambió") {
		t.Fatalf("se esperaba aviso de archivo cambiado, obtuve %v", err)
	}
	if st, _ := g.GetStatus(ctx, db, "main"); len(st.Changes) != 0 {
		t.Fatalf("tras cancelar la resolución, el árbol debe quedar limpio: %+v", st)
	}
	_, err = g.Pull(ctx, db, r, a)
	if !errors.As(err, &ce) {
		t.Fatalf("se esperaba conflicto otra vez, obtuve %v", err)
	}
	if _, err := g.Resolve(ctx, db, a, ce.Theirs, []Resolution{{Path: "main.typ", OursBlob: ce.Files[0].OursBlob, Content: &merged}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(db, "main.typ")); string(b) != merged {
		t.Fatalf("resultado de la fusión: %q", b)
	}
	if _, err := g.Push(ctx, db, r, a, "tras fusionar"); err != nil {
		t.Fatalf("tras resolver, push debe funcionar: %v", err)
	}
	if _, err := g.Pull(ctx, da, r, a); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(da, "main.typ")); string(b) != merged {
		t.Fatalf("A no recibió la fusión: %q", b)
	}

	// Borrado en un lado y modificado en el otro: se elige una versión entera.
	os.WriteFile(filepath.Join(da, "otro.typ"), []byte("x modificado por A\n"), 0o644)
	if _, err := g.Push(ctx, da, r, a, "A modifica otro"); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(db, "otro.typ"))
	_, err = g.Pull(ctx, db, r, a)
	if !errors.As(err, &ce) || ce.Files[0].HasOurs || !ce.Files[0].HasTheir {
		t.Fatalf("se esperaba conflicto borrado/modificado, obtuve %v", err)
	}
	if _, err := g.Resolve(ctx, db, a, ce.Theirs, []Resolution{{Path: "otro.typ", OursBlob: ce.Files[0].OursBlob, Take: "theirs"}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(db, "otro.typ")); string(b) != "x modificado por A\n" {
		t.Fatalf("debió quedar la versión del remoto: %q", b)
	}
}
