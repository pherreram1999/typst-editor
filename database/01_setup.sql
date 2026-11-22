-- Ejecutar como root de MariaDB:
--   sudo mariadb < database/01_setup.sql
-- Cambia la contraseña antes de usarlo en producción y colócala en backend/.env (DB_PASSWORD).

CREATE DATABASE IF NOT EXISTS typsteditor
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE USER IF NOT EXISTS 'typsteditor'@'localhost' IDENTIFIED BY 'cambiar_esta_clave';
GRANT ALL PRIVILEGES ON typsteditor.* TO 'typsteditor'@'localhost';
FLUSH PRIVILEGES;

USE typsteditor;

-- El backend también crea estas tablas al arrancar si no existen.
CREATE TABLE IF NOT EXISTS users (
  id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  username      VARCHAR(64)  NOT NULL UNIQUE,
  name          VARCHAR(128) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role          ENUM('admin','user') NOT NULL DEFAULT 'user',
  active        TINYINT(1)   NOT NULL DEFAULT 1,
  created_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Cuentas de GitHub (usuario + token) de cada usuario. El token nunca se devuelve por la API.
CREATE TABLE IF NOT EXISTS github_accounts (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id     BIGINT UNSIGNED NOT NULL,
  label       VARCHAR(128) NOT NULL DEFAULT '',
  login       VARCHAR(100) NOT NULL,
  -- is_org: login es una organización a la que da acceso el token de un usuario.
  is_org      TINYINT(1)   NOT NULL DEFAULT 0,
  token       VARCHAR(512) NOT NULL,
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_github_user_login (user_id, login),
  CONSTRAINT fk_github_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- github_account_id: cuenta cuyo token se usa para pull/push por HTTPS.
-- is_template: el proyecto aparece en la galería de plantillas (visible para todos) y se puede copiar.
CREATE TABLE IF NOT EXISTS projects (
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Usuarios invitados a un proyecto (colaboradores con permiso de edición).
CREATE TABLE IF NOT EXISTS project_members (
  project_id  BIGINT UNSIGNED NOT NULL,
  user_id     BIGINT UNSIGNED NOT NULL,
  invited_by  BIGINT UNSIGNED NULL,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (project_id, user_id),
  KEY idx_members_user (user_id),
  CONSTRAINT fk_members_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_members_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_members_inviter FOREIGN KEY (invited_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Organización personal de cada usuario: carpetas y proyectos destacados
-- (no afecta a lo que ven los demás miembros del proyecto).
CREATE TABLE IF NOT EXISTS project_folders (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id     BIGINT UNSIGNED NOT NULL,
  name        VARCHAR(64)  NOT NULL,
  color       VARCHAR(16)  NOT NULL DEFAULT 'blue',
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uq_folder_user_name (user_id, name),
  CONSTRAINT fk_folders_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS project_user_prefs (
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
