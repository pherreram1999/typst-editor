package httpapi

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Importar un proyecto desde la computadora: un .zip (p. ej. el que produce
// «Exportar») o los archivos de una carpeta. El proyecto nuevo es del usuario,
// con su propio repositorio git y sin remoto.
const (
	maxImportBody  = 512 << 20  // cuerpo de la petición
	maxImportFile  = 100 << 20  // cada archivo (descomprimido)
	maxImportTotal = 1024 << 20 // suma descomprimida
	maxImportFiles = 10000
)

// importLimits acota lo que se escribe en disco (también frente a zips bomba).
type importLimits struct {
	files int
	total int64
}

func (l *importLimits) add(n int64) error {
	if n > maxImportFile {
		return errf(http.StatusBadRequest, "un archivo supera el máximo de 100 MB")
	}
	l.total += n
	if l.total > maxImportTotal {
		return errf(http.StatusBadRequest, "el proyecto supera el máximo de 1 GB")
	}
	return nil
}

func (l *importLimits) next() error {
	l.files++
	if l.files > maxImportFiles {
		return errf(http.StatusBadRequest, "demasiados archivos (máx. 10000)")
	}
	return nil
}

// importPath normaliza una ruta del zip o de la carpeta. skip indica una
// entrada que se omite sin error (ocultos, .git, __MACOSX…).
func importPath(p string) (rel string, skip bool, err error) {
	p = strings.ReplaceAll(p, `\`, "/")
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." || part == "__MACOSX" || part == "Thumbs.db" || part == "desktop.ini" {
			return "", true, nil
		}
	}
	rel, err = cleanPath(p)
	if err != nil {
		return "", false, errf(http.StatusBadRequest, "ruta no válida en el proyecto: "+p)
	}
	return rel, false, nil
}

// writeImported escribe un archivo del proyecto importado leyendo como mucho
// maxImportFile bytes de src.
func writeImported(dir, rel string, src io.Reader, lim *importLimits) error {
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return errf(http.StatusBadRequest, "conflicto entre carpetas y archivos en «"+rel+"»")
	}
	if fi, err := os.Lstat(abs); err == nil && !fi.Mode().IsRegular() {
		return errf(http.StatusBadRequest, "conflicto entre carpetas y archivos en «"+rel+"»")
	}
	out, err := os.OpenFile(abs, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return errf(http.StatusInternalServerError, "no se pudo escribir «"+rel+"»")
	}
	n, err := io.Copy(out, io.LimitReader(src, maxImportFile+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errf(http.StatusRequestEntityTooLarge, "la subida supera el máximo de 512 MB")
		}
		if errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errf(http.StatusBadRequest, "el archivo .zip está dañado")
		}
		return errf(http.StatusBadRequest, "no se pudo leer «"+rel+"»")
	}
	return lim.add(n)
}

// importProject crea un proyecto a partir de una subida multipart:
//
//	name     nombre del proyecto (antes que los archivos)
//	archive  un .zip con el proyecto, o bien
//	path + file (repetidos, en ese orden) con la ruta relativa de cada archivo
//
// POST /api/projects/import
func (s *Server) importProject(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "se esperaba un formulario multipart")
		return
	}
	longRequest(w, 15*time.Minute)
	u := currentUser(r)
	ctx := r.Context()

	var (
		name       string
		id         int64
		dir        string
		pendingRel string // ruta del siguiente «file»
		skipNext   bool
		hasFiles   bool
		lim        importLimits
	)
	fail := func(err error) {
		if id != 0 {
			_ = os.RemoveAll(dir)
			_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `DELETE FROM projects WHERE id = ?`, id)
		}
		writeAPIError(w, err)
	}
	// start crea la fila y el directorio cuando llega el primer archivo.
	start := func() error {
		if id != 0 {
			return nil
		}
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 128*4 || utf8.RuneCountInString(name) > 128 {
			return errf(http.StatusBadRequest, "nombre requerido (máx. 128)")
		}
		res, err := s.db.ExecContext(ctx, `INSERT INTO projects (name, owner_id, git_branch) VALUES (?, ?, 'main')`, name, u.ID)
		if err != nil {
			return internalErr(err)
		}
		id, _ = res.LastInsertId()
		dir = s.projectDir(id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errf(http.StatusInternalServerError, "no se pudo crear el directorio del proyecto")
		}
		return nil
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				fail(errf(http.StatusRequestEntityTooLarge, "la subida supera el máximo de 512 MB"))
			} else {
				fail(errf(http.StatusBadRequest, "subida incompleta"))
			}
			return
		}
		switch part.FormName() {
		case "name":
			b, _ := io.ReadAll(io.LimitReader(part, 1024))
			name = string(b)
		case "path":
			b, _ := io.ReadAll(io.LimitReader(part, 1024))
			pendingRel, skipNext, err = importPath(string(b))
			if err != nil {
				fail(err)
				return
			}
		case "file":
			if pendingRel == "" && !skipNext {
				fail(errf(http.StatusBadRequest, "falta la ruta de un archivo"))
				return
			}
			if err := start(); err != nil {
				fail(err)
				return
			}
			if !skipNext {
				if err := lim.next(); err != nil {
					fail(err)
					return
				}
				if err := writeImported(dir, pendingRel, part, &lim); err != nil {
					fail(err)
					return
				}
				hasFiles = true
			}
			pendingRel, skipNext = "", false
		case "archive":
			if hasFiles {
				fail(errf(http.StatusBadRequest, "envía un .zip o archivos sueltos, no ambos"))
				return
			}
			if err := start(); err != nil {
				fail(err)
				return
			}
			if err := s.extractUpload(part, dir, &lim); err != nil {
				fail(err)
				return
			}
			hasFiles = true
		}
		part.Close()
	}
	if !hasFiles {
		fail(errf(http.StatusBadRequest, "no se recibió ningún archivo"))
		return
	}
	main, err := pickMainFile(dir)
	if err != nil {
		fail(err)
		return
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE projects SET main_file = ? WHERE id = ?`, main, id); err != nil {
		fail(internalErr(err))
		return
	}
	if err := s.git.Init(ctx, dir, "main"); err != nil {
		fail(errf(http.StatusInternalServerError, "git init: "+err.Error()))
		return
	}
	p, err := s.projectByID(ctx, u, id)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// extractUpload guarda el .zip subido en un temporal (zip necesita acceso
// aleatorio) junto a los proyectos, no en /tmp (puede ser tmpfs), y lo extrae.
func (s *Server) extractUpload(src io.Reader, dir string, lim *importLimits) error {
	tmp, err := os.CreateTemp(filepath.Dir(dir), "import-*.zip")
	if err != nil {
		return errf(http.StatusInternalServerError, "no se pudo guardar el .zip")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, err := io.Copy(tmp, src)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errf(http.StatusRequestEntityTooLarge, "la subida supera el máximo de 512 MB")
		}
		return errf(http.StatusBadRequest, "subida incompleta")
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return errf(http.StatusBadRequest, "el archivo no es un .zip válido")
	}
	return extractZip(zr, dir, lim)
}

// extractZip extrae los archivos regulares del zip. Si todo cuelga de una
// única carpeta (lo habitual al comprimir una carpeta), se quita ese nivel.
func extractZip(zr *zip.Reader, dir string, lim *importLimits) error {
	type entry struct {
		f   *zip.File
		rel string
	}
	var entries []entry
	for _, f := range zr.File {
		name := f.Name
		if f.NonUTF8 || !utf8.ValidString(name) {
			name = decodeCP437(name)
		}
		if strings.HasSuffix(name, "/") || f.Mode().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			continue // enlaces simbólicos y otros tipos se ignoran
		}
		rel, skip, err := importPath(name)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		if f.UncompressedSize64 > maxImportFile {
			return errf(http.StatusBadRequest, "«"+rel+"» supera el máximo de 100 MB")
		}
		entries = append(entries, entry{f, rel})
	}
	if len(entries) == 0 {
		return errf(http.StatusBadRequest, "el .zip no contiene archivos")
	}
	if prefix := commonDir(len(entries), func(i int) string { return entries[i].rel }); prefix != "" {
		for i := range entries {
			entries[i].rel = strings.TrimPrefix(entries[i].rel, prefix)
		}
	}
	for _, e := range entries {
		if err := lim.next(); err != nil {
			return err
		}
		rc, err := e.f.Open()
		if err != nil {
			return errf(http.StatusBadRequest, "el archivo .zip está dañado")
		}
		err = writeImported(dir, e.rel, rc, lim)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// commonDir devuelve «carpeta/» si todas las rutas empiezan por esa misma
// carpeta (y no hay archivos sueltos en la raíz); si no, "".
func commonDir(n int, at func(int) string) string {
	first, _, ok := strings.Cut(at(0), "/")
	if !ok {
		return ""
	}
	prefix := first + "/"
	for i := 1; i < n; i++ {
		if !strings.HasPrefix(at(i), prefix) {
			return ""
		}
	}
	return prefix
}

// pickMainFile elige el archivo principal: main.typ en la raíz; si no, el
// primer .typ de la raíz que no se incluya desde otro; si no, el .typ menos
// profundo.
func pickMainFile(dir string) (string, error) {
	var typs []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && strings.EqualFold(filepath.Ext(p), ".typ") {
			rel, _ := filepath.Rel(dir, p)
			typs = append(typs, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(typs) == 0 {
		return "", errf(http.StatusBadRequest, "el proyecto no contiene ningún archivo .typ")
	}
	depth := func(p string) int { return strings.Count(p, "/") }
	sort.Slice(typs, func(i, j int) bool {
		a, b := typs[i], typs[j]
		if depth(a) != depth(b) {
			return depth(a) < depth(b)
		}
		// main.typ primero dentro de cada nivel
		if am, bm := path.Base(a) == "main.typ", path.Base(b) == "main.typ"; am != bm {
			return am
		}
		return a < b
	})
	if typs[0] == "main.typ" || depth(typs[0]) > 0 {
		return typs[0], nil
	}
	// Varios .typ en la raíz: preferir uno que nadie incluya/importe.
	var root []string
	for _, t := range typs {
		if depth(t) == 0 {
			root = append(root, t)
		}
	}
	if len(root) > 1 {
		var all strings.Builder
		for _, t := range typs {
			if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(t))); err == nil && len(b) < 4<<20 {
				all.Write(b)
				all.WriteByte('\n')
			}
		}
		text := all.String()
		for _, t := range root {
			if !strings.Contains(text, `"`+t+`"`) && !strings.Contains(text, `"./`+t+`"`) {
				return t, nil
			}
		}
	}
	return root[0], nil
}

// cp437 son los caracteres 0x80–0xFF de la página de códigos 437, la que usan
// por defecto los compresores de Windows cuando el nombre no va en UTF-8.
const cp437 = "ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ "

var cp437High = []rune(cp437)

func decodeCP437(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x80 {
			b.WriteByte(c)
		} else {
			b.WriteRune(cp437High[c-0x80])
		}
	}
	return b.String()
}
