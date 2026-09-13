package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestASettingThisBuildDoesNotKnowSurvivesAChangeMadeHere checks preferences
// written by a newer build keep the settings this one does not know when a
// change made here writes them back.
//
// The file is written back whole from what was read, and what was read held
// only what this build knew: step back a version, dismiss a hint, and
// stepping forward again found the newer build's settings gone.
func TestASettingThisBuildDoesNotKnowSurvivesAChangeMadeHere(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, prefsFile)
	written := `{"fontSize": 17, "helpSeen": true, "themeFromTheFuture": {"accent": "teal"}, "spend": {"statusLine": "on"}}`
	if err := os.WriteFile(file, []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := ReadPrefs()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// One change made here, and one setting put back to its default.
	p.Dismiss("palette")
	p.HelpSeen = false
	if err := SavePrefs(p); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved prefs are not JSON: %v\n%s", err, data)
	}
	if raw, ok := got["themeFromTheFuture"]; !ok {
		t.Errorf("the newer build's setting was dropped: %s", data)
	} else if want := `{"accent":"teal"}`; string(compact(t, raw)) != want {
		t.Errorf("the newer build's setting is %s, want %s", raw, want)
	}
	if _, ok := got["helpSeen"]; ok {
		t.Errorf("a setting put back to its default came back from the file: %s", data)
	}
	back := LoadPrefs()
	if back.FontSize != 17 || !back.Dismissed("palette") || back.Spend.StatusLine != "on" {
		t.Errorf("read back %+v, want the settings this build knows as they were saved", back)
	}
}

func compact(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
