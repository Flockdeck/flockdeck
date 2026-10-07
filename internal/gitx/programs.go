package gitx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
// A scan never turns something it cannot read into "nothing runs": a hooks
// directory or file git may still be able to run, a submodule whose
// configuration cannot be read, or a search that hit its limit becomes an item
// of its own, so that it is shown and has to be accepted. Only a git that
// cannot be run at all, or a directory that is not a repository, is an error.

// Program is one thing git would run for a commit, push, pull or fetch, and
// where it is set.
type Program struct {
	// Kind is "setting" for a configuration key, "hook" for an executable file
	// in a hooks directory, "unreadable" for something that may run and could
	// not be read, and "limit" for a search that stopped at its limit.
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
//   - gpg.program, gpg.<format>.program and gpg.ssh.defaultKeyCommand, run when
//     commits are signed
//   - hook.<name>.command, hooks configured in git's own configuration
//
// submodule.<name>.update and the protocol settings need their value looked at
// as well; see runsProgram.
//
// Left out on purpose: core.fsmonitor, which every git Flockdeck starts has
// blanked (see ownConfig); core.pager and core.editor, which a commit given its
// message on stdin and a push never start; diff drivers, which Flockdeck's own
// diffs are told not to use (--no-ext-diff, --no-textconv); and alias.*, which
// cannot replace the built-in commands Flockdeck runs.
var risky = regexp.MustCompile(`^(core\.(sshcommand|hookspath|askpass|gitproxy|alternaterefscommand)|credential\.(.+\.)?helper|remote\..+\.(uploadpack|receivepack|vcs)|filter\..+\.(clean|smudge|process)|lfs\.extension\..+\.(clean|smudge)|lfs\.customtransfer\..+\.path|gpg\.program|gpg\..+\.program|gpg\.ssh\.defaultkeycommand|hook\..+\.command)$`)

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

// remoteHooksDir is the hooks directory of a local remote, or "".
func remoteHooksDir(u, base string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(strings.ToLower(u), "file://") {
		u = u[len("file://"):]
		// file:///C:/x is /C:/x.
		if len(u) > 2 && u[0] == '/' && u[2] == ':' {
			u = u[1:]
		}
	}
	p := filepath.FromSlash(u)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	p = canonical(p)
	for _, d := range []string{p, filepath.Join(p, ".git")} {
		if _, err := os.Stat(filepath.Join(d, "HEAD")); err == nil {
			return filepath.Join(d, "hooks")
		}
	}
	return ""
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
	// maxHashed is the largest hook read whole. A larger one is identified by
	// its size and modification time, which an edit changes.
	maxHashed = 16 << 20
)

// ScanPrograms finds what git would run, in dir, for a commit, push, pull or
// fetch: the configuration git itself would read (system, global, repository,
// worktree, and whatever they include), the hooks it would find, where a remote
// is on this machine the hooks that remote would run, and the same for every
// submodule, whose own configuration and hooks git consults when it descends
// into one.
//
// An error means git could not be run, or dir is not a repository, and says
// why. Callers must not take it for "nothing to run". Anything else that cannot
// be read comes back as an item.
func ScanPrograms(dir string) (Report, error) {
	paths, err := gitPaths(dir)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Repo: paths.common}

	entries, err := listConfig(dir, paths.topdirOr(), nil)
	if err != nil {
		return Report{}, err
	}
	items, hooksPath := settingsIn(entries, "", paths.topdirOr(), true)
	rep.Items = append(rep.Items, items...)
	rep.Items = append(rep.Items, hooksIn(paths, hooksPath, "")...)

	subs, truncated, err := submoduleGitDirs(dir, paths)
	if err != nil {
		return Report{}, err
	}
	for _, sub := range subs {
		rep.Items = append(rep.Items, scanSubmodule(sub)...)
	}
	if truncated {
		rep.Items = append(rep.Items, Program{
			Kind: "limit", Value: "submodules beyond the first " + strconv.Itoa(maxSubmodules) + ", or nested more than " +
				strconv.Itoa(maxModuleDeep) + " deep, were not looked at", Where: filepath.Join(paths.common, "modules"),
		})
	}
	sort.SliceStable(rep.Items, func(i, j int) bool { return rep.Items[i].ID() < rep.Items[j].ID() })
	return rep, nil
}

// settingsIn picks the entries that run a program, a local or helper remote
// among them, and returns the effective core.hooksPath, the last one set. For
// the main repository it also lists the hooks of a remote on this machine.
func settingsIn(entries []configEntry, submodule, base string, remoteHooks bool) ([]Program, *configEntry) {
	var items []Program
	var hooksPath *configEntry
	for i := range entries {
		e := entries[i]
		if e.key == "core.hookspath" && strings.TrimSpace(e.value) != "" {
			hooksPath = &entries[i]
		}
		item := Program{Kind: "setting", Name: e.key, Value: e.value, Where: e.origin, Submodule: submodule, Machine: e.machine}
		switch {
		case runsProgram(e.key, e.value):
			items = append(items, item)
		case isRemoteURL(e.key):
			where := remoteLocation(e.value)
			if where == "" {
				continue
			}
			items = append(items, item)
			if where == "local" && remoteHooks {
				if hd := remoteHooksDir(e.value, base); hd != "" {
					items = append(items, hooksInDir(hd, submodule, e.machine)...)
				}
			}
		case isURLRewrite(e.key):
			if remoteLocation(rewriteBase(e.key)) != "" {
				items = append(items, item)
			}
		}
	}
	return items, hooksPath
}

func isRemoteURL(key string) bool {
	return strings.HasPrefix(key, "remote.") && (strings.HasSuffix(key, ".url") || strings.HasSuffix(key, ".pushurl"))
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

// gitLocations are the directories of a repository that matter here.
type gitLocations struct {
	gitDir string // this worktree's git directory
	common string // shared by all worktrees; where hooks and modules live
	topdir string // the working tree; empty for a bare repository
}

// gitPaths asks git where things are. A linked worktree's .git is a file
// pointing at its directory under the main repository's .git/worktrees, so
// the paths are never built from dir by hand.
func gitPaths(dir string) (gitLocations, error) {
	out, err := run(dir, "rev-parse", "--absolute-git-dir", "--git-common-dir", "--show-toplevel")
	var loc gitLocations
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
	if err != nil {
		// --show-toplevel fails in a bare repository; the other two do not.
		out2, err2 := run(dir, "rev-parse", "--absolute-git-dir", "--git-common-dir")
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
func listConfig(dir, base string, file *string) ([]configEntry, error) {
	args := []string{"config", "--null", "--includes", "--list", "--show-origin", "--show-scope"}
	if file != nil {
		args = []string{"config", "--null", "--includes", "--file", *file, "--list", "--show-origin", "--show-scope"}
	}
	out, err := run(dir, args...)
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
func hooksIn(loc gitLocations, hooksPath *configEntry, submodule string) []Program {
	dirPath := filepath.Join(loc.common, "hooks")
	machine := false
	if hooksPath != nil {
		dirPath = expandHome(hooksPath.value)
		if !filepath.IsAbs(dirPath) {
			dirPath = filepath.Join(loc.topdirOr(), filepath.FromSlash(dirPath))
		}
		dirPath = canonical(dirPath)
		machine = hooksPath.machine
	}
	return hooksInDir(dirPath, submodule, machine)
}

// hooksInDir looks for each hook git knows by its name. That finds a file the
// directory cannot be listed to show (a directory that can be searched but not
// read), matches names as the file system does, and cannot be fooled by a name
// that only looks like a hook's.
func hooksInDir(dirPath, submodule string, machine bool) []Program {
	var items []Program
	for _, name := range hookNames {
		full := filepath.Join(dirPath, name)
		fi, err := os.Stat(full)
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
		sum, err := hashFile(full, fi)
		if err != nil {
			// A hook that can be run and not read still runs.
			sum = "unreadable: " + errText(err)
		}
		items = append(items, Program{Kind: "hook", Name: name, Sum: sum, Where: dirPath, Submodule: submodule, Machine: machine})
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

func hashFile(path string, fi fs.FileInfo) (string, error) {
	if fi.Size() > maxHashed {
		return fmt.Sprintf("large:%d:%d", fi.Size(), fi.ModTime().UnixNano()), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16], nil
}

// expandHome turns a leading ~/ into the home directory, as git does for a
// path-valued setting.
func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// submodule is a submodule's git directory and where it is checked out.
type submodule struct {
	gitDir string
	path   string // relative to the working tree, "" when only found under modules
	work   string // its working tree, "" when only found under modules
}

// finder collects submodule git directories up to the limits.
type finder struct {
	seen      map[string]bool
	subs      []submodule
	visits    int
	truncated bool
}

func (f *finder) add(gd, rel, work string) {
	gd = canonical(gd)
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
func submoduleGitDirs(dir string, loc gitLocations) (subs []submodule, truncated bool, err error) {
	f := &finder{seen: map[string]bool{}}
	if loc.topdir != "" {
		out, err := run(dir, "ls-files", "--stage", "-z")
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
	return f.subs, f.truncated, nil
}

// walkModules finds the git directories under a modules directory: each is a
// directory holding a HEAD file, and may have a modules directory of its own.
// How many directories are visited, and how deep, is bounded; going past either
// is recorded and not skipped silently.
func (f *finder) walkModules(base string, depth int) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	if depth > maxModuleDeep {
		f.truncated = true
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if f.visits++; f.visits > maxModuleDirs {
			f.truncated = true
			return
		}
		gd := filepath.Join(base, e.Name())
		if _, err := os.Stat(filepath.Join(gd, "HEAD")); err == nil {
			f.add(gd, "", "")
			f.walkModules(filepath.Join(gd, "modules"), depth+1)
		} else {
			// A submodule named with a slash lives in a nested directory.
			f.walkModules(gd, depth+1)
		}
	}
}

// resolveGitDir is the git directory of a checked-out repository: its .git
// directory, or where a .git file says it is.
func resolveGitDir(work string) (string, bool) {
	dot := filepath.Join(work, ".git")
	fi, err := os.Lstat(dot)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dot, true
	}
	data, err := os.ReadFile(dot)
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

// cachedConfig is a submodule configuration file as it was last read, so that a
// repository with many submodules is not asked about each one on every press.
// It is used only when the file's contents are unchanged and it includes
// nothing, since an included file can change on its own.
type cachedConfig struct {
	sum     string
	entries []configEntry
}

var configCache sync.Map // path -> cachedConfig

// submoduleConfig reads one submodule's configuration file.
func submoduleConfig(gitDir, cfg string) ([]configEntry, error) {
	data, err := os.ReadFile(cfg)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	cacheable := !bytes.Contains(bytes.ToLower(data), []byte("include"))
	if v, ok := configCache.Load(cfg); ok && cacheable {
		if c := v.(cachedConfig); c.sum == key {
			return c.entries, nil
		}
	}
	entries, err := listConfig(gitDir, gitDir, &cfg)
	if err != nil {
		return nil, err
	}
	if cacheable {
		configCache.Store(cfg, cachedConfig{sum: key, entries: entries})
	}
	return entries, nil
}

// scanSubmodule is ScanPrograms for a submodule's own git directory. A
// hooksPath that is relative is relative to the submodule's working tree.
func scanSubmodule(sub submodule) []Program {
	name := sub.path
	if name == "" {
		name = sub.gitDir
	}
	cfg := filepath.Join(sub.gitDir, "config")
	var items []Program
	var hooksPath *configEntry
	if _, err := os.Stat(cfg); err == nil {
		entries, err := submoduleConfig(sub.gitDir, cfg)
		if err != nil {
			items = append(items, unreadable(cfg, err, name))
		} else {
			var found []Program
			found, hooksPath = settingsIn(entries, name, sub.gitDir, false)
			items = append(items, found...)
		}
	} else if !absent(err) {
		items = append(items, unreadable(cfg, err, name))
	}
	loc := gitLocations{gitDir: sub.gitDir, common: sub.gitDir, topdir: sub.work}
	return append(items, hooksIn(loc, hooksPath, name)...)
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
		if !had[p.ID()] {
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
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}
