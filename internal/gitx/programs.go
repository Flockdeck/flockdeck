package gitx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// Commit, push, pull and fetch run programs that the repository, and not
// Flockdeck, names: hooks in .git/hooks, a core.sshCommand, a credential
// helper, a filter driver, a gpg program. An agent that can write inside .git
// can set any of them, and Flockdeck then runs them as you, outside the agent's
// own permission prompts, the next time you press Commit or Push.
//
// Hooks are run on purpose (see CommitAll) and tools such as husky set
// core.hooksPath in .git/config, so none of this is switched off. ScanPrograms
// finds what would run; the caller decides whether you have seen it before.
//
// A scan never turns something it could not finish into "nothing runs". A
// hooks directory or file git may still be able to run, a submodule whose
// configuration cannot be read, a search that hit its limit, a git that did not
// answer in time: each becomes an item of its own, so that it is shown. Only a
// machine without git, or a directory that is not a repository, is an error,
// because there is then nothing for git to run.

// Program is one thing git would run for a commit, push, pull or fetch, and
// where it is set.
type Program struct {
	// Kind is "setting" for a configuration key, "hook" for an executable file
	// in a hooks directory, "unreadable" for something that may run and could
	// not be read, "limit" for a search that stopped at its limit, and
	// "unscannable" for a scan that could not be finished.
	Kind string
	// Name is the configuration key as git prints it (core.sshCommand is
	// core.sshcommand), or the hook's file name.
	Name string
	// Value is the configuration value. Empty for a hook.
	Value string
	// Sum is a hash of a hook's contents, so that editing a hook that was
	// already there is a change. Empty for a setting.
	Sum string
	// Where is the file that sets it: a configuration file, or the hooks
	// directory the hook is in.
	Where string
	// Submodule is the checked-out path of the submodule it belongs to, when
	// it belongs to one.
	Submodule string
	// Machine is set when it comes from the system or global configuration,
	// which is not in the repository. Everything else lives in the repository.
	Machine bool
}

// ID is a stable name for the program as it is configured: it changes when the
// setting, its value, the file it is in, or a hook's contents change.
func (p Program) ID() string {
	h := sha256.New()
	for _, part := range []string{p.Kind, p.Name, p.Value, p.Sum, p.Where, p.Submodule} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Display limits for a line of a warning. A value is whatever the repository's
// configuration says, so it may be long, and it may hold anything.
const (
	maxValueRunes = 160
	maxWhereRunes = 200
)

// Line is the program as one plain fragment for a warning:
// "core.sshCommand = ssh -i k (in /repo/.git/config)". What it is built from is
// cut to a length and stripped of line breaks, other control characters and
// the marks that reorder text, so that a value cannot pose as another item or
// push the rest of the warning out of view.
func (p Program) Line() string {
	var s string
	switch p.Kind {
	case "hook":
		s = "hook " + clean(p.Name, 60) + " (in " + clean(p.Where, maxWhereRunes) + ")"
	case "unreadable":
		s = "something Flockdeck could not read, which git may still run: " + clean(p.Where, maxWhereRunes) +
			" (" + clean(p.Value, maxValueRunes) + ")"
	case "unscannable":
		s = "Flockdeck could not finish reading this repository, so git may run something it did not see: " +
			clean(p.Value, maxValueRunes) + " (" + clean(p.Where, maxWhereRunes) + ")"
	case "limit":
		s = "more than Flockdeck looks through: " + clean(p.Value, maxValueRunes) + " (in " + clean(p.Where, maxWhereRunes) + ")"
	default:
		s = clean(p.Name, 80) + " = " + clean(p.Value, maxValueRunes) + " (in " + clean(p.Where, maxWhereRunes) + ")"
	}
	if p.Submodule != "" {
		s += ", submodule " + clean(p.Submodule, 80)
	}
	return s
}

// clean drops control characters (a line break included) and the characters
// that change the direction text is drawn in, and cuts s to max characters.
func clean(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u200e' || r == '\u200f' || r == '\u061c' ||
			(r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			continue
		}
		if n == max {
			b.WriteString("\u2026")
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// Report is what a repository would run.
type Report struct {
	// Repo identifies the repository: its common git directory, shared by
	// every linked worktree.
	Repo  string
	Items []Program
}

// IDs are the programs' IDs, sorted.
func (r Report) IDs() []string {
	ids := make([]string, 0, len(r.Items))
	for _, p := range r.Items {
		ids = append(ids, p.ID())
	}
	sort.Strings(ids)
	return ids
}

// AcceptableIDs are the IDs of what can be remembered as accepted. A scan that
// could not be finished cannot: what it did not see may be different next time,
// so it is asked about every time.
func (r Report) AcceptableIDs() []string {
	ids := make([]string, 0, len(r.Items))
	for _, p := range r.Items {
		if p.Kind != "unscannable" {
			ids = append(ids, p.ID())
		}
	}
	sort.Strings(ids)
	return ids
}

// risky matches the configuration keys that make git run a program during a
// commit, push, pull or fetch, as git prints them: section and name lower-case,
// the subsection as written.
//
//   - core.sshCommand, which a push, pull or fetch over ssh runs
//   - core.hooksPath, which moves where hooks are found
//   - core.askPass and credential.helper, run when a remote asks for a login
//   - core.gitProxy and core.alternateRefsCommand, run by the network commands
//   - remote.<name>.uploadpack, receivepack and vcs: a command run on the other
//     end, and a remote helper started in place of git's own transports
//   - a filter driver's clean, smudge or process, run by git add and checkout,
//     and Git LFS's extensions and custom transfer agents, which its own filter
//     then starts
//   - a merge driver (merge.<name>.recursive only names another driver), which .gitattributes can assign to any file and which a
//     merge runs
//   - gpg.program, gpg.<format>.program and gpg.ssh.defaultKeyCommand, run when
//     commits are signed
//   - hook.<name>.command, hooks configured in git's own configuration
//
// submodule.<name>.update and the protocol settings need their value looked at
// as well; see runsProgram.
//
// Left out on purpose: core.fsmonitor, which every git Flockdeck starts has
// blanked (see ownConfig); core.pager and core.editor, which a commit given its
// message on stdin and a push never start; diff drivers, which every diff
// Flockdeck runs is told not to use (--no-ext-diff, --no-textconv); merge.tool
// and mergetool.*, which only `git mergetool` runs and Flockdeck never does;
// and alias.*, which cannot replace the built-in commands Flockdeck runs.
var risky = regexp.MustCompile(`^(core\.(sshcommand|hookspath|askpass|gitproxy|alternaterefscommand)|credential\.(.+\.)?helper|remote\..+\.(uploadpack|receivepack|vcs)|filter\..+\.(clean|smudge|process)|merge\..+\.driver|lfs\.extension\..+\.(clean|smudge)|lfs\.customtransfer\..+\.path|gpg\.program|gpg\..+\.program|gpg\.ssh\.defaultkeycommand|hook\..+\.command)$`)

var (
	submoduleUpdate = regexp.MustCompile(`^submodule\..+\.update$`)
	helperURL       = regexp.MustCompile(`^[A-Za-z0-9+.-]+::`)
)

// lfsFilters are the three entries `git lfs install` writes, with the values it
// writes.
var lfsFilters = map[string]string{
	"filter.lfs.clean":   "git-lfs clean -- %f",
	"filter.lfs.smudge":  "git-lfs smudge -- %f",
	"filter.lfs.process": "git-lfs filter-process",
}

// StandardLFSFilter reports whether a configuration entry is one of the three
// that `git lfs install` writes, with exactly the value it writes. It is common
// enough in a user's own configuration that asking about it would make both the
// check on Flockdeck's own git commands and auto-review pointless for everyone
// who has Git LFS. Only these exact values are let through: the value is run by
// a shell, so anything added to it is a program of its own.
func StandardLFSFilter(key, value string) bool {
	want, ok := lfsFilters[key]
	return ok && value == want
}

// runsProgram reports whether a configuration entry names a program to run.
func runsProgram(key, value string) bool {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		// An empty value turns a setting off.
		return false
	case StandardLFSFilter(key, value):
		return false
	case risky.MatchString(key):
		return true
	case submoduleUpdate.MatchString(key):
		// "!command" is run in place of git's own update; "checkout", "rebase",
		// "merge" and "none" are not.
		return strings.HasPrefix(value, "!")
	case key == "protocol.ext.allow":
		return !strings.EqualFold(value, "never")
	case key == "protocol.allow":
		return strings.EqualFold(value, "always")
	}
	return false
}

// remoteLocation says where a remote URL, or the replacement in a url.<base>
// setting, takes a push or fetch to that is not an ordinary network address:
// "helper" for <helper>:: (ext:: runs a command), "local" for a path on this
// machine, where the other repository's own hooks run. "" is a network address.
func remoteLocation(u string) string {
	u = strings.TrimSpace(u)
	switch {
	case u == "":
		return ""
	case helperURL.MatchString(u):
		return "helper"
	case strings.HasPrefix(strings.ToLower(u), "file://"):
		return "local"
	case strings.Contains(u, "://"):
		return ""
	}
	// user@host:path and host:path are ssh; a colon after a single letter is a
	// Windows drive, and a colon after a slash is part of a path.
	if i := strings.Index(u, ":"); i > 1 && !strings.ContainsAny(u[:i], `/\`) {
		return ""
	}
	return "local"
}

// hookNames are the hooks git can run. A file in a hooks directory with any
// other name is never started. They are looked for by name, one at a time,
// because the file system decides what that name matches: Pre-Commit is
// pre-commit on Windows and macOS.
var hookNames = []string{
	"applypatch-msg", "commit-msg", "post-applypatch", "post-checkout",
	"post-commit", "post-index-change", "post-merge", "post-receive",
	"post-rewrite", "post-update", "pre-applypatch", "pre-auto-gc",
	"pre-commit", "pre-merge-commit", "pre-push", "pre-rebase",
	"pre-receive", "prepare-commit-msg", "proc-receive", "push-to-checkout",
	"reference-transaction", "sendemail-validate", "update",
}

// Limits, so that a repository built to be slow to inspect is not.
const (
	maxSubmodules = 200
	maxModuleDirs = 2000
	maxModuleDeep = 6
	// maxConfigBytes is the largest configuration file listed. A larger one is
	// an item of its own: git is slow to read one, and padding is how a setting
	// is hidden.
	maxConfigBytes = 4 << 20
	// maxHashBudget is how many bytes of hooks and submodule configuration a
	// scan reads in all.
	maxHashBudget = 64 << 20
)

// scanTimeout bounds a whole scan. A scan that cannot finish in this long is an
// item to accept, not a reason to go ahead: a repository can be made slow to
// read for the scan and not for the command that follows. It is a variable so a
// test can shorten it.
var scanTimeout = 15 * time.Second

// ErrNotRepository and ErrNoGit are the only ways ScanPrograms fails: the
// directory is not a git repository, or there is no git to run.
var (
	ErrNotRepository = errors.New("not a git repository")
	ErrNoGit         = errors.New("git is not installed")
)

// scanner is one run of ScanPrograms.
type scanner struct {
	netSubs []string // submodule git directories found on a share
	ctx     context.Context
	budget  int64
	spent   int64
}

// maxScanOutput is the most of one git command's output a scan keeps. A
// configuration that includes itself many times prints hundreds of megabytes.
const maxScanOutput = 8 << 20

var errTooMuchOutput = errors.New("git printed more than 8 MB")

type cappedBuf struct {
	bytes.Buffer
	over bool
}

// ReadFrom keeps io.Copy on Write: the one bytes.Buffer has would take all of it.
func (b *cappedBuf) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{b}, r)
}

func (b *cappedBuf) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxScanOutput {
		b.over = true
		return 0, errTooMuchOutput
	}
	return b.Buffer.Write(p)
}

func (s *scanner) git(dir string, args ...string) (string, error) {
	var out cappedBuf
	_, err := runTo(s.ctx, scanTimeout, dir, nil, &out, args...)
	if out.over {
		return "", errTooMuchOutput
	}
	if err != nil {
		return "", err
	}
	return out.String(), nil
}

// unscannable is the item for a scan that could not be finished.
func unscannable(where, why string, submodule string) Program {
	return Program{Kind: "unscannable", Where: where, Value: why, Submodule: submodule}
}

// ScanPrograms finds what git would run, in dir, for a commit, push, pull or
// fetch: the configuration git itself would read (system, global, repository,
// worktree, and whatever they include), the hooks it would find, where a remote
// is on this machine the hooks that remote would run, and the same for every
// submodule, whose own configuration and hooks git consults when it descends
// into one.
//
// An error is ErrNotRepository or ErrNoGit and nothing else. Anything that
// cannot be read, or that takes too long, comes back as an item.
func ScanPrograms(dir string) (Report, error) {
	if !Available() {
		return Report{}, ErrNoGit
	}
	if strings.TrimSpace(dir) == "" {
		return Report{}, ErrNotRepository
	}
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()
	s := &scanner{ctx: ctx, budget: maxHashBudget}

	paths, err := s.gitPaths(dir)
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return Report{}, ErrNotRepository
		}
		return Report{Repo: canonical(dir), Items: []Program{unscannable(dir, "git rev-parse failed: "+firstWords(err), "")}}, nil
	}
	for _, d := range []string{paths.gitDir, paths.common, paths.topdir} {
		if d != "" && isNetworkPath(d) {
			return Report{Repo: d, Items: []Program{unscannable(d, "the repository's git directory is on a network share, which Flockdeck does not touch", "")}}, nil
		}
	}
	rep := Report{Repo: paths.common}

	// A configuration file that is large is hiding something, or is slow to
	// read, or both.
	for _, f := range []string{filepath.Join(paths.common, "config"), filepath.Join(paths.gitDir, "config.worktree")} {
		if fi, err := statWithin(f); err == nil && fi.Size() > maxConfigBytes {
			rep.Items = append(rep.Items, Program{Kind: "limit", Where: f,
				Value: "a configuration file of " + strconv.FormatInt(fi.Size(), 10) + " bytes is larger than the " +
					strconv.Itoa(maxConfigBytes) + " Flockdeck reads"})
		}
	}

	entries, err := s.listConfig(dir, paths.topdirOr(), nil)
	if err != nil {
		rep.Items = append(rep.Items, unscannable(dir, "git config could not be read: "+firstWords(err), ""))
	} else {
		items, hooksPath := s.settingsIn(entries, "", paths.topdirOr(), true, false)
		rep.Items = append(rep.Items, items...)
		rep.Items = append(rep.Items, s.hooksIn(paths, hooksPath, "")...)
	}
	if err != nil {
		// Without its configuration the hooks directory is a guess at best, but
		// the default one is still worth listing.
		rep.Items = append(rep.Items, s.hooksIn(paths, nil, "")...)
	}

	subs, truncated, err := s.submoduleGitDirs(dir, paths)
	if err != nil {
		rep.Items = append(rep.Items, unscannable(dir, "the submodules could not be listed: "+firstWords(err), ""))
	}
	for _, sub := range subs {
		rep.Items = append(rep.Items, s.scanSubmodule(sub)...)
	}
	for _, n := range s.netSubs {
		rep.Items = append(rep.Items, unscannable(n, "a submodule's git directory on a network share, which Flockdeck does not touch", ""))
	}
	if truncated {
		rep.Items = append(rep.Items, Program{
			Kind: "limit", Value: "submodules beyond the first " + strconv.Itoa(maxSubmodules) + ", more than " +
				strconv.Itoa(maxModuleDirs) + " directories, or nesting more than " +
				strconv.Itoa(maxModuleDeep) + " deep were not looked at", Where: filepath.Join(paths.common, "modules"),
		})
	}
	seen := map[string]bool{}
	uniq := rep.Items[:0]
	for _, p := range rep.Items {
		if id := p.ID(); !seen[id] {
			seen[id] = true
			uniq = append(uniq, p)
		}
	}
	rep.Items = uniq
	sort.SliceStable(rep.Items, func(i, j int) bool { return rep.Items[i].ID() < rep.Items[j].ID() })
	return rep, nil
}

// firstWords is the start of an error's text.
func firstWords(err error) string {
	var te *timeoutError
	if errors.As(err, &te) || errors.Is(err, context.DeadlineExceeded) {
		return "git did not answer within " + scanTimeout.String()
	}
	s := strings.TrimSpace(err.Error())
	if line, _, ok := strings.Cut(s, "\n"); ok {
		s = line
	}
	return s
}

// settingsIn picks the entries that run a program, a local or helper remote
// among them, and returns the effective core.hooksPath, the last one set. For
// the main repository it also lists the hooks of a remote on this machine.
func (s *scanner) settingsIn(entries []configEntry, submodule, base string, remoteHooks, remoteSide bool) ([]Program, *configEntry) {
	var items []Program
	var hooksPath *configEntry
	names := map[string]bool{}
	rules := collectRewrites(entries)
	for _, e := range entries {
		if n, ok := remoteName(e.key); ok {
			names[n] = true
		}
	}
	for i := range entries {
		e := entries[i]
		if e.key == "core.hookspath" && strings.TrimSpace(e.value) != "" {
			hooksPath = &entries[i]
		}
		item := Program{Kind: "setting", Name: e.key, Value: e.value, Where: e.origin, Submodule: submodule, Machine: e.machine}
		switch {
		case runsProgram(e.key, e.value) || (remoteSide && fsmonitorCommand(e.key, e.value)):
			items = append(items, item)
		case isRemoteURL(e.key):
			items = append(items, s.remote(item, remoteHooks, base, rules)...)
		case isRemoteRef(e.key):
			// A branch's remote, or the default one, is usually the name of a
			// remote, which is looked at under its own settings. It can also be
			// a URL or a folder, and git then goes there.
			v := strings.TrimSpace(e.value)
			if v != "" && v != "." && !names[v] {
				items = append(items, s.remote(item, remoteHooks, base, rules)...)
			}
		case isURLRewrite(e.key):
			if remoteLocation(rewriteBase(e.key)) != "" {
				items = append(items, item)
			}
		}
	}
	return items, hooksPath
}

// rewriteRule is one url.<base>.insteadOf or pushInsteadOf setting: a URL that
// starts with prefix is taken to start with base instead.
type rewriteRule struct{ prefix, base string }

// rewriteRules are a configuration's insteadOf and pushInsteadOf rules.
type rewriteRules struct{ fetch, push []rewriteRule }

func collectRewrites(entries []configEntry) rewriteRules {
	var r rewriteRules
	for _, e := range entries {
		if !isURLRewrite(e.key) || e.value == "" {
			continue
		}
		rule := rewriteRule{prefix: e.value, base: rewriteBase(e.key)}
		if strings.HasSuffix(e.key, ".pushinsteadof") {
			r.push = append(r.push, rule)
		} else {
			r.fetch = append(r.fetch, rule)
		}
	}
	return r
}

// apply rewrites u by the longest rule that matches, as git does.
func apply(u string, rules []rewriteRule) string {
	best := -1
	for i, r := range rules {
		if strings.HasPrefix(u, r.prefix) && (best < 0 || len(r.prefix) > len(rules[best].prefix)) {
			best = i
		}
	}
	if best < 0 {
		return u
	}
	return rules[best].base + u[len(rules[best].prefix):]
}

// fsmonitorCommand reports whether core.fsmonitor names a program. For a
// remote it is one that git runs there, with none of Flockdeck's own settings
// to blank it; for the repository itself it is blanked and not asked about.
func fsmonitorCommand(key, value string) bool {
	if key != "core.fsmonitor" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "true", "false", "yes", "no", "on", "off", "1", "0":
		return false
	}
	return true
}

// remote lists a remote setting that goes to a helper or a folder on this
// machine, and where it is a folder, the hooks the repository there would run.
// A rewrite can turn an ordinary address into a folder, so the address is looked
// at as it stands and as each kind of rewrite would leave it.
func (s *scanner) remote(item Program, hooks bool, base string, rules rewriteRules) []Program {
	cands := []string{item.Value}
	for _, set := range [][]rewriteRule{rules.fetch, rules.push} {
		if r := apply(item.Value, set); r != item.Value {
			cands = append(cands, r)
		}
	}
	var items []Program
	listed := false
	for _, cand := range cands {
		where := remoteLocation(cand)
		if where == "" {
			continue
		}
		if !listed {
			items = append(items, item)
			listed = true
		}
		if where == "local" && hooks {
			items = append(items, s.remoteHooks(cand, base, item.Submodule, item.Machine)...)
		}
	}
	return items
}

// remoteName is the remote a remote.<name>.<key> setting is of.
func remoteName(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "remote.")
	if !ok {
		return "", false
	}
	i := strings.LastIndex(rest, ".")
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
}

func isRemoteURL(key string) bool {
	return strings.HasPrefix(key, "remote.") && (strings.HasSuffix(key, ".url") || strings.HasSuffix(key, ".pushurl"))
}

// isRemoteRef is a setting that names the remote a push or fetch goes to.
func isRemoteRef(key string) bool {
	return key == "remote.pushdefault" ||
		(strings.HasPrefix(key, "branch.") && (strings.HasSuffix(key, ".remote") || strings.HasSuffix(key, ".pushremote")))
}

func isURLRewrite(key string) bool {
	return strings.HasPrefix(key, "url.") && (strings.HasSuffix(key, ".insteadof") || strings.HasSuffix(key, ".pushinsteadof"))
}

// rewriteBase is the replacement a url.<base>.insteadOf setting names.
func rewriteBase(key string) string {
	key = strings.TrimPrefix(key, "url.")
	key = strings.TrimSuffix(strings.TrimSuffix(key, ".pushinsteadof"), ".insteadof")
	return key
}

// statTimeout bounds one look at the file system for something a repository
// names: a remote's folder, a hooks directory, a submodule's git directory. Any
// of them can be on a share or a mount that does not answer.
var statTimeout = 2 * time.Second

var errStatTimeout = errors.New("no answer from the file system")

// boxed runs f and gives up on it after statTimeout. The call is left to finish
// on its own; what it returns then is dropped.
func boxed[T any](f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(statTimeout):
		var zero T
		return zero, errStatTimeout
	}
}

func statWithin(p string) (fs.FileInfo, error) {
	return boxed(func() (fs.FileInfo, error) { return os.Stat(p) })
}

func lstatWithin(p string) (fs.FileInfo, error) {
	return boxed(func() (fs.FileInfo, error) { return os.Lstat(p) })
}

func readFileWithin(p string) ([]byte, error) {
	return boxed(func() ([]byte, error) { return os.ReadFile(p) })
}

// isNetworkPath reports whether p is on another machine's share. Looking at one
// makes Windows connect to it and offer the user's credentials, so such a path
// is never touched: this is decided from the spelling of the path, before
// anything is opened, and again on whatever a link or a canonical name turns it
// into.
func isNetworkPath(p string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	p = filepath.FromSlash(p)
	if !strings.HasPrefix(p, `\\`) {
		return false
	}
	// \\?\C:\x is a local path spelled the long way.
	return !(strings.HasPrefix(p, `\\?\`) && len(p) >= 6 && p[5] == ':')
}

// readLink and evalSymlinks are variables so a test can stand in for a link to a
// share (making one needs a privilege, and connects to the share) and see that
// a share is never resolved.
var (
	readLink     = os.Readlink
	evalSymlinks = filepath.EvalSymlinks
)

// linksToNetwork follows the links on the way to p one at a time, reading each
// link and not the place it leads to, and returns the first target that is on a
// share ("" if none is). filepath.EvalSymlinks would open the target, which is
// the connection to the share this is here to avoid. Only Windows has the
// problem.
func linksToNetwork(p string, depth int) string {
	if runtime.GOOS != "windows" || depth > 8 {
		return ""
	}
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p[len(vol):], string(filepath.Separator))
	cur := vol + string(filepath.Separator)
	for _, comp := range strings.Split(rest, string(filepath.Separator)) {
		if comp == "" {
			continue
		}
		cur = filepath.Join(cur, comp)
		fi, err := lstatWithin(cur)
		if err != nil {
			return ""
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
			continue
		}
		t, err := readLink(cur)
		if err != nil {
			continue
		}
		if isNetworkPath(t) {
			return t
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(cur), t)
		}
		if n := linksToNetwork(filepath.Clean(t), depth+1); n != "" {
			return n
		}
	}
	return ""
}

// fileURLPath is the folder a remote URL names. A file:// URL is decoded as git
// decodes it: %XX is a byte, a host of localhost is this machine, and any other
// host is another one (ok is false). A drive letter written as the host
// (file://c:/x) is a host as far as the URL goes, so it is reported as another
// machine too: that is asked about every time, which is intended and safe. A
// plain path is itself.
func fileURLPath(u string) (string, bool) {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(strings.ToLower(u), "file://") {
		return filepath.FromSlash(u), true
	}
	rest := u[len("file://"):]
	if !strings.HasPrefix(rest, "/") {
		host, path, _ := strings.Cut(rest, "/")
		if host != "" && !strings.EqualFold(host, "localhost") {
			return "", false
		}
		rest = "/" + path
	}
	dec, err := url.PathUnescape(rest)
	if err != nil {
		return "", false
	}
	// file:///C:/x is /C:/x.
	if len(dec) > 2 && dec[0] == '/' && dec[2] == ':' {
		dec = dec[1:]
	}
	return filepath.FromSlash(dec), true
}

// remoteHooks lists what a push into the repository a local remote URL names
// would run on that side: its hooks, wherever its own core.hooksPath puts them,
// and the settings in its own configuration that run a program (an fsmonitor
// command or a filter, which a push that updates its checked-out branch runs).
// A folder that is not there is a push that fails and nothing runs. A folder
// that is there and cannot be worked out, a file share, or a file URL for
// another host, is an item: it is not assumed to be harmless.
//
// A remote with no core.hooksPath of its own runs the hooks the user's own
// global core.hooksPath names, if there is one, because that is the
// configuration its git reads. That is the user's setting and is listed with
// the others when it is set; it is not read again here.
func (s *scanner) remoteHooks(u, base, submodule string, machine bool) []Program {
	p, ok := fileURLPath(u)
	if !ok {
		return []Program{unscannable(u, "a file URL for another machine, or one that cannot be decoded", submodule)}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	netItem := func(where string) []Program {
		return []Program{unscannable(where, "a network share, which Flockdeck does not touch", submodule)}
	}
	if isNetworkPath(p) {
		return netItem(p)
	}
	// Decided on the spelling above, and again on where links lead.
	p = canonical(p)
	if isNetworkPath(p) {
		return netItem(p)
	}
	if _, err := statWithin(p); err != nil {
		if absent(err) {
			return nil
		}
		return []Program{unscannable(p, "the remote's folder could not be looked at: "+errText(err), submodule)}
	}
	gd := ""
	for _, d := range []string{p, filepath.Join(p, ".git")} {
		if _, err := statWithin(filepath.Join(d, "HEAD")); err == nil {
			gd = d
			break
		}
	}
	if gd == "" {
		if g, ok := resolveGitDir(p); ok {
			gd = g
		}
	}
	if gd == "" {
		return []Program{unscannable(p, "a folder Flockdeck cannot tell is not a repository", submodule)}
	}
	if isNetworkPath(gd) {
		return netItem(gd)
	}
	// A linked worktree's hooks and configuration are the main repository's.
	if data, err := readFileWithin(filepath.Join(gd, "commondir")); err == nil && len(data) < 4096 {
		c := filepath.FromSlash(strings.TrimSpace(string(data)))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gd, c)
		}
		if isNetworkPath(c) {
			return netItem(c)
		}
		gd = canonical(c)
		if isNetworkPath(gd) {
			return netItem(gd)
		}
	}
	var items []Program
	dirPath := filepath.Join(gd, "hooks")
	cfg := filepath.Join(gd, "config")
	if fi, err := statWithin(cfg); err == nil {
		if fi.Size() > maxConfigBytes {
			return []Program{unscannable(cfg, "the remote's configuration is too large to read", submodule)}
		}
		entries, err := s.listConfig(gd, gd, &cfg)
		if err != nil {
			return []Program{unscannable(cfg, "the remote's configuration could not be read: "+firstWords(err), submodule)}
		}
		found, hooksPath := s.settingsIn(entries, submodule, gd, false, true)
		items = append(items, found...)
		if hooksPath != nil {
			exp, ok := expandPath(hooksPath.value)
			if !ok {
				return append(items, unscannable(cfg, "the remote's core.hooksPath cannot be worked out: "+hooksPath.value, submodule))
			}
			if !filepath.IsAbs(exp) {
				exp = filepath.Join(gd, filepath.FromSlash(exp))
			}
			if isNetworkPath(exp) {
				return append(items, netItem(exp)...)
			}
			dirPath = canonical(exp)
		}
	} else if !absent(err) {
		return []Program{unscannable(cfg, "the remote's configuration could not be looked at: "+errText(err), submodule)}
	}
	if isNetworkPath(dirPath) {
		return append(items, unscannable(dirPath, "the remote's hooks are on a network share, which Flockdeck does not touch", submodule))
	}
	return append(items, s.hooksInDir(dirPath, submodule, machine)...)
}

// isExitOne reports whether git said "no such key", which is exit status 1
// with nothing printed.
func isExitOne(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 1
}

// gitLocations are the directories of a repository that matter here.
type gitLocations struct {
	gitDir string // this worktree's git directory
	common string // shared by all worktrees; where hooks and modules live
	topdir string // the working tree; empty for a bare repository
}

// gitPaths asks git where things are. A linked worktree's .git is a file
// pointing at its directory under the main repository's .git/worktrees, so
// the paths are never built from dir by hand.
func (s *scanner) gitPaths(dir string) (gitLocations, error) {
	out, err := s.git(dir, "rev-parse", "--absolute-git-dir", "--git-common-dir", "--show-toplevel")
	var loc gitLocations
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
	if err != nil {
		// --show-toplevel fails in a bare repository; the other two do not.
		out2, err2 := s.git(dir, "rev-parse", "--absolute-git-dir", "--git-common-dir")
		if err2 != nil {
			return loc, err
		}
		lines = strings.Split(strings.TrimRight(strings.ReplaceAll(out2, "\r\n", "\n"), "\n"), "\n")
		if len(lines) < 2 {
			return loc, errors.New("git rev-parse gave an unexpected answer")
		}
	} else if len(lines) < 3 {
		return loc, errors.New("git rev-parse gave an unexpected answer")
	}
	loc.gitDir = canonical(filepath.FromSlash(lines[0]))
	loc.common = filepath.FromSlash(lines[1])
	if !filepath.IsAbs(loc.common) {
		loc.common = filepath.Join(dir, loc.common)
	}
	loc.common = canonical(loc.common)
	if len(lines) >= 3 {
		loc.topdir = canonical(filepath.FromSlash(lines[2]))
	}
	return loc, nil
}

// topdirOr is the working tree, or the git directory of a bare repository.
func (l gitLocations) topdirOr() string {
	if l.topdir != "" {
		return l.topdir
	}
	return l.gitDir
}

// configEntry is one line of git config --list.
type configEntry struct {
	key, value string
	origin     string // the file, as a path
	machine    bool   // system or global
}

// listConfig reads configuration through git, so that includes, conditional
// includes and per-worktree configuration are resolved as git would resolve
// them. With a file, it reads that file alone (a submodule's); entries given
// on a command line or in the environment are not the repository's and are
// skipped.
func (s *scanner) listConfig(dir, base string, file *string) ([]configEntry, error) {
	args := []string{"config", "--null", "--includes", "--list", "--show-origin", "--show-scope"}
	if file != nil {
		args = []string{"config", "--null", "--includes", "--file", *file, "--list", "--show-origin", "--show-scope"}
	}
	out, err := s.git(dir, args...)
	if err != nil {
		return nil, err
	}
	entries := parseConfig(out, file != nil)
	for i := range entries {
		// git prints a path inside the working tree relative to its top.
		if !filepath.IsAbs(entries[i].origin) {
			entries[i].origin = filepath.Join(base, filepath.FromSlash(entries[i].origin))
		}
	}
	return entries, nil
}

// parseConfig reads the output of git config --null --list --show-origin
// --show-scope: scope NUL origin NUL key NL value NUL. A key with no value
// ("[core] bare") has no NL.
func parseConfig(out string, fromFile bool) []configEntry {
	fields := strings.Split(out, "\x00")
	var entries []configEntry
	for i := 0; i+2 < len(fields); i += 3 {
		scope, origin, kv := fields[i], fields[i+1], fields[i+2]
		scope = strings.TrimLeft(scope, "\n")
		if scope == "command" && !fromFile {
			continue
		}
		file, ok := strings.CutPrefix(origin, "file:")
		if !ok {
			continue
		}
		key, value, _ := strings.Cut(kv, "\n")
		entries = append(entries, configEntry{
			key:     key,
			value:   value,
			origin:  file,
			machine: scope == "system" || scope == "global",
		})
	}
	return entries
}

// unreadable is the item for something that may run and could not be read.
func unreadable(path string, err error, submodule string) Program {
	return Program{Kind: "unreadable", Where: path, Value: errText(err), Submodule: submodule}
}

func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}

// absent reports whether err only says there is nothing there.
func absent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// hooksIn lists the hooks git would run from a repository: those in its hooks
// directory, or in core.hooksPath when that is set. A hooks path that is
// relative is relative to the working tree, where git runs hooks from.
//
// A hooks directory inside the working tree is hashed like any other. Its
// files are the project's, and a Pull can bring a changed one, which is not
// visible in the list of changes at the time of a Push, Pull or Fetch. Scripts
// that a hook goes on to call from elsewhere are not read.
func (s *scanner) hooksIn(loc gitLocations, hooksPath *configEntry, submodule string) []Program {
	dirPath := filepath.Join(loc.common, "hooks")
	machine := false
	if hooksPath != nil {
		exp, ok := expandPath(hooksPath.value)
		if !ok {
			return []Program{unscannable(hooksPath.origin, "core.hooksPath cannot be worked out: "+hooksPath.value, submodule)}
		}
		dirPath = exp
		if !filepath.IsAbs(dirPath) {
			dirPath = filepath.Join(loc.topdirOr(), filepath.FromSlash(dirPath))
		}
		if isNetworkPath(dirPath) {
			return []Program{unscannable(hooksPath.origin, "core.hooksPath is on a network share, which Flockdeck does not touch: "+hooksPath.value, submodule)}
		}
		dirPath = canonical(dirPath)
		// A hooks path in the user's own configuration that lands inside the
		// working tree (".githooks") is the project's, not the machine's.
		machine = hooksPath.machine && !(loc.topdir != "" && within(loc.topdir, dirPath))
		if isNetworkPath(dirPath) {
			return []Program{unscannable(hooksPath.origin, "core.hooksPath is on a network share, which Flockdeck does not touch: "+hooksPath.value, submodule)}
		}
	}
	return s.hooksInDir(dirPath, submodule, machine)
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hooksInDir looks for each hook git knows by its name. That finds a file the
// directory cannot be listed to show (a directory that can be searched but not
// read), matches names as the file system does, and cannot be fooled by a name
// that only looks like a hook's. On Windows git also starts <hook>.exe where
// there is no <hook>.
func (s *scanner) hooksInDir(dirPath, submodule string, machine bool) []Program {
	var items []Program
	for _, name := range hookNames {
		candidates := []string{name}
		if runtime.GOOS == "windows" {
			candidates = append(candidates, name+".exe")
		}
		for _, cand := range candidates {
			full := filepath.Join(dirPath, cand)
			fi, err := lstatWithin(full)
			if err == nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				// Git follows a link, so where it leads decides whether it is looked at.
				if isNetworkPath(canonical(full)) {
					items = append(items, unscannable(full, "a hook that links to a network share, which Flockdeck does not touch", submodule))
					continue
				}
				fi, err = statWithin(full)
			}
			if err != nil {
				if absent(err) {
					continue
				}
				// Git may be able to run what this cannot see.
				return append(items, unreadable(dirPath, err, submodule))
			}
			if !fi.Mode().IsRegular() || !executable(fi) {
				continue
			}
			sum, err := s.hashFile(full, fi.Size())
			switch {
			case err == errOverBudget:
				items = append(items, unscannable(full, "a hook too large to read within the limit of "+
					strconv.Itoa(maxHashBudget>>20)+" MB for a scan", submodule))
				continue
			case err != nil:
				// A hook that can be run and not read still runs.
				sum = "unreadable: " + errText(err)
			}
			items = append(items, Program{Kind: "hook", Name: cand, Sum: sum, Where: dirPath, Submodule: submodule, Machine: machine})
		}
	}
	return items
}

// executable reports whether git would start the file as a hook. Windows has
// no execute bit; git there starts any file with a hook's name.
func executable(fi fs.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

var errOverBudget = errors.New("over the budget for reading files")

// hashFile hashes a whole file, streamed, within what is left of the budget for
// the scan. The size is checked first and again as it is read, so a file that
// grows is not read without end.
func (s *scanner) hashFile(path string, size int64) (string, error) {
	if size > s.budget-s.spent {
		return "", errOverBudget
	}
	type result struct {
		sum string
		n   int64
		err error
	}
	ch := make(chan result, 1)
	limit := s.budget - s.spent + 1
	go func() {
		f, err := os.Open(path)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, limit))
		ch <- result{hex.EncodeToString(h.Sum(nil))[:16], n, err}
	}()
	select {
	case <-s.ctx.Done():
		return "", s.ctx.Err()
	case r := <-ch:
		s.spent += r.n
		if r.err != nil {
			return "", r.err
		}
		if s.spent > s.budget {
			return "", errOverBudget
		}
		return r.sum, nil
	}
}

// expandPath turns a leading ~/ into the home directory, as git does for a
// path-valued setting. ~user/ and %(prefix)/ are expanded by git in ways this
// does not follow, so they are not worked out (ok is false).
func expandPath(p string) (string, bool) {
	if strings.HasPrefix(p, "%(") {
		return "", false
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(home, p[1:]), true
	}
	if strings.HasPrefix(p, "~") {
		return "", false
	}
	return p, true
}

// submodule is a submodule's git directory and where it is checked out.
type submodule struct {
	gitDir string
	path   string // relative to the working tree, "" when only found under modules
	work   string // its working tree, "" when only found under modules
}

// finder collects submodule git directories up to the limits.
type finder struct {
	network   []string
	seen      map[string]bool
	subs      []submodule
	visits    int
	truncated bool
}

func (f *finder) add(gd, rel, work string) {
	gd = canonical(gd)
	if isNetworkPath(gd) {
		f.network = append(f.network, gd)
		return
	}
	key := gd
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if f.seen[key] {
		return
	}
	if len(f.subs) >= maxSubmodules {
		f.truncated = true
		return
	}
	f.seen[key] = true
	f.subs = append(f.subs, submodule{gitDir: gd, path: rel, work: work})
}

// submoduleGitDirs finds the git directories git would descend into: every
// submodule the index records, wherever its .git file points (a repository
// can say "gitdir: ../x" for a submodule, and git then reads x's
// configuration), and every directory under .git/modules. truncated is set when
// a limit was hit, which the caller must not ignore.
func (s *scanner) submoduleGitDirs(dir string, loc gitLocations) (subs []submodule, truncated bool, err error) {
	f := &finder{seen: map[string]bool{}}
	if loc.topdir != "" {
		out, err := s.git(dir, "ls-files", "--stage", "-z")
		if err != nil {
			return nil, false, err
		}
		for _, rec := range strings.Split(out, "\x00") {
			meta, path, ok := strings.Cut(rec, "\t")
			if !ok || !strings.HasPrefix(meta, "160000 ") {
				continue
			}
			work := filepath.Join(loc.topdir, filepath.FromSlash(path))
			if gd, ok := resolveGitDir(work); ok {
				f.add(gd, path, work)
			}
		}
	}
	for _, base := range []string{filepath.Join(loc.gitDir, "modules"), filepath.Join(loc.common, "modules")} {
		f.walkModules(base, 0)
	}
	s.netSubs = f.network
	return f.subs, f.truncated, nil
}

// walkModules finds the git directories under a modules directory: each is a
// directory holding a HEAD file, and may have a modules directory of its own.
// A directory is read a few entries at a time and given up on at the limit, so
// one with millions of entries is not read whole. How many directories are
// visited, and how deep, is bounded; going past either is recorded and not
// skipped silently.
func (f *finder) walkModules(base string, depth int) {
	d, err := boxed(func() (*os.File, error) { return os.Open(base) })
	if err != nil {
		if errors.Is(err, errStatTimeout) {
			f.truncated = true
		}
		return
	}
	defer d.Close()
	if depth > maxModuleDeep {
		f.truncated = true
		return
	}
	for {
		entries, err := boxed(func() ([]os.DirEntry, error) { return d.ReadDir(128) })
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if f.visits++; f.visits > maxModuleDirs {
				f.truncated = true
				return
			}
			gd := filepath.Join(base, e.Name())
			if c := canonical(gd); isNetworkPath(c) {
				f.network = append(f.network, c)
				continue
			}
			if _, err := statWithin(filepath.Join(gd, "HEAD")); err == nil {
				f.add(gd, "", "")
				f.walkModules(filepath.Join(gd, "modules"), depth+1)
			} else {
				// A submodule named with a slash lives in a nested directory.
				f.walkModules(gd, depth+1)
			}
			if f.truncated && f.visits > maxModuleDirs {
				return
			}
		}
		if err != nil {
			if errors.Is(err, errStatTimeout) {
				f.truncated = true
			}
			return
		}
	}
}

// resolveGitDir is the git directory of a checked-out repository: its .git
// directory, or where a .git file says it is.
func resolveGitDir(work string) (string, bool) {
	dot := filepath.Join(work, ".git")
	fi, err := lstatWithin(dot)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dot, true
	}
	if fi.Size() > 4096 {
		return "", false
	}
	data, err := readFileWithin(dot)
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	target, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return "", false
	}
	target = filepath.FromSlash(strings.TrimSpace(target))
	if !filepath.IsAbs(target) {
		target = filepath.Join(work, target)
	}
	return filepath.Clean(target), true
}

// scanSubmodule is ScanPrograms for a submodule's own git directory. A
// hooksPath that is relative is relative to the submodule's working tree. Its
// configuration is read by git every time, and not remembered between scans:
// anything remembered by what was read could be a different file by the time
// git reads it.
func (s *scanner) scanSubmodule(sub submodule) []Program {
	name := sub.path
	if name == "" {
		name = sub.gitDir
	}
	cfg := filepath.Join(sub.gitDir, "config")
	var items []Program
	var hooksPath *configEntry
	fi, err := statWithin(cfg)
	switch {
	case err == nil && fi.Size() > maxConfigBytes:
		items = append(items, Program{Kind: "limit", Where: cfg, Submodule: name,
			Value: "a configuration file of " + strconv.FormatInt(fi.Size(), 10) + " bytes is larger than the " +
				strconv.Itoa(maxConfigBytes) + " Flockdeck reads"})
	case err == nil && fi.Size() > s.budget-s.spent:
		items = append(items, unscannable(cfg, "the submodules' configuration is more than a scan reads in all", name))
	case err == nil:
		s.spent += fi.Size()
		entries, err := s.listConfig(sub.gitDir, sub.gitDir, &cfg)
		if err != nil {
			items = append(items, unscannable(cfg, "git could not read it: "+firstWords(err), name))
		} else {
			var found []Program
			found, hooksPath = s.settingsIn(entries, name, sub.gitDir, false, false)
			items = append(items, found...)
		}
	case !absent(err):
		items = append(items, unreadable(cfg, err, name))
	}
	loc := gitLocations{gitDir: sub.gitDir, common: sub.gitDir, topdir: sub.work}
	return append(items, s.hooksIn(loc, hooksPath, name)...)
}

// Known is what somebody last accepted for a repository.
type Known struct {
	// IDs are the Program IDs they were shown, sorted.
	IDs []string
}

// Verdict is whether to stop and tell the user before running git.
type Verdict struct {
	// Warn is set when the user should be told before git runs.
	Warn bool
	// First is set when nothing has been accepted for the repository before.
	First bool
	// New are the programs not in what was accepted: all of the repository's
	// own on first sight.
	New []Program
}

// Judge compares a scan with what the user last accepted. known is nil when
// nothing has been recorded for the repository.
//
//   - Nothing recorded and nothing in the repository's own files runs a
//     program: no warning, and the caller records the scan. What the user's
//     own system and global git configuration name (a credential helper, Git
//     LFS) is not what an agent put there, and warning about it for every
//     repository would teach people to click through.
//   - Nothing recorded and the repository itself names a program: warn once.
//     A husky hook and a planted one look the same from here, and there is no
//     earlier state to compare with.
//   - Recorded: warn when something runs that was not there when it was
//     accepted, whether new, edited or moved. A program that has gone is not a
//     risk and is not asked about.
//   - A scan that could not be finished is asked about every time, whatever
//     was accepted.
func Judge(r Report, known *Known) Verdict {
	v := judge(r, known)
	sort.SliceStable(v.New, func(i, j int) bool {
		a, b := v.New[i], v.New[j]
		if a.Machine != b.Machine {
			return !a.Machine
		}
		if a.Submodule != b.Submodule {
			return a.Submodule < b.Submodule
		}
		if a.Kind != b.Kind {
			return a.Kind > b.Kind
		}
		return a.Name < b.Name
	})
	return v
}

func judge(r Report, known *Known) Verdict {
	if known == nil {
		var own []Program
		for _, p := range r.Items {
			if !p.Machine {
				own = append(own, p)
			}
		}
		if len(own) == 0 {
			return Verdict{First: true}
		}
		// Only what the repository names is listed: the user's own system and
		// global configuration is the same in every repository.
		return Verdict{Warn: true, First: true, New: own}
	}
	had := make(map[string]bool, len(known.IDs))
	for _, id := range known.IDs {
		had[id] = true
	}
	var added []Program
	for _, p := range r.Items {
		if !had[p.ID()] || p.Kind == "unscannable" {
			added = append(added, p)
		}
	}
	return Verdict{Warn: len(added) > 0, New: added}
}

// maxListed is how many programs a warning spells out.
const maxListed = 20

// Lines are the programs of a verdict as plain lines, at most maxListed, the
// last saying how many more there are.
func (v Verdict) Lines() []string {
	var lines []string
	for i, p := range v.New {
		if i == maxListed {
			lines = append(lines, "and "+strconv.Itoa(len(v.New)-maxListed)+" more")
			break
		}
		lines = append(lines, p.Line())
	}
	return lines
}

// Intro is what a warning says before it lists anything, and the advice that
// goes with it, so that neither can be pushed out of view by the list.
func (v Verdict) Intro(action string) string {
	var b strings.Builder
	if v.First {
		b.WriteString("This repository's git configuration or hooks make git run a program, and Flockdeck has not asked you about them before. ")
	} else {
		b.WriteString("This repository's git configuration or hooks were changed to run a program since you last accepted them. ")
	}
	b.WriteString("Flockdeck is about to run git " + action + ". If you or your tools set these up, continue. " +
		"If you did not, an agent may have written them: look at the files named before you go on. It would run:")
	return b.String()
}

// Warning is the whole warning as plain text.
func (v Verdict) Warning(action string) string {
	text := v.Intro(action)
	for _, l := range v.Lines() {
		text += "\n- " + l
	}
	return text
}

// canonical is a path with symbolic links and, on Windows, short 8.3 names
// resolved, so the same directory is one name however it was reached: a
// worktree under /var on macOS is under /private/var as far as git is told, and
// a temporary folder on Windows has a long name and a short one. A path that
// cannot be resolved is cleaned and used as it is.
func canonical(p string) string {
	p = filepath.Clean(p)
	// Whether a path is on a share is decided from its spelling, and from where
	// its links lead, before anything is opened: EvalSymlinks on Windows opens
	// the target, which connects to the share and offers the user's credentials.
	// A path that is, or leads to, a share comes back as the share's path and is
	// not resolved.
	if isNetworkPath(p) {
		return p
	}
	if t := linksToNetwork(p, 0); t != "" {
		return filepath.Clean(t)
	}
	if real, err := evalSymlinks(p); err == nil {
		return real
	}
	return p
}
