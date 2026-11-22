package httpapi

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestWriteProjectZip(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.typ", "= Hola")
	write("media/fig.png", "png")
	write(".git/config", "secreto")
	write(".oculto", "x")
	_ = os.Symlink("/etc/passwd", filepath.Join(dir, "enlace"))

	var buf bytes.Buffer
	if err := writeProjectZip(&buf, dir, "Mi tesis"); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Name == "Mi tesis/media/fig.png" && f.Method != zip.Store {
			t.Errorf("png debería ir sin comprimir")
		}
	}
	sort.Strings(names)
	want := []string{"Mi tesis/main.typ", "Mi tesis/media/", "Mi tesis/media/fig.png"}
	if len(names) != len(want) {
		t.Fatalf("entradas %v, se esperaba %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entradas %v, se esperaba %v", names, want)
		}
	}
}

func TestTicketUnSoloUso(t *testing.T) {
	var s ticketStore
	id := s.issue(1, 2)
	if v, ok := s.take(id); !ok || v.userID != 1 || v.projectID != 2 {
		t.Fatalf("ticket válido rechazado: %v %v", v, ok)
	}
	if _, ok := s.take(id); ok {
		t.Fatal("el ticket se pudo usar dos veces")
	}
	id = s.issue(1, 2)
	s.m[id] = downloadTicket{1, 2, time.Now().Add(-time.Second)}
	if _, ok := s.take(id); ok {
		t.Fatal("ticket caducado aceptado")
	}
	if exportName(` a/b:"c". `) != "a-b--c-" || exportName("...") != "proyecto" {
		t.Fatalf("exportName: %q", exportName(` a/b:"c". `))
	}
}
