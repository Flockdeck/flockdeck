package session

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeConfig lays down a Claude Code configuration for the test to work on.
func writeConfig(t *testing.T, dir string, cfg map[string]any) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInheritTrustOnlyCarriesAnAnswerAlreadyGiven is the important one: this
// writes to a security-relevant setting, so it must never invent trust.
func TestInheritTrustOnlyCarriesAnAnswerAlreadyGiven(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	untrusted := filepath.Clean(filepath.Join(dir, "elsewhere"))
	target := filepath.Clean(filepath.Join(dir, "repo-worktree"))

	writeConfig(t, dir, map[string]any{
		"numStartups": 7,
		"projects": map[string]any{
			trusted:   map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []any{"Bash"}},
			untrusted: map[string]any{"hasTrustDialogAccepted": false},
		},
	})

	if !IsTrusted(trusted) {
		t.Fatal("the trusted directory should be reported as trusted")
	}
	if IsTrusted(untrusted) {
		t.Fatal("a directory with the answer 'no' is not trusted")
	}
	if IsTrusted(target) {
		t.Fatal("an unknown directory is not trusted")
	}

	// Inheriting from something that is not itself trusted must be refused.
	if err := InheritTrust(untrusted, target); err == nil {
		t.Error("expected inheriting from an untrusted directory to be refused")
	}
	if IsTrusted(target) {
		t.Fatal("the refusal must not have written anything")
	}

	// Inheriting from a trusted directory carries the answer over.
	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	if !IsTrusted(target) {
		t.Error("the worktree should now be trusted")
	}
}

// TestInheritTrustPreservesTheRestOfTheFile matters because this file belongs
// to Claude Code, not to us.
func TestInheritTrustPreservesTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	target := filepath.Clean(filepath.Join(dir, "wt"))

	path := writeConfig(t, dir, map[string]any{
		"numStartups":   42,
		"installMethod": "native",
		"tipsHistory":   map[string]any{"a": float64(1)},
		"projects": map[string]any{
			trusted: map[string]any{
				"hasTrustDialogAccepted": true,
				"allowedTools":           []any{"Bash", "Edit"},
				"mcpServers":             map[string]any{"x": "y"},
			},
		},
	})

	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}

	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the configuration is no longer valid JSON: %v", err)
	}
	if got["numStartups"] != float64(42) {
		t.Errorf("numStartups = %v, want it untouched", got["numStartups"])
	}
	if got["installMethod"] != "native" {
		t.Errorf("installMethod = %v, want it untouched", got["installMethod"])
	}
	projects := got["projects"].(map[string]any)
	orig := projects[trusted].(map[string]any)
	if len(orig["allowedTools"].([]any)) != 2 {
		t.Error("the original project's settings were damaged")
	}
	if _, ok := orig["mcpServers"]; !ok {
		t.Error("unrelated project fields were dropped")
	}
}

// TestInheritedTrustIsWhereClaudeLooks covers Windows, where Claude Code keys
// its projects by the path with forward slashes. An answer written under the
// backslashed path is never read, and every child of a fan-out stopped on the
// trust question the dialog had offered to answer for it.
func TestInheritedTrustIsWhereClaudeLooks(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	target := filepath.Clean(filepath.Join(dir, "repo-wt"))
	path := writeConfig(t, dir, map[string]any{
		"projects": map[string]any{filepath.ToSlash(trusted): map[string]any{"hasTrustDialogAccepted": true}},
	})

	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := target
	if runtime.GOOS == "windows" {
		want = filepath.ToSlash(target)
	}
	entry, _ := got["projects"].(map[string]any)[want].(map[string]any)
	if ok, _ := entry["hasTrustDialogAccepted"].(bool); !ok {
		t.Errorf("no trust recorded under %q, where Claude Code looks; projects = %v", want, got["projects"])
	}
}

// TestInheritTrustCarriesTheExternalImportsAnswer covers the other question a
// fresh checkout is asked: a project whose CLAUDE.md imports files from
// outside it is asked whether to allow that, and a worktree is a project
// Claude Code has never seen, so every child of a fan-out stopped on it.
func TestInheritTrustCarriesTheExternalImportsAnswer(t *testing.T) {
	dir := t.TempDir()
	answered := filepath.Clean(filepath.Join(dir, "answered"))
	unasked := filepath.Clean(filepath.Join(dir, "unasked"))
	path := writeConfig(t, dir, map[string]any{
		"projects": map[string]any{
			answered: map[string]any{
				"hasTrustDialogAccepted":                  true,
				"hasClaudeMdExternalIncludesApproved":     false,
				"hasClaudeMdExternalIncludesWarningShown": true,
			},
			unasked: map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	fromAnswered := filepath.Join(dir, "answered-wt")
	fromUnasked := filepath.Join(dir, "unasked-wt")
	for from, to := range map[string]string{answered: fromAnswered, unasked: fromUnasked} {
		if err := InheritTrust(from, to); err != nil {
			t.Fatalf("inherit %s: %v", from, err)
		}
	}

	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	projects := got["projects"].(map[string]any)
	child, _ := projects[claudeProjectKey(fromAnswered)].(map[string]any)
	// A "no" is carried as a "no": the child is not asked, and does not load
	// the imports either, exactly as the project does not.
	if child["hasClaudeMdExternalIncludesApproved"] != false || child["hasClaudeMdExternalIncludesWarningShown"] != true {
		t.Errorf("the worktree's entry is %v, want the project's answer to the imports question", child)
	}
	other, _ := projects[claudeProjectKey(fromUnasked)].(map[string]any)
	if _, asked := other["hasClaudeMdExternalIncludesWarningShown"]; asked {
		t.Errorf("an answer was invented for a project never asked: %v", other)
	}
}

// TestInheritTrustIsIdempotent covers fanning out twice into the same worktree.
func TestInheritTrustIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	target := filepath.Clean(filepath.Join(dir, "wt"))

	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{trusted: map[string]any{"hasTrustDialogAccepted": true}},
	})

	for i := 0; i < 2; i++ {
		if err := InheritTrust(trusted, target); err != nil {
			t.Fatalf("inherit %d: %v", i, err)
		}
	}
	if !IsTrusted(target) {
		t.Error("the worktree should be trusted")
	}
}

// readJSON reads a configuration file back.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestInheritTrustDoesNotLoseClaudeCodesSave covers a Claude Code saving its
// configuration while a fan-out carries trust over. Claude Code reads the file
// under its lock, changes it and writes it back; a carry-over that ignored the
// lock read the file in between and wrote it after, so one of the two writes
// was thrown away. Here Claude Code has read the file and holds the lock, and
// writes its change a moment later.
func TestInheritTrustDoesNotLoseClaudeCodesSave(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Join(dir, "repo")
	path := writeConfig(t, dir, map[string]any{
		"numStartups": 7,
		"projects":    map[string]any{claudeProjectKey(trusted): map[string]any{"hasTrustDialogAccepted": true}},
	})
	lock := path + ".lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeRead := readJSON(t, path)
	saved := make(chan struct{})
	go func() {
		defer close(saved)
		time.Sleep(200 * time.Millisecond)
		claudeRead["numStartups"] = 8
		data, _ := json.Marshal(claudeRead)
		_ = os.WriteFile(path, data, 0o600)
		_ = os.Remove(lock)
	}()

	target := filepath.Join(dir, "wt")
	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	<-saved
	if got := readJSON(t, path)["numStartups"]; got != float64(8) {
		t.Errorf("numStartups = %v: Claude Code's save was thrown away", got)
	}
	if !IsTrusted(target) {
		t.Error("the carried-over trust was thrown away by Claude Code's save")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the lock was left behind: %v", err)
	}
}

// TestInheritTrustTakesOverAStaleLock covers a Claude Code that stopped while
// holding its lock: nobody has touched the lock for longer than the stale
// threshold, so it is taken over, as Claude Code's own library does.
func TestInheritTrustTakesOverAStaleLock(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Join(dir, "repo")
	path := writeConfig(t, dir, map[string]any{
		"projects": map[string]any{claudeProjectKey(trusted): map[string]any{"hasTrustDialogAccepted": true}},
	})
	lock := path + ".lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "wt")
	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	if !IsTrusted(target) {
		t.Error("the worktree should be trusted")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the lock was left behind: %v", err)
	}
}

// TestInheritTrustWaitsForTheLockOnlySoLong covers a lock held for longer than
// a carry-over will wait: nothing is written, and the holder's lock is left
// where it is.
func TestInheritTrustWaitsForTheLockOnlySoLong(t *testing.T) {
	wait := configLockWait
	configLockWait = 150 * time.Millisecond
	t.Cleanup(func() { configLockWait = wait })

	dir := t.TempDir()
	trusted := filepath.Join(dir, "repo")
	path := writeConfig(t, dir, map[string]any{
		"projects": map[string]any{claudeProjectKey(trusted): map[string]any{"hasTrustDialogAccepted": true}},
	})
	lock := path + ".lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "wt")
	if err := InheritTrust(trusted, target); err == nil {
		t.Fatal("expected a carry-over to give up while Claude Code holds the lock")
	}
	if IsTrusted(target) {
		t.Error("something was written without the lock")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("the holder's lock was removed: %v", err)
	}
}

// mkdirs creates each directory, with its parents.
func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTrustReachesDownToASubfolder covers a pane opened below the repository
// root. Claude Code accepts the answer given for any folder between the
// working directory and its repository root, so the subfolder of a trusted
// repository is trusted -- but not past the root, and not from a folder that
// merely contains the repository.
func TestTrustReachesDownToASubfolder(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	deep := filepath.Join(repo, "services", "api")
	other := filepath.Join(dir, "outer", "inner-repo")
	otherSub := filepath.Join(other, "pkg")
	loose := filepath.Join(dir, "outer", "loose", "sub")
	mkdirs(t, filepath.Join(repo, ".git"), deep, filepath.Join(other, ".git"), otherSub, loose)
	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{
			claudeProjectKey(repo):                        map[string]any{"hasTrustDialogAccepted": true},
			claudeProjectKey(filepath.Join(dir, "outer")): map[string]any{"hasTrustDialogAccepted": true},
		},
	})

	if !IsTrusted(deep) {
		t.Error("a folder inside a trusted repository should be trusted")
	}
	if IsTrusted(otherSub) {
		t.Error("trust given to a folder above a repository must not reach into it")
	}
	if !IsTrusted(loose) {
		t.Error("outside any repository, trust given to a parent folder should count")
	}
}

// TestAWorktreeHasItsRepositorysTrust covers a pane in a linked worktree:
// Claude Code treats one as part of the repository it was made from, so the
// repository's answer is the worktree's.
func TestAWorktreeHasItsRepositorysTrust(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	admin := filepath.Join(repo, ".git", "worktrees", "wt")
	wt := filepath.Join(dir, "wt")
	stray := filepath.Join(dir, "stray")
	mkdirs(t, admin, filepath.Join(wt, "sub"), stray)
	// The layout `git worktree add` leaves behind.
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+admin+"\n")
	writeFile(t, filepath.Join(admin, "commondir"), "../..\n")
	writeFile(t, filepath.Join(admin, "gitdir"), filepath.Join(wt, ".git")+"\n")
	// A .git file claiming the same administrative folder, which does not
	// point back at it: Claude Code does not count it as the repository's.
	writeFile(t, filepath.Join(stray, ".git"), "gitdir: "+admin+"\n")
	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{claudeProjectKey(repo): map[string]any{"hasTrustDialogAccepted": true}},
	})

	if !IsTrusted(wt) || !IsTrusted(filepath.Join(wt, "sub")) {
		t.Error("a worktree of a trusted repository should be trusted")
	}
	if IsTrusted(stray) {
		t.Error("a .git file that the repository does not point back at must not borrow its trust")
	}
}

// farAway is a location on another machine that does not exist: .invalid is
// never resolved, so a test that reaches for it by mistake fails rather than
// talking to anybody.
func farAway() string {
	if runtime.GOOS == "windows" {
		return `\\flockdeck.invalid\share`
	}
	return "/net/flockdeck.invalid/share"
}

// worktreeLayout lays down a trusted repository and a linked worktree of it
// the way `git worktree add` does, and checks the worktree is trusted before
// the test breaks something.
func worktreeLayout(t *testing.T) (dir, repo, wt, admin string) {
	t.Helper()
	dir = t.TempDir()
	repo = filepath.Join(dir, "repo")
	admin = filepath.Join(repo, ".git", "worktrees", "wt")
	wt = filepath.Join(dir, "wt")
	mkdirs(t, admin, wt)
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+admin+"\n")
	writeFile(t, filepath.Join(admin, "commondir"), "../..\n")
	writeFile(t, filepath.Join(admin, "gitdir"), filepath.Join(wt, ".git")+"\n")
	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{claudeProjectKey(repo): map[string]any{"hasTrustDialogAccepted": true}},
	})
	if !IsTrusted(wt) {
		t.Fatal("the intact worktree should be trusted, or the test proves nothing")
	}
	return dir, repo, wt, admin
}

// symlink makes a link, skipping the test where the system will not let it.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make links here: %v", err)
	}
}

// TestTrustFailsSafe covers every way a worktree's links to its repository
// can fail to hold. IsTrusted decides whether trust is written into Claude
// Code's configuration, so each must leave the worktree judged on its own
// answers alone -- and it has none.
func TestTrustFailsSafe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(t *testing.T, dir, wt, admin string)
	}{
		{"a .git file that names no gitdir", func(t *testing.T, dir, wt, admin string) {
			writeFile(t, filepath.Join(wt, ".git"), "worktree of repo\n")
		}},
		{"an empty gitdir", func(t *testing.T, dir, wt, admin string) {
			writeFile(t, filepath.Join(wt, ".git"), "gitdir:\n")
		}},
		{"an unreadable .git file", func(t *testing.T, dir, wt, admin string) {
			if !lockAgainstReading(t, filepath.Join(wt, ".git")) {
				t.Skip("nothing here can stop this user reading a file")
			}
		}},
		{"a missing commondir", func(t *testing.T, dir, wt, admin string) {
			os.Remove(filepath.Join(admin, "commondir"))
		}},
		{"a commondir naming another repository", func(t *testing.T, dir, wt, admin string) {
			mkdirs(t, filepath.Join(dir, "other", ".git"))
			writeFile(t, filepath.Join(admin, "commondir"), filepath.Join(dir, "other", ".git"))
		}},
		{"a missing back-pointer", func(t *testing.T, dir, wt, admin string) {
			os.Remove(filepath.Join(admin, "gitdir"))
		}},
		{"a back-pointer to another worktree", func(t *testing.T, dir, wt, admin string) {
			writeFile(t, filepath.Join(admin, "gitdir"), filepath.Join(dir, "elsewhere", ".git"))
		}},
		{"a gitdir on another machine", func(t *testing.T, dir, wt, admin string) {
			writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+farAway()+"/repo/.git/worktrees/wt")
		}},
		{"a commondir on another machine", func(t *testing.T, dir, wt, admin string) {
			writeFile(t, filepath.Join(admin, "commondir"), farAway()+"/repo/.git")
		}},
		{"a .git that is a link", func(t *testing.T, dir, wt, admin string) {
			real := filepath.Join(dir, "pointer")
			writeFile(t, real, "gitdir: "+admin)
			os.Remove(filepath.Join(wt, ".git"))
			symlink(t, real, filepath.Join(wt, ".git"))
		}},
		{"a commondir that is a link", func(t *testing.T, dir, wt, admin string) {
			real := filepath.Join(dir, "commondir")
			writeFile(t, real, "../..")
			os.Remove(filepath.Join(admin, "commondir"))
			symlink(t, real, filepath.Join(admin, "commondir"))
		}},
		{"a gitdir through a loop of links", func(t *testing.T, dir, wt, admin string) {
			symlink(t, filepath.Join(dir, "loop-b"), filepath.Join(dir, "loop-a"))
			symlink(t, filepath.Join(dir, "loop-a"), filepath.Join(dir, "loop-b"))
			writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(dir, "loop-a", "wt"))
		}},
		{"a gitdir through a link to another machine", func(t *testing.T, dir, wt, admin string) {
			symlink(t, farAway(), filepath.Join(dir, "away"))
			writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(dir, "away", "repo", ".git", "worktrees", "wt"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, wt, admin := worktreeLayout(t)
			tc.spoil(t, dir, wt, admin)
			if IsTrusted(wt) || IsTrusted(filepath.Join(wt, "sub")) {
				t.Error("the worktree borrowed its repository's trust through a link that does not hold")
			}
		})
	}
}

// fakeInfo is a file-system entry of a given kind, and nothing else.
type fakeInfo fs.FileMode

func (f fakeInfo) Name() string       { return "" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return fs.FileMode(f) }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return fs.FileMode(f).IsDir() }
func (f fakeInfo) Sys() any           { return nil }

// TestStaysLocalFollowsLinks covers the walk that keeps a worktree's links from
// leading off the machine, with the links laid out in memory: the test cases
// that make real ones skip on a Windows machine without the privilege, and
// this is the part of the trust check that most needs to have run everywhere.
func TestStaysLocalFollowsLinks(t *testing.T) {
	base := t.TempDir()
	links := map[string]string{
		filepath.Join(base, "away"):   farAway(),
		filepath.Join(base, "here"):   filepath.Join(base, "real"),
		filepath.Join(base, "rel"):    "real",
		filepath.Join(base, "hop"):    filepath.Join(base, "away"),
		filepath.Join(base, "loop-a"): filepath.Join(base, "loop-b"),
		filepath.Join(base, "loop-b"): filepath.Join(base, "loop-a"),
	}
	lstatWas, readlinkWas := lstat, readlink
	t.Cleanup(func() { lstat, readlink = lstatWas, readlinkWas })
	lstat = func(p string) (fs.FileInfo, error) {
		if _, ok := links[p]; ok {
			return fakeInfo(fs.ModeSymlink), nil
		}
		return fakeInfo(fs.ModeDir), nil
	}
	readlink = func(p string) (string, error) {
		if target, ok := links[p]; ok {
			return target, nil
		}
		return "", errors.New("not a link")
	}

	for _, c := range []struct {
		name string
		want bool
	}{
		{"real", true},
		{"here", true},    // a link to a folder on this machine
		{"rel", true},     // the same, written relative to where it is
		{"away", false},   // a link to another machine
		{"hop", false},    // a local link to a link to another machine
		{"loop-a", false}, // a loop, which no count of hops gets out of
	} {
		if got := staysLocal(filepath.Join(base, c.name, "repo", ".git")); got != c.want {
			t.Errorf("staysLocal through %q = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestACraftedWorktreeCannotBorrowTrust covers a folder dressed up as a
// worktree of a trusted repository: its .git file names that repository's
// administrative folder for a real worktree, which does not point back at it.
func TestACraftedWorktreeCannotBorrowTrust(t *testing.T) {
	dir, _, _, admin := worktreeLayout(t)
	crafted := filepath.Join(dir, "crafted")
	mkdirs(t, crafted)
	writeFile(t, filepath.Join(crafted, ".git"), "gitdir: "+admin+"\n")
	if IsTrusted(crafted) {
		t.Error("a folder claiming another worktree's administrative folder was trusted")
	}
}

// TestTrustIsNotFetchedFromAnotherMachine covers a worktree whose .git file
// reaches its repository over the network. The loopback share leads back to
// this very disk, so everything would check out if it were followed: only the
// refusal to follow it keeps the answer untrusted.
func TestTrustIsNotFetchedFromAnotherMachine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC paths are a Windows form")
	}
	dir, repo, wt, admin := worktreeLayout(t)
	vol := filepath.VolumeName(admin)
	if len(vol) != 2 || vol[1] != ':' {
		t.Skip("the temporary folder is not on a lettered drive")
	}
	share := `\\localhost\` + vol[:1] + "$"
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+share+admin[len(vol):])
	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{
			claudeProjectKey(repo):                    map[string]any{"hasTrustDialogAccepted": true},
			claudeProjectKey(share + repo[len(vol):]): map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	if IsTrusted(wt) {
		t.Error("trust was taken from a repository reached over the network")
	}
}

// TestTrustStopsWhereClaudeStops covers the edges of the walk: a sibling with
// a longer name, a path that climbs out with "..", another spelling of the
// name, a folder above the repository, the top of the disk, and a directory
// on another machine.
func TestTrustStopsWhereClaudeStops(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	sibling := filepath.Join(dir, "repo-other")
	outer := filepath.Join(dir, "outer")
	inner := filepath.Join(outer, "inner")
	mkdirs(t, filepath.Join(repo, ".git"), filepath.Join(repo, "sub"), sibling, filepath.Join(inner, ".git"))
	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{
			claudeProjectKey(repo):                   map[string]any{"hasTrustDialogAccepted": true},
			claudeProjectKey(outer):                  map[string]any{"hasTrustDialogAccepted": true},
			claudeProjectKey(farAway() + "/project"): map[string]any{"hasTrustDialogAccepted": true},
		},
	})

	for _, c := range []struct {
		dir  string
		want bool
	}{
		{filepath.Join(repo, "sub", ".."), true},
		{sibling, false},
		{filepath.Join(repo, "..", "repo-other"), false},
		{filepath.Join(repo, "sub", "..", "..", "repo-other"), false},
		// Claude Code looks a project up by its exact spelling, so another
		// spelling is another project -- even where the disk ignores case.
		{filepath.Join(dir, "REPO"), false},
		{filepath.Join(dir, "REPO", "sub"), false},
		// An answer given above a repository does not reach into it.
		{inner, false},
		{filepath.Join(inner, "pkg"), false},
		// A directory on another machine is not looked at at all.
		{farAway() + "/project", false},
		{"", false},
	} {
		if got := IsTrusted(c.dir); got != c.want {
			t.Errorf("IsTrusted(%q) = %v, want %v", c.dir, got, c.want)
		}
	}

	// Outside any repository the walk goes all the way up to the top of the
	// disk, and stops there.
	loose := filepath.Join(dir, "loose", "a", "b")
	mkdirs(t, loose)
	if repoRoot(loose) != "" {
		t.Skip("the temporary folder is inside a repository")
	}
	top := filepath.VolumeName(loose) + string(filepath.Separator)
	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{claudeProjectKey(top): map[string]any{"hasTrustDialogAccepted": true}},
	})
	if !IsTrusted(loose) {
		t.Errorf("the walk did not reach %s, the top of the disk", top)
	}
}

// TestNetworkPaths pins down what counts as another machine.
func TestNetworkPaths(t *testing.T) {
	remote := []string{"/net/host/share", "/Network/Servers/host", "//x/../net/host"}
	local := []string{"/home/user/net", "/netlify/site", "relative/net"}
	if runtime.GOOS == "windows" {
		remote = []string{`\\server\share`, `//server/share`, `\/server\share`, `\\?\C:\repo`, `\\.\pipe\x`}
		local = []string{`C:\repo`, `C:\net\x`, `repo\sub`}
	}
	for _, p := range remote {
		if !networkPath(p) {
			t.Errorf("networkPath(%q) = false, want true", p)
		}
	}
	for _, p := range local {
		if networkPath(p) {
			t.Errorf("networkPath(%q) = true, want false", p)
		}
	}
}

// TestInheritTrustWritesThroughALinkedConfiguration covers a configuration kept
// elsewhere and linked into place, as a dotfiles checkout does. Claude Code
// writes through the link; renaming over it replaced the link with a plain
// file, and the configuration in the checkout stopped being the one in use.
func TestInheritTrustWritesThroughALinkedConfiguration(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "dotfiles")
	cfgDir := filepath.Join(dir, "home")
	mkdirs(t, realDir, cfgDir)
	trusted := filepath.Join(dir, "repo")
	realPath := filepath.Join(realDir, ".claude.json")
	data, err := json.Marshal(map[string]any{
		"projects": map[string]any{claudeProjectKey(trusted): map[string]any{"hasTrustDialogAccepted": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realPath, data, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cfgDir, ".claude.json")
	symlink(t, realPath, link)
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)

	target := filepath.Join(dir, "wt")
	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link to the configuration was replaced by a file (%v, %v)", fi, err)
	}
	if !IsTrusted(target) {
		t.Error("the answer was not written to the file the link points at")
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(realPath); err != nil || fi.Mode().Perm() != 0o640 {
			t.Errorf("the configuration's permissions were not kept (%v, %v)", fi, err)
		}
	}
}

// TestIsTrustedWithoutAConfiguration covers a machine where Claude has not run.
func TestIsTrustedWithoutAConfiguration(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "missing"))
	if IsTrusted(t.TempDir()) {
		t.Error("nothing is trusted when there is no configuration")
	}
}

// TestATrustCarryOverThatFailsSaysWhatHappensNext covers the two ways a
// fan-out's trust carry-over is refused, which it shows as "could not carry
// over folder trust: " and then the reason. A project nobody has answered for
// said only that it was "not itself trusted", and a busy configuration ended
// by repeating the notice's own words; neither said what to do, or that each
// new agent would now ask about its folder itself.
func TestATrustCarryOverThatFailsSaysWhatHappensNext(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	project := filepath.Join(t.TempDir(), "my-project")
	err := InheritTrust(project, filepath.Join(t.TempDir(), "worktree"))
	if err == nil {
		t.Fatal("trust was carried over from a project nobody has answered for")
	}
	for _, want := range []string{"my-project", "Claude Code", "answer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, want it to say %q", err, want)
		}
	}
	busy := errConfigBusy.Error()
	if strings.Contains(busy, "not carried over") || !strings.Contains(busy, "ask") {
		t.Errorf("busy = %q, want the reason alone, and that each agent will ask for itself", busy)
	}
}
