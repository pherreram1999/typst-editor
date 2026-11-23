package config

import (
	"bufio"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Addr string
	// Motor de base de datos (DB_DRIVER): "mysql" (MariaDB, usa DSN) o
	// "libsql" (Bunny Database, Turso, sqld: DBURL + DBAuthToken).
	DBDriver      string
	DSN           string
	DBURL         string
	DBAuthToken   string
	JWTSecret     []byte
	TokenTTL      time.Duration
	AdminUser     string
	AdminPassword string
	AdminName     string
	AdminEmail    string // correo del administrador inicial (se asigna también si aún no tiene)
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
	// URL pública del servidor (sin barra final): emisor OAuth y recurso del
	// endpoint MCP (PUBLIC_URL/mcp). Por defecto http://localhost:<puerto>.
	PublicURL  string
	MCPEnabled bool
	// Correo saliente (Resend): clave de la API (RESEND o RESEND_API_KEY) y
	// remitente (MAIL_FROM). Sin clave no se envían correos y los enlaces de
	// alta y de invitación se muestran a quien los crea para copiarlos.
	ResendKey string
	MailFrom  string
	// MailEnabled (MAIL_ENABLED, por defecto true): false desactiva el correo
	// aunque haya clave (p. ej. si Resend no responde). Sin correo, las cuentas
	// nuevas reciben una contraseña temporal que hay que cambiar al entrar.
	MailEnabled bool
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

	driver := strings.ToLower(get("DB_DRIVER", "mysql"))
	dbURL := get("DB_URL", os.Getenv("BUNNY_DATABASE_URL"))
	dbToken := get("DB_AUTH_TOKEN", os.Getenv("BUNNY_DATABASE_AUTH_TOKEN"))
	switch driver {
	case "mysql", "mariadb":
		driver = "mysql"
	case "libsql":
		if dbURL == "" {
			return nil, errors.New("DB_DRIVER=libsql requiere DB_URL (o BUNNY_DATABASE_URL)")
		}
		// El driver no acepta el token en la URL: se separa si viene ahí.
		if u, err := url.Parse(dbURL); err == nil {
			q := u.Query()
			if t := q.Get("authToken"); t != "" {
				if dbToken == "" {
					dbToken = t
				}
				q.Del("authToken")
				u.RawQuery = q.Encode()
				dbURL = u.String()
			}
		}
	default:
		return nil, errors.New("DB_DRIVER inválido: use mysql o libsql")
	}

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

	addr := get("ADDR", ":8080")
	publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
	if publicURL == "" {
		port := "8080"
		if i := strings.LastIndex(addr, ":"); i >= 0 && i < len(addr)-1 {
			port = addr[i+1:]
		}
		publicURL = "http://localhost:" + port
	}
	u, err := url.Parse(publicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("PUBLIC_URL inválida (p. ej. https://typst.ejemplo.com)")
	}

	return &Config{
		PublicURL:     publicURL,
		MCPEnabled:    get("MCP_ENABLED", "true") != "false",
		Addr:          addr,
		DBDriver:      driver,
		DSN:           dsn,
		DBURL:         dbURL,
		DBAuthToken:   dbToken,
		JWTSecret:     []byte(secret),
		TokenTTL:      ttl,
		AdminUser:     get("ADMIN_USER", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		AdminName:     get("ADMIN_NAME", "Administrador"),
		AdminEmail:    strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))),
		ResendKey:     strings.TrimSpace(firstEnv("RESEND", "RESEND_API_KEY")),
		MailFrom:      get("MAIL_FROM", "Typst Extend <onboarding@resend.dev>"),
		MailEnabled:   !isFalse(os.Getenv("MAIL_ENABLED")),
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

// firstEnv devuelve la primera de las variables que no esté vacía.
func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// isFalse: «false», «0», «no» u «off» (sin distinguir mayúsculas).
func isFalse(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return true
	}
	return false
}
