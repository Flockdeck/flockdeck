package review

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// runningConfig matches the git configuration keys that have a read-only
// subcommand run something other than git, or reach the network:
//
//   - core.fsmonitor naming a program, which git status, git diff and
//     git describe --dirty run as they refresh the index
//   - diff.external, and a diff driver's command or textconv, run by git diff,
//     git log -p, git show and git blame on the files .gitattributes assigns
//   - a filter driver's clean, smudge or process, which git status and git
//     diff run on a file whose stat has changed
//   - a gpg program, run by git log --show-signature
//   - diff.submodule, which has git log and git show open each submodule as a
//     repository of its own, under its own configuration
//   - trace2 targets, which have every git command write to a file or socket
//   - a partial clone's promisor remote, which git log -p, git show and git
//     blame fetch a missing file from, credential helper, ssh command and all
//
// Keys are matched as git prints them: section and name lower-cased, a
// subsection (the driver's name) as written.
//
// A pager is left off on purpose: git only starts one when its output is a
// terminal, and an agent's Bash call never is.
const runningConfig = `^(core\.fsmonitor|diff\.external|diff\.submodule|diff\..+\.(command|textconv)|filter\..+\.(clean|smudge|process)|gpg\.program|gpg\..+\.program|trace2\..+|extensions\.partialclone|remote\..+\.promisor)$`

// touchesWorkTree are the subcommands that look at the working tree, and so at
// every submodule checked out in it: each one runs git again inside the
// submodule, under whatever configuration the submodule's own git directory
// has -- which gitRunsNothing cannot see from the top of the repository.
var touchesWorkTree = map[string]bool{"status": true, "diff": true, "describe": true}

// gitTimeout bounds each git command gitRunsNothing starts. It runs while an
// agent waits on a permission decision, and a git that cannot answer in this
// long is one whose answer is not worth waiting for: the call is simply asked
// about, as it would have been with auto-review off.
const gitTimeout = 5 * time.Second

// gitRunsNothing reports whether a read-only git subcommand, run in dir, would
// run nothing but git: no configured program, no network fetch, and -- where
// worktree is set -- no submodule whose own configuration could name one.
// Anything it cannot establish, an empty dir or a git that fails included, is
// a no.
//
// This is not a question for Claude Code's own protections: it keeps an agent
// from editing .git/config or .git/modules, but a repository cloned with a
// submodule entry in it and two ordinary files an agent may write anywhere
// in the project -- sub/.git, saying "gitdir: ../x", and x/config with a
// core.fsmonitor -- had a plain git status at the top of the repository run
// the program x/config named.
//
// It is a variable so tests can check the policy without a repository.
var gitRunsNothing = func(dir string, worktree bool) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	if !configRunsNothing(dir) {
		return false
	}
	return !worktree || !hasSubmodule(dir)
}

// configRunsNothing asks git, in dir, for every configuration key
// runningConfig matches -- from the system, global, repository and worktree
// files and whatever they include, exactly as the command itself would see
// them -- and reports whether none is set to anything that would run.
func configRunsNothing(dir string) bool {
	out, err := gitIn(dir, "config", "--null", "--get-regexp", runningConfig)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 {
		// Exit 1 with nothing printed is git's "no key matched".
		return true
	}
	if err != nil {
		return false
	}
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		key, value, _ := strings.Cut(string(entry), "\n")
		if !harmlessValue(key, value) {
			return false
		}
	}
	return true
}

// harmlessValue reports whether a key runningConfig matched is set to
// something that runs nothing after all: core.fsmonitor as a boolean (true is
// git's own built-in monitor, which is git), and Git LFS's own filter.
func harmlessValue(key, value string) bool {
	if key == "core.fsmonitor" {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "true", "false", "yes", "no", "on", "off", "1", "0":
			return true
		}
		return false
	}
	// Git LFS's own filter, exactly as `git lfs install` writes it.
	return gitx.StandardLFSFilter(key, value)
}

// hasSubmodule reports whether the index in dir records a submodule: an entry
// of mode 160000, whether or not .gitmodules names it. Anything git cannot
// answer counts as a yes.
func hasSubmodule(dir string) bool {
	out, err := gitIn(dir, "ls-files", "--stage")
	if err != nil {
		return true
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if bytes.HasPrefix(sc.Bytes(), []byte("160000 ")) {
			return true
		}
	}
	return sc.Err() != nil
}

// gitIn runs git in dir and returns its stdout. It starts no pager, asks for
// no password, takes no optional lock an agent's own git might want, and
// turns core.fsmonitor off for itself, so that asking about a repository
// cannot run what the question is about.
func gitIn(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor="}, args...)...)
	cmd.Dir = dir
	sysproc.NoWindow(cmd)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat")
	return cmd.Output()
}
