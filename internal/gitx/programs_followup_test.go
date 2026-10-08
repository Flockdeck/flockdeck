package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A hook is known by its name whatever the case it is spelled in, on every
// system: a Linux build cannot tell a file system that ignores case (vfat,
// exfat, ntfs, cifs, ext4 with casefold) from one that keeps it.
func TestHookNamesAreComparedWithoutRegardToCase(t *testing.T) {
	cases := []struct {
		name, want string
		ok         bool
	}{
		{"pre-commit", "pre-commit", true},
		{"Pre-Commit", "pre-commit", true},
		{"PRE-COMMIT", "pre-commit", true},
		{"Commit-Msg", "commit-msg", true},
		{"pre-commit.sample", "", false},
		{"pre-commit-", "", false},
		{"README", "", false},
	}
	for _, c := range cases {
		got, ok := hookFor(c.name)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("hookFor(%q) = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if runtime.GOOS == "windows" {
		if got, ok := hookFor("Pre-Commit.EXE"); !ok || got != "pre-commit.exe" {
			t.Errorf("hookFor(Pre-Commit.EXE) = %q, %v", got, ok)
		}
	} else if _, ok := hookFor("pre-commit.exe"); ok {
		t.Error("a .exe is a hook only on Windows")
	}
}

// A listing finds a hook spelled in another case, and a hook spelled the usual
// way is listed once.
func TestAListedHookInAnotherCaseIsFoundAndOneSpellingIsOneItem(t *testing.T) {
	repo := newRepo(t)
	hooks := filepath.Join(repo, ".git", "hooks")
	writeHook(t, hooks, "Pre-Commit", "#!/bin/sh\n")
	rep := scan(t, repo)
	if _, ok := find(rep, "hook", "pre-commit"); !ok {
		t.Fatalf("a hook spelled Pre-Commit was not listed: %+v", rep.Items)
	}

	plain := newRepo(t)
	writeHook(t, filepath.Join(plain, ".git", "hooks"), "pre-commit", "#!/bin/sh\n")
	n := 0
	for _, p := range scan(t, plain).Items {
		if p.Kind == "hook" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("one hook file gave %d items", n)
	}
}

// runOutOfTime scans a repository of submodules whose time runs out at the
// first submodule's stage given ("start": while it is scanned, "after": once it
// is done and before the next).
func runOutOfTime(t *testing.T, outer, stage string) Report {
	t.Helper()
	wasTimeout, wasStep := scanTimeout, submoduleStep
	t.Cleanup(func() { scanTimeout, submoduleStep = wasTimeout, wasStep })
	scanTimeout = 5 * time.Second
	waited := false
	submoduleStep = func(ctx context.Context, at string) {
		if at == stage && !waited {
			waited = true
			<-ctx.Done()
		}
	}
	rep, err := ScanPrograms(outer)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// The time falls between two submodules, so nothing in the scan fails: only the
// check after the loop says the rest was not looked at.
func TestAScanWhoseTimeRunsOutBetweenSubmodulesSaysSo(t *testing.T) {
	outer := submoduleFarm(t, 3)
	rep := runOutOfTime(t, outer, "after")
	if !hasItem(rep, "took too long") {
		t.Fatalf("a scan that stopped early said nothing: %+v", rep.Items)
	}
	if v := Judge(rep, &Known{IDs: rep.AcceptableIDs()}); !v.Warn {
		t.Error("a scan that stopped early was let through")
	}
}

// Where a slow scan stops decides which paths it gave up on, and none of that
// is in what it reports: the same slow repository is the same set of items.
func TestAScanThatTimesOutIsTheSameWhereverItStopped(t *testing.T) {
	outer := submoduleFarm(t, 3)
	rep := runOutOfTime(t, outer, "start")
	var long []Program
	for _, p := range rep.Items {
		if p.Kind == "unscannable" && strings.Contains(p.Value, "took too long") {
			long = append(long, p)
		}
		if p.Kind == "unscannable" && p.Value == errScanTooLong.Error() {
			t.Errorf("an item that depends on how far the scan got: %+v", p)
		}
	}
	if len(long) != 1 {
		t.Fatalf("%d items say the scan took too long: %+v", len(long), rep.Items)
	}
	again := runOutOfTime(t, outer, "after")
	for _, p := range again.Items {
		if p.Kind == "unscannable" && strings.Contains(p.Value, "took too long") && p.ID() != long[0].ID() {
			t.Errorf("the item for a scan that timed out changed: %+v and %+v", long[0], p)
		}
	}
}

// A submodule's configuration that is a link is followed through the guarded
// call, as git follows it, and is read for what it holds.
func TestASubmoduleConfigThatIsALinkIsFollowedAndRead(t *testing.T) {
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	gitRun(t, mod, "config", "core.sshCommand", "ssh -i linked")
	cfg := filepath.Join(outer, ".git", "modules", "mod", "config")
	real := filepath.Join(filepath.Dir(cfg), "config.real")
	if err := os.Rename(cfg, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, cfg); err != nil {
		t.Skipf("a link cannot be made here: %v", err)
	}
	p, ok := find(scan(t, outer), "setting", "core.sshcommand")
	if !ok || p.Submodule != "mod" {
		t.Errorf("the setting in a linked configuration was not found: %+v ok=%v", p, ok)
	}
}

// A link to a share is refused without being opened. A junction stands for the
// link, and the link-reading step says it leads to a share.
func TestASubmoduleConfigThatLinksToAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	outer := newRepo(t)
	addSubmodule(t, outer)
	cfg := filepath.Join(outer, ".git", "modules", "mod", "config")
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	links(t, map[string]string{cfg: t.TempDir()}, map[string]string{"config": blackhole + `\config`})
	var rep Report
	quickly(t, "a scan with a submodule whose config links to a share", func() { rep = scan(t, outer) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

// A folder with more entries than are listed is looked through by name, one
// look for each hook git knows, and a folder with no more than that is listed.
// The calls made tell the two apart: a listing is a few, the other one at least
// one for each name.
func TestAHooksFolderOverTheLimitIsLookedThroughByNameAndOneAtItIsListed(t *testing.T) {
	for _, c := range []struct {
		entries int
		byName  bool
	}{
		{maxHooksDirEntries - 1, false},
		{maxHooksDirEntries, false},
		{maxHooksDirEntries + 1, true},
		{maxHooksDirEntries * 2, true},
	} {
		t.Run(fmt.Sprintf("%d entries", c.entries), func(t *testing.T) {
			repo := newRepo(t)
			hooks := filepath.Join(repo, ".git", "hooks")
			// The sample hooks git leaves are entries too.
			if err := os.RemoveAll(hooks); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(hooks, 0o755); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < c.entries-1; i++ {
				if err := os.WriteFile(filepath.Join(hooks, fmt.Sprintf("filler%04d", i)), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writeHook(t, hooks, "pre-commit", "#!/bin/sh\n")
			s := &scanner{ctx: context.Background(), budget: maxHashBudget, fs: newScanFS(context.Background())}
			// The walk to the folder is made once, and is not what is counted.
			if err := s.fs.guard(hooks); err != nil {
				t.Fatal(err)
			}
			before := s.fs.calls
			items := s.hooksInDir(hooks, "", false)
			if len(items) != 1 || items[0].Name != "pre-commit" {
				t.Fatalf("items = %+v", items)
			}
			calls := s.fs.calls - before
			if got := calls >= len(hookNames); got != c.byName {
				t.Errorf("%d entries took %d calls; looked through by name = %v, want %v", c.entries, calls, got, c.byName)
			}
		})
	}
}
