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
	StaticDir     string // frontend compilado que sirve el propio servidor
	// Carpetas de fuentes adicionales para typst, además de las del sistema:
	// las incluidas en el repositorio (fonts/), las locales del servidor
	// (DATA_DIR/fonts, p. ej. fuentes con licencia que no se versionan) y las
	// de FONT_PATHS.
	FontDirs      []string
	LocalFontsDir string // DATA_DIR/fonts (también está en FontDirs)
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

	staticDir, err := filepath.Abs(get("STATIC_DIR", defaultStaticDir()))
	if err != nil {
		return nil, errors.New("STATIC_DIR inválido")
	}

	localFonts := filepath.Join(dataDir, "fonts")
	fontDirs := []string{localFonts}
	if d := defaultFontsDir(); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			fontDirs = append([]string{abs}, fontDirs...)
		}
	}
	for _, d := range filepath.SplitList(os.Getenv("FONT_PATHS")) {
		if d = strings.TrimSpace(d); d != "" {
			abs, err := filepath.Abs(d)
			if err != nil {
				return nil, errors.New("FONT_PATHS inválido")
			}
			fontDirs = append(fontDirs, abs)
		}
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
		StaticDir:     staticDir,
		FontDirs:      fontDirs,
		LocalFontsDir: localFonts,
	}, nil
}

// defaultStaticDir prefiere public/ junto al ejecutable (lo que genera build.sh)
// y, si no existe, el build de desarrollo en ../frontend/dist.
func defaultStaticDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "public")
		if st, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !st.IsDir() {
			return dir
		}
	}
	return "../frontend/dist"
}

// defaultFontsDir busca las fuentes incluidas en el repositorio: fonts/ junto
// al ejecutable (build.sh lo deja en la raíz) o ../fonts al ejecutar desde
// backend/. Vacío si no hay ninguna.
func defaultFontsDir() string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "fonts"))
	}
	dirs = append(dirs, "fonts", "../fonts")
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}
