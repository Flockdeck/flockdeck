package server

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/store"
)

// Accepting what a repository runs.
//
// A commit, push, pull or fetch started here runs the programs the repository
// names (see gitx.ScanPrograms) as the user, with no agent permission prompt in
// front of it. None of that is switched off, because hooks are run on purpose.
// What is checked is whether the programs are the ones the user last accepted:
// if something new, edited or moved is there, the window is sent the list and
// asked, and the command is sent again with the answer.
//
// The check is not atomic. The scan runs just ahead of the command, and a
// commit stages files first, so a hook or setting written in between is run
// unchecked. It also rests on a file an agent running as the same user could
// write too: it holds against an agent that can write inside the project and
// not outside it.

// scanPrograms and the store functions are variables so a test can have the
// check itself fail.
var (
	scanPrograms = gitx.ScanPrograms
	loadGitSeen  = store.LoadGitSeen
	saveGitSeen  = store.SaveGitSeen
)

// gitWarnMsg asks the window whether to go ahead. Resend is the command to send
// again, with Accept set to "once" or "remember" and Seen to what is echoed
// here, which is the scan that was shown: a repository that changed again in
// between is asked about again instead of accepted unseen.
//
// Intro is the sentence that says what is wrong and what to do about it, and
// Items the programs, each one line of plain text. Text is both as one string,
// for a window that draws only that.
type gitWarnMsg struct {
	Type   string   `json:"type"`
	Cwd    string   `json:"cwd"`
	Path   string   `json:"path"`
	Action string   `json:"action"`
	Resend string   `json:"resend"`
	First  bool     `json:"first"`
	Intro  string   `json:"intro"`
	Items  []string `json:"items"`
	Text   string   `json:"text"`
	Seen   string   `json:"seen"`
}

// gitProgramsAccepted reports whether git may be run in dir for action. When it
// may not, the window has been sent a gitWarn and nothing has run.
//
// accept and seen are the window's answer to an earlier gitWarn: "once" goes
// ahead this time, "remember" goes ahead and records what was shown as accepted.
// Either counts only if seen still names what is there. That echo is a check
// against a repository that changed in between, and not against a client that
// lies: it is the client that sends it. So a window reached through the relay
// is never asked and its answers are ignored; accepting is done on the machine.
//
// A check that cannot be made because git cannot be run does not stop the
// command: the window is told it went unchecked, and git runs as it did before
// there was a check. Anything git can still run but the check could not read is
// not that: it is an item in the warning.
func (s *Server) gitProgramsAccepted(c *controlClient, dir, path, action, resend, accept, seen string) bool {
	rep, err := scanPrograms(dir)
	if err != nil {
		if _, rootErr := gitx.Root(dir); rootErr != nil {
			// Not a repository at all: git says so itself, in the command's own error.
			return true
		}
		c.notify("Flockdeck could not check this repository's git settings and hooks before running git "+action+" ("+
			firstLine(err.Error())+"). Going ahead without the check.", false)
		return true
	}
	// A record that cannot be read counts as no record: the repository's own
	// programs are then asked about, which is the safe way to fail.
	saved, have, err := loadGitSeen(rep.Repo)
	var kp *gitx.Known
	if err == nil && have {
		kp = &gitx.Known{IDs: saved.IDs}
	}
	v := gitx.Judge(rep, kp)
	ids := rep.IDs()
	if !v.Warn {
		if kp == nil {
			// A first look at a repository that names nothing of its own. A
			// record that cannot be written costs only a second look next time.
			_ = saveGitSeen(rep.Repo, ids)
		}
		return true
	}
	now := fingerprint(ids)
	if !c.remote && (accept == "once" || accept == "remember") && seen == now {
		if accept == "remember" {
			// What was accepted for the repository stays accepted: a linked
			// worktree can see more than another does, and replacing the record
			// with the latest scan had them undo each other's answers.
			_ = saveGitSeen(rep.Repo, union(saved.IDs, ids))
		}
		return true
	}
	if c.remote {
		c.notify("This repository's git settings or hooks need your approval before git "+action+" runs, and that is given "+
			"on the machine Flockdeck runs on, not from a window reached through the relay. Press the button there.", true)
		return false
	}
	c.sendJSON(gitWarnMsg{
		Type: "gitWarn", Cwd: dir, Path: path, Action: action, Resend: resend,
		First: v.First, Intro: v.Intro(action), Items: v.Lines(), Text: v.Warning(action), Seen: now,
	})
	return false
}

// union is a and b together, sorted, each once.
func union(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, id := range append(append([]string{}, a...), b...) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// fingerprint names a scan, to tell whether it is the one that was shown.
func fingerprint(ids []string) string {
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return hex.EncodeToString(sum[:])[:16]
}

// firstLine is the first line of s, for a message that has room for one.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
