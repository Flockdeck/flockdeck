package gitx

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestOnlyTheFileSystemLayerTouchesTheDisk is the guard on the structure: the
// scan reaches the disk through the functions in scanfs.go, which refuse a share
// before anything follows a link to it. A call to the standard library's file
// system functions anywhere else in the scan is a way round that, and fails
// here, so a later change cannot add a site that forgets the check.
func TestOnlyTheFileSystemLayerTouchesTheDisk(t *testing.T) {
	banned := map[string]map[string]bool{
		"os": {"Stat": true, "Lstat": true, "ReadFile": true, "ReadDir": true, "Open": true, "OpenFile": true,
			"Readlink": true, "Create": true, "WriteFile": true, "Readlink2": true},
		"path/filepath": {"EvalSymlinks": true, "Walk": true, "WalkDir": true, "Glob": true},
		"io/ioutil":     {"ReadFile": true, "ReadDir": true, "WriteFile": true, "TempFile": true, "TempDir": true},
	}
	files, err := filepath.Glob("programs*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scan files found: %v", err)
	}
	var bad []string
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		checked++
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		local := map[string]string{} // name used in the file -> import path
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(path)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			local[name] = path
		}
		ast.Inspect(af, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if banned[local[id.Name]][sel.Sel.Name] {
				bad = append(bad, fmt.Sprintf("%s: %s.%s", fset.Position(sel.Pos()), id.Name, sel.Sel.Name))
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no scan source files were checked")
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("the scan reaches the disk outside scanfs.go, where a share is not refused:\n%s", strings.Join(bad, "\n"))
	}

	// And the layer does make those calls, so the check above is not vacuous.
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "scanfs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	ast.Inspect(af, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && (sel.Sel.Name == "Stat" || sel.Sel.Name == "Lstat") {
				found++
			}
		}
		return true
	})
	if found == 0 {
		t.Error("scanfs.go makes no os.Stat or os.Lstat call, so the check above looks at the wrong file")
	}
}

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
	if !hasNetworkItem(rep) {
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
	if err := guard(filepath.Join(via, "rel")); err == nil || !strings.Contains(err.Error(), "network share") {
		t.Errorf("guard = %v, want the share reached from the link's real directory", err)
	}
}

func TestACallThatCannotBeStartedBecauseTooManyAreWaitingIsAnItem(t *testing.T) {
	repo := newRepo(t)
	for i := 0; i < maxInFlight; i++ {
		inflight <- struct{}{}
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		for i := 0; i < maxInFlight; i++ {
			<-inflight
		}
	}
	t.Cleanup(release)
	if _, err := fsStat(repo); err != errNoAnswer {
		t.Errorf("with every slot taken, fsStat = %v, want errNoAnswer without calling", err)
	}
	rep, err := ScanPrograms(repo)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range rep.Items {
		if p.Kind == "unscannable" || p.Kind == "unreadable" {
			found = true
		}
	}
	if !found {
		t.Errorf("a scan that could not touch the disk reported %+v", rep.Items)
	}
	release()
	if _, err := fsStat(repo); err != nil {
		t.Errorf("after the slots are free, fsStat = %v", err)
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
		"fsStat":      func() error { _, err := fsStat(through); return err },
		"fsLstat":     func() error { _, err := fsLstat(through); return err },
		"fsReadFile":  func() error { _, err := fsReadFile(through); return err },
		"fsOpenDir":   func() error { _, err := fsOpenDir(through); return err },
		"fsHash":      func() error { _, _, err := fsHash(context.Background(), through, 10); return err },
		"fsCanonical": func() error { _, err := fsCanonical(through); return err },
	}
	for name, call := range calls {
		quickly(t, name, func() {
			if err := call(); !errors.Is(err, errNetworkPath) {
				t.Errorf("%s through a link to a share = %v, want errNetworkPath", name, err)
			}
		})
	}
}
