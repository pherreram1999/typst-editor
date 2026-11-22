package database

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return db, nil
}

var schema = []string{`CREATE TABLE IF NOT EXISTS users (
  id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  username      VARCHAR(64)  NOT NULL UNIQUE,
  name          VARCHAR(128) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role          ENUM('admin','user') NOT NULL DEFAULT 'user',
  active        TINYINT(1)   NOT NULL DEFAULT 1,
  created_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
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
}

// Migrate creates the schema and seeds the first administrator if none exists.
func Migrate(db *sql.DB, adminUser, adminPassword, adminName string) error {
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if len(adminPassword) < 8 {
		return errors.New("no existe administrador: defina ADMIN_PASSWORD (mín. 8 caracteres) para crearlo")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO users (username, name, password_hash, role) VALUES (?, ?, ?, 'admin')`,
		adminUser, adminName, string(hash))
	if err == nil {
		log.Printf("administrador inicial %q creado", adminUser)
	}
	return err
}
