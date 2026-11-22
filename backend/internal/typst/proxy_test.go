package typst

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gradient genera una imagen con algo de detalle (y alfa si se pide).
func gradient(w, h int, alpha bool) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if alpha {
				a = uint8(x * 255 / w)
			}
			m.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8((x ^ y) & 0xFF), a})
		}
	}
	return m
}

// withChunks inserta fragmentos tras IHDR.
func withChunks(t *testing.T, img image.Image, chunks ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	enc := buf.Bytes()
	end := len(pngMagic) + 12 + int(binary.BigEndian.Uint32(enc[len(pngMagic):]))
	out := append([]byte{}, enc[:end]...)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, enc[end:]...)
}

func physChunk(dpi float64) []byte {
	ppm := uint32(math.Round(dpi / 0.0254))
	return pngChunk("pHYs", append(binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, ppm), ppm), 1))
}

// withSegments inserta segmentos tras SOI.
func withSegments(t *testing.T, img image.Image, segs ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	enc := buf.Bytes()
	out := append([]byte{}, enc[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, enc[2:]...)
}

func jfifSeg(dpi uint16) []byte {
	s := []byte{0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 1}
	s = binary.BigEndian.AppendUint16(s, dpi)
	s = binary.BigEndian.AppendUint16(s, dpi)
	return append(s, 0, 0)
}

func exifSeg(tiff []byte) []byte {
	s := []byte{0xFF, 0xE1}
	s = binary.BigEndian.AppendUint16(s, uint16(2+len(exifHead)+len(tiff)))
	return append(append(s, exifHead...), tiff...)
}

// orientationOnly es un TIFF (little endian) con solo la orientación.
func orientationOnly(o uint16) []byte {
	b := []byte("II\x2A\x00\x08\x00\x00\x00\x01\x00")
	b = binary.LittleEndian.AppendUint16(b, 0x0112)
	b = binary.LittleEndian.AppendUint16(b, 3)
	b = binary.LittleEndian.AppendUint32(b, 1)
	b = binary.LittleEndian.AppendUint16(b, o)
	return append(b, 0, 0, 0, 0, 0, 0)
}

func TestResizeAverages(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	// Izquierda: rojo opaco y transparente; derecha: blanco y negro.
	src.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 255})
	src.SetNRGBA(1, 0, color.NRGBA{255, 0, 0, 255})
	src.SetNRGBA(0, 1, color.NRGBA{0, 255, 0, 0}) // el color de un píxel transparente no cuenta
	src.SetNRGBA(1, 1, color.NRGBA{0, 255, 0, 0})
	src.SetNRGBA(2, 0, color.NRGBA{255, 255, 255, 255})
	src.SetNRGBA(3, 0, color.NRGBA{255, 255, 255, 255})
	src.SetNRGBA(2, 1, color.NRGBA{0, 0, 0, 255})
	src.SetNRGBA(3, 1, color.NRGBA{0, 0, 0, 255})
	got := resize(src, 2, 1)
	if c := got.RGBAAt(0, 0); c != (color.RGBA{128, 0, 0, 128}) {
		t.Errorf("izquierda = %v, quiero rojo al 50 %% premultiplicado", c)
	}
	if c := got.RGBAAt(1, 0); c != (color.RGBA{128, 128, 128, 255}) {
		t.Errorf("derecha = %v, quiero gris", c)
	}

	// Factor no entero (3 → 2): cada píxel de destino cubre 1,5 de origen.
	g := image.NewGray(image.Rect(0, 0, 3, 1))
	g.Pix = []uint8{0, 90, 180}
	got = resize(g, 2, 1)
	// (0·1 + 90·0,5) / 1,5 = 30 y (90·0,5 + 180·1) / 1,5 = 150
	if a, b := got.RGBAAt(0, 0).R, got.RGBAAt(1, 0).R; a != 30 || b != 150 {
		t.Errorf("factor 1,5: %d, %d; quiero 30, 150", a, b)
	}

	// Paleta y YCbCr pasan por sus caminos rápidos con el mismo resultado.
	pal := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White})
	for i := range pal.Pix {
		pal.Pix[i] = uint8(i % 2)
	}
	if c := resize(pal, 1, 1).RGBAAt(0, 0); c.R != 128 || c.A != 255 {
		t.Errorf("paleta: %v", c)
	}
}

func TestProxySizeKeepsAspect(t *testing.T) {
	for _, d := range [][2]int{{3000, 1000}, {5010, 12954}, {4000, 3000}, {1999, 1201}, {20000, 90}, {90, 40000}} {
		w, h := proxySize(d[0], d[1])
		if w*h > proxyMaxPixels || w < 1 || h < 1 {
			t.Errorf("%v → %d×%d excede el límite", d, w, h)
		}
		long, short, nl, ns := d[0], d[1], w, h
		if d[1] > d[0] {
			long, short, nl, ns = d[1], d[0], h, w
		}
		// Error del lado menor respecto a la proporción exacta, en píxeles.
		if e := math.Abs(float64(ns) - float64(short)*float64(nl)/float64(long)); e > 0.05 && short > 100 {
			t.Errorf("%v → %d×%d: el lado menor se desvía %.3f px", d, w, h, e)
		}
	}
}

func TestReadMeta(t *testing.T) {
	img := gradient(40, 20, false)
	cases := []struct {
		name        string
		data        []byte
		ok          bool
		dpi         float64
		orientation int
	}{
		{"png sin dpi", withChunks(t, img), true, 72, 0},
		{"png pHYs", withChunks(t, img, physChunk(150)), true, 150, 0},
		{"png eXIf", withChunks(t, img, physChunk(150), pngChunk("eXIf", buildExif(6, 200))), true, 200, 6},
		{"png eXIf solo orientación", withChunks(t, img, physChunk(150), pngChunk("eXIf", orientationOnly(3))), true, 150, 3},
		{"jpeg sin jfif", withSegments(t, img), true, 72, 0},
		{"jpeg jfif", withSegments(t, img, jfifSeg(96)), true, 96, 0},
		{"jpeg exif gana a jfif", withSegments(t, img, jfifSeg(96), exifSeg(buildExif(8, 300))), true, 300, 8},
		{"jpeg exif solo orientación", withSegments(t, img, jfifSeg(96), exifSeg(orientationOnly(6))), true, 96, 6},
		{"jpeg exif resolución 0", withSegments(t, img, exifSeg(buildExif(1, 0))), false, 0, 0},
		{"jpeg exif roto", withSegments(t, img, exifSeg([]byte("XX\x2A\x00"))), false, 0, 0},
		{"gif", []byte("GIF89a..."), false, 0, 0},
	}
	for _, c := range cases {
		m, ok := readMeta(c.data)
		if ok != c.ok || (ok && (math.Abs(m.dpi-c.dpi) > 0.05 || m.orientation != c.orientation)) {
			t.Errorf("%s: ok=%v dpi=%.3f orient=%d; quiero ok=%v dpi=%.0f orient=%d", c.name, ok, m.dpi, m.orientation, c.ok, c.dpi, c.orientation)
		}
	}
}

// TestProxyNaturalSize comprueba con el propio typst que cada copia reducida
// tiene exactamente el tamaño natural del original (la paginación de la vista
// previa no puede cambiar).
func TestProxyNaturalSize(t *testing.T) {
	bin, err := exec.LookPath("typst")
	if err != nil {
		t.Skip("typst no está en el PATH")
	}
	big := gradient(2400, 1000, false)
	alpha := gradient(1300, 1900, true)
	pal := image.NewPaletted(image.Rect(0, 0, 2100, 900), color.Palette{color.Black, color.White, color.NRGBA{200, 0, 0, 255}})
	for i := range pal.Pix {
		pal.Pix[i] = uint8(i % 3)
	}
	gray := image.NewGray(image.Rect(0, 0, 1700, 1100))
	files := map[string][]byte{
		"sin-dpi.png":       withChunks(t, big),
		"phys-300.png":      withChunks(t, alpha, physChunk(300)),
		"exif-o6.png":       withChunks(t, big, physChunk(150), pngChunk("eXIf", orientationOnly(6))),
		"paleta.png":        withChunks(t, pal, physChunk(96)),
		"sin-jfif.jpg":      withSegments(t, big),
		"jfif-200.jpeg":     withSegments(t, alpha, jfifSeg(200)),
		"exif-o6-300.jpg":   withSegments(t, big, jfifSeg(96), exifSeg(buildExif(6, 300))),
		"exif-o5-jfif.JPG":  withSegments(t, gray, jfifSeg(110), exifSeg(orientationOnly(5))),
		"pequena.png":       withChunks(t, gradient(300, 200, false)),
		"capitulo/fig.jpeg": withSegments(t, big, jfifSeg(72)),
	}
	src := t.TempDir()
	var names []string
	for name, data := range files {
		p := filepath.Join(src, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, `"`+name+`"`)
	}
	mainTyp := "#let files = (" + strings.Join(names, ", ") + ",)\n" +
		"#context for f in files [#metadata((f, measure(image(f)).width.pt(), measure(image(f)).height.pt())) <m>]\n"
	os.WriteFile(filepath.Join(src, "main.typ"), []byte(mainTyp), 0o644)

	mirror := filepath.Join(t.TempDir(), "preview")
	if err := syncMirror(src, mirror, "main.typ"); err != nil {
		t.Fatal(err)
	}
	measure := func(root string) map[string][2]float64 {
		cmd := exec.Command(bin, "eval", "--root", root, "--in", filepath.Join(root, "main.typ"), "query(<m>).map(it => it.value)")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("typst eval: %v\n%s", err, out)
		}
		var rows [][]any
		if err := json.Unmarshal(out, &rows); err != nil {
			t.Fatalf("salida de typst: %v\n%s", err, out)
		}
		m := map[string][2]float64{}
		for _, r := range rows {
			m[r[0].(string)] = [2]float64{r[1].(float64), r[2].(float64)}
		}
		return m
	}
	want, got := measure(src), measure(mirror)
	for name := range files {
		fi, err := os.Lstat(filepath.Join(mirror, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		proxied := fi.Mode().IsRegular()
		if proxied == (name == "pequena.png") {
			t.Errorf("%s: copia reducida = %v", name, proxied)
		}
		w, g := want[name], got[name]
		if math.Abs(w[0]-g[0]) > 0.01 || math.Abs(w[1]-g[1]) > 0.01 {
			t.Errorf("%s: original %.3f×%.3f pt, copia %.3f×%.3f pt", name, w[0], w[1], g[0], g[1])
		}
	}
}
