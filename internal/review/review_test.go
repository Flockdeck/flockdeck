package review

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A read-only command is allowed, and says why -- the reason a permission
// decision carries back to the agent.
func TestDecideAllowsAReadOnlyCommand(t *testing.T) {
	dir := cleanRepo(t)
	for _, cmd := range []string{
		"ls -la", "git status", "git status --short", "cat internal/review/review.go",
		"git log -5", "git show HEAD~1", "pwd", "grep -rn TODO .",
	} {
		d := Decide("Bash", `{"command":`+quote(cmd)+`}`, dir)
		if !d.Allow {
			t.Errorf("Decide(Bash, %q).Allow = false, want true", cmd)
		}
		if d.Reason == "" {
			t.Errorf("Decide(Bash, %q).Reason is empty, want an explanation", cmd)
		}
	}
}

// Anything that writes, deletes, sends network traffic or chains into
// something else is left to ask, exactly as it always has been -- including a
// safe verb smuggling something else in behind it.
func TestDecideAsksForAnythingElse(t *testing.T) {
	// A clean repository, so that every git command below is asked about for
	// what it says, not for where it runs.
	dir := cleanRepo(t)
	for _, cmd := range []string{
		"rm -rf /tmp/x",
		"git commit -m x",
		"git push",
		"git branch -D main",
		"curl http://example.com",
		"echo hi > out.txt",
		"cat foo && rm bar",
		"cat foo; rm bar",
		"cat foo | rm bar",
		"cat $(rm bar)",
		"cat `rm bar`",
		"(rm -rf /)",
		"cat {a,b}",
		"npm install",
		"chmod +x a.sh",
		"sudo ls",
		// A read-only name that runs, or writes, whatever it is handed.
		"env rm -rf /tmp/x",
		"sort -o out.txt in.txt",
		"uniq in.txt out.txt",
		"tree -o out.txt",
		"rg --pre sh x",
		"file -C -m magic",
		"date -s 2020-01-01",
		"printenv",
		// A read-only subcommand given the one flag that makes it write or run.
		"git diff --output=patch.txt",
		"git log -p --output patch.txt",
		"git diff --ext-diff",
		"git show --textconv HEAD",
		// No go command at all: go env writes with -w, -u or Go's own --w and
		// --u spellings of them, and any go command fetches the toolchain the
		// project's go.mod names.
		"go env -w GOFLAGS=-toolexec=x",
		"go env -u GOFLAGS",
		"go env --w GOFLAGS=-toolexec=x",
		"go env --u GOFLAGS",
		"go env GOPATH",
		"go version",
		"go doc -http",
		"go list -toolexec x ./...",
		// Reading outside the project, which Claude Code would have asked about.
		"cat /etc/passwd",
		"cat ~/.ssh/id_rsa",
		"cat ../secret.txt",
		"cat C:/Users/me/token.json",
		`cat C:\Users\me\token.json`,
		`cat \\server\share\x`,
		"grep -r password /",
		"grep --file=/etc/shadow x",
		"git diff --no-index ../a b",
		// A submodule opened as a repository of its own, under its own
		// configuration.
		"git log -p --submodule=diff",
		"git diff --submodule",
		// The same, hidden behind quoting, escaping or a glob the shell
		// rewrites before the command ever sees the path.
		`cat '.''.'/secret.txt`,
		`cat .\./secret.txt`,
		`cat "/etc/passwd"`,
		`cat '/'etc/passwd`,
		`cat ""~/.ssh/id_rsa`,
		"cat .[.]/secret.txt",
		"cat .?/secret.txt",
		"cat .*/secret.txt",
	} {
		d := Decide("Bash", `{"command":`+quote(cmd)+`}`, dir)
		if d.Allow {
			t.Errorf("Decide(Bash, %q).Allow = true, want false", cmd)
		}
		if d.Reason != "" {
			t.Errorf("Decide(Bash, %q).Reason = %q, want empty", cmd, d.Reason)
		}
	}
}

// Only Bash is judged: a file write is exactly the request auto-review must
// never wave through, and an unrecognised tool is unknown territory.
func TestDecideAsksForEveryToolButBash(t *testing.T) {
	for _, tc := range []struct{ tool, input string }{
		{"Write", `{"filePath":"a.go","content":"package a"}`},
		{"Edit", `{"filePath":"a.go","oldString":"a","newString":"b"}`},
		{"MultiEdit", `{"filePath":"a.go"}`},
		{"AskUserQuestion", `{"questions":[]}`},
		{"mcp__example__tool", `{}`},
	} {
		if d := Decide(tc.tool, tc.input, ""); d.Allow {
			t.Errorf("Decide(%s, ...).Allow = true, want false", tc.tool)
		}
	}
}

// Nothing to read -- no tool input at all, or input that is not the JSON
// object Decide expects -- is asked rather than misread as safe.
func TestDecideAsksWhenThereIsNothingToRead(t *testing.T) {
	for _, in := range []string{"", "not json", `{}`, `{"command":""}`, `{"command":123}`} {
		if d := Decide("Bash", in, ""); d.Allow {
			t.Errorf("Decide(Bash, %q).Allow = true, want false", in)
		}
	}
}

// A git command with no directory to run in cannot be checked for what git
// would run there, so it is asked about -- while a command that is not git
// needs no directory at all.
func TestDecideAsksForGitWithNoDirectory(t *testing.T) {
	cleanRepo(t) // for its empty global configuration
	if d := Decide("Bash", `{"command":"git status"}`, ""); d.Allow {
		t.Error("git status with no directory was allowed, want it asked")
	}
	if d := Decide("Bash", `{"command":"ls"}`, ""); !d.Allow {
		t.Error("ls with no directory was asked, want it allowed")
	}
}

// Every configuration key that has a read-only git subcommand run a program,
// write a file or reach the network makes every git command there ask --
// whether the repository sets it, a file it includes does, or the user's own
// global configuration does.
func TestDecideAsksWhereGitConfigRunsSomething(t *testing.T) {
	for _, kv := range [][2]string{
		{"core.fsmonitor", "echo ran > ran.txt"},
		{"diff.external", "evil"},
		{"diff.foo.textconv", "evil"},
		{"diff.foo.command", "evil"},
		{"filter.foo.clean", "evil"},
		{"filter.foo.process", "evil"},
		{"filter.lfs.process", "git-lfs filter-process; evil"},
		{"filter.lfs.clean", "evil"},
		{"gpg.program", "evil"},
		{"gpg.ssh.program", "evil"},
		{"diff.submodule", "diff"},
		{"trace2.eventTarget", "out.json"},
		{"remote.origin.promisor", "true"},
		{"extensions.partialClone", "origin"},
	} {
		dir := cleanRepo(t)
		git(t, dir, "config", kv[0], kv[1])
		for _, cmd := range []string{"git status", "git diff", "git log -p", "git show", "git blame f", "git describe --dirty"} {
			if d := Decide("Bash", `{"command":`+quote(cmd)+`}`, dir); d.Allow {
				t.Errorf("with %s=%q, %q was allowed, want it asked", kv[0], kv[1], cmd)
			}
		}
	}

	t.Run("included", func(t *testing.T) {
		dir := cleanRepo(t)
		inc := filepath.Join(t.TempDir(), "more")
		if err := os.WriteFile(inc, []byte("[core]\n\tfsmonitor = evil\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "config", "include.path", inc)
		if d := Decide("Bash", `{"command":"git status"}`, dir); d.Allow {
			t.Error("an included core.fsmonitor was allowed, want it asked")
		}
	})

	t.Run("global", func(t *testing.T) {
		dir := cleanRepo(t)
		if err := os.WriteFile(os.Getenv("GIT_CONFIG_GLOBAL"), []byte("[diff \"x\"]\n\ttextconv = evil\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if d := Decide("Bash", `{"command":"git log -p"}`, dir); d.Allow {
			t.Error("a global textconv driver was allowed, want it asked")
		}
	})
}

// What looks like a runner and is not one is let through: core.fsmonitor as a
// boolean (git's own built-in monitor), and exactly what `git lfs install`
// writes, which too many people have globally to ask about every time.
func TestDecideAllowsHarmlessGitConfig(t *testing.T) {
	for _, kvs := range [][][2]string{
		{{"core.fsmonitor", "false"}},
		{{"core.fsmonitor", "true"}},
		{
			{"filter.lfs.clean", "git-lfs clean -- %f"},
			{"filter.lfs.smudge", "git-lfs smudge -- %f"},
			{"filter.lfs.process", "git-lfs filter-process"},
			{"filter.lfs.required", "true"},
		},
	} {
		dir := cleanRepo(t)
		for _, kv := range kvs {
			git(t, dir, "config", kv[0], kv[1])
		}
		if d := Decide("Bash", `{"command":"git status"}`, dir); !d.Allow {
			t.Errorf("with %v, git status was asked, want it allowed", kvs)
		}
	}
}

// The chain the check exists for, as it was reproduced: a repository whose
// index records a submodule (which a clone can bring, .gitmodules or not),
// and two ordinary files an agent can write anywhere in the project --
// sub/.git pointing at a git directory of its own making, and that
// directory's config naming a core.fsmonitor. Nothing under .git is touched,
// and nothing in the top repository's configuration changes, yet git status
// at the top runs git status in the submodule, which runs the monitor.
//
// So a command that looks at the working tree is asked about wherever a
// submodule is recorded, while one that reads only history is not.
func TestDecideAsksWhereASubmoduleCouldRunSomething(t *testing.T) {
	dir := cleanRepo(t)
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,1111111111111111111111111111111111111111,sub")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "sub")
	for path, body := range map[string]string{
		"sub/.git":          "gitdir: ../evilgit\n",
		"evilgit/HEAD":      "ref: refs/heads/main\n",
		"evilgit/config":    "[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = ../sub\n\tfsmonitor = echo ran > ../ran.txt\n",
		"evilgit/refs/k":    "",
		"evilgit/objects/k": "",
	} {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, cmd := range []string{"git status", "git diff", "git describe --dirty"} {
		if d := Decide("Bash", `{"command":`+quote(cmd)+`}`, dir); d.Allow {
			t.Errorf("with a submodule recorded, %q was allowed, want it asked", cmd)
		}
	}
	for _, cmd := range []string{"git log", "git show", "git rev-parse HEAD"} {
		if d := Decide("Bash", `{"command":`+quote(cmd)+`}`, dir); !d.Allow {
			t.Errorf("with a submodule recorded, %q was asked, want it allowed: it never opens one", cmd)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ran.txt")); err == nil {
		t.Error("deciding ran the submodule's core.fsmonitor itself")
	}
}

// cleanRepo is a fresh repository with one file, f, committed, under a
// global and system configuration that are empty -- so what the test's own
// machine has in ~/.gitconfig cannot change the answer. GIT_CONFIG_GLOBAL is
// left pointing at that empty file, for a test that means to fill it.
func cleanRepo(t *testing.T) string {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "f")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "a")
	return dir
}

// git runs git in dir for a test's own setup, failing the test if it fails.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// quote renders s as a JSON string literal, so a command holding a quote, a
// backslash or a backtick still builds a tool_input Decide can parse.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// A command longer than any read-only one is asked about, whatever it starts
// with: the hook clips a long command before it reaches Decide, and what
// follows the cut -- here a chained echo -- is still run by Claude Code.
func TestDecideAsksForAnOverlongCommand(t *testing.T) {
	long := strings.Repeat("cat a ", 64<<10/6+10) + "; echo CANARY"
	clipped := long[:64<<10]
	for _, cmd := range []string{clipped, strings.Repeat("cat a ", 1000)} {
		if d := Decide("Bash", `{"command":`+quote(cmd)+`}`, t.TempDir()); d.Allow {
			t.Errorf("Decide allowed a %d-byte command", len(cmd))
		}
	}
}
