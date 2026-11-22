package typst

// Sincronización entre la vista previa y el código.
//
// El CLI de typst no expone el origen (archivo y línea) de lo que dibuja, pero
// sí permite consultar el documento: blocksQuery devuelve, para cada bloque
// (párrafo, título, figura, lista, tabla, código), su página, su posición
// vertical y su texto plano. Ese texto se localiza después en los .typ del
// proyecto comparando secuencias de palabras (el marcado como *negrita* o
// `código` no cambia las palabras). El resultado es una lista de puntos
// página/posición ↔ archivo/línea con precisión de bloque. Los bloques cuyo
// texto no está en las fuentes (generado por código, numeraciones…) se omiten.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf16"
)

// blocksQuery se evalúa con `typst eval --in main.typ`.
const blocksQuery = `{
  let plain(c) = {
    if type(c) == str { c }
    else if type(c) != content { "" }
    else if c.has("text") { if type(c.text) == str { c.text } else { plain(c.text) } }
    else if c.has("children") { c.children.map(plain).join(" ") }
    else if c.has("body") { plain(c.body) }
    else if c.has("child") { plain(c.child) }
    else { "" }
  }
  query(selector(par).or(heading).or(figure).or(list).or(enum).or(terms).or(table).or(raw.where(block: true))).map(e => {
    let p = e.location().position()
    let t = if e.func() == figure { plain(e.caption) } else { plain(e) }
    let cl = t.clusters()
    (p.page, calc.round(p.y.pt(), digits: 1), cl.slice(0, calc.min(cl.len(), 200)).join())
  })
}`

// SyncPoint relaciona una posición de la vista previa con una del código.
type SyncPoint struct {
	Page int     `json:"page"` // desde 1
	Y    float64 `json:"y"`    // pt desde el borde superior
	File string  `json:"file"`
	Line int     `json:"line"` // desde 1
	Col  int     `json:"col"`  // desde 1, en unidades UTF-16 (como CodeMirror)
}

type block struct {
	page int
	y    float64
	text string
}

// SyncMap compila la consulta de bloques sobre el espejo de la vista previa
// (misma paginación) y empareja los bloques con las fuentes de root.
func (c *Compiler) SyncMap(ctx context.Context, root, main, outDir string) ([]SyncPoint, error) {
	mirror, err := previewRoot(outDir)
	if err != nil {
		return nil, err
	}
	if err := syncMirror(root, mirror, main); err != nil {
		return nil, err
	}
	args := []string{"eval", "--root", mirror, "--diagnostic-format", "short",
		"--in", filepath.Join(mirror, filepath.FromSlash(main)), blocksQuery}
	out, _, err := c.exec(ctx, mirror, args)
	if err != nil {
		return nil, err
	}
	var rows [][]any
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	blocks := make([]block, 0, len(rows))
	for _, r := range rows {
		if len(r) != 3 {
			continue
		}
		page, _ := r[0].(float64)
		y, _ := r[1].(float64)
		text, _ := r[2].(string)
		blocks = append(blocks, block{int(page), y, text})
	}
	srcs, err := loadSources(root)
	if err != nil {
		return nil, err
	}
	return matchBlocks(blocks, srcs), nil
}

type token struct {
	word      string
	line, col int
}

type source struct {
	path   string
	tokens []token
}

// loadSources lee los .typ del proyecto (sin .git).
func loadSources(root string) ([]source, error) {
	var srcs []source
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() || !strings.EqualFold(filepath.Ext(p), ".typ") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		srcs = append(srcs, source{filepath.ToSlash(rel), tokenizeSource(string(b))})
		return nil
	})
	return srcs, err
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// words separa un texto en palabras en minúsculas.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !isWordRune(r) })
}

// tokenizeSource extrae las palabras de un .typ con su línea y columna,
// saltando los comentarios de línea («//» al inicio o tras un espacio, para no
// confundirlos con URL).
func tokenizeSource(src string) []token {
	var toks []token
	for i, line := range strings.Split(src, "\n") {
		col := 1 // en unidades UTF-16
		var cur []rune
		start := 0
		flush := func() {
			if len(cur) > 0 {
				toks = append(toks, token{strings.ToLower(string(cur)), i + 1, start})
				cur = cur[:0]
			}
		}
		runes := []rune(line)
		for j, r := range runes {
			if r == '/' && j+1 < len(runes) && runes[j+1] == '/' && (j == 0 || unicode.IsSpace(runes[j-1])) {
				break
			}
			if isWordRune(r) {
				if len(cur) == 0 {
					start = col
				}
				cur = append(cur, r)
			} else {
				flush()
			}
			col += len(utf16.Encode([]rune{r}))
		}
		flush()
	}
	return toks
}

type srcPos struct{ file, tok int }

// matchBlocks localiza cada bloque en las fuentes buscando sus primeras
// palabras (de tres en tres; menos si el bloque es corto). Si una secuencia
// aparece varias veces se prefiere la que sigue al bloque anterior en el
// mismo archivo, porque el documento suele recorrer las fuentes en orden.
func matchBlocks(blocks []block, srcs []source) []SyncPoint {
	index := [4]map[string][]srcPos{nil, {}, {}, {}}
	for fi, s := range srcs {
		for ti := range s.tokens {
			for n := 1; n <= 3 && ti+n <= len(s.tokens); n++ {
				key := joinTokens(s.tokens[ti : ti+n])
				index[n][key] = append(index[n][key], srcPos{fi, ti})
			}
		}
	}
	last := make([]int, len(srcs))
	prevFile := -1
	var points []SyncPoint
	for _, b := range blocks {
		ws := words(b.text)
		if len(ws) == 0 {
			continue
		}
		n := min(3, len(ws))
		var found *srcPos
		for i := 0; i+n <= len(ws) && i < 8 && found == nil; i++ {
			cands := index[n][strings.Join(ws[i:i+n], " ")]
			if len(cands) > 0 {
				p := pick(cands, last, prevFile)
				found = &p
			}
		}
		if found == nil {
			continue
		}
		last[found.file], prevFile = found.tok, found.file
		t := srcs[found.file].tokens[found.tok]
		pt := SyncPoint{Page: b.page, Y: b.y, File: srcs[found.file].path, Line: t.line, Col: t.col}
		if len(points) > 0 && points[len(points)-1] == pt {
			continue
		}
		points = append(points, pt)
	}
	return points
}

func joinTokens(ts []token) string {
	w := make([]string, len(ts))
	for i, t := range ts {
		w[i] = t.word
	}
	return strings.Join(w, " ")
}

// pick elige entre varias apariciones: primero la siguiente en el archivo
// del bloque anterior, después la siguiente en cualquier archivo y, si no
// hay, la primera.
func pick(cands []srcPos, last []int, prevFile int) srcPos {
	var other *srcPos
	for i, c := range cands {
		if c.tok < last[c.file] {
			continue
		}
		if c.file == prevFile {
			return c
		}
		if other == nil {
			other = &cands[i]
		}
	}
	if other != nil {
		return *other
	}
	return cands[0]
}
