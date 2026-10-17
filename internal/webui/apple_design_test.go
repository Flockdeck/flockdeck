package webui

import (
	"regexp"
	"strings"
	"testing"
)

// --fg-faint is for glyphs and rules. Words set in it fall under 4.5:1, so the
// rules below, which colour words, may not use it. The two other places it
// still colours text (pane header buttons at rest, a disabled locked button) are glyphs, and a disabled control is exempt.
func TestNoWordsAreSetInTheFaintColour(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	for _, sel := range []string{".pi-value.unset", ".pi-note", ".pf-why"} {
		body := ruleBody(css, sel)
		if body == "" {
			t.Errorf("app.css has no rule for %s", sel)
			continue
		}
		if strings.Contains(body, "color: var(--fg-faint)") {
			t.Errorf("%s sets words in --fg-faint, which is for glyphs and rules", sel)
		}
	}
}

// Buttons in a pane header sit on a tinted ground when the pane needs the
// user. Their resting colour there has to be the dim text colour, which clears
// 3:1 against every tint in both palettes; the faint one did not.
func TestPaneHeaderButtonsOnATintUseTheDimColour(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	re := regexp.MustCompile(`:where\(\.pane\[data-status="waiting"\][^)]*\) \.pane-actions button\s*\{[^}]*color:\s*var\(--fg-dim\)`)
	if !re.MatchString(css) {
		t.Error("app.css does not set the pane header buttons to --fg-dim on a waiting, blocked or failed pane")
	}
}

// Increased contrast is a request from the system, and the window answers it.
func TestIncreasedContrastIsAnswered(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	if !strings.Contains(css, "@media (prefers-contrast: more)") {
		t.Error("app.css has no prefers-contrast: more block")
	}
	if !strings.Contains(css, "@media (prefers-reduced-motion: reduce)") {
		t.Error("app.css has no prefers-reduced-motion block")
	}
}

// The desktop default target is 28px and nothing is under 24.
func TestDesktopControlsMeetTheTargetSize(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	for sel, min := range map[string]int{".bar-btn": 28, ".icon-btn": 28, ".seg": 28, ".commands": 28, ".pane-actions button": 24, ".tab .close": 24} {
		body := ruleBody(css, sel)
		got := cssPx(body, "min-height")
		if got == 0 {
			got = cssPx(body, "height")
		}
		if got < min {
			t.Errorf("%s is %dpx tall, under %dpx", sel, got, min)
		}
	}
	if h := cssPx(ruleBody(css, ":root"), "--header-h"); h < 30 {
		t.Errorf("--header-h is %dpx, too short for 28px header buttons", h)
	}
}

// The pane lock is drawn with the app's icon set, like its neighbours, and not
// with an emoji that takes the system font's colour and size.
func TestThePaneLockIsAnIconNotAnEmoji(t *testing.T) {
	js := readAsset(t, "app.js")
	if strings.ContainsAny(js, "\U0001F512\U0001F513") {
		t.Error("app.js still draws the pane lock with an emoji")
	}
	for _, name := range []string{"lock:", "unlock:"} {
		if !strings.Contains(js, name) {
			t.Errorf("ICON_PATHS has no %s", name)
		}
	}
}

// No transition may list "all", and nothing in the window uses a backdrop
// filter: the deck is flat.
func TestTheDeckStaysFlat(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	if strings.Contains(css, "backdrop-filter") {
		t.Error("app.css uses backdrop-filter; the deck has no glass")
	}
	if regexp.MustCompile(`transition:[^;]*\ball\b`).MatchString(css) {
		t.Error("a transition lists all instead of the properties that change")
	}
}

// Increased contrast thickens the focus ring to 3px. A tab draws its ring
// outside itself and the strip clips at its padding edge, so the strip's
// padding inside that block has to hold the ring and its offset.
func TestIncreasedContrastDoesNotClipATabsRing(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	i := strings.Index(css, "@media (prefers-contrast: more)")
	if i < 0 {
		t.Fatal("no prefers-contrast block")
	}
	block := css[i:]
	if j := strings.Index(block, "\n}\n"); j > 0 {
		block = block[:j]
	}
	ring := cssPx(ruleBody(css, ".tab:has(> .tab-btn:focus-visible)"), "outline-offset")
	if got := cssPx(ruleBody(block, "#tabs"), "padding"); got < 3+ring {
		t.Errorf("in increased contrast the tab strip is padded %dpx and the ring reaches %dpx", got, 3+ring)
	}
}

// The switch and colour chip grow their hit area on a touch screen, as the
// comment on the desktop one promises.
func TestSwitchAndSwatchHaveATouchHitArea(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	i := strings.Index(css, "@media (pointer: coarse) {\n  .rail-btn")
	if i < 0 {
		t.Fatal("no coarse-pointer block")
	}
	block := css[i:]
	for _, sel := range []string{".switch::after", ".swatch::after"} {
		if !strings.Contains(block, sel) {
			t.Errorf("the coarse-pointer block has no rule for %s", sel)
		}
	}
}

// A pressed pane header button shows the pressed ground, not the hover one.
func TestPanePressedBeatsHover(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	press := strings.LastIndex(css, ".pane .pane-actions button:active")
	hover := strings.LastIndex(css, `.pane[data-status="failed"] .pane-actions button:hover`)
	if press < 0 || hover < 0 || press < hover {
		t.Error("the pane header's pressed rule must come after its hover rules")
	}
}
