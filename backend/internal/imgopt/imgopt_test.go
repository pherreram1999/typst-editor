package imgopt

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"
)

func encodePNG(t *testing.T, img image.Image, extra ...chunk) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	cs, _ := readChunks(b.Bytes())
	var out bytes.Buffer
	out.Write(pngMagic)
	for i, c := range cs {
		writeChunk(&out, c.typ, c.data)
		if i == 0 {
			for _, e := range extra {
				writeChunk(&out, e.typ, e.data)
			}
		}
	}
	return out.Bytes()
}

// diagram imita un diagrama: pocos colores, bloques y líneas finas.
func diagram(w, h int, alpha bool) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	cols := []color.NRGBA{{255, 255, 255, 255}, {31, 56, 100, 255}, {220, 50, 47, 255}, {0, 0, 0, 255}}
	for y := range h {
		for x := range w {
			c := cols[0]
			switch {
			case x%40 == 0 || y%30 == 0:
				c = cols[3]
			case (x/40+y/30)%5 == 0:
				c = cols[1]
			case (x/40)%7 == 3:
				c = cols[2]
			}
			if alpha && x < 10 {
				c = color.NRGBA{uint8(x), 0, 0, 0} // transparente con «basura» en el color
			}
			m.SetNRGBA(x, y, c)
		}
	}
	return m
}

func noise(w, h int) *image.NRGBA {
	r := rand.New(rand.NewSource(1))
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	r.Read(m.Pix)
	for i := 3; i < len(m.Pix); i += 4 {
		m.Pix[i] = 255
	}
	return m
}

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func chunkTypes(b []byte) map[string][]byte {
	cs, _ := readChunks(b)
	m := map[string][]byte{}
	for _, c := range cs {
		m[c.typ] = c.data
	}
	return m
}

func TestPNGLossless(t *testing.T) {
	phys := chunk{"pHYs", append(binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 11811), 11811), 1)}
	exif := chunk{"eXIf", []byte("MM\x00\x2a\x00\x00\x00\x08\x00\x00\x00\x00\x00\x00")}
	text := chunk{"tEXt", []byte("Software\x00Editor de capturas")}
	gray := image.NewGray(image.Rect(0, 0, 300, 200))
	for i := range gray.Pix {
		gray.Pix[i] = uint8(i / 300)
	}
	cases := []struct {
		name    string
		img     image.Image
		extra   []chunk
		smaller bool
	}{
		{"diagrama", diagram(800, 600, false), []chunk{phys, exif, text}, true},
		{"diagrama con transparencia", diagram(400, 300, true), nil, true},
		{"grises", gray, nil, true},
		{"ruido (no se puede reducir sin pérdida)", noise(200, 200), nil, false},
	}
	for _, c := range cases {
		in := encodePNG(t, c.img, c.extra...)
		out, r := Optimize(in)
		if c.smaller != (r.After < r.Before) {
			t.Errorf("%s: %d → %d (%s)", c.name, r.Before, r.After, r.Note)
		}
		if !samePixels(decode(t, in), decode(t, out)) {
			t.Errorf("%s: los píxeles cambiaron", c.name)
		}
		if len(c.extra) > 0 {
			ct := chunkTypes(out)
			if !bytes.Equal(ct["pHYs"], phys.data) || !bytes.Equal(ct["eXIf"], exif.data) {
				t.Errorf("%s: se perdieron pHYs o eXIf", c.name)
			}
			if _, ok := ct["tEXt"]; ok {
				t.Errorf("%s: no se quitó tEXt", c.name)
			}
		}
	}
}

func TestPNGAnimatedUntouched(t *testing.T) {
	in := encodePNG(t, diagram(100, 100, false), chunk{"acTL", make([]byte, 8)})
	out, r := Optimize(in)
	if !bytes.Equal(in, out) || r.Note == "" {
		t.Errorf("un APNG no debe tocarse: %+v", r)
	}
}

func TestJPEGStripsOnlyIrrelevantSegments(t *testing.T) {
	var b bytes.Buffer
	jpeg.Encode(&b, noise(64, 64), &jpeg.Options{Quality: 90})
	enc := b.Bytes()
	seg := func(m byte, body string) []byte {
		s := []byte{0xFF, m}
		s = binary.BigEndian.AppendUint16(s, uint16(len(body)+2))
		return append(s, body...)
	}
	exif := seg(0xE1, "Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x00")
	icc := seg(0xE2, "ICC_PROFILE\x00\x01\x01perfil")
	xmp := seg(0xE1, "http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>"+string(make([]byte, 2000))+"</x:xmpmeta>")
	com := seg(0xFE, "comentario largo del programa de captura")
	in := append(append([]byte{}, enc[:2]...), append(append(append(append(exif, icc...), xmp...), com...), enc[2:]...)...)
	out, r := Optimize(in)
	if r.After >= r.Before {
		t.Fatalf("no se redujo: %+v", r)
	}
	if !bytes.Contains(out, exif) || !bytes.Contains(out, icc) || bytes.Contains(out, []byte("xmpmeta")) || bytes.Contains(out, []byte("comentario")) {
		t.Error("se quitaron segmentos incorrectos")
	}
	if !bytes.Equal(out[len(out)-len(enc)+2:], enc[2:]) {
		t.Error("los datos de imagen cambiaron")
	}
}

func TestUnsupported(t *testing.T) {
	in := []byte("GIF89a....")
	out, r := Optimize(in)
	if !bytes.Equal(in, out) || r.Format != "" {
		t.Errorf("%+v", r)
	}
}
