package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"typsteditor/backend/internal/auth"
	"typsteditor/backend/internal/config"
	"typsteditor/backend/internal/database"
	"typsteditor/backend/internal/httpapi"
	"typsteditor/backend/internal/keygen"
)

const usage = `Uso: server [comando]

Comandos:
  (ninguno)        Inicia el servidor HTTP
  serve            Inicia el servidor HTTP
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
	db, err := database.Open(cfg.DSN)
	if err != nil {
		log.Fatalf("conexión a MariaDB: %v", err)
	}
	defer db.Close()
	if err := database.Migrate(db, cfg.AdminUser, cfg.AdminPassword, cfg.AdminName); err != nil {
		log.Fatalf("migración: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("DATA_DIR: %v", err)
	}
	for _, bin := range []string{cfg.TypstBin, cfg.GitBin} {
		if _, err := exec.LookPath(bin); err != nil {
			log.Printf("aviso: no se encuentra %q en el sistema: %v", bin, err)
		}
	}
	api := httpapi.New(db, auth.NewIssuer(cfg.JWTSecret, cfg.TokenTTL), httpapi.Options{
		DataDir: cfg.DataDir, TypstBin: cfg.TypstBin, GitBin: cfg.GitBin, GitHubAPI: cfg.GitHubAPI,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	go func() {
		log.Printf("API escuchando en %s", cfg.Addr)
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
