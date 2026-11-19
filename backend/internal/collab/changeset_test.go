package collab

import (
	"encoding/json"
	"testing"
)

func TestApplyChangeSet(t *testing.T) {
	cases := []struct {
		doc, cs, want string
		err           bool
	}{
		{"hola mundo", `[5,[5,"typst"]]`, "hola typst", false},
		{"", `[[0,"a","b"]]`, "a\nb", false},
		{"abc", `[[3]]`, "", false},
		{"a😀b", `[1,[2,"x"],1]`, "axb", false}, // el emoji ocupa 2 unidades UTF-16
		{"abc", `[2]`, "", true},               // longitud base distinta
		{"abc", `[5]`, "", true},
		{"abc", `{"x":1}`, "", true},
	}
	for _, c := range cases {
		got, err := applyChangeSet(encode(c.doc), json.RawMessage(c.cs))
		if c.err {
			if err == nil {
				t.Errorf("%q + %s: se esperaba error", c.doc, c.cs)
			}
			continue
		}
		if err != nil || decode(got) != c.want {
			t.Errorf("%q + %s = %q, %v; quiero %q", c.doc, c.cs, decode(got), err, c.want)
		}
	}
}

func TestDiffChangeSetRoundTrip(t *testing.T) {
	pairs := [][2]string{
		{"hola mundo", "hola typst mundo"},
		{"", "nuevo\ntexto"},
		{"todo", ""},
		{"a😀b", "a😁b"},
		{"línea1\nlínea2", "línea1\nlínea2\nlínea3"},
	}
	for _, p := range pairs {
		cs := diffChangeSet(encode(p[0]), encode(p[1]))
		got, err := applyChangeSet(encode(p[0]), cs)
		if err != nil || decode(got) != p[1] {
			t.Errorf("diff %q -> %q: %s => %q, %v", p[0], p[1], cs, decode(got), err)
		}
	}
	if diffChangeSet(encode("x"), encode("x")) != nil {
		t.Error("textos iguales deben producir nil")
	}
}
