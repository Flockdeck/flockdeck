package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func patch(kv ...string) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = json.RawMessage(kv[i+1])
	}
	return m
}

func TestPatchMergesKeyByKeyAndNullRemoves(t *testing.T) {
	var p Prefs
	if ch, err := p.PatchUI(patch("a", `1`, "b", ` {"x": [1, 2]} `)); err != nil || !ch {
		t.Fatalf("first patch: %v %v", ch, err)
	}
	if string(p.UI["b"]) != `{"x":[1,2]}` {
		t.Errorf("a value is kept compact: %s", p.UI["b"])
	}
	// The same value again is no change, spelled any way.
	if ch, err := p.PatchUI(patch("a", ` 1 `)); err != nil || ch {
		t.Errorf("same value: %v %v", ch, err)
	}
	// Another key does not undo the first.
	if ch, _ := p.PatchUI(patch("c", `"z"`)); !ch || string(p.UI["a"]) != "1" || string(p.UI["c"]) != `"z"` {
		t.Errorf("merge lost a key: %v", p.UI)
	}
	if ch, _ := p.PatchUI(patch("a", `null`, "nope", `null`)); !ch {
		t.Error("removing a key is a change")
	}
	if _, has := p.UI["a"]; has {
		t.Error("null did not remove the key")
	}
	if ch, _ := p.PatchUI(patch("b", `null`, "c", `null`)); !ch || p.UI != nil {
		t.Errorf("nothing left should be nil, got %v", p.UI)
	}
}

func TestPatchRefusesWhatIsOutsideTheLimitsAndChangesNothing(t *testing.T) {
	var p Prefs
	p.PatchUI(patch("keep", `1`))
	bad := map[string]map[string]json.RawMessage{
		"a key with a space":        patch("not ok", `1`),
		"a key that starts a digit": patch("1x", `1`),
		"a long key":                patch(strings.Repeat("k", 65), `1`),
		"not JSON":                  patch("a", `{oops`),
		"empty":                     patch("a", ``),
		"a long value":              patch("a", `"`+strings.Repeat("x", MaxUIValueBytes)+`"`),
	}
	for name, pt := range bad {
		before := string(p.UI["keep"])
		if ch, err := p.PatchUI(pt); err == nil || ch {
			t.Errorf("%s: want an error and no change, got %v %v", name, ch, err)
		}
		if string(p.UI["keep"]) != before || len(p.UI) != 1 {
			t.Errorf("%s: changed the preferences: %v", name, p.UI)
		}
	}
	// Too many names, and too many bytes together.
	many := map[string]json.RawMessage{}
	for i := range MaxUIKeys {
		many["k"+string(rune('a'+i%26))+string(rune('a'+i/26))] = json.RawMessage(`1`)
	}
	if _, err := p.PatchUI(many); err == nil {
		t.Error("more than MaxUIKeys names was kept")
	}
	var big Prefs
	for i := 0; i < 10; i++ {
		_, err := big.PatchUI(patch("v"+string(rune('a'+i)), `"`+strings.Repeat("x", MaxUIValueBytes-10)+`"`))
		if err != nil {
			if i < 7 {
				t.Errorf("refused too early, at %d: %v", i, err)
			}
			return
		}
	}
	t.Error("over MaxUIBytes was kept")
}

func TestPatchDoesNotChangeACopyThatSharesTheMap(t *testing.T) {
	var p Prefs
	p.PatchUI(patch("a", `1`))
	q := p // Prefs is copied by value, and the copies share the map.
	q.PatchUI(patch("a", `2`, "b", `3`))
	if string(p.UI["a"]) != "1" || len(p.UI) != 1 {
		t.Errorf("the first copy changed: %v", p.UI)
	}
}

// A prefs.json from before the blob is no longer moved into it. The four old
// top-level settings stay where they are, so a window still reads them through
// the old fields and nothing in the file is dropped or rewritten.
func TestAnOldFileIsNotMovedIntoTheBlob(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, prefsFile)
	old := `{"fontSize": 17, "fontFamily": "Fira Code", "helpSeen": true, "scrollback": 5000, "dismissedTips": ["welcome"], "theme": "light"}`
	if err := os.WriteFile(file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.UI) != 0 {
		t.Errorf("the blob was filled from the old fields: %v", p.UI)
	}
	if p.FontSize != 17 || p.FontFamily != "Fira Code" || !p.HelpSeen || p.Scrollback != 5000 {
		t.Errorf("the old fields were not read as they are: %+v", p)
	}
	if !p.Dismissed("welcome") || p.Theme != "light" {
		t.Errorf("a setting that stays typed was lost: %+v", p)
	}
	if after, _ := os.ReadFile(file); string(after) != old {
		t.Errorf("reading rewrote the file: %s", after)
	}
}

func TestAWindowIsSentTheOldFieldsFilledFromTheBlob(t *testing.T) {
	var p Prefs
	p.PatchUI(patch(UIFontSize, `18`, UIFontFamily, `"Mono"`, UIHelpSeen, `true`, UIScrollback, `9000`, "other", `"x"`))
	w := p.ForWindow()
	if w.FontSize != 18 || w.FontFamily != "Mono" || !w.HelpSeen || w.Scrollback != 9000 {
		t.Errorf("window prefs: %+v", w)
	}
	if string(w.UI["other"]) != `"x"` {
		t.Errorf("the blob is sent too: %v", w.UI)
	}
	// A value of the wrong kind is not turned into one.
	var q Prefs
	q.PatchUI(patch(UIFontSize, `"big"`, UIHelpSeen, `1`))
	if w := q.ForWindow(); w.FontSize != 0 || w.HelpSeen {
		t.Errorf("a wrong kind was read as a value: %+v", w)
	}
}
