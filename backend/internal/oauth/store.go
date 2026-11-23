package oauth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"typsteditor/backend/internal/database"
)

const (
	CodeTTL    = 10 * time.Minute
	AccessTTL  = time.Hour
	RefreshTTL = 30 * 24 * time.Hour
	// Máximo de clientes registrados dinámicamente (el registro es anónimo).
	MaxClients = 5000
)

var (
	ErrNotFound     = errors.New("no encontrado")
	ErrInvalidGrant = errors.New("código o token inválido o caducado")
)

// Store guarda clientes, códigos, concesiones y tokens en la base de datos.
type Store struct {
	DB *sql.DB
	D  database.Dialect
}

type Client struct {
	ID           string    `json:"client_id"`
	Name         string    `json:"client_name"`
	RedirectURIs []string  `json:"redirect_uris"`
	CreatedAt    time.Time `json:"created_at"`
	secretHash   string    // vacío: cliente público (solo PKCE)
}

// Confidential indica si el cliente se registró con secreto.
func (c *Client) Confidential() bool { return c.secretHash != "" }

// CheckSecret compara el secreto presentado con el del cliente.
func (c *Client) CheckSecret(secret string) bool {
	return c.secretHash != "" && subtle.ConstantTimeCompare([]byte(Hash(secret)), []byte(c.secretHash)) == 1
}

// Grant es una autorización del usuario: una aplicación OAuth o un token personal.
type Grant struct {
	ID          int64      `json:"id"`
	UserID      int64      `json:"-"`
	ClientID    string     `json:"client_id,omitempty"`
	ClientName  string     `json:"client_name,omitempty"`
	Name        string     `json:"name"`
	Personal    bool       `json:"personal"`
	Scopes      []string   `json:"scopes"`
	AllProjects bool       `json:"all_projects"`
	Projects    []int64    `json:"projects"`
	Resource    string     `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at"` // solo tokens personales
}

// Code es un código de autorización pendiente de canjear.
type Code struct {
	ClientID      string
	UserID        int64
	Scopes        []string
	RedirectURI   string
	CodeChallenge string
	Resource      string
	AllProjects   bool
	Projects      []int64
}

// ---------- clientes ----------

// RegisterClient registra un cliente. Con confidential se genera un secreto,
// que se devuelve una sola vez (en la base de datos queda su hash).
func (s *Store) RegisterClient(ctx context.Context, name string, uris []string, confidential bool) (*Client, string, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM oauth_clients`).Scan(&n); err != nil {
		return nil, "", err
	}
	if n >= MaxClients {
		// Se descartan los clientes que nunca llegaron a usarse.
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM oauth_clients WHERE last_used_at IS NULL AND created_at < ?`,
			time.Now().Add(-24*time.Hour))
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM oauth_clients`).Scan(&n); err != nil {
			return nil, "", err
		}
		if n >= MaxClients {
			return nil, "", errors.New("demasiados clientes registrados")
		}
	}
	c := &Client{ID: NewClientID(), Name: name, RedirectURIs: uris, CreatedAt: time.Now().UTC().Truncate(time.Second)}
	secret := ""
	var secretHash sql.NullString
	if confidential {
		secret = NewToken("tss_")
		c.secretHash = Hash(secret)
		secretHash = sql.NullString{String: c.secretHash, Valid: true}
	}
	raw, _ := json.Marshal(uris)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_clients (id, name, redirect_uris, secret_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.Name, string(raw), secretHash, c.CreatedAt)
	return c, secret, err
}

func (s *Store) Client(ctx context.Context, id string) (*Client, error) {
	c := &Client{}
	var raw string
	var secret sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, redirect_uris, secret_hash, created_at FROM oauth_clients WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &raw, &secret, &c.CreatedAt)
	c.secretHash = secret.String
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(raw), &c.RedirectURIs)
	return c, nil
}

// ---------- códigos ----------

func (s *Store) CreateCode(ctx context.Context, c *Code) (string, error) {
	code := NewToken(prefixCode)
	projects, _ := json.Marshal(nonNil(c.Projects))
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM oauth_codes WHERE expires_at < ?`, time.Now())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_codes
  (code_hash, client_id, user_id, scopes, redirect_uri, code_challenge, resource, all_projects, projects, expires_at)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		Hash(code), c.ClientID, c.UserID, strings.Join(c.Scopes, " "), c.RedirectURI, c.CodeChallenge, c.Resource,
		c.AllProjects, string(projects), time.Now().Add(CodeTTL))
	return code, err
}

// TakeCode consume un código (solo sirve una vez) y lo devuelve.
func (s *Store) TakeCode(ctx context.Context, code string) (*Code, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c := &Code{}
	var scopes, projects string
	var exp time.Time
	err = tx.QueryRowContext(ctx, `SELECT client_id, user_id, scopes, redirect_uri, code_challenge, resource, all_projects, projects, expires_at
  FROM oauth_codes WHERE code_hash = ?`+s.D.ForUpdate(), Hash(code)).
		Scan(&c.ClientID, &c.UserID, &scopes, &c.RedirectURI, &c.CodeChallenge, &c.Resource, &c.AllProjects, &projects, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE code_hash = ?`, Hash(code)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if time.Now().After(exp) {
		return nil, ErrInvalidGrant
	}
	c.Scopes = strings.Fields(scopes)
	_ = json.Unmarshal([]byte(projects), &c.Projects)
	return c, nil
}

// ---------- concesiones y tokens ----------

// TokenPair es la respuesta del endpoint de token.
type TokenPair struct {
	Access    string
	Refresh   string
	ExpiresIn int
	Scopes    []string
}

// GrantFromCode crea la concesión de una aplicación y su primer par de tokens.
// Si el usuario ya había autorizado a ese cliente, la concesión anterior se
// sustituye (así la lista de aplicaciones no se llena de duplicados).
func (s *Store) GrantFromCode(ctx context.Context, c *Code) (*TokenPair, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_grants WHERE user_id = ? AND client_id = ?`, c.UserID, c.ClientID); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO api_grants (user_id, client_id, scopes, all_projects, resource, last_used_at)
  VALUES (?, ?, ?, ?, ?, ?)`, c.UserID, c.ClientID, strings.Join(c.Scopes, " "), c.AllProjects, c.Resource, time.Now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := s.setProjects(ctx, tx, id, c.AllProjects, c.Projects); err != nil {
		return nil, err
	}
	pair, err := issuePair(ctx, tx, id, c.Scopes)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_clients SET last_used_at = ? WHERE id = ?`, time.Now(), c.ClientID); err != nil {
		return nil, err
	}
	return pair, tx.Commit()
}

func issuePair(ctx context.Context, tx *sql.Tx, grantID int64, scopes []string) (*TokenPair, error) {
	p := &TokenPair{Access: NewToken(PrefixAccess), Refresh: NewToken(PrefixRefresh), ExpiresIn: int(AccessTTL.Seconds()), Scopes: scopes}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (token_hash, grant_id, kind, expires_at) VALUES (?, ?, 'access', ?), (?, ?, 'refresh', ?)`,
		Hash(p.Access), grantID, now.Add(AccessTTL), Hash(p.Refresh), grantID, now.Add(RefreshTTL)); err != nil {
		return nil, err
	}
	// Limpieza de tokens caducados de la misma concesión.
	_, _ = tx.ExecContext(ctx, `DELETE FROM api_tokens WHERE grant_id = ? AND expires_at < ?`, grantID, now)
	return p, nil
}

// Refresh canjea un token de refresco por un par nuevo (rotación). Si el
// token ya se había usado (posible robo) se revoca toda la concesión.
func (s *Store) Refresh(ctx context.Context, refresh, clientID string) (*TokenPair, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var (
		grantID int64
		exp     time.Time
		used    sql.NullTime
		client  sql.NullString
		scopes  string
	)
	err = tx.QueryRowContext(ctx, `SELECT t.grant_id, t.expires_at, t.used_at, g.client_id, g.scopes
  FROM api_tokens t JOIN api_grants g ON g.id = t.grant_id
  WHERE t.token_hash = ? AND t.kind = 'refresh'`+s.D.ForUpdate(), Hash(refresh)).Scan(&grantID, &exp, &used, &client, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	if client.String != clientID {
		return nil, ErrInvalidGrant
	}
	if used.Valid {
		// Un refresco reutilizado: alguien más tiene una copia. Fuera todo.
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_grants WHERE id = ?`, grantID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, ErrInvalidGrant
	}
	if time.Now().After(exp) {
		return nil, ErrInvalidGrant
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET used_at = ? WHERE token_hash = ?`, time.Now(), Hash(refresh)); err != nil {
		return nil, err
	}
	pair, err := issuePair(ctx, tx, grantID, strings.Fields(scopes))
	if err != nil {
		return nil, err
	}
	return pair, tx.Commit()
}

// CreatePersonal crea un token personal (sin caducidad si expires es nil).
func (s *Store) CreatePersonal(ctx context.Context, userID int64, name string, scopes []string, all bool, projects []int64, expires *time.Time) (string, int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO api_grants (user_id, name, scopes, all_projects) VALUES (?, ?, ?, ?)`,
		userID, name, strings.Join(scopes, " "), all)
	if err != nil {
		return "", 0, err
	}
	id, _ := res.LastInsertId()
	if err := s.setProjects(ctx, tx, id, all, projects); err != nil {
		return "", 0, err
	}
	tok := NewToken(PrefixPersonal)
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (token_hash, grant_id, kind, expires_at) VALUES (?, ?, 'personal', ?)`,
		Hash(tok), id, expires); err != nil {
		return "", 0, err
	}
	return tok, id, tx.Commit()
}

func (s *Store) setProjects(ctx context.Context, tx *sql.Tx, grantID int64, all bool, projects []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_grant_projects WHERE grant_id = ?`, grantID); err != nil {
		return err
	}
	if all {
		return nil
	}
	for _, p := range projects {
		// INSERT IGNORE: un proyecto borrado entretanto no rompe la autorización.
		if _, err := tx.ExecContext(ctx, s.D.InsertIgnore()+` INTO api_grant_projects (grant_id, project_id)
  SELECT ?, id FROM projects WHERE id = ?`, grantID, p); err != nil {
			return err
		}
	}
	return nil
}

// Verified es el resultado de comprobar un token de acceso o personal.
type Verified struct {
	GrantID     int64
	UserID      int64
	Scopes      []string
	AllProjects bool
	Projects    []int64
	Resource    string
	Expires     time.Time // cero si el token personal no caduca
}

// Verify comprueba un token de acceso o personal.
func (s *Store) Verify(ctx context.Context, token string) (*Verified, error) {
	if !strings.HasPrefix(token, PrefixAccess) && !strings.HasPrefix(token, PrefixPersonal) {
		return nil, ErrInvalidGrant
	}
	v := &Verified{}
	var (
		exp      sql.NullTime
		lastUsed sql.NullTime
		scopes   string
	)
	err := s.DB.QueryRowContext(ctx, `SELECT g.id, g.user_id, g.scopes, g.all_projects, g.resource, t.expires_at, g.last_used_at
  FROM api_tokens t JOIN api_grants g ON g.id = t.grant_id
  WHERE t.token_hash = ? AND t.kind IN ('access', 'personal')`, Hash(token)).
		Scan(&v.GrantID, &v.UserID, &scopes, &v.AllProjects, &v.Resource, &exp, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	if exp.Valid {
		if time.Now().After(exp.Time) {
			return nil, ErrInvalidGrant
		}
		v.Expires = exp.Time
	}
	v.Scopes = strings.Fields(scopes)
	if !v.AllProjects {
		if v.Projects, err = s.grantProjects(ctx, v.GrantID); err != nil {
			return nil, err
		}
	}
	// Último uso con un minuto de resolución (evita escribir en cada petición).
	if !lastUsed.Valid || time.Since(lastUsed.Time) > time.Minute {
		_, _ = s.DB.ExecContext(ctx, `UPDATE api_grants SET last_used_at = ? WHERE id = ?`, time.Now(), v.GrantID)
	}
	return v, nil
}

func (s *Store) grantProjects(ctx context.Context, grantID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT project_id FROM api_grant_projects WHERE grant_id = ? ORDER BY project_id`, grantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GrantAccess vuelve a leer una concesión (p. ej. al usar un enlace creado con
// ella). ErrInvalidGrant si se revocó.
func (s *Store) GrantAccess(ctx context.Context, grantID int64) (*Verified, error) {
	v := &Verified{GrantID: grantID}
	var scopes string
	err := s.DB.QueryRowContext(ctx, `SELECT user_id, scopes, all_projects, resource FROM api_grants WHERE id = ?`, grantID).
		Scan(&v.UserID, &scopes, &v.AllProjects, &v.Resource)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	v.Scopes = strings.Fields(scopes)
	if !v.AllProjects {
		if v.Projects, err = s.grantProjects(ctx, grantID); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// AddProject añade un proyecto a una concesión limitada (no hace nada si es de todos).
func (s *Store) AddProject(ctx context.Context, grantID, projectID int64) error {
	_, err := s.DB.ExecContext(ctx, s.D.InsertIgnore()+` INTO api_grant_projects (grant_id, project_id)
  SELECT id, ? FROM api_grants WHERE id = ? AND all_projects = 0`, projectID, grantID)
	return err
}

// Revoke revoca un token (RFC 7009). Revocar un refresco o un token personal
// revoca la concesión entera; un token de acceso, solo ese token.
func (s *Store) Revoke(ctx context.Context, token string) error {
	var grantID int64
	var kind string
	err := s.DB.QueryRowContext(ctx, `SELECT grant_id, kind FROM api_tokens WHERE token_hash = ?`, Hash(token)).Scan(&grantID, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // RFC 7009 §2.2: un token desconocido también responde 200
	}
	if err != nil {
		return err
	}
	if kind == "access" {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE token_hash = ?`, Hash(token))
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM api_grants WHERE id = ?`, grantID)
	return err
}

// ---------- gestión desde la SPA ----------

// ListGrants devuelve las aplicaciones autorizadas y tokens personales del usuario.
func (s *Store) ListGrants(ctx context.Context, userID int64) ([]*Grant, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.id, COALESCE(g.client_id, ''), COALESCE(c.name, ''), g.name, g.scopes,
  g.all_projects, g.created_at, g.last_used_at,
  (SELECT MAX(t.expires_at) FROM api_tokens t WHERE t.grant_id = g.id AND t.kind = 'personal'),
  (SELECT GROUP_CONCAT(gp.project_id) FROM api_grant_projects gp WHERE gp.grant_id = g.id)
FROM api_grants g LEFT JOIN oauth_clients c ON c.id = g.client_id
WHERE g.user_id = ? ORDER BY g.created_at DESC, g.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Grant{}
	for rows.Next() {
		g := &Grant{UserID: userID}
		var scopes string
		var last sql.NullTime
		var exp database.NullTime // MAX(…): en libSQL llega como texto
		var projects sql.NullString
		if err := rows.Scan(&g.ID, &g.ClientID, &g.ClientName, &g.Name, &scopes, &g.AllProjects, &g.CreatedAt, &last, &exp, &projects); err != nil {
			return nil, err
		}
		g.Personal = g.ClientID == ""
		g.Scopes = strings.Fields(scopes)
		if last.Valid {
			g.LastUsedAt = &last.Time
		}
		if exp.Valid {
			g.ExpiresAt = &exp.Time
		}
		g.Projects = []int64{}
		for _, p := range strings.Split(projects.String, ",") {
			if id, err := strconv.ParseInt(p, 10, 64); err == nil {
				g.Projects = append(g.Projects, id)
			}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateGrant cambia los scopes y proyectos de una concesión del usuario.
func (s *Store) UpdateGrant(ctx context.Context, userID, id int64, scopes []string, all bool, projects []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE api_grants SET scopes = ?, all_projects = ? WHERE id = ? AND user_id = ?`,
		strings.Join(scopes, " "), all, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var x int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM api_grants WHERE id = ? AND user_id = ?`, id, userID).Scan(&x); err != nil {
			return ErrNotFound
		}
	}
	if err := s.setProjects(ctx, tx, id, all, projects); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteGrant revoca una concesión del usuario (y todos sus tokens).
func (s *Store) DeleteGrant(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM api_grants WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func nonNil(p []int64) []int64 {
	if p == nil {
		return []int64{}
	}
	return p
}
