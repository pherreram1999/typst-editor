package collab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestHub(t *testing.T) (*Hub, string) {
	dir := t.TempDir()
	return NewHub(func(int64) string { return dir }), dir
}

func TestEditTextClosed(t *testing.T) {
	h, dir := newTestHub(t)
	abs := filepath.Join(dir, "main.typ")
	if err := os.WriteFile(abs, []byte("hola mundo\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, created, err := h.EditText(1, "main.typ", abs, false, func(old string) (string, error) {
		return strings.Replace(old, "mundo", "typst", 1), nil
	})
	if err != nil || created || text != "hola typst\n" {
		t.Fatalf("EditText = %q, %v, %v", text, created, err)
	}
	if b, _ := os.ReadFile(abs); string(b) != "hola typst\n" {
		t.Fatalf("disco = %q", b)
	}
	if h.Rev(1) != 1 {
		t.Fatalf("rev = %d; quiero 1", h.Rev(1))
	}
	// Sin cambios: no se escribe ni cambia la revisión.
	if _, _, err := h.EditText(1, "main.typ", abs, false, func(string) (string, error) { return "", ErrNoChange }); err != nil {
		t.Fatal(err)
	}
	if h.Rev(1) != 1 {
		t.Fatalf("rev tras ErrNoChange = %d", h.Rev(1))
	}
	// Archivo inexistente sin create: error; con create: se crea con sus carpetas.
	nuevo := filepath.Join(dir, "cap", "uno.typ")
	if _, _, err := h.EditText(1, "cap/uno.typ", nuevo, false, func(string) (string, error) { return "x", nil }); err == nil {
		t.Fatal("se esperaba error para un archivo inexistente")
	}
	if _, created, err := h.EditText(1, "cap/uno.typ", nuevo, true, func(string) (string, error) { return "= Uno", nil }); err != nil || !created {
		t.Fatalf("crear = %v, %v", created, err)
	}
	if b, _ := os.ReadFile(nuevo); string(b) != "= Uno" {
		t.Fatalf("nuevo = %q", b)
	}
}

func TestEditTextOpen(t *testing.T) {
	h, dir := newTestHub(t)
	abs := filepath.Join(dir, "main.typ")
	if err := os.WriteFile(abs, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewPeer("p1", 7, "Ana", "#000")
	if err := h.Join(1, "main.typ", abs, p, -1); err != nil {
		t.Fatal(err)
	}
	<-p.Send // init
	text, _, err := h.EditText(1, "main.typ", abs, false, func(old string) (string, error) { return old + "d", nil })
	if err != nil || text != "abcd" {
		t.Fatalf("EditText = %q, %v", text, err)
	}
	var msg struct {
		Type    string   `json:"type"`
		Version int      `json:"version"`
		Updates []Update `json:"updates"`
	}
	for {
		raw := <-p.Send
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type == "updates" {
			break
		}
	}
	if msg.Version != 0 || len(msg.Updates) != 1 {
		t.Fatalf("updates = %+v", msg)
	}
	got, err := applyChangeSet(encode("abc"), msg.Updates[0].Changes)
	if err != nil || decode(got) != "abcd" {
		t.Fatalf("el peer obtiene %q, %v", decode(got), err)
	}
	// El disco se pone al día al guardar (FlushProject) o al salir el último peer.
	h.Leave(p)
	if b, _ := os.ReadFile(abs); string(b) != "abcd" {
		t.Fatalf("disco = %q", b)
	}
}
