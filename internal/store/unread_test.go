package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestALayoutThatCouldNotBeReadIsNotWrittenOver checks that a layout the run
// failed to read survives the save on the way out.
//
// A project whose layout cannot be read opens empty, or on one fresh tab, and
// that is what gets saved when the window closes. Written straight over the
// file, it took with it every tab the user had saved, for nothing worse than a
// read that failed once.
func TestALayoutThatCouldNotBeReadIsNotWrittenOver(t *testing.T) {
	isolateConfig(t)
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}

	// The read fails the way one held past the retry budget does, or one on a
	// drive that has gone away for a moment: not because the file is missing.
	gone := errors.New("the device is not ready")
	readFile = func(name string) ([]byte, error) {
		if name == p {
			return nil, gone
		}
		return os.ReadFile(name)
	}
	t.Cleanup(func() { readFile = os.ReadFile })
	if _, err := Load("/repo/a"); !errors.Is(err, gone) {
		t.Fatalf("load gave %v, want the read's own failure", err)
	}
	readFile = os.ReadFile

	// The run went on with a fresh tab, and saves it on the way out.
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "fresh"}}}); err != nil {
		t.Fatalf("save after a failed read: %v", err)
	}
	kept, err := os.ReadFile(p + unreadSuffix)
	if err != nil {
		t.Fatalf("the layout that could not be read was not kept: %v", err)
	}
	if !strings.Contains(string(kept), `"alpha"`) {
		t.Errorf("the kept layout holds %s, want the tabs saved before", kept)
	}
	if got, err := Load("/repo/a"); err != nil || got == nil || got.Tabs[0].Title != "fresh" {
		t.Fatalf("load after saving = %+v, %v; want the tabs just saved", got, err)
	}

	// Once it is kept, a save is an ordinary save again: moving each new
	// layout aside would put the one that could not be read out of reach.
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "later"}}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if again, err := os.ReadFile(p + unreadSuffix); err != nil || !strings.Contains(string(again), `"alpha"`) {
		t.Errorf("a later save replaced the kept layout: %s, %v", again, err)
	}
}

// TestALayoutKeptAsideSaysWhereItWent checks a layout moved aside by the
// save after a failed read is reported, once, with the project it belonged to
// and the name it is kept under.
//
// Nothing said so before: the project came up on one fresh tab, and half a
// minute later the user's tabs were a file with a new name in a folder nobody
// looks in.
func TestALayoutKeptAsideSaysWhereItWent(t *testing.T) {
	isolateConfig(t)
	TakeKept() // whatever earlier tests in this process left behind
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	gone := errors.New("the device is not ready")
	readFile = func(name string) ([]byte, error) {
		if name == p {
			return nil, gone
		}
		return os.ReadFile(name)
	}
	t.Cleanup(func() { readFile = os.ReadFile })
	if _, err := Load("/repo/a"); !errors.Is(err, gone) {
		t.Fatalf("load gave %v, want the read's own failure", err)
	}
	readFile = os.ReadFile
	if kept := TakeKept(); len(kept) != 0 {
		t.Fatalf("kept before anything was moved: %+v", kept)
	}

	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "fresh"}}}); err != nil {
		t.Fatalf("save after a failed read: %v", err)
	}
	kept := TakeKept()
	if len(kept) != 1 {
		t.Fatalf("kept = %+v, want the one layout moved aside", kept)
	}
	if kept[0].Path != p+unreadSuffix {
		t.Errorf("kept at %s, want %s", kept[0].Path, p+unreadSuffix)
	}
	if !strings.Contains(kept[0].What, filepath.Clean("/repo/a")) {
		t.Errorf("kept %q, want it named after its project", kept[0].What)
	}
	if again := TakeKept(); len(again) != 0 {
		t.Errorf("reported a second time: %+v", again)
	}
}

// TestALegacyLayoutThatCouldNotBeReadIsKeptAndReported checks a layout still
// under the name an earlier build gave it, which could not be read, is moved
// aside by the next save and reported like any other.
//
// The save writes the name in use now, and every load after it finds that and
// never looks at the old name again. The tabs the old file held were left
// under a name nothing reads, while the error at start promised they would be
// kept and moved aside, and nothing ever said where they were.
func TestALegacyLayoutThatCouldNotBeReadIsKeptAndReported(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("roots are only respelled on case-folding platforms")
	}
	isolateConfig(t)
	TakeKept() // whatever earlier tests in this process left behind

	root := filepath.Clean("/Repo/Legacy")
	old, err := legacyPath(root)
	if err != nil || old == "" {
		t.Fatalf("legacy path = %q, %v; want a distinct pre-normalization name", old, err)
	}
	if err := os.WriteFile(old, []byte(`{"version": 2, "tabs": [{"title": "alpha"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	gone := errors.New("the device is not ready")
	readFile = func(name string) ([]byte, error) {
		if name == old {
			return nil, gone
		}
		return os.ReadFile(name)
	}
	t.Cleanup(func() { readFile = os.ReadFile })
	if _, err := Load(root); !errors.Is(err, gone) {
		t.Fatalf("load gave %v, want the read's own failure", err)
	}
	readFile = os.ReadFile

	// The run went on with a fresh tab, and saves it.
	if err := Save(root, &State{Tabs: []Tab{{Title: "fresh"}}}); err != nil {
		t.Fatalf("save after a failed read: %v", err)
	}
	kept, err := os.ReadFile(old + unreadSuffix)
	if err != nil || !strings.Contains(string(kept), `"alpha"`) {
		t.Fatalf("the legacy layout that could not be read was not kept aside: %q, %v", kept, err)
	}
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the legacy layout is still under a name nothing will read again: %v", err)
	}
	told := TakeKept()
	if len(told) != 1 || told[0].Path != old+unreadSuffix || !strings.Contains(told[0].What, root) {
		t.Errorf("kept = %+v, want the legacy layout reported at %s", told, old+unreadSuffix)
	}
}

// TestASecondLayoutThatCouldNotBeReadDoesNotReplaceTheFirst checks a file
// kept aside is still there after the same file cannot be read a second time.
//
// The first copy is the tabs the user had before a run came up without them,
// and nothing says they have been back for it. Moved aside onto the same name,
// the second copy took its place: the one kept for the user was lost to the
// next moment's trouble reading the file that replaced it.
func TestASecondLayoutThatCouldNotBeReadDoesNotReplaceTheFirst(t *testing.T) {
	isolateConfig(t)
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	gone := errors.New("the device is not ready")
	unreadable := func() {
		t.Helper()
		readFile = func(name string) ([]byte, error) {
			if name == p {
				return nil, gone
			}
			return os.ReadFile(name)
		}
		defer func() { readFile = os.ReadFile }()
		if _, err := Load("/repo/a"); !errors.Is(err, gone) {
			t.Fatalf("load gave %v, want the read's own failure", err)
		}
	}
	t.Cleanup(func() { readFile = os.ReadFile })

	for _, title := range []string{"alpha", "fresh", "later"} {
		if _, err := os.Stat(p); err == nil {
			unreadable()
		}
		if err := Save("/repo/a", &State{Tabs: []Tab{{Title: title}}}); err != nil {
			t.Fatalf("save %s: %v", title, err)
		}
	}
	for file, want := range map[string]string{
		p + unreadSuffix:        `"alpha"`,
		p + unreadSuffix + ".1": `"fresh"`,
		p:                       `"later"`,
	} {
		got, err := os.ReadFile(file)
		if err != nil || !strings.Contains(string(got), want) {
			t.Errorf("%s holds %s, %v; want %s", filepath.Base(file), got, err, want)
		}
	}
}
