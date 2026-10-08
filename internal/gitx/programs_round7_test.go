package gitx

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// copyTree copies a folder and what is in it.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// submoduleFarm makes a repository with n submodules the way git leaves them: a
// real submodule git directory for each under .git/modules (copied from one made
// by git, so each carries its sample hooks), a .git file in each submodule's
// folder, and a gitlink in the index. It does not run git once per submodule.
func submoduleFarm(t *testing.T, n int) string {
	t.Helper()
	template := filepath.Join(addSubmoduleOuter(t), ".git", "modules", "mod")
	outer := newRepo(t)
	var index strings.Builder
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("sub%03d", i)
		copyTree(t, template, filepath.Join(outer, ".git", "modules", name))
		cfg := filepath.Join(outer, ".git", "modules", name, "config")
		if data, err := os.ReadFile(cfg); err == nil {
			fixed := strings.ReplaceAll(string(data), "../../../mod", "../../../"+name)
			if err := os.WriteFile(cfg, []byte(fixed), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		work := filepath.Join(outer, name)
		if err := os.MkdirAll(work, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: ../.git/modules/"+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&index, "160000 %s 0\t%s\n", strings.Repeat("a", 40), name)
	}
	cmd := exec.Command("git", "update-index", "--add", "--index-info")
	cmd.Dir = outer
	cmd.Stdin = strings.NewReader(index.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git update-index: %v: %s", err, out)
	}
	return outer
}

// addSubmoduleOuter makes a repository with one real submodule and returns it.
func addSubmoduleOuter(t *testing.T) string {
	t.Helper()
	outer := newRepo(t)
	addSubmodule(t, outer)
	return outer
}

func TestARealShapedRepositoryWithManySubmodulesIsScannedQuicklyAndCleanly(t *testing.T) {
	const n = maxSubmodules
	outer := submoduleFarm(t, n)
	start := time.Now()
	rep := scan(t, outer)
	took := time.Since(start)
	calls := lastScanCalls.Load()
	t.Logf("%d submodules: %d file system calls, %s, %d items", n, calls, took, len(rep.Items))
	if took > 5*time.Second {
		t.Errorf("the scan took %s", took)
	}
	if calls > 3800 {
		t.Errorf("the scan made %d calls to the file system", calls)
	}
	for _, p := range rep.Items {
		if p.Kind == "unscannable" || p.Kind == "limit" {
			t.Errorf("a clean repository reported %s", p.Line())
			break
		}
	}
}

func gitList(t *testing.T, file string) []string {
	t.Helper()
	cmd := exec.Command("git", "config", "--file", file, "--list")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git config --list: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(out), "\r\n"), "\n") {
		if l != "" {
			lines = append(lines, strings.TrimRight(l, "\r"))
		}
	}
	return lines
}

func ours(entries []configEntry) []string {
	var lines []string
	for _, e := range entries {
		if e.value == "" {
			lines = append(lines, e.key)
			continue
		}
		lines = append(lines, e.key+"="+e.value)
	}
	return lines
}

// The plain reader must give what git gives, for every file it accepts.
func TestThePlainConfigReaderAgreesWithGit(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	cases := map[string]string{
		"a submodule's": "[core]\n\trepositoryformatversion = 0\n\tfilemode = false\n\tbare = false\n\tworktree = ../../../mod\n" +
			"[remote \"origin\"]\n\turl = https://example.com/mod.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n" +
			"[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n",
		"mixed case":          "[CORE]\n\tSshCommand = ssh -i k\n[Remote \"Origin\"]\n\tURL = x\n",
		"subsection spaces":   "[remote \"my remote\"]\n\turl = u\n",
		"comments, blanks":    "# a comment\n\n; another\n[core]\n\n\tbare = true\n# in between\n\tfilemode = false\n",
		"crlf":                "[core]\r\n\tbare = true\r\n\tfilemode = false\r\n",
		"no value":            "[core]\n\tbare\n",
		"no trailing newline": "[core]\n\tbare = true",
		"spaces in value":     "[core]\n\tsshCommand = ssh   -i   k\n",
		"repeated keys":       "[remote \"o\"]\n\turl = a\n\turl = b\n",
		"hooks path":          "[core]\n\thooksPath = .husky/_\n",
	}
	dir := t.TempDir()
	for name, text := range cases {
		file := filepath.Join(dir, "cfg")
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		entries, ok := parseSimpleConfig([]byte(text), file)
		if !ok {
			t.Errorf("%s: a plain file was handed to git", name)
			continue
		}
		got, want := ours(entries), gitList(t, file)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s:\nplain reader: %q\ngit:          %q", name, got, want)
		}
	}
}

func TestAnythingThePlainConfigReaderIsNotSureOfGoesToGit(t *testing.T) {
	for name, text := range map[string]string{
		"include":          "[include]\n\tpath = other\n",
		"includeIf":        "[includeIf \"gitdir:/x/\"]\n\tpath = other\n",
		"quoted value":     "[core]\n\tsshCommand = \"ssh -i k\"\n",
		"backslash":        "[core]\n\tsshCommand = C:" + `\` + "ssh\n",
		"comment after":    "[core]\n\tbare = true # why\n",
		"semicolon":        "[core]\n\tsshCommand = a;b\n",
		"continued":        "[core]\n\tsshCommand = a " + `\` + "\n b\n",
		"old subsection":   "[remote.origin]\n\turl = x\n",
		"same line":        "[core] bare = true\n",
		"bom":              "\xef\xbb\xbf[core]\n\tbare = true\n",
		"nul":              "[core]\n\tbare = tr\x00ue\n",
		"before section":   "bare = true\n[core]\n",
		"bad name":         "[core]\n\t1x = y\n",
		"empty subsection": "[remote \"\"]\n\turl = x\n",
		"unterminated":     "[core\n\tbare = true\n",
	} {
		if _, ok := parseSimpleConfig([]byte(text), "f"); ok {
			t.Errorf("%s: read here, and git would read it differently or not at all", name)
		}
	}
}

// What the plain reader hands to git comes back the same as before.
func TestASubmoduleConfigWithAnIncludeOrAQuoteIsStillRead(t *testing.T) {
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	extra := filepath.Join(t.TempDir(), "extra.conf")
	if err := os.WriteFile(extra, []byte("[core]\n\tsshCommand = ssh -i included\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, mod, "config", "include.path", filepath.ToSlash(extra))
	p, ok := find(scan(t, outer), "setting", "core.sshcommand")
	if !ok || p.Value != "ssh -i included" {
		t.Fatalf("a setting reached by an include in a submodule's config = %+v ok=%v", p, ok)
	}
	gitRun(t, mod, "config", "--unset", "include.path")
	cfg := filepath.Join(outer, ".git", "modules", "mod", "config")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("[core]\n\tsshCommand = \"ssh -i quoted\"\n")
	f.Close()
	p, ok = find(scan(t, outer), "setting", "core.sshcommand")
	if !ok || p.Value != "ssh -i quoted" {
		t.Errorf("a quoted setting in a submodule's config = %+v ok=%v", p, ok)
	}
}

func TestAHooksFolderWithManyEntriesIsLookedThroughByName(t *testing.T) {
	repo := newRepo(t)
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxHooksDirEntries+50; i++ {
		if err := os.WriteFile(filepath.Join(hooks, fmt.Sprintf("filler%04d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHook(t, hooks, "pre-commit", "#!/bin/sh\n")
	if _, ok := find(scan(t, repo), "hook", "pre-commit"); !ok {
		t.Error("a hook in a folder with more entries than are listed was not found")
	}
}

// A scan that runs out of its time says so, and does not freeze.
func TestAScanThatRunsOutOfTimeInAFarmSaysSoAndStops(t *testing.T) {
	outer := submoduleFarm(t, 190)
	was := scanTimeout
	t.Cleanup(func() { scanTimeout = was })
	scanTimeout = 250 * time.Millisecond
	start := time.Now()
	rep, err := ScanPrograms(outer)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("a scan with %s to run took %s", scanTimeout, d)
	}
	if !hasItem(rep, "took too long") && !hasItem(rep, "did not answer") {
		t.Skipf("the machine was quick enough to finish: %d items", len(rep.Items))
	}
	if v := Judge(rep, &Known{IDs: rep.AcceptableIDs()}); !v.Warn {
		t.Error("a scan that ran out of time was let through")
	}
}
