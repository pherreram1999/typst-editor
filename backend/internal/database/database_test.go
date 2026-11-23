package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/tursodatabase/libsql-client-go/libsql"
)

func TestNullTime(t *testing.T) {
	want := time.Date(2026, 11, 22, 10, 30, 0, 0, time.UTC)
	for _, v := range []any{
		want,
		want.In(time.FixedZone("x", -6*3600)),
		"2026-11-22 10:30:00",
		[]byte("2026-11-22 10:30:00"),
		"2026-11-22T10:30:00Z",
		"2026-11-22 04:30:00-06:00",
	} {
		var n NullTime
		if err := n.Scan(v); err != nil || !n.Valid || !n.Time.Equal(want) || n.Time.Location() != time.UTC {
			t.Errorf("Scan(%#v) = %v %v %v", v, n.Time, n.Valid, err)
		}
	}
	var n NullTime
	if err := n.Scan(nil); err != nil || n.Valid {
		t.Errorf("Scan(nil) = %v %v", n.Valid, err)
	}
	if err := n.Scan("no es fecha"); err == nil {
		t.Error("Scan aceptó un texto que no es fecha")
	}
}

func TestDialects(t *testing.T) {
	if !MySQL.IsDuplicate(&mysql.MySQLError{Number: 1062}) || MySQL.IsDuplicate(&mysql.MySQLError{Number: 1452}) {
		t.Error("mysql IsDuplicate")
	}
	if !LibSQL.IsDuplicate(errors.New("failed to execute SQL: SQLite error: UNIQUE constraint failed: users.username")) ||
		LibSQL.IsDuplicate(errors.New("FOREIGN KEY constraint failed")) || LibSQL.IsDuplicate(nil) {
		t.Error("libsql IsDuplicate")
	}
	if got := MySQL.GroupConcat("x", "it's"); got != `GROUP_CONCAT(x SEPARATOR 'it''s')` {
		t.Error(got)
	}
	if got := LibSQL.OnConflict("a", "b"); got != "ON CONFLICT(a, b) DO UPDATE SET" {
		t.Error(got)
	}
}

// TestLibSQL necesita un servidor libSQL desechable, p. ej.
//
//	podman run --rm -p 127.0.0.1:18081:8080 ghcr.io/tursodatabase/libsql-server
//	TYPSTEDITOR_LIBSQL_URL=http://127.0.0.1:18081 go test ./internal/database -run LibSQL
func TestLibSQL(t *testing.T) {
	u := os.Getenv("TYPSTEDITOR_LIBSQL_URL")
	if u == "" {
		t.Skip("TYPSTEDITOR_LIBSQL_URL no definida")
	}
	inner, err := libsql.NewConnector(u)
	if err != nil {
		t.Fatal(err)
	}
	// Se fuerza el PRAGMA por stream aunque el servidor ya lo active.
	db := sql.OpenDB(&libsqlConnector{inner: inner, pragma: true})
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for i := 0; i < 3; i++ { // cada vuelta reutiliza la conexión con un stream nuevo
		var fk int
		if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Fatalf("foreign_keys = %d, %v", fk, err)
		}
	}
	for _, s := range []string{
		`DROP TABLE IF EXISTS t_fechas`,
		`CREATE TABLE t_fechas (id INTEGER PRIMARY KEY, at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	defer db.ExecContext(ctx, `DROP TABLE t_fechas`)
	at := time.Date(2026, 1, 2, 3, 4, 5, 999, time.FixedZone("x", 3600))
	if _, err := db.ExecContext(ctx, `INSERT INTO t_fechas (id, at) VALUES (1, ?), (2, CURRENT_TIMESTAMP)`, at); err != nil {
		t.Fatal(err)
	}
	var raw string
	var got time.Time
	if err := db.QueryRowContext(ctx, `SELECT CAST(at AS TEXT), at FROM t_fechas WHERE id = 1`).Scan(&raw, &got); err != nil {
		t.Fatal(err)
	}
	if raw != "2026-01-02 02:04:05" || !got.Equal(at.Truncate(time.Second)) {
		t.Errorf("fecha guardada %q, leída %v", raw, got)
	}
	var max NullTime
	if err := db.QueryRowContext(ctx, `SELECT MAX(at) FROM t_fechas WHERE at < ?`, time.Now().Add(time.Hour)).Scan(&max); err != nil || !max.Valid {
		t.Fatalf("MAX(at) = %v, %v", max, err)
	}
}
