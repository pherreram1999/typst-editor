package typst

// Copias reducidas de imágenes para la vista previa.
//
// Las capturas de pantalla y fotos de alta resolución dominan el tiempo y la
// memoria de typst: una tesis con ~140 PNG de hasta 65 Mpx tarda ~36 s y usa
// ~4 GB por compilación, casi todo en decodificar imágenes. La vista previa no
// necesita esa resolución, así que se compila contra copias de como máximo
// proxyMaxPixels que conservan el tamaño natural que typst calcula para el
// original (mismo DPI efectivo y misma orientación EXIF): la paginación de la
// vista previa es la misma que la del PDF, que sí usa los originales.
//
// Typst determina el DPI así: XResolution/YResolution de EXIF (el mayor, sin
// mirar la unidad); si no hay, la densidad JFIF (JPEG) o pHYs (PNG); si no,
// 72. Las copias llevan siempre un bloque EXIF con la resolución exacta (como
// racional) y la orientación del original. Ante metadatos que no se entienden
// con certeza no se genera copia y se usa el original.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const (
	// Imágenes con más píxeles que esto se reducen para la vista previa
	// (≈ 1690×950: de sobra para una figura a ancho de página en pantalla).
	proxyMaxPixels = 1_600_000
	// Por encima de esto no se decodifican en el servidor (≈ 480 MB en RGBA).
	proxyMaxSource = 120_000_000
	// DPI que typst asume cuando la imagen no declara ninguno.
	defaultDPI = 72
)

var (
	pngMagic  = []byte("\x89PNG\r\n\x1a\n")
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	exifHead  = []byte("Exif\x00\x00")
)

// imageMeta son los metadatos del original que afectan a su tamaño natural.
type imageMeta struct {
	format      string  // "png" | "jpeg"
	dpi         float64 // DPI efectivo para typst
	orientation int     // orientación EXIF; 0 si no tiene
}

// readMeta interpreta los metadatos como lo hace typst. ok es false si el
// formato no se reconoce o algún metadato es dudoso (no se hace copia).
func readMeta(data []byte) (m imageMeta, ok bool) {
	var tiff []byte
	switch {
	case bytes.HasPrefix(data, pngMagic):
		m.format = "png"
		tiff = pngExif(data)
	case bytes.HasPrefix(data, jpegMagic):
		m.format = "jpeg"
		tiff = jpegExif(data)
	default:
		return m, false
	}
	if tiff != nil {
		ex, ok := parseExif(tiff)
		if !ok {
			return m, false
		}
		m.orientation = ex.orientation
		if ex.hasRes {
			if ex.dpi <= 0 {
				return m, false
			}
			m.dpi = ex.dpi
			return m, true
		}
	}
	if m.format == "jpeg" {
		m.dpi = jfifDPI(data)
	} else {
		m.dpi = pngDPI(data)
	}
	if m.dpi <= 0 {
		m.dpi = defaultDPI
	}
	return m, true
}

// jpegExif devuelve el TIFF del primer segmento APP1 «Exif» de un JPEG.
func jpegExif(data []byte) []byte {
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return nil
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF: // relleno
			i++
			continue
		case marker == 0x01 || marker >= 0xD0 && marker <= 0xD8: // sin longitud
			i += 2
			continue
		case marker == 0xDA || marker == 0xD9: // inicio de datos / fin
			return nil
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return nil
		}
		seg := data[i+4 : i+2+n]
		if marker == 0xE1 && bytes.HasPrefix(seg, exifHead) {
			return seg[len(exifHead):]
		}
		i += 2 + n
	}
	return nil
}

// pngExif devuelve el contenido del fragmento eXIf de un PNG.
func pngExif(data []byte) []byte {
	i := len(pngMagic)
	for i+8 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[i:]))
		typ := string(data[i+4 : i+8])
		if n < 0 || i+12+n > len(data) {
			return nil
		}
		if typ == "eXIf" {
			return data[i+8 : i+8+n]
		}
		if typ == "IEND" {
			return nil
		}
		i += 12 + n
	}
	return nil
}

// jfifDPI lee la densidad del segmento JFIF, que typst solo busca justo
// después de SOI.
func jfifDPI(data []byte) float64 {
	if len(data) < 18 || !bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF, 0xE0}) ||
		string(data[6:11]) != "JFIF\x00" || binary.BigEndian.Uint16(data[4:]) < 16 {
		return 0
	}
	d := float64(max(binary.BigEndian.Uint16(data[14:]), binary.BigEndian.Uint16(data[16:])))
	switch data[13] {
	case 1: // puntos por pulgada
		return d
	case 2: // puntos por centímetro
		return d * 2.54
	}
	return 0
}

// pngDPI lee el fragmento pHYs (anterior a los datos de imagen).
func pngDPI(data []byte) float64 {
	i := len(pngMagic)
	for i+8 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[i:]))
		typ := string(data[i+4 : i+8])
		if typ == "IDAT" || n < 0 || i+12+n > len(data) {
			return 0
		}
		if typ == "pHYs" && n == 9 {
			p := data[i+8:]
			if p[8] != 1 { // unidad desconocida
				return 0
			}
			return float64(max(binary.BigEndian.Uint32(p), binary.BigEndian.Uint32(p[4:]))) * 0.0254
		}
		i += 12 + n
	}
	return 0
}

type exifInfo struct {
	orientation int
	hasRes      bool
	dpi         float64
}

// parseExif lee del IFD0 la orientación y la resolución.
func parseExif(tiff []byte) (ex exifInfo, ok bool) {
	if len(tiff) < 8 {
		return ex, false
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return ex, false
	}
	if bo.Uint16(tiff[2:]) != 42 {
		return ex, false
	}
	off := int(bo.Uint32(tiff[4:]))
	if off < 8 || off+2 > len(tiff) {
		return ex, false
	}
	n := int(bo.Uint16(tiff[off:]))
	for k := range n {
		e := off + 2 + 12*k
		if e+12 > len(tiff) {
			return ex, false
		}
		tag, typ, count := bo.Uint16(tiff[e:]), bo.Uint16(tiff[e+2:]), bo.Uint32(tiff[e+4:])
		switch tag {
		case 0x0112: // Orientation
			switch {
			case typ == 3 && count >= 1:
				ex.orientation = int(bo.Uint16(tiff[e+8:]))
			case typ == 4 && count >= 1:
				ex.orientation = int(bo.Uint32(tiff[e+8:]))
			default:
				return ex, false
			}
			if ex.orientation < 1 || ex.orientation > 8 {
				return ex, false
			}
		case 0x011A, 0x011B: // XResolution, YResolution
			if typ != 5 || count < 1 {
				return ex, false
			}
			p := int(bo.Uint32(tiff[e+8:]))
			if p < 0 || p+8 > len(tiff) {
				return ex, false
			}
			num, den := bo.Uint32(tiff[p:]), bo.Uint32(tiff[p+4:])
			ex.hasRes = true
			if den != 0 {
				ex.dpi = max(ex.dpi, float64(num)/float64(den))
			}
		}
	}
	return ex, true
}

// buildExif genera un TIFF (big endian) con la orientación y la resolución.
func buildExif(orientation int, dpi float64) []byte {
	type entry struct {
		tag, typ uint16
		val      uint32
	}
	entries := []entry{}
	if orientation > 0 {
		entries = append(entries, entry{0x0112, 3, uint32(orientation) << 16})
	}
	n := len(entries) + 3
	ratOff := uint32(8 + 2 + 12*n + 4)
	entries = append(entries,
		entry{0x011A, 5, ratOff},
		entry{0x011B, 5, ratOff + 8},
		entry{0x0128, 3, 2 << 16}, // ResolutionUnit = pulgadas
	)
	var b bytes.Buffer
	b.WriteString("MM\x00\x2A")
	be := binary.BigEndian
	b.Write(be.AppendUint32(nil, 8))
	b.Write(be.AppendUint16(nil, uint16(n)))
	for _, e := range entries {
		b.Write(be.AppendUint16(nil, e.tag))
		b.Write(be.AppendUint16(nil, e.typ))
		b.Write(be.AppendUint32(nil, 1))
		b.Write(be.AppendUint32(nil, e.val))
	}
	b.Write(be.AppendUint32(nil, 0)) // sin más IFD
	num, den := uint32(math.Round(dpi*10000)), uint32(10000)
	for range 2 {
		b.Write(be.AppendUint32(nil, num))
		b.Write(be.AppendUint32(nil, den))
	}
	return b.Bytes()
}

// pngChunk codifica un fragmento PNG con su CRC.
func pngChunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	return binary.BigEndian.AppendUint32(out, crc.Sum32())
}

// encodeProxy codifica la imagen reducida con los metadatos que fijan su
// tamaño natural.
func encodeProxy(img image.Image, format string, orientation int, dpi float64) ([]byte, error) {
	var buf bytes.Buffer
	exif := buildExif(orientation, dpi)
	if format == "jpeg" {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, err
		}
		enc := buf.Bytes()
		app1 := []byte{0xFF, 0xE1, 0, 0}
		binary.BigEndian.PutUint16(app1[2:], uint16(2+len(exifHead)+len(exif)))
		out := make([]byte, 0, len(enc)+len(app1)+len(exifHead)+len(exif))
		out = append(out, enc[:2]...) // SOI
		out = append(out, app1...)
		out = append(out, exifHead...)
		out = append(out, exif...)
		return append(out, enc[2:]...), nil
	}
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	enc := buf.Bytes()
	ihdrEnd := len(pngMagic) + 12 + int(binary.BigEndian.Uint32(enc[len(pngMagic):]))
	ppm := uint32(math.Round(dpi / 0.0254))
	phys := append(binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, ppm), ppm), 1)
	out := make([]byte, 0, len(enc)+len(exif)+64)
	out = append(out, enc[:ihdrEnd]...)
	out = append(out, pngChunk("pHYs", phys)...)
	out = append(out, pngChunk("eXIf", exif)...)
	return append(out, enc[ihdrEnd:]...), nil
}

// proxySize calcula el tamaño de la copia (≤ proxyMaxPixels). El DPI de la
// copia hace exacto el lado mayor; para que el otro también lo sea (y el
// tamaño natural no cambie ni una fracción de punto) se busca, hasta un 10 %
// por debajo del tamaño ideal, el lado mayor cuya proporción da un lado menor
// casi entero.
func proxySize(w, h int) (int, int) {
	long, short := w, h
	if h > w {
		long, short = h, w
	}
	k := math.Sqrt(proxyMaxPixels / (float64(w) * float64(h)))
	hi := max(1, int(float64(long)*k))
	lo := max(1, int(float64(hi)*0.9))
	best, bestErr := hi, math.Inf(1)
	for n := hi; n >= lo; n-- {
		exact := float64(short) * float64(n) / float64(long)
		if e := math.Abs(exact - math.Round(exact)); e < bestErr-1e-9 {
			best, bestErr = n, e
			if e < 1e-6 {
				break
			}
		}
	}
	nl := best
	ns := max(1, int(math.Round(float64(short)*float64(nl)/float64(long))))
	if h > w {
		return ns, nl
	}
	return nl, ns
}

// makeProxy escribe en dst una copia reducida de src si conviene. Devuelve
// false (sin error) cuando la imagen es pequeña o no se puede reducir con
// garantías; en ese caso se usa el original.
func makeProxy(src, dst string) (bool, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	meta, ok := readMeta(data)
	if !ok {
		return false, nil
	}
	cfg, err := decodeConfig(meta.format, data)
	if err != nil {
		return false, nil // typst informará del error con el original
	}
	px := cfg.Width * cfg.Height
	if px <= proxyMaxPixels || px > proxyMaxSource || cfg.ColorModel == color.CMYKModel {
		return false, nil
	}
	img, err := decode(meta.format, data)
	data = nil
	if err != nil {
		return false, nil
	}
	b := img.Bounds()
	nw, nh := proxySize(b.Dx(), b.Dy())
	small := resize(img, nw, nh)
	img = nil
	// El lado mayor de la copia mide en natural exactamente lo del original.
	scale := float64(nw) / float64(b.Dx())
	if b.Dy() > b.Dx() {
		scale = float64(nh) / float64(b.Dy())
	}
	out, err := encodeProxy(small, meta.format, meta.orientation, meta.dpi*scale)
	if err != nil {
		return false, err
	}
	return true, writeAtomic(dst, out)
}

func decodeConfig(format string, data []byte) (image.Config, error) {
	if format == "png" {
		return png.DecodeConfig(bytes.NewReader(data))
	}
	return jpeg.DecodeConfig(bytes.NewReader(data))
}

func decode(format string, data []byte) (image.Image, error) {
	r := bufio.NewReader(bytes.NewReader(data))
	if format == "png" {
		return png.Decode(r)
	}
	return jpeg.Decode(r)
}

// writeAtomic escribe data en path mediante un temporal y rename, que
// reemplaza la entrada (aunque sea un enlace simbólico) sin seguirla.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// resize reduce src a dw×dh promediando, para cada píxel de destino, el área
// de origen que cubre (filtro de caja): conserva bien el texto de las
// capturas. Trabaja en RGBA premultiplicado (los bordes transparentes no se
// oscurecen) y fila a fila, sin búferes del tamaño del original.
func resize(src image.Image, dw, dh int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	sx, sy := float64(sw)/float64(dw), float64(sh)/float64(dh)

	// Para cada columna de destino: primera columna de origen y la cobertura
	// de cada una (todas suman sx).
	starts := make([]int, dw)
	weights := make([][]float32, dw)
	for x := range dw {
		x0, x1 := float64(x)*sx, min(float64(x+1)*sx, float64(sw))
		i0, i1 := int(x0), min(int(math.Ceil(x1)), sw)
		ws := make([]float32, i1-i0)
		for i := i0; i < i1; i++ {
			ws[i-i0] = float32(min(float64(i+1), x1) - max(float64(i), x0))
		}
		starts[x], weights[x] = i0, ws
	}

	row := make([]float32, sw*4)  // fila de origen
	hrow := make([]float32, dw*4) // fila de origen reducida en horizontal
	acc := make([]float32, dw*4)  // fila de destino en curso
	pal := paletteTable(src)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	last := -1
	for j := range dh {
		y0, y1 := float64(j)*sy, min(float64(j+1)*sy, float64(sh))
		clear(acc)
		for y := int(y0); y < sh && float64(y) < y1; y++ {
			wy := float32(min(float64(y+1), y1) - max(float64(y), y0))
			if wy <= 0 {
				continue
			}
			if y != last { // la fila frontera se comparte con la siguiente
				readRow(src, b.Min.X, b.Min.Y+y, sw, row, pal)
				for x, ws := range weights {
					p := starts[x] * 4
					var r, g, bl, a float32
					for _, w := range ws {
						r += w * row[p]
						g += w * row[p+1]
						bl += w * row[p+2]
						a += w * row[p+3]
						p += 4
					}
					hrow[x*4], hrow[x*4+1], hrow[x*4+2], hrow[x*4+3] = r, g, bl, a
				}
				last = y
			}
			for i, v := range hrow {
				acc[i] += wy * v
			}
		}
		norm := float32(1 / (sx * (y1 - y0)))
		px := dst.Pix[j*dst.Stride : j*dst.Stride+dw*4]
		for i, v := range acc {
			px[i] = uint8(min(max(v*norm+0.5, 0), 255))
		}
	}
	return dst
}

// paletteTable precalcula los colores premultiplicados de una imagen con paleta.
func paletteTable(src image.Image) [][4]float32 {
	m, ok := src.(*image.Paletted)
	if !ok {
		return nil
	}
	t := make([][4]float32, len(m.Palette))
	for i, c := range m.Palette {
		r, g, b, a := c.RGBA()
		t[i] = [4]float32{float32(r) / 257, float32(g) / 257, float32(b) / 257, float32(a) / 257}
	}
	return t
}

// readRow copia la fila y (w píxeles desde x0) en row como RGBA premultiplicado 0–255.
func readRow(src image.Image, x0, y, w int, row []float32, pal [][4]float32) {
	switch m := src.(type) {
	case *image.RGBA:
		p := m.Pix[m.PixOffset(x0, y):]
		for i := range w * 4 {
			row[i] = float32(p[i])
		}
	case *image.NRGBA:
		p := m.Pix[m.PixOffset(x0, y):]
		for i := 0; i < w*4; i += 4 {
			a := float32(p[i+3])
			k := a / 255
			row[i], row[i+1], row[i+2], row[i+3] = float32(p[i])*k, float32(p[i+1])*k, float32(p[i+2])*k, a
		}
	case *image.Gray:
		p := m.Pix[m.PixOffset(x0, y):]
		for x := range w {
			v := float32(p[x])
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = v, v, v, 255
		}
	case *image.YCbCr:
		for x := range w {
			yi, ci := m.YOffset(x0+x, y), m.COffset(x0+x, y)
			r, g, b := color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = float32(r), float32(g), float32(b), 255
		}
	case *image.Paletted:
		p := m.Pix[m.PixOffset(x0, y):]
		for x := range w {
			var c [4]float32
			if int(p[x]) < len(pal) {
				c = pal[p[x]]
			}
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = c[0], c[1], c[2], c[3]
		}
	default:
		for x := range w {
			r, g, b, a := src.At(x0+x, y).RGBA()
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = float32(r)/257, float32(g)/257, float32(b)/257, float32(a)/257
		}
	}
}
