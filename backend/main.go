package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"typsteditor/backend/internal/auth"
	"typsteditor/backend/internal/config"
	"typsteditor/backend/internal/database"
	"typsteditor/backend/internal/httpapi"
	"typsteditor/backend/internal/keygen"
	"typsteditor/backend/internal/mail"
)

const usage = `Uso: server [comando]

Comandos:
  (ninguno)        Inicia el servidor HTTP
  serve            Inicia el servidor HTTP (crea la base y migra el esquema si hace falta)
  migrate          Solo crea la base (MariaDB) y migra el esquema; luego termina
  key:generate     Genera JWT_SECRET y lo guarda en .env
      --show       Solo muestra la clave, no modifica .env
  help             Muestra esta ayuda
`

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		serve()
	case "migrate":
		cfg, err := config.Load()
		if err != nil {
			log.Fatal(err)
		}
		db, _ := openDB(cfg)
		db.Close()
	case "key:generate":
		keyGenerate(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "comando desconocido: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func keyGenerate(args []string) {
	show := false
	for _, a := range args {
		switch a {
		case "--show":
			show = true
		default:
			fmt.Fprintf(os.Stderr, "opción desconocida: %s\n", a)
			os.Exit(2)
		}
	}
	secret, err := keygen.NewSecret()
	if err != nil {
		log.Fatalf("no se pudo generar la clave: %v", err)
	}
	if show {
		fmt.Println(secret)
		return
	}
	replaced, err := keygen.WriteEnv(".env", ".env.example", secret)
	if err != nil {
		log.Fatalf("no se pudo escribir .env: %v", err)
	}
	fmt.Println("JWT_SECRET generado y guardado en .env")
	if replaced {
		fmt.Println("Aviso: se reemplazó la clave anterior; los tokens emitidos dejarán de ser válidos.")
	}
}

func serve() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	db, dialect := openDB(cfg)
	defer db.Close()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("DATA_DIR: %v", err)
	}
	// Sin permiso de escritura nada funciona (proyectos, compilaciones), pero
	// el fallo solo aparecería al usarlo: mejor no arrancar y decir por qué.
	// Típico en Docker: un volumen montado como root y el proceso sin root.
	if f, err := os.CreateTemp(cfg.DataDir, ".escritura-*"); err != nil {
		log.Fatalf("DATA_DIR %s no tiene permiso de escritura para el usuario %d:%d: %v "+
			"(en Docker, el volumen debe pertenecer a ese usuario; la imagen lo corrige al arrancar como root)",
			cfg.DataDir, os.Getuid(), os.Getgid(), err)
	} else {
		f.Close()
		os.Remove(f.Name())
	}
	// Fuentes adicionales: typst lee TYPST_FONT_PATHS, y todos los procesos
	// typst (compilación, vista previa, sincronización, catálogo del
	// navegador) heredan el entorno del servidor.
	if err := os.MkdirAll(cfg.LocalFontsDir, 0o755); err != nil {
		log.Printf("aviso: carpeta de fuentes: %v", err)
	}
	fontPaths := cfg.FontDirs
	if prev := os.Getenv("TYPST_FONT_PATHS"); prev != "" {
		fontPaths = append(append([]string{}, fontPaths...), prev)
	}
	os.Setenv("TYPST_FONT_PATHS", strings.Join(fontPaths, string(os.PathListSeparator)))
	log.Printf("Fuentes adicionales: %s", strings.Join(cfg.FontDirs, ", "))

	for _, bin := range []string{cfg.TypstBin, cfg.GitBin} {
		if _, err := exec.LookPath(bin); err != nil {
			log.Printf("aviso: no se encuentra %q en el sistema: %v", bin, err)
		}
	}
	key := cfg.ResendKey
	if !cfg.MailEnabled {
		key = ""
	}
	mailer := mail.New(key, cfg.MailFrom)
	if mailer.Enabled() {
		log.Printf("Correo: Resend, remitente %s", cfg.MailFrom)
		if strings.Contains(cfg.PublicURL, "://localhost") || strings.Contains(cfg.PublicURL, "://127.") {
			log.Printf("aviso: PUBLIC_URL es %s: los enlaces de los correos solo funcionarán en este equipo", cfg.PublicURL)
		}
	} else {
		reason := "sin RESEND (clave de Resend)"
		if !cfg.MailEnabled {
			reason = "MAIL_ENABLED=false"
		}
		log.Printf("aviso: correo desactivado (%s): las cuentas nuevas reciben una contraseña temporal que deben cambiar al entrar; las invitaciones por correo muestran el enlace para copiarlo", reason)
	}
	api := httpapi.New(db, dialect, auth.NewIssuer(cfg.JWTSecret, cfg.TokenTTL), httpapi.Options{
		DataDir: cfg.DataDir, TypstBin: cfg.TypstBin, GitBin: cfg.GitBin, GitHubAPI: cfg.GitHubAPI,
		StaticDir: cfg.StaticDir, FontDirs: cfg.FontDirs,
		PublicURL: cfg.PublicURL, MCPEnabled: cfg.MCPEnabled,
		Mail: mailer,
	})
	if cfg.MCPEnabled {
		log.Printf("MCP: %s/mcp", cfg.PublicURL)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	go func() {
		log.Printf("Servidor escuchando en %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	api.Shutdown()
}

// openDB conecta con el motor de DB_DRIVER (en MariaDB crea la base si no
// existe) y migra el esquema; sale con error si algo falla.
func openDB(cfg *config.Config) (*sql.DB, database.Dialect) {
	db, dialect, err := database.Open(database.Options{
		Driver: cfg.DBDriver, DSN: cfg.DSN, URL: cfg.DBURL, AuthToken: cfg.DBAuthToken,
	})
	if err != nil {
		log.Fatalf("conexión a la base de datos (%s): %v", cfg.DBDriver, err)
	}
	if err := database.Migrate(db, dialect, cfg.AdminUser, cfg.AdminPassword, cfg.AdminName, cfg.AdminEmail); err != nil {
		db.Close()
		log.Fatalf("migración: %v", err)
	}
	return db, dialect
}
