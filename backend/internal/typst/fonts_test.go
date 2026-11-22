package typst

import (
	"bytes"
	"encoding/binary"
	"os/exec"
	"testing"
)

// tables devuelve las tablas (etiqueta -> contenido) de la cara en off.
func tables(t *testing.T, font []byte, off uint32) map[string][]byte {
	t.Helper()
	_, recs, err := faceTables(font, off)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string][]byte{}
	for _, r := range recs {
		m[r.tag] = font[r.off : r.off+r.size]
	}
	return m
}

// collection arma un .ttc con las caras dadas; las tablas se copian una vez
// por fuente distinta (dos caras de la misma fuente las comparten).
func collection(faces ...[]byte) []byte {
	var out bytes.Buffer
	out.WriteString("ttcf")
	binary.Write(&out, binary.BigEndian, uint32(0x00010000))
	binary.Write(&out, binary.BigEndian, uint32(len(faces)))
	out.Write(make([]byte, 4*len(faces)))
	dirs := make([]int, len(faces))
	for i, f := range faces {
		n := int(binary.BigEndian.Uint16(f[4:]))
		dirs[i] = out.Len()
		out.Write(f[:12+16*n])
	}
	placed := map[*byte]map[string]uint32{}
	for i, f := range faces {
		_, recs, _ := faceTables(f, 0)
		at, ok := placed[&f[0]]
		if !ok {
			at = map[string]uint32{}
			for _, r := range recs {
				at[r.tag] = uint32(out.Len())
				out.Write(f[r.off : r.off+r.size])
				for out.Len()%4 != 0 {
					out.WriteByte(0)
				}
			}
			placed[&f[0]] = at
		}
		buf := out.Bytes()
		binary.BigEndian.PutUint32(buf[12+4*i:], uint32(dirs[i]))
		for j, r := range recs {
			binary.BigEndian.PutUint32(buf[dirs[i]+12+16*j+8:], at[r.tag])
		}
	}
	return out.Bytes()
}

func TestFontSkeleton(t *testing.T) {
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst no está en el PATH")
	}
	fonts, err := New("typst").embeddedFonts()
	if err != nil || len(fonts) < 2 {
		t.Fatalf("fuentes incluidas en typst: %d, %v", len(fonts), err)
	}
	a, b := fonts[0].data, fonts[1].data
	ttc := collection(a, b, a)

	for name, font := range map[string][]byte{"otf": a, "ttc": ttc} {
		sk, err := fontSkeleton(font)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		offs, skOffs := []uint32{0}, []uint32{0}
		if name == "ttc" {
			offs, skOffs = nil, nil
			for i := 0; i < 3; i++ {
				offs = append(offs, binary.BigEndian.Uint32(font[12+4*i:]))
				skOffs = append(skOffs, binary.BigEndian.Uint32(sk[12+4*i:]))
			}
			// La tercera cara es la primera: comparte todas sus tablas.
			if !bytes.Equal(sk[skOffs[0]+12:skOffs[1]], sk[skOffs[2]+12:skOffs[2]+uint32(skOffs[1]-skOffs[0])]) {
				t.Errorf("ttc: las caras iguales no comparten tablas")
			}
		}
		if len(sk) >= len(font)/2 {
			t.Errorf("%s: el esqueleto ocupa %d de %d bytes", name, len(sk), len(font))
		}
		for i := range offs {
			orig, got := tables(t, font, offs[i]), tables(t, sk, skOffs[i])
			for tag, data := range got {
				if !metaTables[tag] {
					t.Errorf("%s: tabla %q no esperada", name, tag)
				}
				if tag == "post" {
					if len(data) != postHeader || binary.BigEndian.Uint32(data) != 0x00030000 || !bytes.Equal(data[4:], orig[tag][4:postHeader]) {
						t.Errorf("%s: tabla post mal recortada", name)
					}
				} else if !bytes.Equal(data, orig[tag]) {
					t.Errorf("%s: tabla %q distinta", name, tag)
				}
			}
			for tag := range orig {
				if metaTables[tag] && got[tag] == nil {
					t.Errorf("%s: falta la tabla %q", name, tag)
				}
			}
		}
	}
}
