package server

import (
	"crypto/sha256"
	"encoding/hex"
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
type gitWarnMsg struct {
	Type   string   `json:"type"`
	Cwd    string   `json:"cwd"`
	Path   string   `json:"path"`
	Action string   `json:"action"`
	Resend string   `json:"resend"`
	First  bool     `json:"first"`
	Items  []string `json:"items"`
	Text   string   `json:"text"`
	Seen   string   `json:"seen"`
}

// gitProgramsAccepted reports whether git may be run in dir for action. When it
// may not, the window has been sent a gitWarn and nothing has run.
//
// accept and seen are the window's answer to an earlier gitWarn: "once" goes
// ahead this time, "remember" goes ahead and records what was shown as accepted.
// Either counts only if seen still names what is there.
//
// A check that cannot be made does not stop the command: the window is told it
// went unchecked, and git runs as it did before there was a check.
func (s *Server) gitProgramsAccepted(c *controlClient, dir, path, action, resend, accept, seen string) bool {
	rep, err := scanPrograms(dir)
	var (
		known gitx.Known
		have  bool
	)
	if err == nil {
		var saved store.GitSeen
		saved, have, err = loadGitSeen(rep.Repo)
		known = gitx.Known{IDs: saved.IDs}
	}
	if err != nil {
		if _, rootErr := gitx.Root(dir); rootErr != nil {
			// Not a repository at all: git says so itself, in the command's own error.
			return true
		}
		c.notify("Flockdeck could not check this repository's git settings and hooks before running git "+action+" ("+
			firstLine(err.Error())+"). Going ahead without the check.", false)
		return true
	}
	var kp *gitx.Known
	if have {
		kp = &known
	}
	v := gitx.Judge(rep, kp)
	ids := rep.IDs()
	if !v.Warn {
		if v.First || v.Narrowed {
			// Nothing here that was not accepted, or less than was. A record that
			// cannot be written costs only a second look next time.
			_ = saveGitSeen(rep.Repo, ids)
		}
		return true
	}
	now := fingerprint(ids)
	if (accept == "once" || accept == "remember") && seen == now {
		if accept == "remember" {
			_ = saveGitSeen(rep.Repo, ids)
		}
		return true
	}
	items := make([]string, 0, len(v.New))
	for _, p := range v.New {
		items = append(items, p.Line())
	}
	c.sendJSON(gitWarnMsg{
		Type: "gitWarn", Cwd: dir, Path: path, Action: action, Resend: resend,
		First: v.First, Items: items, Text: v.Warning(action), Seen: now,
	})
	return false
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
