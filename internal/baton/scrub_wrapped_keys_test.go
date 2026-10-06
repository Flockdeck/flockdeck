package baton

import (
	"math/rand"
	"strings"
	"testing"
)

// wrapCase is a random key wrapped over lines, and the pieces that must not survive.
type wrapCase struct {
	text   string
	chunks []string
}

// wrapCases makes n wrapped keys: 40 to 120 characters, widths of 16 to 76, five
// prefixes, four ways of ending and starting a line, CRLF and LF.
func wrapCases(n int) []wrapCase {
	r := rand.New(rand.NewSource(6))
	prefixes := []string{"", "ghp_", "sk-", "xoxb-", "glpat-"}
	seps := []struct{ end, start string }{{"\n", ""}, {"\\n", ""}, {"\n", "    "}, {"\n", "- "}}
	keyNames := []string{"token=", "API_KEY=", "secret: ", ""}
	var out []wrapCase
	for len(out) < n {
		length := 40 + r.Intn(81)
		width := 16 + r.Intn(61)
		key := prefixes[r.Intn(len(prefixes))] + gatePick(r, gateAlpha+"-_", length)
		key = key[:length]
		sep := seps[r.Intn(len(seps))]
		crlf := r.Intn(2) == 0
		name := keyNames[r.Intn(len(keyNames))]
		var chunks []string
		for i := 0; i < len(key); i += width {
			chunks = append(chunks, key[i:min(i+width, len(key))])
		}
		if len(chunks) < 2 {
			continue
		}
		var b strings.Builder
		for i, c := range chunks {
			if i == 0 {
				b.WriteString(name)
			} else {
				b.WriteString(sep.start)
			}
			b.WriteString(c)
			if i < len(chunks)-1 {
				end := sep.end
				if crlf {
					end = strings.Replace(end, "\n", "\r\n", 1)
				}
				b.WriteString(end)
			}
		}
		out = append(out, wrapCase{b.String(), chunks})
	}
	return out
}

// A random key wrapped over three lines or more loses every line; over two, almost
// every one. The bound for two lines is a documented limit: a 16-character first line
// and a short second look like two words.
func TestWrappedKeysLoseEveryLine(t *testing.T) {
	sc := NewScrubber()
	cases := wrapCases(2304)
	leaked3, runs3, leaked2, runs2 := 0, 0, 0, 0
	shown := 0
	for _, c := range cases {
		got, _ := sc.Scrub(c.text)
		leaked := false
		for _, ch := range c.chunks {
			if len(ch) >= 8 && strings.Contains(got, ch) {
				leaked = true
			}
		}
		if len(c.chunks) >= 3 {
			runs3++
			if leaked {
				leaked3++
				if shown < 6 {
					shown++
					t.Errorf("a wrapped key kept a line:\n%q\n->\n%q", c.text, got)
				}
			}
		} else {
			runs2++
			if leaked {
				leaked2++
			}
		}
	}
	t.Logf("runs of 3+ lines: %d of %d leaked a line; runs of 2: %d of %d", leaked3, runs3, leaked2, runs2)
	if leaked3 > 0 {
		t.Errorf("%d of %d keys of three lines or more kept a line", leaked3, runs3)
	}
	if runs2 > 0 && leaked2*10 > runs2 {
		t.Errorf("%d of %d keys of two lines kept a line, over the bound of one in ten", leaked2, runs2)
	}
}

// Columns of identifiers and prose that ends in a long name are not wrapped keys.
func TestColumnsOfIdentifiersAndProseAreNotWrappedKeys(t *testing.T) {
	for name, in := range map[string]string{
		"container ids": "3f9a7c1d2e4b\n9b1c2d3e4f50\na1b2c3d4e5f6\n0f1e2d3c4b5a\n7a6b5c4d3e2f\n",
		"short shas":    "3f9a7c1d\n9b1c2d3e\na1b2c3d4\n0f1e2d3c\n7a6b5c4d\n",
		"user ids":      "user-3f9a7c1d2e4b6a80\nuser-9b1c2d3e4f506172\nuser-a1b2c3d4e5f60718\nuser-0f1e2d3c4b5a6978\n",
		"uuids":         "550e8400-e29b-41d4-a716-446655440000\n6ba7b810-9dad-11d1-80b4-00c04fd430c8\n886313e1-3b8a-5372-9b90-0c9aee199e5d\n",
		"prose":         "token count is 4096 for ConversationSessionIdentifier\nXyz1234ABcd\n",
		"prose lines":   "the limit comes from ConversationSessionIdentifier\nand then nothing follows here\n",
	} {
		got, _ := NewScrubber().Scrub(in)
		if got != in {
			t.Errorf("%s: Scrub(%q) = %q", name, in, got)
		}
	}
}
