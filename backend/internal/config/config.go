package config

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Addr          string
	DSN           string
	JWTSecret     []byte
	TokenTTL      time.Duration
	AdminUser     string
	AdminPassword string
	AdminName     string
	DataDir       string // proyectos (repos git) y artefactos de compilación
	TypstBin      string
	GitBin        string
	GitHubAPI     string
}

// loadDotEnv reads KEY=VALUE pairs from path without overriding existing env vars.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func get(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Load() (*Config, error) {
	loadDotEnv(".env")

	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return nil, errors.New("JWT_SECRET debe tener al menos 32 caracteres")
	}
	ttl, err := time.ParseDuration(get("TOKEN_TTL", "8h"))
	if err != nil {
		return nil, errors.New("TOKEN_TTL inválido")
	}

	dsn := get("DB_USER", "typsteditor") + ":" + os.Getenv("DB_PASSWORD") +
		"@tcp(" + get("DB_HOST", "127.0.0.1") + ":" + get("DB_PORT", "3306") + ")/" +
		get("DB_NAME", "typsteditor") + "?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci" +
		// Sesión en UTC y fechas leídas como UTC: así los TIMESTAMP no se desplazan
		// según la zona horaria del servidor de base de datos.
		"&loc=UTC&time_zone=%27%2B00%3A00%27"

	dataDir, err := filepath.Abs(get("DATA_DIR", "data"))
	if err != nil {
		return nil, errors.New("DATA_DIR inválido")
	}

	return &Config{
		Addr:          get("ADDR", ":8080"),
		DSN:           dsn,
		JWTSecret:     []byte(secret),
		TokenTTL:      ttl,
		AdminUser:     get("ADMIN_USER", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		AdminName:     get("ADMIN_NAME", "Administrador"),
		DataDir:       dataDir,
		TypstBin:      get("TYPST_BIN", "typst"),
		GitBin:        get("GIT_BIN", "git"),
		GitHubAPI:     get("GITHUB_API", "https://api.github.com"),
	}, nil
}
