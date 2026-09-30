package webui

import (
	"testing"

	"github.com/jmwri/flockdeck/design"
)

// The window's colours, type, shape and motion are design/tokens.css's, under
// the names this style sheet has always used. Nothing at run time reads one
// from the other, so this is what stops the application drifting away from
// the site, the docs and the phone again.
func TestTheStyleSheetCarriesTheDesignTokens(t *testing.T) {
	css := readAsset(t, "app.css")
	colours := map[string]string{
		"--bg": "bg", "--bg-raised": "bg-raised", "--bg-inset": "bg-sunken",
		"--bg-overlay": "bg-overlay", "--bg-hover": "bg-hover", "--bg-active": "bg-active",
		"--line-subtle": "line-subtle", "--line": "line",
		"--line-strong": "line-strong", "--field-line": "line-strong",
		"--fg": "fg", "--fg-dim": "fg-muted", "--fg-faint": "fg-faint",
		"--accent": "accent", "--accent-strong": "accent-strong", "--accent-subtle": "accent-subtle",
		"--accent-line": "accent-line", "--on-accent": "on-accent",
		"--working": "working",
		"--waiting": "waiting", "--waiting-subtle": "waiting-subtle", "--waiting-line": "waiting-line",
		"--idle": "idle",
		// The name this sheet keeps for the red of a failure; a pane that
		// simply ended is --ended.
		"--exited": "failed", "--exited-subtle": "failed-subtle", "--exited-line": "failed-line",
		"--ended": "exited",
		"--ok":    "ok", "--ok-subtle": "ok-subtle", "--ok-line": "ok-line",
		"--cast": "cast", "--on-cast": "on-cast", "--on-exited": "on-failed", "--on-ok": "on-ok",
		"--scrim": "scrim", "--shadow-1": "shadow-1", "--shadow-2": "shadow-2", "--shadow-3": "shadow-3",
	}
	// The same in both palettes, so declared once, in the first block.
	fixed := map[string]string{
		"--term-bg": "term-bg", "--term-fg": "term-fg", "--term-cursor": "term-cursor",
		"--sans": "sans", "--mono": "mono",
		"--radius-sm": "r-1", "--radius": "r-2", "--radius-lg": "r-3", "--radius-full": "r-full",
		"--dur-1": "dur-1", "--dur-2": "dur-2", "--dur-3": "dur-3", "--pulse": "pulse",
		"--ease-out": "ease-out", "--ease-in": "ease-in", "--ease-in-out": "ease-in-out",
	}
	design.AssertSurface(t, "app.css", css, ":root", design.Dark, colours)
	design.AssertSurface(t, "app.css", css, ":root", design.Dark, fixed)
	design.AssertSurface(t, "app.css", css, `:root[data-theme="light"]`, design.Light, colours)

	for _, name := range []string{"blue", "purple", "green", "orange", "pink"} {
		swatch := map[string]string{"--accent": "swatch-" + name}
		design.AssertSurface(t, "app.css", css, `:root[data-accent="`+name+`"]`, design.Dark, swatch)
		design.AssertSurface(t, "app.css", css, `:root[data-theme="light"][data-accent="`+name+`"]`, design.Light, swatch)
	}
	if dark := design.Tokens(design.Dark); dark["swatch-cyan"] != dark["accent"] {
		t.Errorf("the default swatch is %s and the accent %s", dark["swatch-cyan"], dark["accent"])
	}
}
