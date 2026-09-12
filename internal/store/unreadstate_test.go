package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionAndPrefsThatCouldNotBeReadAreNotWrittenOver checks the list of
// open projects and the preferences survive a run that could not read them.
//
// Either one unread is taken as empty: a start that reopens no other project,
// or the default preferences. The next save then wrote that over the file,
// and every project the user had open, or every setting they had chosen, was
// gone for good.
func TestSessionAndPrefsThatCouldNotBeReadAreNotWrittenOver(t *testing.T) {
	isolateConfig(t)
	if err := SaveSession(&Session{Open: []string{"alpha", "beta"}, Active: "alpha"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if err := SavePrefs(Prefs{FontSize: 17}); err != nil {
		t.Fatalf("save prefs: %v", err)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	sess, prefs := filepath.Join(dir, sessionFile), filepath.Join(dir, prefsFile)

	gone := errors.New("the device is not ready")
	readFile = func(name string) ([]byte, error) {
		if name == sess || name == prefs {
			return nil, gone
		}
		return os.ReadFile(name)
	}
	t.Cleanup(func() { readFile = os.ReadFile })
	if _, err := LoadSession(); !errors.Is(err, gone) {
		t.Fatalf("load session gave %v, want the read's own failure", err)
	}
	if p := LoadPrefs(); p.FontSize != 0 {
		t.Fatalf("unreadable prefs gave %+v, want the defaults", p)
	}
	readFile = os.ReadFile

	// The run went on with one project and the defaults, and saves both.
	if err := SaveSession(&Session{Open: []string{"gamma"}}); err != nil {
		t.Fatalf("save session after a failed read: %v", err)
	}
	if err := SavePrefs(Prefs{HelpSeen: true}); err != nil {
		t.Fatalf("save prefs after a failed read: %v", err)
	}
	for file, want := range map[string]string{sess: `"beta"`, prefs: `"fontSize": 17`} {
		kept, err := os.ReadFile(file + unreadSuffix)
		if err != nil {
			t.Errorf("%s could not be read and was not kept: %v", filepath.Base(file), err)
			continue
		}
		if !strings.Contains(string(kept), want) {
			t.Errorf("kept %s holds %s, want what was saved before", filepath.Base(file), kept)
		}
	}
	if got, err := LoadSession(); err != nil || got == nil || len(got.Open) != 1 || got.Open[0] != "gamma" {
		t.Errorf("load session after saving = %+v, %v; want the projects just saved", got, err)
	}
}
