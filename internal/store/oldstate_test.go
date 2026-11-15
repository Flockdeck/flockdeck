package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A perch directory is never read, moved or written to, by Dir or by a save.
func TestAPerchDirectoryIsLeftAlone(t *testing.T) {
	isolateConfig(t)
	base, _ := os.UserConfigDir()
	perch := filepath.Join(base, "perch")
	if err := os.MkdirAll(perch, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(perch, "layout-abc.json")
	if err := os.WriteFile(f, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := Dir()
	if err != nil || filepath.Base(dir) != "flockdeck" {
		t.Fatalf("Dir = %q, %v", dir, err)
	}
	if err := Save("/repo/a", &State{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(f); err != nil || string(got) != `{"version":2}` {
		t.Errorf("the perch file changed: %q, %v", got, err)
	}
	if ents, _ := os.ReadDir(perch); len(ents) != 1 {
		t.Errorf("the perch directory gained or lost files: %v", ents)
	}
}

func TestTheLegacyDirNoticeNamesAPerchDirectoryOnlyWhenThereIsOne(t *testing.T) {
	isolateConfig(t)
	if msg := LegacyDirNotice(); msg != "" {
		t.Fatalf("notice with no perch directory: %q", msg)
	}
	base, _ := os.UserConfigDir()
	perch := filepath.Join(base, "perch")
	if err := os.MkdirAll(perch, 0o700); err != nil {
		t.Fatal(err)
	}
	want := "State from an older version is in " + perch + " and is not read any more. Nothing was deleted."
	if msg := LegacyDirNotice(); msg != want {
		t.Errorf("notice = %q, want %q", msg, want)
	}
}

// A layout under the pre-normalisation hash is not read and a save of the
// same root leaves it where it is.
func TestAPreNormalisationLayoutIsLeftAloneBySave(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("the two names only differ where case is folded")
	}
	isolateConfig(t)
	root := `/Users/Jim/Repo`
	if runtime.GOOS == "windows" {
		root = `C:\Users\Jim\Repo`
	}
	dir, _ := Dir()
	old := filepath.Join(dir, "layout-"+hashString(filepath.Clean(root))+".json")
	body := fmt.Sprintf(`{"version":2,"root":%q,"tabs":[{"title":"old"}]}`, root)
	if err := os.WriteFile(old, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := Load(root); err != nil || st != nil {
		t.Fatalf("Load = %v, %v; the old name should not be read", st, err)
	}
	if err := Save(root, &State{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(old); err != nil || !strings.Contains(string(got), `"old"`) {
		t.Errorf("the old-name layout changed: %q, %v", got, err)
	}
}
