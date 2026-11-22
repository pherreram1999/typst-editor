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
	"path"
	"path/filepath"
	"regexp"
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
	srcs, err := loadSources(root, main)
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

// loadSources lee los .typ del proyecto (sin .git) en el orden en que el
// documento los recorre: main y, en profundidad, cada #include en el orden en
// que aparece. Los archivos que no se alcanzan así (plantillas importadas,
// includes calculados) van al final. matchBlocks depende de este orden.
func loadSources(root, main string) ([]source, error) {
	texts := map[string]string{}
	var all []string
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
		rel = filepath.ToSlash(rel)
		texts[rel] = string(b)
		all = append(all, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var order []string
	seen := map[string]bool{}
	var visit func(string)
	visit = func(f string) {
		if seen[f] {
			return
		}
		if _, ok := texts[f]; !ok {
			return
		}
		seen[f] = true
		order = append(order, f)
		for _, inc := range includes(texts[f]) {
			if strings.HasPrefix(inc, "/") {
				inc = path.Clean(strings.TrimPrefix(inc, "/"))
			} else {
				inc = path.Join(path.Dir(f), inc)
			}
			visit(inc)
		}
	}
	visit(path.Clean(filepath.ToSlash(main)))
	for _, f := range all {
		if !seen[f] {
			order = append(order, f)
		}
	}
	srcs := make([]source, len(order))
	for i, f := range order {
		srcs[i] = source{f, tokenizeSource(texts[f])}
	}
	return srcs, nil
}

var includeRe = regexp.MustCompile(`(?:^|[^\w-])include\s*\(?\s*"([^"]+)"`)

// includes devuelve, en orden, las rutas de los include con cadena literal de
// un .typ, ignorando los comentarios de línea.
func includes(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if i := lineComment(line); i >= 0 {
			line = line[:i]
		}
		for _, m := range includeRe.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// lineComment devuelve el índice de «//» que abre un comentario (al inicio o
// tras un espacio, para no confundirlo con URL), o -1.
func lineComment(line string) int {
	for i := 0; i+1 < len(line); i++ {
		if line[i] == '/' && line[i+1] == '/' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return i
		}
	}
	return -1
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

// before indica si a va antes que b en el orden del documento.
func (a srcPos) before(b srcPos) bool {
	return a.file < b.file || a.file == b.file && a.tok < b.tok
}

// matchBlocks localiza cada bloque en las fuentes buscando sus primeras
// palabras (de tres en tres; menos si el bloque es corto). Si una secuencia
// aparece varias veces gana la aparición que coincide con más palabras
// seguidas del bloque y, a igualdad, la siguiente al bloque anterior en el
// orden del documento (srcs viene ordenado como lo recorre main).
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
	var cur srcPos
	var points []SyncPoint
	for _, b := range blocks {
		ws := words(b.text)
		if len(ws) == 0 {
			continue
		}
		n := min(3, len(ws))
		found := false
		for i := 0; i+n <= len(ws) && i < 8 && !found; i++ {
			if cands := index[n][strings.Join(ws[i:i+n], " ")]; len(cands) > 0 {
				cur, found = pick(cands, ws[i:], srcs, cur), true
			}
		}
		if !found {
			continue
		}
		t := srcs[cur.file].tokens[cur.tok]
		pt := SyncPoint{Page: b.page, Y: b.y, File: srcs[cur.file].path, Line: t.line, Col: t.col}
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

// maxRun limita cuántas palabras seguidas se comparan al desempatar.
const maxRun = 24

// pick elige entre varias apariciones (en orden del documento) la que
// coincide con más palabras seguidas de ws; a igualdad, la primera que no
// queda antes de cur y, si todas quedan antes, la más cercana a cur.
func pick(cands []srcPos, ws []string, srcs []source, cur srcPos) srcPos {
	ws = ws[:min(len(ws), maxRun)]
	bestRun := -1
	var ahead, behind *srcPos
	for i, c := range cands {
		toks := srcs[c.file].tokens[c.tok:]
		run := 0
		for run < len(ws) && run < len(toks) && toks[run].word == ws[run] {
			run++
		}
		if run > bestRun {
			bestRun, ahead, behind = run, nil, nil
		}
		if run < bestRun {
			continue
		}
		if c.before(cur) {
			behind = &cands[i]
		} else if ahead == nil {
			ahead = &cands[i]
		}
	}
	if ahead != nil {
		return *ahead
	}
	return *behind
}
