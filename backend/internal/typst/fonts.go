package typst

// Catálogo de fuentes para la vista previa en el navegador: las mismas que ve
// el typst del servidor (las del sistema, las de TYPST_FONT_PATHS y las que
// lleva incluidas el binario), para que el compilador WASM pagine igual que el
// PDF. El navegador recibe primero los esqueletos (fontmeta.go) y descarga
// cada fuente completa solo si el documento la usa.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// FontFile es un archivo de fuentes (una o varias caras, si es .ttc).
type FontFile struct {
	ID   string `json:"id"` // SHA-256 del contenido, en hexadecimal
	Size int64  `json:"size"`
	path string // archivo del sistema; vacío si está incluida en el binario
	data []byte // fuentes incluidas en el binario de typst
}

// Open devuelve el contenido completo de la fuente.
func (f *FontFile) Open() (io.ReadSeekCloser, error) {
	if f.path == "" {
		return nopCloser{bytes.NewReader(f.data)}, nil
	}
	return os.Open(f.path)
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

// FontCatalog es el resultado de Fonts.
type FontCatalog struct {
	Version string      // versión de typst (`typst --version`)
	ETag    string      // cambia si cambia cualquier fuente
	Files   []*FontFile // en el orden en que typst las carga
	byID    map[string]*FontFile

	metaOnce sync.Once
	meta     []byte // esqueletos, comprimidos con gzip (ver Meta)
	metaErr  error
}

// Font busca una fuente del catálogo por su ID.
func (c *FontCatalog) Font(id string) *FontFile { return c.byID[id] }

// Meta devuelve, comprimidos con gzip y en el orden de Files, los esqueletos
// de todas las fuentes, cada uno precedido de su longitud (uint32 big endian).
// Se calcula una vez.
func (c *FontCatalog) Meta() ([]byte, error) {
	c.metaOnce.Do(func() {
		var buf bytes.Buffer
		gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		for _, f := range c.Files {
			data := f.data
			if f.path != "" {
				var err error
				if data, err = os.ReadFile(f.path); err != nil {
					c.metaErr = err
					return
				}
			}
			sk, err := fontSkeleton(data)
			if err != nil {
				log.Printf("fuentes: no se pudo leer %s: %v", f.path, err)
				sk = nil // la fuente no se ofrece (typst tampoco podría leerla)
			}
			var n [4]byte
			binary.BigEndian.PutUint32(n[:], uint32(len(sk)))
			gz.Write(n[:])
			gz.Write(sk)
		}
		c.metaErr = gz.Close()
		c.meta = buf.Bytes()
	})
	return c.meta, c.metaErr
}

var fontPathRe = regexp.MustCompile(`^\s*[├└]\s+(.+?)\s*$`)

// Fonts devuelve el catálogo de fuentes. Se calcula la primera vez (lee y
// resume todas las fuentes del sistema, del orden de 1 s) y se conserva hasta
// que cambia alguna de las carpetas de fuentes adicionales (FontDirs): así
// una fuente copiada en DATA_DIR/fonts está disponible sin reiniciar. Las del
// sistema solo se vuelven a leer al reiniciar el servidor.
func (c *Compiler) Fonts(ctx context.Context) (*FontCatalog, error) {
	c.fontsMu.Lock()
	defer c.fontsMu.Unlock()
	sig := fontDirsSignature(c.FontDirs)
	if c.fonts != nil && sig == c.fontsSig {
		return c.fonts, nil
	}
	ver, err := exec.CommandContext(ctx, c.Bin, "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("typst --version: %w", err)
	}
	out, err := exec.CommandContext(ctx, c.Bin, "fonts", "--variants").Output()
	if err != nil {
		return nil, fmt.Errorf("typst fonts: %w", err)
	}
	cat := &FontCatalog{Version: strings.TrimSpace(string(ver)), byID: map[string]*FontFile{}}
	add := func(f *FontFile) {
		if cat.byID[f.ID] == nil {
			cat.byID[f.ID] = f
			cat.Files = append(cat.Files, f)
		}
	}
	seen := map[string]bool{}
	embedded := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := fontPathRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		p := m[1]
		// Las variables llevan « (Variable)» detrás de la ruta.
		if i := strings.LastIndex(p, " ("); i > 0 && strings.HasSuffix(p, ")") {
			if _, err := os.Stat(p); err != nil {
				p = p[:i]
			}
		}
		if p == "(Embedded)" {
			embedded++
			continue
		}
		if seen[p] {
			continue // una línea por cara de un .ttc
		}
		seen[p] = true
		f, err := hashFont(p)
		if err != nil {
			log.Printf("fuentes: se omite %s: %v", p, err)
			continue
		}
		add(f)
	}
	// typst carga las incluidas después de las del sistema.
	if embedded > 0 {
		emb, err := c.embeddedFonts()
		switch {
		case err != nil:
			log.Printf("fuentes: no se pudieron extraer las fuentes incluidas en typst: %v", err)
		case len(emb) != embedded:
			log.Printf("fuentes: typst incluye %d fuentes y se extrajeron %d; la vista previa en el navegador puede diferir", embedded, len(emb))
			fallthrough
		default:
			for _, f := range emb {
				add(f)
			}
		}
	}
	h := sha256.New()
	io.WriteString(h, cat.Version)
	for _, f := range cat.Files {
		io.WriteString(h, f.ID)
	}
	cat.ETag = hex.EncodeToString(h.Sum(nil)[:12])
	c.fonts = cat
	c.fontsSig = sig
	return cat, nil
}

// FontsSignature cambia cuando cambia el contenido de FontDirs.
func (c *Compiler) FontsSignature() string { return fontDirsSignature(c.FontDirs) }

// fontDirsSignature resume ruta, tamaño y fecha de cada archivo de las
// carpetas (recorrerlas cuesta microsegundos: son unas decenas de archivos).
func fontDirsSignature(dirs []string) string {
	h := sha256.New()
	for _, d := range dirs {
		filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			if info, err := e.Info(); err == nil {
				fmt.Fprintf(h, "%s\x00%d\x00%d\n", p, info.Size(), info.ModTime().UnixNano())
			}
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFont(p string) (*FontFile, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil, err
	}
	return &FontFile{ID: hex.EncodeToString(h.Sum(nil)), Size: n, path: p}, nil
}

// embeddedFonts extrae del ejecutable de typst las fuentes que lleva
// incluidas (se guardan tal cual en el binario): se buscan cabeceras de
// TrueType/OpenType con un directorio de tablas coherente y se recorta cada
// fuente hasta el final de su última tabla. Así coinciden siempre con la
// versión de typst instalada.
func (c *Compiler) embeddedFonts() ([]*FontFile, error) {
	bin, err := exec.LookPath(c.Bin)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return nil, err
	}
	var fonts []*FontFile
	for off := 0; off+12 <= len(data); off++ {
		if m := data[off : off+4]; string(m) != "OTTO" && string(m) != "\x00\x01\x00\x00" {
			continue
		}
		end, ok := sfntEnd(data[off:])
		if !ok {
			continue
		}
		font := data[off : off+end]
		if _, err := fontSkeleton(font); err != nil {
			continue
		}
		sum := sha256.Sum256(font)
		fonts = append(fonts, &FontFile{ID: hex.EncodeToString(sum[:]), Size: int64(len(font)), data: bytes.Clone(font)})
		off += end - 1
	}
	return fonts, nil
}

// sfntEnd comprueba que b empieza por un directorio de tablas plausible (con
// head y name) y devuelve dónde acaba la última tabla.
func sfntEnd(b []byte) (int, bool) {
	if len(b) < 12 {
		return 0, false
	}
	n := int(binary.BigEndian.Uint16(b[4:]))
	if n < 4 || n > 64 || 12+16*n > len(b) {
		return 0, false
	}
	end := 0
	head, name := false, false
	for k := 0; k < n; k++ {
		e := b[12+16*k:]
		for _, ch := range e[:4] {
			if !(ch == ' ' || ch == '/' || ch == '_' || ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z') {
				return 0, false
			}
		}
		switch string(e[:4]) {
		case "head":
			head = true
		case "name":
			name = true
		}
		off, size := int(binary.BigEndian.Uint32(e[8:])), int(binary.BigEndian.Uint32(e[12:]))
		if off < 12+16*n || off+size > len(b) || off+size > 32<<20 {
			return 0, false
		}
		end = max(end, off+size)
	}
	return end, head && name
}
