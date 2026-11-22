// Package github es un cliente mínimo de la API REST de GitHub para validar
// tokens y listar repositorios y ramas.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL string // https://api.github.com (o la de GitHub Enterprise)
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Error es un error de la API apto para mostrar al usuario.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

type User struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type Repo struct {
	FullName      string    `json:"full_name"`
	Name          string    `json:"name"`
	Owner         string    `json:"owner"`
	Private       bool      `json:"private"`
	Description   string    `json:"description"`
	CloneURL      string    `json:"clone_url"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	Empty         bool      `json:"empty"`
	CanPush       bool      `json:"can_push"`
	PushedAt      time.Time `json:"pushed_at"`
}

func (c *Client) get(ctx context.Context, token, path string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "typsteditor")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &Error{http.StatusBadGateway, "no se pudo conectar con GitHub"}
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return nil, &Error{res.StatusCode, "GitHub rechazó el token (inválido o caducado)"}
	case res.StatusCode == http.StatusForbidden && res.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, &Error{res.StatusCode, "límite de peticiones a GitHub alcanzado; inténtalo más tarde"}
	case res.StatusCode == http.StatusForbidden:
		return nil, &Error{res.StatusCode, "el token no tiene permiso para esta operación"}
	case res.StatusCode == http.StatusNotFound:
		return nil, &Error{res.StatusCode, "no encontrado en GitHub (o el token no tiene acceso)"}
	case res.StatusCode == http.StatusConflict:
		// GitHub devuelve 409 al listar ramas de un repositorio vacío.
		return res.Header, &Error{res.StatusCode, "repositorio vacío"}
	case res.StatusCode >= 300:
		return nil, &Error{http.StatusBadGateway, fmt.Sprintf("GitHub respondió %d", res.StatusCode)}
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return nil, &Error{http.StatusBadGateway, "respuesta inválida de GitHub"}
	}
	return res.Header, nil
}

// User devuelve el usuario dueño del token.
func (c *Client) User(ctx context.Context, token string) (*User, error) {
	u := &User{}
	_, err := c.get(ctx, token, "/user", u)
	return u, err
}

// Account es una cuenta pública de GitHub: usuario u organización.
type Account struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "User" u "Organization"
}

func (a *Account) IsOrg() bool { return a.Type == "Organization" }

// Account busca la cuenta login (usuario u organización).
func (c *Client) Account(ctx context.Context, token, login string) (*Account, error) {
	a := &Account{}
	_, err := c.get(ctx, token, "/users/"+url.PathEscape(login), a)
	var ge *Error
	if errors.As(err, &ge) && ge.Status == http.StatusNotFound {
		return nil, &Error{http.StatusBadRequest, "no existe el usuario u organización de GitHub «" + login + "»"}
	}
	return a, err
}

type apiRepo struct {
	FullName      string                 `json:"full_name"`
	Name          string                 `json:"name"`
	Owner         struct{ Login string } `json:"owner"`
	Private       bool                   `json:"private"`
	Description   *string                `json:"description"`
	CloneURL      string                 `json:"clone_url"`
	HTMLURL       string                 `json:"html_url"`
	DefaultBranch string                 `json:"default_branch"`
	Size          int                    `json:"size"`
	PushedAt      time.Time              `json:"pushed_at"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

// Repos lista los repositorios accesibles con el token (propios, de
// colaboración y de organizaciones), los más recientes primero.
func (c *Client) Repos(ctx context.Context, token string) ([]Repo, error) {
	out := []Repo{}
	for page := 1; page <= 10; page++ { // máx. 1000 repositorios
		var batch []apiRepo
		path := fmt.Sprintf("/user/repos?per_page=100&page=%d&sort=pushed&affiliation=owner,collaborator,organization_member", page)
		if _, err := c.get(ctx, token, path, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			desc := ""
			if r.Description != nil {
				desc = *r.Description
			}
			out = append(out, Repo{
				FullName: r.FullName, Name: r.Name, Owner: r.Owner.Login, Private: r.Private, Description: desc,
				CloneURL: r.CloneURL, HTMLURL: r.HTMLURL, DefaultBranch: r.DefaultBranch,
				Empty: r.Size == 0, CanPush: r.Permissions.Push, PushedAt: r.PushedAt,
			})
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

// Branches lista las ramas de owner/repo (vacío si el repositorio está vacío).
func (c *Client) Branches(ctx context.Context, token, fullName string) ([]string, error) {
	parts := strings.Split(fullName, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, &Error{http.StatusBadRequest, "repositorio inválido"}
	}
	out := []string{}
	for page := 1; page <= 5; page++ {
		var batch []struct{ Name string }
		path := fmt.Sprintf("/repos/%s/%s/branches?per_page=100&page=%d", url.PathEscape(parts[0]), url.PathEscape(parts[1]), page)
		if _, err := c.get(ctx, token, path, &batch); err != nil {
			var ge *Error
			if errors.As(err, &ge) && ge.Status == http.StatusConflict {
				return out, nil
			}
			return nil, err
		}
		for _, b := range batch {
			out = append(out, b.Name)
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}
