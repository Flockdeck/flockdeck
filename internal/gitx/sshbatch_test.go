package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNetworkCommandsKeepSSHFromAskingUnlessTheUserChoseTheirOwn covers a
// push, pull or fetch over ssh to a host ssh had not seen, or with a key that
// wants its passphrase. GIT_TERMINAL_PROMPT stops only git's own prompts, so
// ssh asked on the terminal Flockdeck was started from and waited there for
// the whole ten-minute deadline. It is now told to fail instead -- but only
// where the user has not chosen an ssh command of their own, which would be
// overridden, and which is often what names the key a repository needs.
func TestNetworkCommandsKeepSSHFromAskingUnlessTheUserChoseTheirOwn(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	// The developer's own git config may choose an ssh command; nothing here
	// is to depend on it.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	unset := func(name string) {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}

	repo := func(config ...string) string {
		dir := t.TempDir()
		if _, err := run(dir, "init", "-q"); err != nil {
			t.Fatalf("init: %v", err)
		}
		if len(config) > 0 {
			if _, err := run(dir, append([]string{"config"}, config...)...); err != nil {
				t.Fatalf("config: %v", err)
			}
		}
		return dir
	}
	// What git hands the ssh it would start, seen through an alias, which runs
	// with git's own environment.
	seen := func(dir string) string {
		t.Helper()
		out, err := runVerbose(dir, "-c", `alias.sshcmd=!echo "[$GIT_SSH_COMMAND]"`, "sshcmd")
		if err != nil {
			t.Fatalf("alias: %v", err)
		}
		return strings.TrimSpace(out)
	}

	unset("GIT_SSH_COMMAND")
	unset("GIT_SSH")
	if got := seen(repo()); got != "[ssh -o BatchMode=yes]" {
		t.Errorf("with no ssh command chosen, git is given %s; want ssh told not to ask", got)
	}
	if got := seen(repo("core.sshCommand", "ssh -i work_key")); got != "[]" {
		t.Errorf("with core.sshCommand set, git is given %s; want the user's own left to it", got)
	}

	t.Setenv("GIT_SSH", "plink")
	if got := seen(repo()); got != "[]" {
		t.Errorf("with GIT_SSH set, git is given %s; want the user's own left to it", got)
	}
	unset("GIT_SSH")

	t.Setenv("GIT_SSH_COMMAND", "ssh -i home_key")
	if got := seen(repo()); got != "[ssh -i home_key]" {
		t.Errorf("with GIT_SSH_COMMAND set, git is given %s; want the user's own", got)
	}
}

// TestAnSSHFailureSaysHowToGetPastIt covers what ssh says once it is kept
// from asking anything. A host it had not been told to trust, and a key whose
// passphrase it could not ask for, came out as its own shorthand followed by
// git's "make sure you have the correct access rights", which sent people
// checking the wrong thing. An ssh that fails as the real one does stands in
// for it, so nothing goes out to the network.
func TestAnSSHFailureSaysHowToGetPastIt(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "git@example.invalid:team/repo.git")
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	for _, tc := range []struct {
		said []string
		want []string
	}{
		{[]string{"No ED25519 host key is known for example.invalid and you have requested strict checking.", "Host key verification failed."},
			[]string{"trust example.invalid yet", "git fetch once from a terminal in " + repo}},
		{[]string{"git@example.invalid: Permission denied (publickey)."},
			[]string{"ssh-add", "git fetch from a terminal in " + repo}},
	} {
		script := "printf '%s\\n'"
		for _, line := range tc.said {
			script += " '" + line + "'"
		}
		t.Setenv("GIT_SSH_COMMAND", script+" >&2; exit 255;")
		_, err := Fetch(repo)
		if err == nil {
			t.Fatalf("a fetch whose ssh said %q succeeded", tc.said)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("ssh said %q, and the error is %q; want it to say %q", tc.said, err, want)
			}
		}
	}
}
