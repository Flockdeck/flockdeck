package team

import (
	"strings"
	"testing"
)

func TestPlainTextIsOneLineOfAtMostMaxRunes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"  a \t b\n\nc  ", 50, "a b c"},
		{"x\x1b[31my\x00z", 50, "x [31my z"},
		{"line two three", 50, "line two three"},
		{"héllo wörld", 7, "héllo w"},
		{"abc def", 4, "abc"},
		{"", 5, ""},
		{"\n\n", 5, ""},
	}
	for _, c := range cases {
		if got := PlainText(c.in, c.max); got != c.want {
			t.Errorf("PlainText(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
	if got := PlainLabel(strings.Repeat("a", 100)); len(got) != MaxLabel {
		t.Errorf("PlainLabel kept %d characters, want %d", len(got), MaxLabel)
	}
}

func TestBlockIndentsEveryLineAndDropsControlCharacters(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"one\ntwo", "  one\n  two"},
		{"one\r\ntwo", "  one\n  two"},
		{"one\rtwo\vthree\ffour", "  one\n  two\n  three\n  four"},
		{"a b c\u0085d", "  a\n  b\n  c\n  d"},
		{"x\x1b[2Jy\x00z\x07", "  x[2Jyz"},
		{"tab\there", "  tab    here"},
		{"", "  "},
		{"héllo 日本", "  héllo 日本"},
	}
	for _, c := range cases {
		if got := Block(c.in, "  "); got != c.want {
			t.Errorf("Block(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
