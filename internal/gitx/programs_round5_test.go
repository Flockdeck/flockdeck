package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// links stands in for the links the scan reads, by the name each link has, and
// makes a junction (which needs no privilege) where each one is so that Lstat
// sees a reparse point.
func links(t *testing.T, paths map[string]string, targets map[string]string) {
	t.Helper()
	for link, target := range paths {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("a junction cannot be made here: %v: %s", err, out)
		}
	}
	was := readLink
	t.Cleanup(func() { readLink = was })
	readLink = func(p string) (string, error) {
		if to, ok := targets[strings.ToLower(filepath.Base(p))]; ok {
			return to, nil
		}
		return was(p)
	}
}

func TestADefaultHooksDirectoryThatLinksToAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.RemoveAll(hooks); err != nil {
		t.Fatal(err)
	}
	links(t, map[string]string{hooks: t.TempDir()}, map[string]string{"hooks": blackhole + `\hooks`})
	var rep Report
	quickly(t, "a scan whose .git/hooks links to a share", func() { rep = scan(t, repo) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func chainOfLinks(t *testing.T, shareAtEnd bool) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	dir := t.TempDir()
	const n = 12
	paths := map[string]string{}
	targets := map[string]string{}
	name := func(i int) string { return "chainlink" + strconv.Itoa(i) }
	end := filepath.Join(dir, "end")
	if err := os.MkdirAll(end, 0o755); err != nil {
		t.Fatal(err)
	}
	// link12 -> end, link11 -> link12, ... link1 -> link2. The innermost
	// reports a share, which is further than the walk goes.
	for i := n; i >= 1; i-- {
		to := end
		if i < n {
			to = filepath.Join(dir, name(i+1))
		}
		paths[filepath.Join(dir, name(i))] = to
		targets[name(i)] = to
	}
	if shareAtEnd {
		targets[name(n)] = blackhole + `end`
	}
	// Junctions must be made innermost first, since a target has to exist.
	for i := n; i >= 1; i-- {
		links(t, map[string]string{filepath.Join(dir, name(i)): paths[filepath.Join(dir, name(i))]}, targets)
	}
	gitRun(t, repo, "config", "core.hooksPath", filepath.ToSlash(filepath.Join(dir, name(1))))
	var rep Report
	quickly(t, "a scan whose hooks path is a chain of links", func() { rep = scan(t, repo) })
	if !hasItem(rep, "too many links") {
		t.Errorf("a chain past the depth limit was resolved: %+v", rep.Items)
	}
}

func TestARemotesGitFileThatLeadsToAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	remote := filepath.Join(t.TempDir(), "remote")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	lnk := filepath.Join(t.TempDir(), "gitdirlink")
	links(t, map[string]string{lnk: t.TempDir()}, map[string]string{"gitdirlink": blackhole + `\gd`})
	if err := os.WriteFile(filepath.Join(remote, ".git"), []byte("gitdir: "+filepath.ToSlash(lnk)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "config", "remote.origin.url", filepath.ToSlash(remote))
	var rep Report
	quickly(t, "a scan of a remote whose .git file leads to a share", func() { rep = scan(t, repo) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestASubmodulesGitFileThatLeadsToAShareThroughALinkIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	lnk := filepath.Join(t.TempDir(), "submodlink")
	links(t, map[string]string{lnk: t.TempDir()}, map[string]string{"submodlink": blackhole + `\gd`})
	if err := os.WriteFile(filepath.Join(mod, ".git"), []byte("gitdir: "+filepath.ToSlash(lnk)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var rep Report
	quickly(t, "a scan of a submodule whose .git file leads to a share", func() { rep = scan(t, outer) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestAModulesDirectoryThatLinksToAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	modules := filepath.Join(repo, ".git", "modules")
	links(t, map[string]string{modules: t.TempDir()}, map[string]string{"modules": blackhole + `\m`})
	var rep Report
	quickly(t, "a scan whose .git/modules links to a share", func() { rep = scan(t, repo) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestARelativeLinkIsReadFromTheDirectoryItIsReallyIn(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	root := t.TempDir()
	realDir := filepath.Join(root, "b", "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	via := filepath.Join(root, "a", "via")
	rel := filepath.Join(realDir, "rel")
	inner := filepath.Join(root, "b", "inner")
	links(t, map[string]string{via: realDir}, map[string]string{"via": realDir, "rel": `..\inner`, "inner": blackhole + `\x`})
	// rel and inner are links as far as Lstat can tell.
	for _, l := range []string{inner, rel} {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", l, t.TempDir()).CombinedOutput(); err != nil {
			t.Skipf("a junction cannot be made here: %v: %s", err, out)
		}
	}
	// Read through a.via, the textual parent of rel is a, where ..\inner is not
	// the share; the real parent is b\real, where it is.
	if err := tfs().guard(filepath.Join(via, "rel")); err == nil || !strings.Contains(err.Error(), "network share") {
		t.Errorf("guard = %v, want the share reached from the link's real directory", err)
	}
}

func TestALongChainOfLinksEndingAtAShareIsNeverResolved(t *testing.T) { chainOfLinks(t, true) }

// A chain past the depth limit is treated as a share even when it ends at an
// ordinary folder: nothing legitimate is that deep, and letting the call that
// follows links resolve it is what must not happen.
func TestALongChainOfLinksPastTheDepthLimitIsTreatedAsAShare(t *testing.T) { chainOfLinks(t, false) }

// Each call in the layer refuses a path that leads to a share, so the guard is
// checked on its own and not through whichever caller reaches it first.
func TestEveryCallInTheLayerRefusesAPathThroughALinkToAShare(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	dir := t.TempDir()
	lnk := filepath.Join(dir, "layerlink")
	links(t, map[string]string{lnk: t.TempDir()}, map[string]string{"layerlink": blackhole + `\x`})
	through := filepath.Join(lnk, "f")
	calls := map[string]func() error{
		"fsStat":      func() error { _, err := tfs().Stat(through); return err },
		"fsLstat":     func() error { _, err := tfs().Lstat(through); return err },
		"fsReadFile":  func() error { _, err := tfs().ReadFile(through); return err },
		"fsOpenDir":   func() error { _, err := tfs().OpenDir(through); return err },
		"fsHash":      func() error { _, _, err := tfs().Hash(through, 10); return err },
		"fsCanonical": func() error { _, err := tfs().Canonical(through); return err },
	}
	for name, call := range calls {
		quickly(t, name, func() {
			if err := call(); !errors.Is(err, errNetworkPath) {
				t.Errorf("%s through a link to a share = %v, want errNetworkPath", name, err)
			}
		})
	}
}

// tfs is a scan's view of the disk for a test that talks to the layer directly.
func tfs() *scanFS { return newScanFS(context.Background()) }

func hasItem(rep Report, text string) bool {
	for _, p := range rep.Items {
		if p.Kind == "unscannable" && strings.Contains(p.Value, text) {
			return true
		}
	}
	return false
}
