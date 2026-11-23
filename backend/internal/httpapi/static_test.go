package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticHandler(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa</html>"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "assets", "app-abc.js"), []byte("console.log(1)"), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".secreto"), []byte("x"), 0o644))

	s := New(nil, nil, nil, Options{DataDir: t.TempDir(), StaticDir: dir})
	h := s.Routes()

	cases := []struct {
		method, path string
		status       int
		body, cache  string
	}{
		{"GET", "/", 200, "spa", "no-cache"},
		{"GET", "/assets/app-abc.js", 200, "console.log", "immutable"},
		{"GET", "/otra/ruta", 200, "spa", "no-cache"},
		{"GET", "/assets/", 200, "spa", "no-cache"},
		{"GET", "/.secreto", 200, "spa", "no-cache"},
		{"GET", "/api/no-existe", 404, `"error"`, ""},
		{"GET", "/api/health", 200, "ok", ""},
		{"POST", "/", 405, "", ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.status {
			t.Errorf("%s %s: estado %d, se esperaba %d", c.method, c.path, rec.Code, c.status)
		}
		if !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s %s: cuerpo %q no contiene %q", c.method, c.path, rec.Body.String(), c.body)
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), c.cache) {
			t.Errorf("%s %s: Cache-Control %q", c.method, c.path, rec.Header().Get("Cache-Control"))
		}
	}

	if staticHandler(t.TempDir()) != nil {
		t.Error("sin index.html no debería servirse el frontend")
	}
}
