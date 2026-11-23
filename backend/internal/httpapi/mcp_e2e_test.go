package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prueba de punta a punta del servidor MCP y OAuth contra un servidor en
// marcha (nunca el de producción: usa otra base de datos y otro puerto).
//
//	TYPSTEDITOR_E2E_URL=http://127.0.0.1:18085 TYPSTEDITOR_E2E_ADMIN=admin:clave go test ./internal/httpapi -run E2E -v
//
// Crea usuarios y proyectos de prueba.
func TestMCPE2E(t *testing.T) {
	base := os.Getenv("TYPSTEDITOR_E2E_URL")
	admin := os.Getenv("TYPSTEDITOR_E2E_ADMIN")
	if base == "" || admin == "" {
		t.Skip("define TYPSTEDITOR_E2E_URL y TYPSTEDITOR_E2E_ADMIN (usuario:clave) para ejecutarla")
	}
	ctx := context.Background()
	user, pass, _ := strings.Cut(admin, ":")
	e := &e2e{t: t, base: base}
	adminJWT := e.login(user, pass)

	// Dos usuarios normales: ana (la que usa el agente) y beto (otro).
	suffix := fmt.Sprint(time.Now().UnixNano() % 1e9)
	for _, n := range []string{"ana", "beto"} {
		// Alta por correo: sin RESEND la respuesta trae el enlace para definir la contraseña.
		var created struct{ Link string }
		e.api(adminJWT, "POST", "/api/users", map[string]any{"email": n + suffix + "@ejemplo.com", "username": n + suffix, "name": n, "role": "user"}, &created)
		if created.Link == "" {
			t.Fatal("el alta no devolvió el enlace (¿servidor con RESEND?)")
		}
		_, tok, _ := strings.Cut(created.Link, "#/cuenta/")
		e.api("", "POST", "/api/auth/account/"+tok+"/password", map[string]any{"password": "clave-segura-1"}, nil)
	}
	ana := e.login("ana"+suffix, "clave-segura-1")
	beto := e.login("beto"+suffix, "clave-segura-1")
	var pAna, pAna2, pBeto, pAdmin struct{ ID int64 }
	e.api(ana, "POST", "/api/projects", map[string]any{"name": "Tesis de Ana"}, &pAna)
	e.api(ana, "POST", "/api/projects", map[string]any{"name": "Otro de Ana"}, &pAna2)
	e.api(beto, "POST", "/api/projects", map[string]any{"name": "De Beto"}, &pBeto)
	e.api(adminJWT, "POST", "/api/projects", map[string]any{"name": "Del admin"}, &pAdmin)

	// --- Token personal con acceso completo ---
	var tok struct{ Token string }
	e.api(ana, "POST", "/api/mcp/tokens", map[string]any{"name": "e2e", "scopes": []string{"typst:read", "typst:write"}, "all_projects": true}, &tok)
	cs := e.connect(ctx, tok.Token)

	ids := e.projectIDs(ctx, cs)
	if !ids[pAna.ID] || !ids[pAna2.ID] || ids[pBeto.ID] || ids[pAdmin.ID] {
		t.Fatalf("list_projects de ana: %v", ids)
	}
	e.wantErr(ctx, cs, "get_project", map[string]any{"project_id": pBeto.ID}, "no encontrado")

	// Escribir, editar, insertar, leer.
	e.call(ctx, cs, "write_file", map[string]any{"project_id": pAna.ID, "path": "cap/uno.typ", "content": "= Capítulo uno\n\nHola mundo.\n"})
	e.call(ctx, cs, "edit_file", map[string]any{"project_id": pAna.ID, "path": "cap/uno.typ", "old_string": "Hola mundo.", "new_string": "Hola *typst*."})
	e.wantErr(ctx, cs, "edit_file", map[string]any{"project_id": pAna.ID, "path": "cap/uno.typ", "old_string": "no existe", "new_string": "x"}, "no aparece")
	e.call(ctx, cs, "insert_text", map[string]any{"project_id": pAna.ID, "path": "main.typ", "line": -1, "text": "#include \"cap/uno.typ\""})
	if got := e.text(ctx, cs, "read_file", map[string]any{"project_id": pAna.ID, "path": "cap/uno.typ"}); !strings.Contains(got, "Hola *typst*.") {
		t.Fatalf("read_file: %s", got)
	}
	if got := e.text(ctx, cs, "search_files", map[string]any{"project_id": pAna.ID, "pattern": "typst*"}); !strings.Contains(got, "cap/uno.typ:3:") {
		t.Fatalf("search_files: %s", got)
	}

	// Compilar, ver páginas, código de una página, PDF.
	res := e.call(ctx, cs, "compile", map[string]any{"project_id": pAna.ID})
	var comp diagnosticsOut
	remarshal(t, res.StructuredContent, &comp)
	if !comp.OK || comp.Pages < 1 {
		t.Fatalf("compile: %+v", comp)
	}
	res = e.call(ctx, cs, "render_pages", map[string]any{"project_id": pAna.ID, "pages": []int{1, 99}, "ppi": 40})
	imgs := 0
	for _, c := range res.Content {
		if im, ok := c.(*mcp.ImageContent); ok && bytes.HasPrefix(im.Data, []byte("\x89PNG")) {
			imgs++
		}
	}
	if imgs != 1 {
		t.Fatalf("render_pages: %d imágenes", imgs)
	}
	res = e.call(ctx, cs, "page_source", map[string]any{"project_id": pAna.ID, "page": comp.Pages})
	if b, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(b), "cap/uno.typ") {
		t.Fatalf("page_source: %s", b)
	}
	res = e.call(ctx, cs, "get_pdf", map[string]any{"project_id": pAna.ID})
	pdf := e.get(linkIn(t, res))
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("get_pdf: el enlace no devuelve un PDF")
	}

	// Errores de compilación con archivo:línea.
	e.call(ctx, cs, "write_file", map[string]any{"project_id": pAna2.ID, "path": "main.typ", "content": "= A\n#variable_que_no_existe\n"})
	res = e.call(ctx, cs, "compile", map[string]any{"project_id": pAna2.ID})
	remarshal(t, res.StructuredContent, &comp)
	if comp.OK || len(comp.Diagnostics) == 0 || comp.Diagnostics[0].File != "main.typ" || comp.Diagnostics[0].Line != 2 {
		t.Fatalf("compile con error: %+v", comp)
	}

	// Subir (base64, enlace, PUT) y descargar.
	png := tinyPNG()
	e.call(ctx, cs, "upload_file", map[string]any{"project_id": pAna.ID, "path": "img/a.png", "content_base64": base64.StdEncoding.EncodeToString(png)})
	e.wantErr(ctx, cs, "upload_file", map[string]any{"project_id": pAna.ID, "path": "img/a.png", "content_base64": base64.StdEncoding.EncodeToString(png)}, "ya existe")
	res = e.call(ctx, cs, "create_upload_url", map[string]any{"project_id": pAna.ID, "path": "datos/tabla.csv"})
	var up struct{ URL string }
	remarshal(t, res.StructuredContent, &up)
	e.do("PUT", up.URL, "a,b\n1,2\n", http.StatusCreated)
	e.do("PUT", up.URL, "otra vez", http.StatusUnauthorized) // un solo uso
	res = e.call(ctx, cs, "download_file", map[string]any{"project_id": pAna.ID, "path": "datos/tabla.csv"})
	if got := e.get(linkIn(t, res)); string(got) != "a,b\n1,2\n" {
		t.Fatalf("download_file: %q", got)
	}
	res = e.call(ctx, cs, "view_image", map[string]any{"project_id": pAna.ID, "path": "img/a.png"})
	if len(res.Content) < 2 {
		t.Fatal("view_image sin imagen")
	}
	e.wantErr(ctx, cs, "upload_from_url", map[string]any{"project_id": pAna.ID, "path": "x.png", "url": "https://127.0.0.1/x.png"}, "privada")
	e.wantErr(ctx, cs, "upload_from_url", map[string]any{"project_id": pAna.ID, "path": "x.png", "url": "http://example.com/x.png"}, "https")

	// Renombrar y borrar.
	e.call(ctx, cs, "rename_file", map[string]any{"project_id": pAna.ID, "from": "datos/tabla.csv", "to": "tablas/t.csv"})
	e.call(ctx, cs, "delete_file", map[string]any{"project_id": pAna.ID, "path": "tablas/t.csv"})
	e.wantErr(ctx, cs, "delete_file", map[string]any{"project_id": pAna.ID, "path": "main.typ"}, "principal")
	res = e.call(ctx, cs, "export_project", map[string]any{"project_id": pAna.ID})
	var exp struct{ URL string }
	remarshal(t, res.StructuredContent, &exp)
	if z := e.get(exp.URL); !bytes.HasPrefix(z, []byte("PK")) {
		t.Fatal("export_project: no es un zip")
	}

	// --- Un admin por MCP no ve proyectos ajenos ---
	var tokAdmin struct{ Token string }
	e.api(adminJWT, "POST", "/api/mcp/tokens", map[string]any{"name": "e2e", "scopes": []string{"typst:read"}, "all_projects": true}, &tokAdmin)
	csAdmin := e.connect(ctx, tokAdmin.Token)
	if ids := e.projectIDs(ctx, csAdmin); ids[pAna.ID] || !ids[pAdmin.ID] {
		t.Fatalf("admin por MCP ve: %v", ids)
	}
	// Solo lectura: escribir falla.
	e.wantErr(ctx, csAdmin, "write_file", map[string]any{"project_id": pAdmin.ID, "path": "x.typ", "content": "x"}, "solo lectura")

	// --- OAuth completo, limitado a un proyecto ---
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect := "http://localhost:33418/callback"
	var reg struct {
		ClientID string `json:"client_id"`
	}
	e.json("POST", base+"/oauth/register", "", map[string]any{"client_name": "Prueba e2e", "redirect_uris": []string{redirect}}, http.StatusCreated, &reg)
	q := url.Values{"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"typst:read typst:write"},
		"state": {"xyz"}, "resource": {base + "/mcp"}}
	// /oauth/authorize redirige a la SPA con la misma petición.
	resp := e.noRedirect(base + "/oauth/authorize?" + q.Encode())
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/#/oauth?") {
		t.Fatalf("authorize → %d %q", resp.StatusCode, loc)
	}
	var approve struct{ Redirect string }
	e.api(ana, "POST", "/api/oauth/authorize", map[string]any{"query": q.Encode(), "approve": true,
		"scopes": []string{"typst:read", "typst:write"}, "all_projects": false, "projects": []int64{pAna.ID}}, &approve)
	ru, _ := url.Parse(approve.Redirect)
	if ru.Query().Get("state") != "xyz" || ru.Query().Get("code") == "" {
		t.Fatalf("redirect: %s", approve.Redirect)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {ru.Query().Get("code")}, "redirect_uri": {redirect},
		"client_id": {reg.ClientID}, "code_verifier": {"incorrecto" + verifier}}
	e.form(base+"/oauth/token", form, http.StatusBadRequest, nil) // PKCE incorrecto (y el código queda gastado)

	// Repetir con el verifier correcto.
	e.api(ana, "POST", "/api/oauth/authorize", map[string]any{"query": q.Encode(), "approve": true,
		"scopes": []string{"typst:read", "typst:write"}, "all_projects": false, "projects": []int64{pAna.ID}}, &approve)
	ru, _ = url.Parse(approve.Redirect)
	form.Set("code", ru.Query().Get("code"))
	form.Set("code_verifier", verifier)
	var tokens struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	e.form(base+"/oauth/token", form, http.StatusOK, &tokens)
	e.form(base+"/oauth/token", form, http.StatusBadRequest, nil) // código de un solo uso

	csOAuth := e.connect(ctx, tokens.Access)
	if ids := e.projectIDs(ctx, csOAuth); len(ids) != 1 || !ids[pAna.ID] {
		t.Fatalf("OAuth limitado ve: %v", ids)
	}
	e.wantErr(ctx, csOAuth, "read_file", map[string]any{"project_id": pAna2.ID, "path": "main.typ"}, "no encontrado")
	// Un proyecto creado con un acceso limitado queda incluido.
	res = e.call(ctx, csOAuth, "create_project", map[string]any{"name": "Creado por el agente"})
	var created projectSummary
	remarshal(t, res.StructuredContent, &created)
	if ids := e.projectIDs(ctx, csOAuth); !ids[created.ID] {
		t.Fatalf("el proyecto creado no queda en el acceso: %v", ids)
	}

	// Refresco rotativo; reutilizar el refresco viejo revoca todo.
	var t2 struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	ref := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.Refresh}, "client_id": {reg.ClientID}}
	e.form(base+"/oauth/token", ref, http.StatusOK, &t2)
	e.form(base+"/oauth/token", ref, http.StatusBadRequest, nil)
	if r := e.mcpStatus(t2.Access); r != http.StatusUnauthorized {
		t.Fatalf("tras reutilizar el refresco, el token nuevo sigue valiendo (%d)", r)
	}

	// --- Revocar un token personal ---
	var grants struct {
		Grants []struct {
			ID       int64
			Personal bool
		}
	}
	e.api(ana, "GET", "/api/mcp/grants", nil, &grants)
	for _, g := range grants.Grants {
		e.api(ana, "DELETE", fmt.Sprintf("/api/mcp/grants/%d", g.ID), nil, nil)
	}
	if r := e.mcpStatus(tok.Token); r != http.StatusUnauthorized {
		t.Fatalf("token revocado sigue valiendo (%d)", r)
	}
}

// ---------- utilidades ----------

type e2e struct {
	t    *testing.T
	base string
}

func (e *e2e) json(method, u, jwt string, body any, want int, out any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Content-Type", "application/json")
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if want != 0 && resp.StatusCode != want || want == 0 && resp.StatusCode >= 300 {
		e.t.Fatalf("%s %s → %d: %s", method, u, resp.StatusCode, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, u, err, b)
		}
	}
}

func (e *e2e) api(jwt, method, p string, body, out any) {
	e.t.Helper()
	e.json(method, e.base+p, jwt, body, 0, out)
}

func (e *e2e) login(user, pass string) string {
	e.t.Helper()
	var out struct{ Token string }
	e.json("POST", e.base+"/api/auth/login", "", map[string]string{"username": user, "password": pass}, http.StatusOK, &out)
	return out.Token
}

func (e *e2e) form(u string, v url.Values, want int, out any) {
	e.t.Helper()
	resp, err := http.PostForm(u, v)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("POST %s → %d: %s", u, resp.StatusCode, b)
	}
	if out != nil {
		_ = json.Unmarshal(b, out)
	}
}

func (e *e2e) do(method, u, body string, want int) {
	e.t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s → %d: %s", method, u, resp.StatusCode, b)
	}
}

func (e *e2e) get(u string) []byte {
	e.t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET %s → %d: %s", u, resp.StatusCode, b)
	}
	return b
}

func (e *e2e) noRedirect(u string) *http.Response {
	e.t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(u)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (e *e2e) mcpStatus(token string) int {
	req, _ := http.NewRequest("POST", e.base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

type bearer struct {
	token string
	rt    http.RoundTripper
}

func (b *bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.rt.RoundTrip(r)
}

func (e *e2e) connect(ctx context.Context, token string) *mcp.ClientSession {
	e.t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: e.base + "/mcp",
		HTTPClient: &http.Client{Transport: &bearer{token, http.DefaultTransport}}}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { cs.Close() })
	return cs
}

func (e *e2e) call(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	e.t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		e.t.Fatalf("%s: %s", name, contentText(res))
	}
	return res
}

func (e *e2e) wantErr(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any, want string) {
	e.t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	if !res.IsError || !strings.Contains(contentText(res), want) {
		e.t.Fatalf("%s: quiero error con %q, obtengo %v: %s", name, want, res.IsError, contentText(res))
	}
}

func (e *e2e) text(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) string {
	e.t.Helper()
	return contentText(e.call(ctx, cs, name, args))
}

func (e *e2e) projectIDs(ctx context.Context, cs *mcp.ClientSession) map[int64]bool {
	e.t.Helper()
	res := e.call(ctx, cs, "list_projects", map[string]any{})
	var out struct{ Projects []projectSummary }
	remarshal(e.t, res.StructuredContent, &out)
	ids := map[int64]bool{}
	for _, p := range out.Projects {
		ids[p.ID] = true
	}
	return ids
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func linkIn(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if l, ok := c.(*mcp.ResourceLink); ok {
			return l.URI
		}
	}
	t.Fatal("respuesta sin enlace")
	return ""
}

func remarshal(t *testing.T, in, out any) {
	t.Helper()
	b, _ := json.Marshal(in)
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
}

// tinyPNG es un PNG válido de 1×1.
func tinyPNG() []byte {
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	return b
}
