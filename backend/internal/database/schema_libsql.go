package database

import (
	"database/sql"
	"fmt"
	"log"
)

// Esquema de libSQL/SQLite: las mismas tablas que schema_mysql.go.
//   - Las fechas se declaran TIMESTAMP: el driver convierte esas columnas a
//     time.Time al leerlas. Se guardan como texto UTC "AAAA-MM-DD hh:mm:ss".
//   - ENUM pasa a CHECK; TINYINT(1) a INTEGER (0/1).
//   - username es UNIQUE COLLATE NOCASE, como utf8mb4_unicode_ci en MariaDB
//     (aquí solo para ASCII).
//   - updated_at se mantiene con triggers (MariaDB: ON UPDATE CURRENT_TIMESTAMP).
//   - SQLite no tiene ADD COLUMN IF NOT EXISTS: una migración futura de una
//     tabla existente debe comprobar antes pragma_table_info.
var libsqlSchema = []string{`CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT      NOT NULL UNIQUE COLLATE NOCASE,
  email         TEXT      NULL,
  name          TEXT      NOT NULL,
  password_hash TEXT      NOT NULL,
  role          TEXT      NOT NULL DEFAULT 'user' CHECK (role IN ('admin','user')),
  active        INTEGER   NOT NULL DEFAULT 1,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
	`CREATE TABLE IF NOT EXISTS github_accounts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label       TEXT      NOT NULL DEFAULT '',
  login       TEXT      NOT NULL,
  is_org      INTEGER   NOT NULL DEFAULT 0,
  token       TEXT      NOT NULL,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (user_id, login)
)`,
	`CREATE TABLE IF NOT EXISTS projects (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT      NOT NULL,
  main_file   TEXT      NOT NULL DEFAULT 'main.typ',
  owner_id    INTEGER   NOT NULL REFERENCES users(id),
  github_account_id INTEGER NULL REFERENCES github_accounts(id) ON DELETE SET NULL,
  git_remote  TEXT      NOT NULL DEFAULT '',
  git_branch  TEXT      NOT NULL DEFAULT 'main',
  is_template          INTEGER NOT NULL DEFAULT 0,
  template_category    TEXT    NOT NULL DEFAULT '',
  template_description TEXT    NOT NULL DEFAULT '',
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
	`CREATE TABLE IF NOT EXISTS project_members (
  project_id  INTEGER   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id     INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  invited_by  INTEGER   NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (project_id, user_id)
)`,
	`CREATE INDEX IF NOT EXISTS idx_members_user ON project_members (user_id)`,
	`CREATE TABLE IF NOT EXISTS project_folders (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT      NOT NULL,
  color       TEXT      NOT NULL DEFAULT 'blue',
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (user_id, name)
)`,
	`CREATE TABLE IF NOT EXISTS project_user_prefs (
  user_id     INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id  INTEGER   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  folder_id   INTEGER   NULL REFERENCES project_folders(id) ON DELETE SET NULL,
  starred     INTEGER   NOT NULL DEFAULT 0,
  starred_at  TIMESTAMP NULL,
  PRIMARY KEY (user_id, project_id)
)`,
	`CREATE INDEX IF NOT EXISTS idx_prefs_project ON project_user_prefs (project_id)`,
	`CREATE TABLE IF NOT EXISTS project_reviews (
  project_id     INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  token          TEXT      NOT NULL UNIQUE,
  allow_download INTEGER   NOT NULL DEFAULT 1,
  created_by     INTEGER   NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  compiled_at    TIMESTAMP NULL,
  compiled_by    INTEGER   NULL REFERENCES users(id) ON DELETE SET NULL
)`,
	// Enlaces de invitación: URL firmadas (HMAC) que caducan y dan acceso de
	// invitado, sin cuenta, a un proyecto (ver httpapi/invites.go).
	`CREATE TABLE IF NOT EXISTS project_invites (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id   INTEGER   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  nonce        TEXT      NOT NULL,
  label        TEXT      NOT NULL DEFAULT '',
  permission   TEXT      NOT NULL DEFAULT 'edit' CHECK (permission IN ('edit','view')),
  created_by   INTEGER   NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at   TIMESTAMP NOT NULL,
  joins        INTEGER   NOT NULL DEFAULT 0,
  last_used_at TIMESTAMP NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_invites_project ON project_invites (project_id)`,
	`CREATE TABLE IF NOT EXISTS oauth_clients (
  id            TEXT PRIMARY KEY,
  name          TEXT      NOT NULL DEFAULT '',
  redirect_uris TEXT      NOT NULL,
  secret_hash   TEXT      NULL,
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_used_at  TIMESTAMP NULL
)`,
	`CREATE TABLE IF NOT EXISTS oauth_codes (
  code_hash      TEXT PRIMARY KEY,
  client_id      TEXT      NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  user_id        INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scopes         TEXT      NOT NULL,
  redirect_uri   TEXT      NOT NULL,
  code_challenge TEXT      NOT NULL,
  resource       TEXT      NOT NULL DEFAULT '',
  all_projects   INTEGER   NOT NULL DEFAULT 1,
  projects       TEXT      NOT NULL,
  expires_at     TIMESTAMP NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS api_grants (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  client_id    TEXT      NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  name         TEXT      NOT NULL DEFAULT '',
  scopes       TEXT      NOT NULL,
  all_projects INTEGER   NOT NULL DEFAULT 1,
  resource     TEXT      NOT NULL DEFAULT '',
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_used_at TIMESTAMP NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_grants_user ON api_grants (user_id)`,
	`CREATE TABLE IF NOT EXISTS api_tokens (
  token_hash TEXT PRIMARY KEY,
  grant_id   INTEGER   NOT NULL REFERENCES api_grants(id) ON DELETE CASCADE,
  kind       TEXT      NOT NULL CHECK (kind IN ('access','refresh','personal')),
  expires_at TIMESTAMP NULL,
  used_at    TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
	`CREATE INDEX IF NOT EXISTS idx_tokens_grant ON api_tokens (grant_id)`,
	`CREATE TABLE IF NOT EXISTS api_grant_projects (
  grant_id   INTEGER NOT NULL REFERENCES api_grants(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  PRIMARY KEY (grant_id, project_id)
)`,
	// Correo: enlaces de un solo uso para definir o restablecer la contraseña y
	// confirmar un correo (httpapi/accounts.go) e invitaciones a un proyecto
	// enviadas a un correo sin cuenta (httpapi/emailinvites.go).
	`CREATE TABLE IF NOT EXISTS account_tokens (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose    TEXT      NOT NULL CHECK (purpose IN ('setup','reset','email')),
  nonce      TEXT      NOT NULL,
  email      TEXT      NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL,
  used_at    TIMESTAMP NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_account_tokens_user ON account_tokens (user_id)`,
	`CREATE TABLE IF NOT EXISTS project_email_invites (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id  INTEGER   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  email       TEXT      NOT NULL,
  nonce       TEXT      NOT NULL,
  invited_by  INTEGER   NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at  TIMESTAMP NOT NULL,
  accepted_at TIMESTAMP NULL,
  accepted_by INTEGER   NULL REFERENCES users(id) ON DELETE SET NULL
)`,
	`CREATE INDEX IF NOT EXISTS idx_email_invites_project ON project_email_invites (project_id)`,
	`CREATE INDEX IF NOT EXISTS idx_email_invites_email ON project_email_invites (email)`,
	// updated_at: solo si la sentencia no lo fijó ella misma.
	updatedTrigger("users"),
	updatedTrigger("github_accounts"),
	updatedTrigger("projects"),
}

func updatedTrigger(table string) string {
	return `CREATE TRIGGER IF NOT EXISTS trg_` + table + `_updated AFTER UPDATE ON ` + table + `
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE ` + table + ` SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END`
}

// libsqlColumns: columnas añadidas después de crear una tabla. SQLite no tiene
// ADD COLUMN IF NOT EXISTS: migrateLibSQL comprueba antes pragma_table_info.
var libsqlColumns = []struct{ table, column, def string }{
	{"users", "email", "TEXT NULL"},
	{"users", "must_change_password", "INTEGER NOT NULL DEFAULT 0"},
}

// libsqlAfterColumns se ejecuta después de añadir las columnas que usan.
var libsqlAfterColumns = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email ON users (email)`,
}

func migrateLibSQL(db *sql.DB) error {
	for _, c := range libsqlColumns {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n); err != nil {
			return fmt.Errorf("columnas de %s: %w", c.table, err)
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.def); err != nil {
				return fmt.Errorf("añadir %s.%s: %w", c.table, c.column, err)
			}
			log.Printf("migración: columna %s.%s añadida", c.table, c.column)
		}
	}
	for _, stmt := range libsqlAfterColumns {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
