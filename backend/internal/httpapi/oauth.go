package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"typsteditor/backend/internal/oauth"
)

// Servidor de autorización OAuth 2.1 del endpoint MCP.
//
// Flujo: el cliente (Claude, ChatGPT, MCP Inspector…) descubre los metadatos
// (/.well-known/oauth-protected-resource → /.well-known/oauth-authorization-server),
// se registra (POST /oauth/register), manda al usuario a GET /oauth/authorize,
// que valida la petición y redirige a la SPA (#/oauth?…). La SPA usa la sesión
// normal del usuario: muestra la aplicación y pide qué permisos y proyectos
// concede (POST /api/oauth/authorize), y redirige de vuelta con el código. El
// cliente lo canjea en POST /oauth/token (PKCE S256) por un token de acceso
// (1 h) y uno de refresco rotativo (30 días).

// mcpResource es el identificador del recurso protegido (RFC 8707/9728).
func (s *Server) mcpResource() string { return s.opts.PublicURL + "/mcp" }

// normalizeResource acepta el recurso tal como lo mandan los clientes (con o
// sin /mcp, con barra final) y lo devuelve normalizado; "" si es de otro servidor.
func (s *Server) normalizeResource(r string) string {
	r = strings.TrimRight(r, "/")
	switch r {
	case "", s.opts.PublicURL, s.mcpResource():
		return s.mcpResource()
	}
	return ""
}

// cors permite llamadas desde cualquier origen: estos endpoints no usan
// cookies (autenticación por cabecera o por el propio cuerpo).
func cors(methods string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", methods+", OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
		h.Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
		h.Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !slices.Contains(strings.Split(methods, ", "), r.Method) {
			w.Header().Set("Allow", methods)
			writeError(w, http.StatusMethodNotAllowed, "método no permitido")
			return
		}
		next(w, r)
	}
}

// asMetadata: metadatos del servidor de autorización (RFC 8414). También se
// sirven como openid-configuration, que es lo que buscan algunos clientes.
func (s *Server) asMetadata(w http.ResponseWriter, r *http.Request) {
	pub := s.opts.PublicURL
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     pub,
		"authorization_endpoint":                     pub + "/oauth/authorize",
		"token_endpoint":                             pub + "/oauth/token",
		"registration_endpoint":                      pub + "/oauth/register",
		"revocation_endpoint":                        pub + "/oauth/revoke",
		"scopes_supported":                           oauth.AllScopes,
		"response_types_supported":                   []string{"code"},
		"response_modes_supported":                   []string{"query"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_post", "client_secret_basic"},
		"revocation_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"code_challenge_methods_supported":           []string{"S256"},
		"service_documentation":                      pub + "/#/mcp",
		"ui_locales_supported":                       []string{"es"},
		"client_id_metadata_document_supported":      false,
	})
}

// ---------- registro dinámico (RFC 7591) ----------

// rateLimiter limita los registros anónimos por IP (ventana fija).
type rateLimiter struct {
	mu sync.Mutex
	m  map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	n     int
}

func (l *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.m == nil {
		l.m = map[string]*rateWindow{}
	}
	for k, v := range l.m {
		if now.Sub(v.start) > window {
			delete(l.m, k)
		}
	}
	w := l.m[key]
	if w == nil {
		w = &rateWindow{start: now}
		l.m[key] = w
	}
	w.n++
	return w.n <= limit
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// oauthError responde un error del protocolo OAuth ({error, error_description}).
func oauthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("register:"+clientIP(r), 30, 10*time.Minute) {
		oauthError(w, http.StatusTooManyRequests, "invalid_request", "demasiados registros; inténtalo más tarde")
		return
	}
	var in struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		GrantTypes   []string `json:"grant_types"`
		Response     []string `json:"response_types"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "JSON inválido")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "se requieren entre 1 y 10 redirect_uris")
		return
	}
	for _, u := range in.RedirectURIs {
		if err := oauth.ValidRedirectURI(u); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_type no soportado: "+g)
			return
		}
	}
	name := strings.TrimSpace(in.ClientName)
	if name == "" {
		name = "Aplicación sin nombre"
	}
	if utf8.RuneCountInString(name) > 100 {
		name = string([]rune(name)[:100])
	}
	method := in.AuthMethod
	switch method {
	case "":
		method = "none"
	case "none", "client_secret_post", "client_secret_basic":
	default:
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method no soportado")
		return
	}
	c, secret, err := s.oauth.RegisterClient(r.Context(), name, in.RedirectURIs, method != "none")
	if err != nil {
		log.Printf("oauth: registro: %v", err)
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "no se pudo registrar el cliente")
		return
	}
	out := map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        c.CreatedAt.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
		"scope":                      strings.Join(oauth.AllScopes, " "),
	}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
}

// ---------- autorización ----------

// authzRequest es una petición de autorización ya validada.
type authzRequest struct {
	client    *oauth.Client
	redirect  string
	state     string
	challenge string
	scopes    []string
	resource  string
}

// redirectError: error que se comunica al cliente redirigiendo con ?error=.
type redirectError struct{ code, desc string }

func (e *redirectError) Error() string { return e.desc }

// parseAuthorize valida los parámetros de /oauth/authorize. Si el cliente o la
// redirect_uri no son válidos devuelve un *apiError (no se debe redirigir);
// el resto de errores son *redirectError.
func (s *Server) parseAuthorize(ctx context.Context, q url.Values) (*authzRequest, error) {
	c, err := s.oauth.Client(ctx, q.Get("client_id"))
	if err != nil {
		return nil, errf(http.StatusBadRequest, "aplicación desconocida (client_id inválido); vuelve a conectar desde la aplicación")
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	if !oauth.RedirectMatches(c.RedirectURIs, redirect) {
		return nil, errf(http.StatusBadRequest, "redirect_uri no registrada para esta aplicación")
	}
	a := &authzRequest{client: c, redirect: redirect, state: q.Get("state")}
	if q.Get("response_type") != "code" {
		return a, &redirectError{"unsupported_response_type", "solo se admite response_type=code"}
	}
	if q.Get("code_challenge_method") != "S256" || !oauth.ValidChallenge(q.Get("code_challenge")) {
		return a, &redirectError{"invalid_request", "se requiere PKCE con code_challenge_method=S256"}
	}
	a.challenge = q.Get("code_challenge")
	if a.scopes = oauth.ParseScopes(q.Get("scope")); len(a.scopes) == 0 {
		return a, &redirectError{"invalid_scope", "scope no soportado"}
	}
	if a.resource = s.normalizeResource(q.Get("resource")); a.resource == "" {
		return a, &redirectError{"invalid_target", "resource no corresponde a este servidor"}
	}
	return a, nil
}

// redirectURL construye la redirección al cliente con los parámetros dados.
func (a *authzRequest) redirectURL(params map[string]string) string {
	u, _ := url.Parse(a.redirect)
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	if a.state != "" {
		q.Set("state", a.state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// oauthAuthorize: GET /oauth/authorize. Valida y pasa la petición a la SPA.
func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	a, err := s.parseAuthorize(r.Context(), r.URL.Query())
	var re *redirectError
	switch {
	case errors.As(err, &re):
		http.Redirect(w, r, a.redirectURL(map[string]string{"error": re.code, "error_description": re.desc}), http.StatusFound)
		return
	case err != nil:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Autorización</title>` +
			`<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;padding:0 1rem">` +
			`<h1>No se puede autorizar</h1><p>` + html.EscapeString(err.Error()) + `</p></body>`))
		return
	}
	// La SPA (con la sesión del usuario) muestra el consentimiento.
	http.Redirect(w, r, "/#/oauth?"+r.URL.RawQuery, http.StatusFound)
}

// oauthAuthorizeInfo: GET /api/oauth/authorize/info?<parámetros de /oauth/authorize>.
func (s *Server) oauthAuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	a, err := s.parseAuthorize(r.Context(), r.URL.Query())
	var re *redirectError
	if err != nil && !errors.As(err, &re) {
		writeAPIError(w, err)
		return
	}
	if re != nil {
		writeError(w, http.StatusBadRequest, re.desc)
		return
	}
	host := a.redirect
	if u, err := url.Parse(a.redirect); err == nil && u.Host != "" {
		host = u.Host
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"client_name":   a.client.Name,
		"redirect_host": host,
		"scopes":        a.scopes,
		"resource":      a.resource,
	})
}

// oauthApprove: POST /api/oauth/authorize con la decisión del usuario.
// Devuelve {"redirect": url} para que la SPA vuelva a la aplicación.
func (s *Server) oauthApprove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query       string   `json:"query"` // parámetros originales de /oauth/authorize
		Approve     bool     `json:"approve"`
		Scopes      []string `json:"scopes"`
		AllProjects bool     `json:"all_projects"`
		Projects    []int64  `json:"projects"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q, err := url.ParseQuery(strings.TrimPrefix(in.Query, "?"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "petición de autorización inválida")
		return
	}
	a, err := s.parseAuthorize(r.Context(), q)
	var re *redirectError
	switch {
	case errors.As(err, &re):
		writeJSON(w, http.StatusOK, map[string]string{"redirect": a.redirectURL(map[string]string{"error": re.code, "error_description": re.desc})})
		return
	case err != nil:
		writeAPIError(w, err)
		return
	}
	if !in.Approve {
		writeJSON(w, http.StatusOK, map[string]string{"redirect": a.redirectURL(map[string]string{
			"error": "access_denied", "error_description": "el usuario denegó el acceso"})})
		return
	}
	scopes := oauth.Intersect(oauth.ParseScopes(strings.Join(in.Scopes, " ")), a.scopes)
	if len(in.Scopes) == 0 || len(scopes) == 0 {
		writeError(w, http.StatusBadRequest, "elige al menos un permiso")
		return
	}
	u := currentUser(r)
	projects, err := s.grantableProjects(r.Context(), u, in.AllProjects, in.Projects)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	code, err := s.oauth.CreateCode(r.Context(), &oauth.Code{
		ClientID: a.client.ID, UserID: u.ID, Scopes: scopes, RedirectURI: a.redirect,
		CodeChallenge: a.challenge, Resource: a.resource, AllProjects: in.AllProjects, Projects: projects,
	})
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": a.redirectURL(map[string]string{"code": code})})
}

// grantableProjects valida la lista de proyectos de una concesión limitada:
// deben ser propios o compartidos con el usuario (por MCP no cuenta el rol de
// administrador, ver mcpUser).
func (s *Server) grantableProjects(ctx context.Context, u *User, all bool, ids []int64) ([]int64, error) {
	if all {
		return nil, nil
	}
	if len(ids) == 0 {
		return nil, errf(http.StatusBadRequest, "elige al menos un proyecto o «todos mis proyectos»")
	}
	if len(ids) > 500 {
		return nil, errf(http.StatusBadRequest, "demasiados proyectos")
	}
	mu := mcpUser(u)
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if slices.Contains(out, id) {
			continue
		}
		if _, err := s.projectByID(ctx, mu, id); err != nil {
			return nil, errf(http.StatusBadRequest, "proyecto no válido: "+strconv.FormatInt(id, 10))
		}
		out = append(out, id)
	}
	return out, nil
}

// ---------- token y revocación ----------

// clientCredentials obtiene client_id/secret del cuerpo o de Basic auth.
func clientCredentials(r *http.Request) (id, secret string) {
	if u, p, ok := r.BasicAuth(); ok {
		id, _ = url.QueryUnescape(u)
		secret, _ = url.QueryUnescape(p)
		return
	}
	return r.PostFormValue("client_id"), r.PostFormValue("client_secret")
}

// authClient comprueba el cliente que llama al endpoint de token.
func (s *Server) authClient(r *http.Request) (*oauth.Client, bool) {
	id, secret := clientCredentials(r)
	c, err := s.oauth.Client(r.Context(), id)
	if err != nil {
		return nil, false
	}
	if c.Confidential() && !c.CheckSecret(secret) {
		return nil, false
	}
	return c, true
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "cuerpo inválido")
		return
	}
	c, ok := s.authClient(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "cliente desconocido o credenciales incorrectas")
		return
	}
	var pair *oauth.TokenPair
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		code, err := s.oauth.TakeCode(r.Context(), r.PostFormValue("code"))
		if err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "código inválido, usado o caducado")
			return
		}
		if code.ClientID != c.ID || code.RedirectURI != r.PostFormValue("redirect_uri") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "el código no corresponde a este cliente o redirect_uri")
			return
		}
		if !oauth.VerifyPKCE(r.PostFormValue("code_verifier"), code.CodeChallenge) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier incorrecto")
			return
		}
		if res := r.PostFormValue("resource"); res != "" && s.normalizeResource(res) != code.Resource {
			oauthError(w, http.StatusBadRequest, "invalid_target", "resource no coincide con el autorizado")
			return
		}
		if u, err := s.userByID(r.Context(), code.UserID); err != nil || !u.Active {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "el usuario ya no está activo")
			return
		}
		if pair, err = s.oauth.GrantFromCode(r.Context(), code); err != nil {
			log.Printf("oauth: emitir tokens: %v", err)
			oauthError(w, http.StatusInternalServerError, "server_error", "no se pudo emitir el token")
			return
		}
	case "refresh_token":
		var err error
		pair, err = s.oauth.Refresh(r.Context(), r.PostFormValue("refresh_token"), c.ID)
		if errors.Is(err, oauth.ErrInvalidGrant) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "token de refresco inválido, usado o caducado")
			return
		}
		if err != nil {
			log.Printf("oauth: refrescar: %v", err)
			oauthError(w, http.StatusInternalServerError, "server_error", "no se pudo emitir el token")
			return
		}
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type no soportado")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  pair.Access,
		"token_type":    "Bearer",
		"expires_in":    pair.ExpiresIn,
		"refresh_token": pair.Refresh,
		"scope":         strings.Join(pair.Scopes, " "),
	})
}

// oauthRevoke: POST /oauth/revoke (RFC 7009).
func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "cuerpo inválido")
		return
	}
	if _, ok := s.authClient(r); !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "cliente desconocido o credenciales incorrectas")
		return
	}
	if err := s.oauth.Revoke(r.Context(), r.PostFormValue("token")); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "no se pudo revocar")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---------- gestión desde la SPA ----------

// listGrants: GET /api/mcp/grants → aplicaciones autorizadas y tokens personales.
func (s *Server) listGrants(w http.ResponseWriter, r *http.Request) {
	gs, err := s.oauth.ListGrants(r.Context(), currentUser(r).ID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mcp_url": s.mcpResource(), "grants": gs})
}

type grantInput struct {
	Scopes      []string `json:"scopes"`
	AllProjects bool     `json:"all_projects"`
	Projects    []int64  `json:"projects"`
}

func (s *Server) parseGrantInput(r *http.Request, in *grantInput) ([]string, []int64, error) {
	scopes := oauth.ParseScopes(strings.Join(in.Scopes, " "))
	if len(in.Scopes) == 0 || len(scopes) == 0 {
		return nil, nil, errf(http.StatusBadRequest, "elige al menos un permiso")
	}
	projects, err := s.grantableProjects(r.Context(), currentUser(r), in.AllProjects, in.Projects)
	return scopes, projects, err
}

// createPersonalToken: POST /api/mcp/tokens → {"token": "tse_…"} (solo se muestra esta vez).
func (s *Server) createPersonalToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		grantInput
		Name        string `json:"name"`
		ExpiresDays int    `json:"expires_days"` // 0 = sin caducidad
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 128 {
		writeError(w, http.StatusBadRequest, "nombre requerido (máx. 128)")
		return
	}
	if in.ExpiresDays < 0 || in.ExpiresDays > 3650 {
		writeError(w, http.StatusBadRequest, "caducidad inválida")
		return
	}
	scopes, projects, err := s.parseGrantInput(r, &in.grantInput)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	var exp *time.Time
	if in.ExpiresDays > 0 {
		t := time.Now().Add(time.Duration(in.ExpiresDays) * 24 * time.Hour)
		exp = &t
	}
	tok, id, err := s.oauth.CreatePersonal(r.Context(), currentUser(r).ID, name, scopes, in.AllProjects, projects, exp)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "token": tok, "mcp_url": s.mcpResource()})
}

// updateGrant: PUT /api/mcp/grants/{id} {scopes, all_projects, projects}.
func (s *Server) updateGrant(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var in grantInput
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopes, projects, err := s.parseGrantInput(r, &in)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	err = s.oauth.UpdateGrant(r.Context(), currentUser(r).ID, id, scopes, in.AllProjects, projects)
	if errors.Is(err, oauth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "acceso no encontrado")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteGrant: DELETE /api/mcp/grants/{id} (revoca la aplicación o el token).
func (s *Server) deleteGrant(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	err = s.oauth.DeleteGrant(r.Context(), currentUser(r).ID, id)
	if errors.Is(err, oauth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "acceso no encontrado")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
