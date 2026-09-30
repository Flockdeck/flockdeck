package webui

import (
	"regexp"
	"testing"
)

// The generic .empty (a whole-panel placeholder: 40px padding all round and an
// auto margin) is also the class of an unbound action's chord button in
// Settings > Keybindings, and gave it a padding that made the button ~80px
// tall. The chord's own rule has to take both back.
func TestAnUnboundChordIsNotPaddedLikeAnEmptyPanel(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	body := ruleBody(css, ".keybind-chord.empty")
	if body == "" {
		t.Fatal("app.css has no .keybind-chord.empty rule")
	}
	if !regexp.MustCompile(`(?:^|[;\s])padding:\s*0 10px`).MatchString(body) {
		t.Errorf(".keybind-chord.empty does not reset the 40px padding of .empty: %q", body)
	}
	if !regexp.MustCompile(`(?:^|[;\s])margin:\s*0[;\s]`).MatchString(body + " ") {
		t.Errorf(".keybind-chord.empty does not reset the auto margin of .empty: %q", body)
	}
}

// Across the top of a narrow window the Settings sections are a strip running
// left to right, and only the vertical arrows walked them. Left and Right walk
// them as well, and whichever section is shown is scrolled to the middle of
// the strip, not just to its edge (where the neighbour is cut in half).
func TestTheSettingsSectionsWalkWithTheSidewaysArrows(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
assert.ok(h.doc.activeElement === h.$("settings-tab-general"));
h.key({ key: "ArrowRight" });
assert.strictEqual(h.$("settings-tab-appearance").getAttribute("aria-selected"), "true", "Right did not move to the next section");
assert.ok(h.doc.activeElement === h.$("settings-tab-appearance"), "the keyboard did not follow Right");
h.key({ key: "ArrowRight" });
h.key({ key: "ArrowLeft" });
assert.strictEqual(h.$("settings-tab-appearance").getAttribute("aria-selected"), "true", "Left did not move back");
h.key({ key: "ArrowLeft" });
assert.strictEqual(h.$("settings-tab-general").getAttribute("aria-selected"), "true");
h.key({ key: "ArrowLeft" });
assert.strictEqual(h.$("settings-tab-general").getAttribute("aria-selected"), "true", "Left at the first section went somewhere");
const tab = h.$("settings-tab-general");
assert.ok(tab.scrolledTo, "the section shown was not scrolled into view");
assert.strictEqual(tab.scrolledWith.inline, "center", "the section shown is not centred in the strip");
`)
}

// A bubble left on screen after the thing it describes has been used - opened
// by a click, or by Enter or Space on the keyboard - describes nothing. It is
// removed on the activation, and a bubble still waiting for its delay never
// opens after it.
func TestATooltipGoesWhenItsTriggerIsActivated(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const tip = () => h.doc.body.querySelector("div.tip");
const hover = (b) => h.dispatch(b, new h.Ev("pointerover", { pointerType: "mouse", target: b }));
const button = h.$("btn-history");

hover(button);
await h.sleep(320);
assert.ok(tip(), "the bubble did not open to start with");
h.dispatch(button, new h.Ev("click", { target: button }));
assert.ok(!tip(), "the bubble stayed after a click");

hover(button);
await h.sleep(320);
assert.ok(tip());
h.key({ key: "Enter" });
assert.ok(!tip(), "the bubble stayed after Enter");

hover(button);
await h.sleep(320);
assert.ok(tip());
h.key({ key: " " });
assert.ok(!tip(), "the bubble stayed after Space");

// Activated before the bubble's delay is up, it never opens.
hover(h.$("btn-agents"));
h.key({ key: "Enter" });
await h.sleep(320);
assert.ok(!tip(), "a bubble opened after the thing it describes was used");
`)
}
