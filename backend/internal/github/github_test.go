package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func fake(t *testing.T) *Client {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer good" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/user", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"login":"octo","name":"Octo Cat"}`)
	}))
	mux.HandleFunc("/user/repos", auth(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		n := 100
		if page == 2 {
			n = 3
		}
		w.Write([]byte("["))
		for i := 0; i < n; i++ {
			if i > 0 {
				w.Write([]byte(","))
			}
			fmt.Fprintf(w, `{"full_name":"octo/r%d-%d","name":"r%d","owner":{"login":"octo"},"private":%v,"description":null,
				"clone_url":"https://github.com/octo/r.git","default_branch":"main","size":%d,"permissions":{"push":true}}`, page, i, i, i%2 == 0, i%5)
		}
		w.Write([]byte("]"))
	}))
	mux.HandleFunc("/users/octo-org", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"login":"Octo-Org","type":"Organization"}`)
	}))
	mux.HandleFunc("/repos/octo/empty/branches", auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusConflict) }))
	mux.HandleFunc("/repos/octo/full/branches", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"main"},{"name":"dev"}]`)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestClient(t *testing.T) {
	c := fake(t)
	ctx := context.Background()
	if u, err := c.User(ctx, "good"); err != nil || u.Login != "octo" {
		t.Fatalf("User: %+v %v", u, err)
	}
	_, err := c.User(ctx, "bad")
	var ge *Error
	if !errors.As(err, &ge) || ge.Status != http.StatusUnauthorized {
		t.Fatalf("token malo: %v", err)
	}
	if a, err := c.Account(ctx, "good", "octo-org"); err != nil || !a.IsOrg() || a.Login != "Octo-Org" {
		t.Fatalf("Account (organización): %+v %v", a, err)
	}
	if _, err := c.Account(ctx, "good", "nadie"); !errors.As(err, &ge) || ge.Status != http.StatusBadRequest {
		t.Fatalf("Account inexistente: %v", err)
	}
	repos, err := c.Repos(ctx, "good")
	if err != nil || len(repos) != 103 {
		t.Fatalf("Repos (paginado): %d %v", len(repos), err)
	}
	if !repos[0].Empty || repos[1].Empty || !repos[0].Private || !repos[0].CanPush || repos[0].Description != "" {
		t.Fatalf("campos mal mapeados: %+v", repos[0])
	}
	if b, err := c.Branches(ctx, "good", "octo/full"); err != nil || len(b) != 2 {
		t.Fatalf("Branches: %v %v", b, err)
	}
	if b, err := c.Branches(ctx, "good", "octo/empty"); err != nil || len(b) != 0 {
		t.Fatalf("Branches de repo vacío: %v %v", b, err)
	}
	if _, err := c.Branches(ctx, "good", "../x"); err == nil {
		t.Fatal("se esperaba error con repo inválido")
	}
}
