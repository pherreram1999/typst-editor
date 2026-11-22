package typst

// Esqueletos de fuentes para la vista previa en el navegador.
//
// El compilador WASM necesita, de cada fuente que podría usar, su FontInfo
// (familia, variante, cobertura…) y solo descarga el archivo completo cuando
// el documento la usa. Ese FontInfo lo calcula el propio compilador a partir
// de las tablas de metadatos, así que el servidor manda un «esqueleto»: la
// misma fuente (o colección .ttc) sin contornos, mapas de bits ni tablas de
// composición tipográfica. Ocupa unos KB y produce exactamente el mismo
// FontInfo que la fuente completa.

import (
	"encoding/binary"
	"errors"
)

// metaTables son las tablas que se conservan en el esqueleto.
var metaTables = map[string]bool{
	"head": true, "hhea": true, "maxp": true, "name": true, "OS/2": true,
	"post": true, "cmap": true, "fvar": true, "avar": true, "STAT": true,
	"MATH": true,
}

// postHeader es el tamaño de la cabecera de la tabla post.
const postHeader = 32

var errBadFont = errors.New("fuente no válida")

type tableRec struct {
	tag            string
	sum, off, size uint32
}

// faceTables lee el directorio de tablas de la cara que empieza en off.
func faceTables(data []byte, off uint32) (uint32, []tableRec, error) {
	if uint64(off)+12 > uint64(len(data)) {
		return 0, nil, errBadFont
	}
	version := binary.BigEndian.Uint32(data[off:])
	n := int(binary.BigEndian.Uint16(data[off+4:]))
	if uint64(off)+12+16*uint64(n) > uint64(len(data)) {
		return 0, nil, errBadFont
	}
	recs := make([]tableRec, 0, n)
	for i := 0; i < n; i++ {
		e := data[off+12+16*uint32(i):]
		r := tableRec{string(e[:4]), binary.BigEndian.Uint32(e[4:]), binary.BigEndian.Uint32(e[8:]), binary.BigEndian.Uint32(e[12:])}
		if uint64(r.off)+uint64(r.size) > uint64(len(data)) {
			return 0, nil, errBadFont
		}
		recs = append(recs, r)
	}
	return version, recs, nil
}

// fontSkeleton devuelve la fuente (TTF/OTF o colección TTC) con solo las
// tablas de metaTables. Las tablas compartidas entre caras de una colección
// se escriben una vez.
func fontSkeleton(data []byte) ([]byte, error) {
	if len(data) < 12 {
		return nil, errBadFont
	}
	offsets := []uint32{0}
	ttc := string(data[:4]) == "ttcf"
	if ttc {
		n := binary.BigEndian.Uint32(data[8:])
		if n == 0 || n > 1000 || 12+4*uint64(n) > uint64(len(data)) {
			return nil, errBadFont
		}
		offsets = make([]uint32, n)
		for i := range offsets {
			offsets[i] = binary.BigEndian.Uint32(data[12+4*i:])
		}
	}
	type face struct {
		version uint32
		recs    []tableRec
	}
	faces := make([]face, len(offsets))
	for i, off := range offsets {
		v, recs, err := faceTables(data, off)
		if err != nil {
			return nil, err
		}
		kept := recs[:0]
		for _, r := range recs {
			if !metaTables[r.tag] {
				continue
			}
			// post: solo la cabecera (sin los nombres de glifos, que en muchas
			// fuentes son la mayor parte del esqueleto); se marca como 3.0.
			if r.tag == "post" && r.size > postHeader {
				r.size = postHeader
			}
			kept = append(kept, r)
		}
		faces[i] = face{v, kept}
	}
	// Disposición: cabecera TTC (si la hay), directorios y datos alineados a 4.
	pos := uint32(0)
	if ttc {
		pos = 12 + 4*uint32(len(faces))
	}
	dirOff := make([]uint32, len(faces))
	for i, f := range faces {
		dirOff[i] = pos
		pos += 12 + 16*uint32(len(f.recs))
	}
	type key struct{ off, size uint32 }
	placed := map[key]uint32{}
	var order []key
	for _, f := range faces {
		for _, r := range f.recs {
			k := key{r.off, r.size}
			if _, ok := placed[k]; ok {
				continue
			}
			placed[k] = pos
			order = append(order, k)
			pos += (r.size + 3) &^ 3
		}
	}
	out := make([]byte, pos)
	if ttc {
		copy(out, data[:8]) // «ttcf» y versión
		binary.BigEndian.PutUint32(out[8:], uint32(len(faces)))
		for i := range faces {
			binary.BigEndian.PutUint32(out[12+4*i:], dirOff[i])
		}
	}
	for i, f := range faces {
		d := out[dirOff[i]:]
		n := uint16(len(f.recs))
		binary.BigEndian.PutUint32(d, f.version)
		binary.BigEndian.PutUint16(d[4:], n)
		// searchRange, entrySelector y rangeShift (como exige el formato).
		es := uint16(0)
		for 1<<(es+1) <= n {
			es++
		}
		binary.BigEndian.PutUint16(d[6:], 16<<es)
		binary.BigEndian.PutUint16(d[8:], es)
		binary.BigEndian.PutUint16(d[10:], 16*n-16<<es)
		for j, r := range f.recs { // los registros ya están ordenados por etiqueta
			e := d[12+16*j:]
			copy(e, r.tag)
			binary.BigEndian.PutUint32(e[4:], r.sum)
			binary.BigEndian.PutUint32(e[8:], placed[key{r.off, r.size}])
			binary.BigEndian.PutUint32(e[12:], r.size)
		}
	}
	for _, k := range order {
		at := placed[k]
		copy(out[at:], data[k.off:k.off+k.size])
	}
	for _, f := range faces {
		for _, r := range f.recs {
			if r.tag == "post" && r.size == postHeader {
				binary.BigEndian.PutUint32(out[placed[key{r.off, r.size}]:], 0x00030000)
			}
		}
	}
	return out, nil
}
