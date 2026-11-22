// Package imgopt reduce el peso de imágenes PNG y JPEG sin perder detalle:
// los píxeles del resultado son idénticos a los del original (se comprueba
// decodificando ambos), algo imprescindible para capturas y diagramas que se
// revisan con zoom. Se conservan los metadatos que afectan a cómo se ven o a
// su tamaño en el documento (DPI, orientación EXIF, perfil de color), así que
// typst las coloca exactamente igual.
//
// PNG: se elige la representación más compacta que admite la imagen (paleta
// de hasta 256 colores —lo habitual en diagramas—, escala de grises o color
// verdadero, con alfa solo si hace falta), se comprime al máximo y se quitan
// los fragmentos de texto, fechas y similares.
// JPEG: no se recomprime (eso sí perdería detalle); solo se quitan los
// segmentos que no afectan a la imagen (comentarios, XMP, Photoshop…).
package imgopt

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// Por encima de esto no se procesa (≈ 480 MB en RGBA): se deja igual.
const maxPixels = 120_000_000

// slots limita a una las imágenes que se procesan a la vez: una captura de
// 65 Mpx necesita ~1 GB entre decodificar, convertir y verificar.
var slots = make(chan struct{}, 1)

var (
	pngMagic  = []byte("\x89PNG\r\n\x1a\n")
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
)

// Result describe lo que hizo Optimize.
type Result struct {
	Format string `json:"format"` // png | jpeg | "" (no es una imagen admitida)
	Before int    `json:"before"`
	After  int    `json:"after"`
	Note   string `json:"note,omitempty"` // por qué no se cambió, si aplica
}

// Optimize devuelve la versión optimizada de data (o data si no se logra
// reducir sin pérdida).
func Optimize(data []byte) ([]byte, Result) {
	r := Result{Before: len(data), After: len(data)}
	var out []byte
	switch {
	case bytes.HasPrefix(data, pngMagic):
		r.Format = "png"
		slots <- struct{}{}
		out, r.Note = optimizePNG(data)
		<-slots
	case bytes.HasPrefix(data, jpegMagic):
		r.Format = "jpeg"
		out, r.Note = optimizeJPEG(data)
	default:
		r.Note = "formato no admitido"
		return data, r
	}
	if out == nil || len(out) >= len(data) {
		if r.Note == "" {
			r.Note = "ya estaba optimizada"
		}
		return data, r
	}
	r.After = len(out)
	return out, r
}

// ---------- PNG ----------

type chunk struct {
	typ  string
	data []byte
}

func readChunks(data []byte) ([]chunk, bool) {
	var cs []chunk
	i := len(pngMagic)
	for i+12 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[i:]))
		if n < 0 || i+12+n > len(data) {
			return nil, false
		}
		cs = append(cs, chunk{string(data[i+4 : i+8]), data[i+8 : i+8+n]})
		if cs[len(cs)-1].typ == "IEND" {
			return cs, true
		}
		i += 12 + n
	}
	return nil, false
}

func writeChunk(b *bytes.Buffer, typ string, data []byte) {
	b.Write(binary.BigEndian.AppendUint32(nil, uint32(len(data))))
	b.WriteString(typ)
	b.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	b.Write(binary.BigEndian.AppendUint32(nil, crc.Sum32()))
}

// Fragmentos que se conservan: tamaño físico (DPI), orientación EXIF y color.
var keep = map[string]bool{"pHYs": true, "eXIf": true, "iCCP": true, "sRGB": true, "gAMA": true, "cHRM": true}

func optimizePNG(data []byte) ([]byte, string) {
	cs, ok := readChunks(data)
	if !ok {
		return nil, "PNG dañado"
	}
	var meta []chunk
	hasICC := false
	for _, c := range cs {
		switch {
		case c.typ == "acTL":
			return nil, "PNG animado: se deja igual"
		case keep[c.typ]:
			meta = append(meta, c)
			hasICC = hasICC || c.typ == "iCCP"
		}
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "PNG dañado"
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, "imagen demasiado grande para procesarla"
	}
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "PNG dañado"
	}

	// Candidatos sin pérdida; gana el más pequeño.
	var cands []image.Image
	if n := toNRGBA(src); n != nil {
		if p := toPaletted(n); p != nil {
			cands = append(cands, p)
		}
		if g := toGray(n); g != nil && !hasICC { // un perfil ICC de color no vale para grises
			cands = append(cands, g)
		}
	}
	cands = append(cands, src)

	enc := png.Encoder{CompressionLevel: png.BestCompression}
	var best []byte
	for _, img := range cands {
		var b bytes.Buffer
		if err := enc.Encode(&b, img); err != nil {
			continue
		}
		if best == nil || b.Len() < len(best) {
			best = b.Bytes()
		}
	}
	if best == nil {
		return nil, "no se pudo recodificar"
	}

	// Reinsertar los metadatos conservados justo después de IHDR.
	out, ok := readChunks(best)
	if !ok {
		return nil, "no se pudo recodificar"
	}
	var b bytes.Buffer
	b.Write(pngMagic)
	for i, c := range out {
		writeChunk(&b, c.typ, c.data)
		if i == 0 {
			for _, m := range meta {
				writeChunk(&b, m.typ, m.data)
			}
		}
	}
	res := b.Bytes()

	// Garantía: mismos píxeles.
	dec, err := png.Decode(bytes.NewReader(res))
	if err != nil || !samePixels(src, dec) {
		return nil, "la recodificación no era idéntica: se deja igual"
	}
	return res, ""
}

// toNRGBA devuelve los píxeles en NRGBA de 8 bits (nil si la imagen usa 16
// bits por canal: entonces solo se recomprime).
func toNRGBA(src image.Image) *image.NRGBA {
	switch m := src.(type) {
	case *image.NRGBA64, *image.RGBA64, *image.Gray16:
		return nil
	case *image.NRGBA:
		return m
	case *image.RGBA: // el decodificador PNG la usa solo para imágenes opacas
		dst := &image.NRGBA{Pix: m.Pix, Stride: m.Stride, Rect: m.Rect}
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] != 255 {
				dst = image.NewNRGBA(m.Rect)
				draw.Draw(dst, m.Rect, m, m.Rect.Min, draw.Src)
				break
			}
		}
		return dst
	}
	dst := image.NewNRGBA(src.Bounds())
	draw.Draw(dst, dst.Rect, src, src.Bounds().Min, draw.Src)
	return dst
}

// toPaletted convierte a paleta si la imagen tiene como mucho 256 colores
// distintos (contando el alfa).
func toPaletted(src *image.NRGBA) *image.Paletted {
	b := src.Rect
	dst := image.NewPaletted(b, nil)
	index := make(map[uint32]uint8, 256)
	var pal color.Palette
	lastKey, lastIdx := uint32(1), uint8(0) // 1 no es un color normalizado posible con A=0
	w := b.Dx()
	for y := 0; y < b.Dy(); y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w*4]
		out := dst.Pix[y*dst.Stride : y*dst.Stride+w]
		for x := 0; x < w; x++ {
			p := row[x*4 : x*4+4]
			key := uint32(p[0])<<24 | uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
			if p[3] == 0 {
				key = 0 // transparente: el color no importa
			}
			if key == lastKey {
				out[x] = lastIdx
				continue
			}
			i, ok := index[key]
			if !ok {
				if len(pal) == 256 {
					return nil
				}
				i = uint8(len(pal))
				index[key] = i
				pal = append(pal, color.NRGBA{uint8(key >> 24), uint8(key >> 16), uint8(key >> 8), uint8(key)})
			}
			out[x], lastKey, lastIdx = i, key, i
		}
	}
	dst.Palette = pal
	return dst
}

// toGray convierte a escala de grises si la imagen es opaca y gris.
func toGray(src *image.NRGBA) *image.Gray {
	b := src.Rect
	dst := image.NewGray(b)
	w := b.Dx()
	for y := 0; y < b.Dy(); y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w*4]
		out := dst.Pix[y*dst.Stride : y*dst.Stride+w]
		for x := 0; x < w; x++ {
			p := row[x*4 : x*4+4]
			if p[3] != 255 || p[0] != p[1] || p[1] != p[2] {
				return nil
			}
			out[x] = p[0]
		}
	}
	return dst
}

// samePixels compara dos imágenes; los píxeles totalmente transparentes son
// iguales sea cual sea su color.
func samePixels(a, b image.Image) bool {
	if a.Bounds().Size() != b.Bounds().Size() {
		return false
	}
	na, nb := toNRGBA(a), toNRGBA(b)
	if na == nil || nb == nil { // 16 bits: comparación genérica (lenta, poco frecuente)
		ra, rb := a.Bounds(), b.Bounds()
		for y := 0; y < ra.Dy(); y++ {
			for x := 0; x < ra.Dx(); x++ {
				ca := color.NRGBA64Model.Convert(a.At(ra.Min.X+x, ra.Min.Y+y)).(color.NRGBA64)
				cb := color.NRGBA64Model.Convert(b.At(rb.Min.X+x, rb.Min.Y+y)).(color.NRGBA64)
				if ca != cb && !(ca.A == 0 && cb.A == 0) {
					return false
				}
			}
		}
		return true
	}
	w := na.Rect.Dx()
	for y := 0; y < na.Rect.Dy(); y++ {
		ra := na.Pix[y*na.Stride : y*na.Stride+w*4]
		rb := nb.Pix[y*nb.Stride : y*nb.Stride+w*4]
		if bytes.Equal(ra, rb) {
			continue
		}
		for x := 0; x < w*4; x += 4 {
			if ra[x+3] == 0 && rb[x+3] == 0 {
				continue
			}
			if !bytes.Equal(ra[x:x+4], rb[x:x+4]) {
				return false
			}
		}
	}
	return true
}

// ---------- JPEG ----------

// optimizeJPEG quita los segmentos que no afectan a la imagen. Se conservan
// JFIF (APP0, DPI), EXIF (APP1, DPI y orientación), ICC (APP2) y Adobe
// (APP14, indica la transformación de color).
func optimizeJPEG(data []byte) ([]byte, string) {
	var out bytes.Buffer
	out.Write(data[:2]) // SOI
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return nil, "JPEG dañado"
		}
		m := data[i+1]
		if m == 0xFF {
			i++
			continue
		}
		if m == 0xDA { // inicio de los datos: se copia el resto tal cual
			out.Write(data[i:])
			return out.Bytes(), ""
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return nil, "JPEG dañado"
		}
		seg := data[i : i+2+n]
		body := seg[4:]
		drop := false
		switch {
		case m == 0xFE: // comentario
			drop = true
		case m == 0xE1 && !bytes.HasPrefix(body, []byte("Exif\x00\x00")): // XMP y similares
			drop = true
		case m >= 0xE3 && m <= 0xED, m == 0xEF: // APP3–APP13, APP15 (Photoshop, etc.)
			drop = true
		}
		if !drop {
			out.Write(seg)
		}
		i += 2 + n
	}
	return nil, "JPEG dañado"
}
