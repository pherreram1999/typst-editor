package typst

// Espejo del proyecto para la vista previa: dst reproduce el árbol de src con
// directorios reales, enlaces simbólicos a los archivos originales y, en lugar
// de las imágenes grandes, sus copias reducidas (proxy.go). El archivo
// principal se copia porque typst canonicaliza la ruta de entrada y un enlace
// apuntaría fuera de --root. Las rutas que typst ve (y las de sus
// diagnósticos) son las mismas que en el proyecto.
//
// En dst+".json" se guarda, por imagen, el tamaño y la fecha del original con
// los que se generó su copia, para no volver a examinarla mientras no cambie.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

type mirrorEntry struct {
	Size  int64 `json:"size"`
	MTime int64 `json:"mtime"`
	Proxy bool  `json:"proxy"` // false: se enlaza al original
}

// proxySlots limita las imágenes que se decodifican a la vez en todo el
// servidor (cada una puede ocupar cientos de MB).
var proxySlots = make(chan struct{}, max(1, min(4, runtime.NumCPU()/2)))

func isImageCandidate(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

// syncMirror actualiza dst para que refleje src; main es la ruta (con /) del
// archivo principal relativa a src.
func syncMirror(src, dst, main string) error {
	src, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if err := realDir(dst); err != nil {
		return err
	}
	manifest := dst + ".json"
	old := map[string]mirrorEntry{}
	if b, err := os.ReadFile(manifest); err == nil {
		_ = json.Unmarshal(b, &old)
	}
	next := map[string]mirrorEntry{}
	want := map[string]bool{}
	type job struct {
		rel  string
		info fs.FileInfo
	}
	var jobs []job

	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			want[rel] = true
			return realDir(target)
		}
		if !d.Type().IsRegular() { // enlaces simbólicos y archivos especiales
			return nil
		}
		want[rel] = true
		key := filepath.ToSlash(rel)
		if key == main {
			return copyIfChanged(p, target)
		}
		if isImageCandidate(rel) {
			info, err := d.Info()
			if err != nil {
				return err
			}
			e, ok := old[key]
			if ok && e.Size == info.Size() && e.MTime == info.ModTime().UnixNano() && mirrored(p, target, e.Proxy) {
				next[key] = e
				return nil
			}
			jobs = append(jobs, job{rel, info})
			return nil
		}
		return ensureLink(p, target)
	})
	if err != nil {
		return err
	}

	// Imágenes nuevas o modificadas: copia reducida o enlace al original.
	if len(jobs) > 0 {
		var mu sync.Mutex
		var wg sync.WaitGroup
		var firstErr error
		proxied := 0
		for _, j := range jobs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				proxySlots <- struct{}{}
				orig, target := filepath.Join(src, j.rel), filepath.Join(dst, j.rel)
				ok, err := makeProxy(orig, target)
				<-proxySlots
				if err != nil {
					log.Printf("vista previa: no se pudo reducir %s: %v", j.rel, err)
					ok = false
				}
				if !ok {
					err = ensureLink(orig, target)
				}
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					firstErr = cmpErr(firstErr, err)
					return
				}
				if ok {
					proxied++
				}
				next[filepath.ToSlash(j.rel)] = mirrorEntry{Size: j.info.Size(), MTime: j.info.ModTime().UnixNano(), Proxy: ok}
			}()
		}
		wg.Wait()
		if firstErr != nil {
			return firstErr
		}
		if proxied > 0 {
			// Las imágenes decodificadas ocupan mucho: devolver la memoria ya.
			debug.FreeOSMemory()
		}
	}

	// Quitar lo que ya no existe en el proyecto (los enlaces se borran sin
	// seguirlos; WalkDir no entra en directorios enlazados).
	var stale []string
	err = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dst, p)
		if rel == "." || want[rel] {
			return nil
		}
		stale = append(stale, p)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range stale {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}

	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return writeAtomic(manifest, b)
}

func cmpErr(first, err error) error {
	if first != nil {
		return first
	}
	return err
}

// mirrored indica si target sigue siendo la copia (proxy) o el enlace a orig.
func mirrored(orig, target string, proxy bool) bool {
	if proxy {
		fi, err := os.Lstat(target)
		return err == nil && fi.Mode().IsRegular()
	}
	cur, err := os.Readlink(target)
	return err == nil && cur == orig
}

// realDir asegura que p es un directorio real (no un enlace).
func realDir(p string) error {
	if fi, err := os.Lstat(p); err == nil {
		if fi.IsDir() {
			return nil
		}
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return os.MkdirAll(p, 0o755)
}

// ensureLink hace que target sea un enlace simbólico a orig.
func ensureLink(orig, target string) error {
	if cur, err := os.Readlink(target); err == nil && cur == orig {
		return nil
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return os.Symlink(orig, target)
}

// copyIfChanged deja en target una copia real de orig.
func copyIfChanged(orig, target string) error {
	b, err := os.ReadFile(orig)
	if err != nil {
		return err
	}
	if fi, err := os.Lstat(target); err == nil && fi.Mode().IsRegular() {
		if cur, err := os.ReadFile(target); err == nil && bytes.Equal(cur, b) {
			return nil
		}
	}
	return writeAtomic(target, b)
}

// MirrorFile es un archivo del espejo de la vista previa, tal como lo ve el
// compilador del navegador.
type MirrorFile struct {
	Path string `json:"path"`
	V    string `json:"v"` // cambia cuando cambia el contenido (tamaño y fecha)
}

// MaxBrowserFile es el tamaño máximo de un archivo que se ofrece al
// compilador del navegador (los mayores no se descargan).
const MaxBrowserFile = 48 << 20

// PreviewFiles actualiza el espejo de la vista previa y lista sus archivos
// (con las imágenes ya reducidas). Devuelve también la ruta del espejo, de
// donde se sirven.
func (c *Compiler) PreviewFiles(root, main, outDir string) (string, []MirrorFile, error) {
	mirror, err := previewRoot(outDir)
	if err != nil {
		return "", nil, err
	}
	if err := syncMirror(root, mirror, main); err != nil {
		return "", nil, err
	}
	var files []MirrorFile
	err = filepath.WalkDir(mirror, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == mirror {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := os.Stat(p) // sigue el enlace al original
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBrowserFile {
			return nil
		}
		rel, _ := filepath.Rel(mirror, p)
		files = append(files, MirrorFile{filepath.ToSlash(rel), strconv.FormatInt(info.Size(), 36) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 36)})
		return nil
	})
	return mirror, files, err
}
