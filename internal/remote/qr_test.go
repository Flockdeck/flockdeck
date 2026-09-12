package remote

import (
	"fmt"
	"strings"
	"testing"

	"rsc.io/qr"
)

// Both drawings of a pairing link carry every module where the encoder put
// it. The other QR tests look only at the shape, which a drawing with its
// half-blocks swapped or its margin shifted would still have, and such a
// code would not scan; nothing else would notice before a phone did.
func TestQRCodesDrawEveryModule(t *testing.T) {
	for _, link := range []string{
		"https://remote.flockdeck.ai/pair#fdp_mOtvVS9Kulc8-x5SvsvcMw",
		"https://relay.example.com/some/longer/base/path/pair#fdp_0123456789abcdefghijklmnopqrstuvwxyz",
	} {
		// In a terminal the light modules are drawn, two rows to a line,
		// inside a margin of two.
		code, err := qr.Encode(link, qr.L)
		if err != nil {
			t.Fatal(err)
		}
		out, err := QRTerminal(link)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		const margin = 2
		n := code.Size + 2*margin
		if len(lines) != (n+1)/2 {
			t.Fatalf("%s: %d lines, want %d", link, len(lines), (n+1)/2)
		}
		for y := 0; y < n; y += 2 {
			row := []rune(lines[y/2])
			if len(row) != n {
				t.Fatalf("%s: line %d is %d wide, want %d", link, y/2, len(row), n)
			}
			for x := range row {
				top := row[x] == '█' || row[x] == '▀'
				bottom := row[x] == '█' || row[x] == '▄'
				if top != !code.Black(x-margin, y-margin) || (y+1 < n && bottom != !code.Black(x-margin, y+1-margin)) {
					t.Fatalf("%s: the terminal code is wrong at column %d, rows %d-%d", link, x, y, y+1)
				}
			}
		}

		// As an SVG the dark modules are drawn, as unit squares inside the
		// standard quiet zone.
		code, err = qr.Encode(link, qr.M)
		if err != nil {
			t.Fatal(err)
		}
		svg, err := QRSVG(link)
		if err != nil {
			t.Fatal(err)
		}
		dark := map[[2]int]bool{}
		for _, square := range strings.Split(svg[strings.Index(svg, `d="`)+len(`d="`):], "z") {
			var x, y int
			if _, err := fmt.Sscanf(square, "M%d %dh1v1h-1", &x, &y); err == nil {
				dark[[2]int{x - quiet, y - quiet}] = true
			}
		}
		for y := -quiet; y < code.Size+quiet; y++ {
			for x := -quiet; x < code.Size+quiet; x++ {
				if dark[[2]int{x, y}] != code.Black(x, y) {
					t.Fatalf("%s: the SVG code is wrong at module %d,%d", link, x, y)
				}
			}
		}
	}
}
