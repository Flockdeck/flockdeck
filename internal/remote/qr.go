package remote

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// A pairing link is typed by nobody: it is opened on the device being paired,
// and the easy way to get it there is for that device to photograph it. Both
// the window and the terminal can show one, so both renderings live here.

// quiet is the margin a QR code needs around it, in modules. The standard asks
// for four; a reader given less finds the finder patterns less reliably.
const quiet = 4

// QRSVG draws text as a QR code, as an SVG document.
//
// It is dark modules on a white square whatever the window's theme is. A code
// drawn in the theme's colours would be light on dark in this application,
// and while some readers cope with an inverted code, a phone's own camera is
// not reliably one of them.
func QRSVG(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", fmt.Errorf("draw the pairing code: %w", err)
	}
	n := code.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

// QRTerminal draws text as a QR code in text, two modules to a line.
//
// Each character covers two rows with the half-block characters, which keeps
// the code square in a terminal whose cells are about twice as tall as they
// are wide. Light modules are the ones drawn, so on the dark background most
// terminals have, the code comes out the right way round: dark modules on a
// light field, quiet zone included.
func QRTerminal(text string) (string, error) {
	// Level M, as the window's, corrects twice what L does, which is what
	// carries a phone's camera past glare on a laptop's screen, an angle, or
	// a code a light theme has inverted. For the hosted relay's links it
	// costs nothing: they are version 4, 33 modules, at L and M alike; only a
	// long address of one's own grows by one version, four columns and two
	// lines.
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", fmt.Errorf("draw the pairing code: %w", err)
	}
	// Two modules of margin rather than four: a terminal is narrow, the
	// background around the drawing is already dark, and the margin is only
	// ever light, which is what a reader looks for.
	const margin = 2
	light := func(x, y int) bool {
		return !code.Black(x-margin, y-margin)
	}
	n := code.Size + 2*margin
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := 0; x < n; x++ {
			top := light(x, y)
			bottom := y+1 < n && light(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
