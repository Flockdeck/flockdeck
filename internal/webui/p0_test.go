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
