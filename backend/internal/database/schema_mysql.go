package database

// Esquema de MariaDB: CREATE TABLE IF NOT EXISTS y, para instalaciones
// anteriores, ALTER TABLE … IF [NOT] EXISTS idempotentes (sintaxis de MariaDB).
// Debe coincidir con database/01_setup.sql y con schema_libsql.go.
var mysqlSchema = []string{`CREATE TABLE IF NOT EXISTS users (
  id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  username      VARCHAR(64)  NOT NULL UNIQUE,
  email         VARCHAR(255) NULL,
  name          VARCHAR(128) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role          ENUM('admin','user') NOT NULL DEFAULT 'user',
  active        TINYINT(1)   NOT NULL DEFAULT 1,
  must_change_password TINYINT(1) NOT NULL DEFAULT 0,
  created_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// Correo de cada usuario (inicio de sesión, alta y avisos); NULL en las
	// cuentas anteriores hasta que lo definan. password_hash vacío = cuenta
	// pendiente de definir su contraseña (no puede iniciar sesión).
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS email VARCHAR(255) NULL AFTER username`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email ON users (email)`,
	// Contraseña temporal (alta o restablecimiento sin correo): hay que
	// cambiarla al iniciar sesión antes de poder usar la aplicación.
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password TINYINT(1) NOT NULL DEFAULT 0 AFTER active`,
	// Cuentas de GitHub (usuario + token) de cada usuario; los proyectos las referencian.
	`CREATE TABLE IF NOT EXISTS github_accounts (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id     BIGINT UNSIGNED NOT NULL,
  label       VARCHAR(128) NOT NULL DEFAULT '',
  login       VARCHAR(100) NOT NULL,
  is_org      TINYINT(1)   NOT NULL DEFAULT 0,
  token       VARCHAR(512) NOT NULL,
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_github_user_login (user_id, login),
  CONSTRAINT fk_github_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS projects (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  name        VARCHAR(128) NOT NULL,
  main_file   VARCHAR(255) NOT NULL DEFAULT 'main.typ',
  owner_id    BIGINT UNSIGNED NOT NULL,
  github_account_id BIGINT UNSIGNED NULL,
  git_remote  VARCHAR(512) NOT NULL DEFAULT '',
  git_branch  VARCHAR(128) NOT NULL DEFAULT 'main',
  is_template          TINYINT(1)   NOT NULL DEFAULT 0,
  template_category    VARCHAR(64)  NOT NULL DEFAULT '',
  template_description VARCHAR(255) NOT NULL DEFAULT '',
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT fk_projects_owner FOREIGN KEY (owner_id) REFERENCES users(id),
  CONSTRAINT fk_projects_github FOREIGN KEY (github_account_id) REFERENCES github_accounts(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// Migración de instalaciones anteriores (el token ya no vive en el proyecto).
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS github_account_id BIGINT UNSIGNED NULL AFTER owner_id`,
	`ALTER TABLE projects ADD CONSTRAINT fk_projects_github FOREIGN KEY IF NOT EXISTS (github_account_id) REFERENCES github_accounts(id) ON DELETE SET NULL`,
	`ALTER TABLE projects DROP COLUMN IF EXISTS git_token`,
	// Cuentas de organización: login es la organización, no el dueño del token.
	`ALTER TABLE github_accounts ADD COLUMN IF NOT EXISTS is_org TINYINT(1) NOT NULL DEFAULT 0 AFTER login`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS is_template TINYINT(1) NOT NULL DEFAULT 0 AFTER git_branch`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS template_category VARCHAR(64) NOT NULL DEFAULT '' AFTER is_template`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS template_description VARCHAR(255) NOT NULL DEFAULT '' AFTER template_category`,
	// Usuarios invitados a un proyecto (colaboradores con permiso de edición).
	`CREATE TABLE IF NOT EXISTS project_members (
  project_id  BIGINT UNSIGNED NOT NULL,
  user_id     BIGINT UNSIGNED NOT NULL,
  invited_by  BIGINT UNSIGNED NULL,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (project_id, user_id),
  KEY idx_members_user (user_id),
  CONSTRAINT fk_members_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_members_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_members_inviter FOREIGN KEY (invited_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// Organización personal de cada usuario: carpetas y proyectos destacados.
	`CREATE TABLE IF NOT EXISTS project_folders (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id     BIGINT UNSIGNED NOT NULL,
  name        VARCHAR(64)  NOT NULL,
  color       VARCHAR(16)  NOT NULL DEFAULT 'blue',
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uq_folder_user_name (user_id, name),
  CONSTRAINT fk_folders_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS project_user_prefs (
  user_id     BIGINT UNSIGNED NOT NULL,
  project_id  BIGINT UNSIGNED NOT NULL,
  folder_id   BIGINT UNSIGNED NULL,
  starred     TINYINT(1)   NOT NULL DEFAULT 0,
  starred_at  TIMESTAMP    NULL,
  PRIMARY KEY (user_id, project_id),
  KEY idx_prefs_project (project_id),
  CONSTRAINT fk_prefs_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_prefs_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_prefs_folder FOREIGN KEY (folder_id) REFERENCES project_folders(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`ALTER TABLE project_folders ADD COLUMN IF NOT EXISTS color VARCHAR(16) NOT NULL DEFAULT 'blue' AFTER name`,
	// Enlace público de revisión (una compilación guardada por proyecto).
	`CREATE TABLE IF NOT EXISTS project_reviews (
  project_id     BIGINT UNSIGNED PRIMARY KEY,
  token          CHAR(43)     NOT NULL,
  allow_download TINYINT(1)   NOT NULL DEFAULT 1,
  created_by     BIGINT UNSIGNED NULL,
  created_at     TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  compiled_at    TIMESTAMP    NULL,
  compiled_by    BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_reviews_token (token),
  CONSTRAINT fk_reviews_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_reviews_creator FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_reviews_compiler FOREIGN KEY (compiled_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// Enlaces de invitación: URL firmadas (HMAC) que caducan y dan acceso de
	// invitado, sin cuenta, a un proyecto (ver httpapi/invites.go).
	`CREATE TABLE IF NOT EXISTS project_invites (
  id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  project_id   BIGINT UNSIGNED NOT NULL,
  nonce        CHAR(22)     NOT NULL,
  label        VARCHAR(100) NOT NULL DEFAULT '',
  permission   ENUM('edit','view') NOT NULL DEFAULT 'edit',
  created_by   BIGINT UNSIGNED NULL,
  created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at   TIMESTAMP    NOT NULL,
  joins        INT UNSIGNED NOT NULL DEFAULT 0,
  last_used_at TIMESTAMP    NULL,
  KEY idx_invites_project (project_id),
  CONSTRAINT fk_invites_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_invites_creator FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// Acceso de agentes (MCP): clientes OAuth registrados dinámicamente,
	// códigos de autorización, concesiones (una por aplicación autorizada o
	// token personal) con sus tokens (solo el SHA-256) y, si la concesión se
	// limita a ciertos proyectos, cuáles.
	`CREATE TABLE IF NOT EXISTS oauth_clients (
  id            VARCHAR(64)  PRIMARY KEY,
  name          VARCHAR(255) NOT NULL DEFAULT '',
  redirect_uris TEXT         NOT NULL,
  secret_hash   CHAR(64)     NULL,
  created_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_used_at  TIMESTAMP    NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS oauth_codes (
  code_hash      CHAR(64)     PRIMARY KEY,
  client_id      VARCHAR(64)  NOT NULL,
  user_id        BIGINT UNSIGNED NOT NULL,
  scopes         VARCHAR(255) NOT NULL,
  redirect_uri   VARCHAR(2048) NOT NULL,
  code_challenge VARCHAR(128) NOT NULL,
  resource       VARCHAR(512) NOT NULL DEFAULT '',
  all_projects   TINYINT(1)   NOT NULL DEFAULT 1,
  projects       TEXT         NOT NULL,
  expires_at     TIMESTAMP    NOT NULL,
  CONSTRAINT fk_codes_client FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE,
  CONSTRAINT fk_codes_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS api_grants (
  id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id      BIGINT UNSIGNED NOT NULL,
  client_id    VARCHAR(64)  NULL,
  name         VARCHAR(128) NOT NULL DEFAULT '',
  scopes       VARCHAR(255) NOT NULL,
  all_projects TINYINT(1)   NOT NULL DEFAULT 1,
  resource     VARCHAR(512) NOT NULL DEFAULT '',
  created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_used_at TIMESTAMP    NULL,
  KEY idx_grants_user (user_id),
  CONSTRAINT fk_grants_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_grants_client FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS api_tokens (
  token_hash CHAR(64)  PRIMARY KEY,
  grant_id   BIGINT UNSIGNED NOT NULL,
  kind       ENUM('access','refresh','personal') NOT NULL,
  expires_at TIMESTAMP NULL,
  used_at    TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_tokens_grant (grant_id),
  CONSTRAINT fk_tokens_grant FOREIGN KEY (grant_id) REFERENCES api_grants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS api_grant_projects (
  grant_id   BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (grant_id, project_id),
  CONSTRAINT fk_gp_grant FOREIGN KEY (grant_id) REFERENCES api_grants(id) ON DELETE CASCADE,
  CONSTRAINT fk_gp_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// Correo: enlaces de un solo uso para definir o restablecer la contraseña y
	// confirmar un correo (httpapi/accounts.go) e invitaciones a un proyecto
	// enviadas a un correo sin cuenta (httpapi/emailinvites.go).
	`CREATE TABLE IF NOT EXISTS account_tokens (
  id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id    BIGINT UNSIGNED NOT NULL,
  purpose    ENUM('setup','reset','email') NOT NULL,
  nonce      CHAR(22)     NOT NULL,
  email      VARCHAR(255) NULL,
  created_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP    NOT NULL,
  used_at    TIMESTAMP    NULL,
  KEY idx_account_tokens_user (user_id),
  CONSTRAINT fk_account_tokens_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS project_email_invites (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  project_id  BIGINT UNSIGNED NOT NULL,
  email       VARCHAR(255) NOT NULL,
  nonce       CHAR(22)     NOT NULL,
  invited_by  BIGINT UNSIGNED NULL,
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at  TIMESTAMP    NOT NULL,
  accepted_at TIMESTAMP    NULL,
  accepted_by BIGINT UNSIGNED NULL,
  KEY idx_email_invites_project (project_id),
  KEY idx_email_invites_email (email),
  CONSTRAINT fk_email_invites_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_email_invites_inviter FOREIGN KEY (invited_by) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_email_invites_accepted FOREIGN KEY (accepted_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
}
