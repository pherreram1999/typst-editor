package database

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// Dialect reúne lo que cambia entre motores. Las consultas se escriben en SQL
// común (CASE WHEN, COALESCE, parámetros ?) y solo piden al dialecto los
// fragmentos que no tienen una forma portable. No escriba SQL propio de un
// motor fuera de aquí.
type Dialect interface {
	// Name es el valor de DB_DRIVER: "mysql" o "libsql".
	Name() string
	// Schema son las sentencias idempotentes que crean (y migran) el esquema.
	Schema() []string
	// InsertIgnore es el comienzo de un INSERT que ignora filas duplicadas.
	InsertIgnore() string
	// OnConflict precede a las asignaciones de un upsert sobre la clave keys.
	OnConflict(keys ...string) string
	// Excluded es el valor que se intentaba insertar en col, dentro de un upsert.
	Excluded(col string) string
	// GroupConcat concatena expr con el separador literal sep (sin orden garantizado).
	GroupConcat(expr, sep string) string
	// ForUpdate bloquea las filas leídas dentro de una transacción (puede ser "").
	ForUpdate() string
	// IsDuplicate indica si err es una violación de clave única.
	IsDuplicate(err error) bool
}

// sqlString escribe s como literal SQL entre comillas simples.
func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

type mysqlDialect struct{}

func (mysqlDialect) Name() string                { return "mysql" }
func (mysqlDialect) Schema() []string            { return mysqlSchema }
func (mysqlDialect) InsertIgnore() string        { return "INSERT IGNORE" }
func (mysqlDialect) OnConflict(...string) string { return "ON DUPLICATE KEY UPDATE" }
func (mysqlDialect) Excluded(col string) string  { return "VALUES(" + col + ")" }
func (mysqlDialect) ForUpdate() string           { return " FOR UPDATE" }
func (mysqlDialect) GroupConcat(e, s string) string {
	return "GROUP_CONCAT(" + e + " SEPARATOR " + sqlString(s) + ")"
}
func (mysqlDialect) IsDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

type libsqlDialect struct{}

func (libsqlDialect) Name() string         { return "libsql" }
func (libsqlDialect) Schema() []string     { return libsqlSchema }
func (libsqlDialect) InsertIgnore() string { return "INSERT OR IGNORE" }
func (libsqlDialect) OnConflict(keys ...string) string {
	return "ON CONFLICT(" + strings.Join(keys, ", ") + ") DO UPDATE SET"
}
func (libsqlDialect) Excluded(col string) string { return "excluded." + col }
func (libsqlDialect) ForUpdate() string          { return "" } // las transacciones de SQLite ya son serializables
func (libsqlDialect) GroupConcat(e, s string) string {
	return "group_concat(" + e + ", " + sqlString(s) + ")"
}
func (libsqlDialect) IsDuplicate(err error) bool {
	// Los errores de libSQL remoto llegan como texto.
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// Dialectos disponibles (Open devuelve el que corresponde a DB_DRIVER).
var (
	MySQL  Dialect = mysqlDialect{}
	LibSQL Dialect = libsqlDialect{}
)
