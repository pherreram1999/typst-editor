// Package collab implementa la autoridad central de edición colaborativa
// compatible con @codemirror/collab (Operational Transformation con servidor
// central): el servidor mantiene una versión lineal del documento, acepta
// cambios solo si se basan en la última versión y los retransmite; los
// clientes transforman (rebase) sus cambios locales pendientes sobre los
// cambios remotos recibidos.
//
// Las posiciones de CodeMirror están en unidades de código UTF-16 y los saltos
// de línea cuentan como una unidad ("\n"), por eso el texto se guarda como
// []uint16.
package collab

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf16"
)

var errBadChangeSet = errors.New("changeset inválido")

// applyChangeSet aplica un ChangeSet serializado con ChangeSet.toJSON():
// un array cuyos elementos son un número (tramo sin cambios de esa longitud)
// o [longitudBorrada, ...líneasInsertadas].
func applyChangeSet(doc []uint16, raw json.RawMessage) ([]uint16, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, errBadChangeSet
	}
	out := make([]uint16, 0, len(doc))
	pos := 0
	for _, p := range parts {
		if len(p) > 0 && p[0] == '[' {
			var arr []json.RawMessage
			if err := json.Unmarshal(p, &arr); err != nil || len(arr) == 0 {
				return nil, errBadChangeSet
			}
			var del int
			if err := json.Unmarshal(arr[0], &del); err != nil || del < 0 || pos+del > len(doc) {
				return nil, errBadChangeSet
			}
			pos += del
			for i, l := range arr[1:] {
				var line string
				if err := json.Unmarshal(l, &line); err != nil {
					return nil, errBadChangeSet
				}
				if i > 0 {
					out = append(out, '\n')
				}
				out = append(out, utf16.Encode([]rune(line))...)
			}
			continue
		}
		var n int
		if err := json.Unmarshal(p, &n); err != nil || n < 0 || pos+n > len(doc) {
			return nil, errBadChangeSet
		}
		out = append(out, doc[pos:pos+n]...)
		pos += n
	}
	if pos != len(doc) {
		return nil, errBadChangeSet // la longitud base no coincide con el documento
	}
	return out, nil
}

// diffChangeSet construye el ChangeSet mínimo (prefijo/sufijo común) que
// transforma a en b. Devuelve nil si son iguales.
func diffChangeSet(a, b []uint16) json.RawMessage {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	if pre == len(a) && pre == len(b) {
		return nil
	}
	// No partir pares sustitutos UTF-16.
	if pre > 0 && utf16.IsSurrogate(rune(a[pre-1])) && a[pre-1] < 0xDC00 {
		pre--
	}
	if suf > 0 && utf16.IsSurrogate(rune(a[len(a)-suf])) && a[len(a)-suf] >= 0xDC00 {
		suf--
	}
	var parts []any
	if pre > 0 {
		parts = append(parts, pre)
	}
	rep := []any{len(a) - pre - suf}
	if ins := b[pre : len(b)-suf]; len(ins) > 0 {
		for _, l := range strings.Split(decode(ins), "\n") {
			rep = append(rep, l)
		}
	}
	parts = append(parts, rep)
	if suf > 0 {
		parts = append(parts, suf)
	}
	raw, _ := json.Marshal(parts)
	return raw
}

func encode(s string) []uint16 { return utf16.Encode([]rune(normalizeNewlines(s))) }
func decode(u []uint16) string { return string(utf16.Decode(u)) }

// normalizeNewlines convierte CRLF/CR a LF, igual que hace CodeMirror.
func normalizeNewlines(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}
