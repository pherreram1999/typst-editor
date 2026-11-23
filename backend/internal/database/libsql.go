package database

import (
	"context"
	"database/sql/driver"
	"time"
)

// libSQL remoto (Bunny Database, Turso, sqld) a través de libsql-client-go.
// El conector envuelve cada conexión del driver para:
//   - mantener las claves foráneas activas (el esquema depende de ON DELETE
//     CASCADE / SET NULL). PRAGMA foreign_keys vale por stream y el driver
//     cierra el stream cada vez que la conexión vuelve al pool (ResetSession),
//     así que, si el servidor no las activa por defecto, se repite antes de la
//     siguiente sentencia;
//   - enviar las fechas como texto UTC "2006-01-02 15:04:05", el mismo formato
//     que CURRENT_TIMESTAMP, para que las comparaciones entre columnas por
//     defecto y valores enviados desde Go coincidan (el driver usaría la zona
//     local con nanosegundos y desfase).
// Al leer, el driver ya convierte a time.Time las columnas declaradas TIMESTAMP;
// las expresiones (MAX(…), COALESCE(…)) necesitan database.NullTime.

const sqliteTime = "2006-01-02 15:04:05"

type libsqlConnector struct {
	inner driver.Connector
	// pragma: el servidor no activa foreign_keys por sí solo (ver detectFK).
	pragma bool
}

func (c *libsqlConnector) Driver() driver.Driver { return c.inner.Driver() }

func (c *libsqlConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &libsqlConn{Conn: conn, pragma: c.pragma, pending: c.pragma}, nil
}

type libsqlConn struct {
	driver.Conn
	pragma  bool // hay que activar foreign_keys en cada stream
	pending bool // el stream actual aún no lo tiene activado
}

// prepare activa las claves foráneas en un stream nuevo.
func (c *libsqlConn) prepare(ctx context.Context) error {
	if !c.pending {
		return nil
	}
	if _, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, "PRAGMA foreign_keys = ON", nil); err != nil {
		return err
	}
	c.pending = false
	return nil
}

// ResetSession: el driver cierra aquí el stream; el siguiente es nuevo.
func (c *libsqlConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		if err := r.ResetSession(ctx); err != nil {
			return err
		}
	}
	c.pending = c.pragma
	return nil
}

// CheckNamedValue normaliza los argumentos antes de que lleguen al driver.
func (c *libsqlConn) CheckNamedValue(nv *driver.NamedValue) error {
	if t, ok := nv.Value.(time.Time); ok {
		nv.Value = t.UTC().Format(sqliteTime)
		return nil
	}
	// El resto pasa por la conversión estándar (Valuer, enteros, bool…).
	return driver.ErrSkip
}

func (c *libsqlConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.prepare(ctx); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

func (c *libsqlConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.prepare(ctx); err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

func (c *libsqlConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if err := c.prepare(ctx); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}

func (c *libsqlConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := c.prepare(ctx); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *libsqlConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(interface{ PingContext(context.Context) error }); ok {
		return p.PingContext(ctx)
	}
	return nil
}

// detectFK indica si un stream recién abierto ya tiene foreign_keys activas.
func detectFK(ctx context.Context, inner driver.Connector) (bool, error) {
	conn, err := inner.Connect(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	rows, err := conn.(driver.QueryerContext).QueryContext(ctx, "PRAGMA foreign_keys", nil)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	v := make([]driver.Value, len(rows.Columns()))
	if err := rows.Next(v); err != nil || len(v) == 0 {
		return false, err
	}
	n, _ := v[0].(int64)
	return n == 1, nil
}
