package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/tursodatabase/libsql-client-go/libsql"
	"golang.org/x/crypto/bcrypt"
)

// Options elige el motor (DB_DRIVER) y cómo conectarse a él.
type Options struct {
	Driver    string // "mysql" (MariaDB) o "libsql" (Bunny Database, Turso, sqld)
	DSN       string // mysql
	URL       string // libsql: libsql://…, https://…, http://host:puerto
	AuthToken string // libsql
}

// Open conecta con el motor elegido y devuelve su dialecto.
func Open(o Options) (*sql.DB, Dialect, error) {
	var (
		db *sql.DB
		d  Dialect
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch o.Driver {
	case "mysql":
		var err error
		if db, err = sql.Open("mysql", o.DSN); err != nil {
			return nil, nil, err
		}
		// Base inexistente (error 1049): se crea y Migrate pone el esquema.
		if err := db.PingContext(ctx); isUnknownDB(err) {
			db.Close()
			if err := createMySQLDatabase(ctx, o.DSN); err != nil {
				return nil, nil, err
			}
			if db, err = sql.Open("mysql", o.DSN); err != nil {
				return nil, nil, err
			}
		}
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(5 * time.Minute)
		d = mysqlDialect{}
	case "libsql":
		var opts []libsql.Option
		if o.AuthToken != "" {
			opts = append(opts, libsql.WithAuthToken(o.AuthToken))
		}
		inner, err := libsql.NewConnector(o.URL, opts...)
		if err != nil {
			return nil, nil, err
		}
		// La base de libSQL se crea en el panel del proveedor (Bunny, Turso):
		// el protocolo no permite crearla. El esquema sí lo crea Migrate.
		fk, err := detectFK(ctx, inner)
		if err != nil {
			return nil, nil, fmt.Errorf("no se pudo conectar con %s (¿existe la base y el token es válido?): %w", redactURL(o.URL), err)
		}
		db = sql.OpenDB(&libsqlConnector{inner: inner, pragma: !fk})
		// Cada conexión es un stream HTTP; el servidor limita cuántos hay abiertos.
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxIdleTime(30 * time.Second)
		d = libsqlDialect{}
	default:
		return nil, nil, fmt.Errorf("DB_DRIVER desconocido: %q (use mysql o libsql)", o.Driver)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, d, nil
}

func isUnknownDB(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1049
}

// createMySQLDatabase crea la base del DSN (utf8mb4, como database/01_setup.sql)
// conectándose sin base. El usuario necesita el privilegio CREATE.
func createMySQLDatabase(ctx context.Context, dsn string) error {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return err
	}
	name := cfg.DBName
	cfg.DBName = ""
	conn, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	q := "CREATE DATABASE IF NOT EXISTS `" + strings.ReplaceAll(name, "`", "``") +
		"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
	if _, err := conn.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("la base %q no existe y no se pudo crear (¿el usuario tiene permiso CREATE?): %w", name, err)
	}
	log.Printf("base de datos %q creada", name)
	return nil
}

// redactURL quita de la URL lo que pueda ser secreto, para los mensajes.
func redactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "la base libSQL"
	}
	u.User, u.RawQuery = nil, ""
	return u.String()
}

// NullTime lee una fecha que puede ser NULL tanto si el driver la entrega como
// time.Time (MariaDB, columnas TIMESTAMP de libSQL) como si llega como texto
// (expresiones en libSQL: MAX(…), COALESCE(…)). Siempre en UTC.
type NullTime struct {
	Time  time.Time
	Valid bool
}

var timeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02",
}

func (n *NullTime) Scan(v any) error {
	n.Time, n.Valid = time.Time{}, false
	var s string
	switch t := v.(type) {
	case nil:
		return nil
	case time.Time:
		n.Time, n.Valid = t.UTC(), true
		return nil
	case string:
		s = t
	case []byte:
		s = string(t)
	default:
		return fmt.Errorf("fecha no válida: %T", v)
	}
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			n.Time, n.Valid = t.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("fecha no válida: %q", s)
}

// Migrate crea y migra el esquema (idempotente: se ejecuta en cada arranque,
// también en Docker) y crea el primer administrador si no hay ninguno. Con
// adminEmail, además se lo asigna al administrador adminUser si aún no tiene
// correo (instalaciones anteriores al inicio de sesión por correo).
func Migrate(db *sql.DB, d Dialect, adminUser, adminPassword, adminName, adminEmail string) error {
	for _, stmt := range d.Schema() {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("esquema (%s): %w", d.Name(), err)
		}
	}
	if d.Name() == "libsql" {
		if err := migrateLibSQL(db); err != nil {
			return fmt.Errorf("esquema (%s): %w", d.Name(), err)
		}
	}
	log.Printf("esquema de la base de datos al día (%s)", d.Name())
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		if adminEmail != "" {
			res, err := db.Exec(`UPDATE users SET email = ? WHERE username = ? AND role = 'admin' AND (email IS NULL OR email = '')
  AND NOT EXISTS (SELECT 1 FROM (SELECT id FROM users WHERE email = ?) x)`, adminEmail, adminUser, adminEmail)
			if err != nil {
				return fmt.Errorf("correo del administrador: %w", err)
			}
			if k, _ := res.RowsAffected(); k > 0 {
				log.Printf("correo %s asignado al administrador %q", adminEmail, adminUser)
			}
		}
		return nil
	}
	if len(adminPassword) < 8 {
		return errors.New("no existe administrador: defina ADMIN_PASSWORD (mín. 8 caracteres) para crearlo")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	var email any
	if adminEmail != "" {
		email = adminEmail
	}
	_, err = db.Exec(`INSERT INTO users (username, email, name, password_hash, role) VALUES (?, ?, ?, ?, 'admin')`,
		adminUser, email, adminName, string(hash))
	if err == nil {
		log.Printf("administrador inicial %q creado", adminUser)
	}
	return err
}
