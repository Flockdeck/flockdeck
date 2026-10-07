package gitx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
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

// Program is one thing git would run for a commit, push, pull or fetch, and
// where it is set.
type Program struct {
	// Kind is "setting" for a configuration key, "hook" for an executable file
	// in a hooks directory.
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

// Line is the program as one plain sentence fragment for a warning:
// "core.sshCommand = ssh -i k (in /repo/.git/config)".
func (p Program) Line() string {
	var s string
	if p.Kind == "hook" {
		s = "hook " + p.Name + " (in " + p.Where + ")"
	} else {
		s = p.Name + " = " + p.Value + " (in " + p.Where + ")"
	}
	if p.Submodule != "" {
		s += ", submodule " + p.Submodule
	}
	return s
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
//   - a filter driver's clean, smudge or process, run by git add and checkout
//   - gpg.program and gpg.<format>.program, run when commits are signed
//   - hook.<name>.command, hooks configured in git's own configuration
//
// Left out on purpose: core.fsmonitor, which every git Flockdeck starts has
// blanked (see ownConfig); core.pager and core.editor, which a commit given its
// message on stdin and a push never start; diff drivers, which Flockdeck's own
// diffs are told not to use; and alias.*, which cannot replace the built-in
// commands Flockdeck runs.
var risky = regexp.MustCompile(`^(core\.(sshcommand|hookspath|askpass|gitproxy|alternaterefscommand)|credential\.(.+\.)?helper|filter\..+\.(clean|smudge|process)|gpg\.program|gpg\..+\.program|hook\..+\.command)$`)

// lfsFilter is what `git lfs install` writes. As in auto-review, only these
// exact values are let through: the value is run by a shell, so anything added
// to it is a program of its own.
var lfsFilter = map[string]string{
	"filter.lfs.clean":   "git-lfs clean -- %f",
	"filter.lfs.smudge":  "git-lfs smudge -- %f",
	"filter.lfs.process": "git-lfs filter-process",
}

// runsProgram reports whether a configuration entry names a program to run.
func runsProgram(key, value string) bool {
	if !risky.MatchString(key) {
		return false
	}
	if want, ok := lfsFilter[key]; ok && value == want {
		return false
	}
	// An empty value turns a setting off.
	return strings.TrimSpace(value) != ""
}

// hookNames are the hooks git can run. A file in a hooks directory with any
// other name is never started.
var hookNames = map[string]bool{
	"applypatch-msg": true, "pre-applypatch": true, "post-applypatch": true,
	"pre-commit": true, "pre-merge-commit": true, "prepare-commit-msg": true,
	"commit-msg": true, "post-commit": true, "pre-rebase": true,
	"post-checkout": true, "post-merge": true, "pre-push": true,
	"pre-receive": true, "update": true, "proc-receive": true,
	"post-receive": true, "post-update": true, "reference-transaction": true,
	"push-to-checkout": true, "pre-auto-gc": true, "post-rewrite": true,
	"sendemail-validate": true, "post-index-change": true,
}

// maxScanned bounds how many submodule git directories and how many hook files
// are looked at, so that a repository built to be slow to inspect is not.
const (
	maxSubmodules = 200
	maxHashed     = 16 << 20
)

// ScanPrograms finds what git would run, in dir, for a commit, push, pull or
// fetch: the configuration git itself would read (system, global, repository,
// worktree, and whatever they include), the hooks it would find, and the same
// for every submodule, whose own configuration and hooks git consults when it
// descends into one.
//
// An error means the scan could not be completed, and says why. Callers must
// not take it for "nothing to run".
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
	var hooksPath *configEntry
	for i := range entries {
		e := entries[i]
		if e.key == "core.hookspath" && strings.TrimSpace(e.value) != "" {
			hooksPath = &entries[i]
		}
		if runsProgram(e.key, e.value) {
			rep.Items = append(rep.Items, Program{Kind: "setting", Name: e.key, Value: e.value, Where: e.origin, Machine: e.machine})
		}
	}
	hooks, err := hooksIn(paths, hooksPath, "")
	if err != nil {
		return Report{}, err
	}
	rep.Items = append(rep.Items, hooks...)

	subs, err := submoduleGitDirs(dir, paths)
	if err != nil {
		return Report{}, err
	}
	for _, sub := range subs {
		items, err := scanSubmodule(sub)
		if err != nil {
			return Report{}, err
		}
		rep.Items = append(rep.Items, items...)
	}
	sort.SliceStable(rep.Items, func(i, j int) bool { return rep.Items[i].ID() < rep.Items[j].ID() })
	return rep, nil
}

// gitLocations are the directories of a repository that matter here.
type gitLocations struct {
	gitDir  string // this worktree's git directory
	common  string // shared by all worktrees; where hooks and modules live
	topdir  string // the working tree; empty for a bare repository
	hookDir string
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
	loc.gitDir = filepath.Clean(filepath.FromSlash(lines[0]))
	loc.common = filepath.FromSlash(lines[1])
	if !filepath.IsAbs(loc.common) {
		loc.common = filepath.Join(dir, loc.common)
	}
	loc.common = filepath.Clean(loc.common)
	if len(lines) >= 3 {
		loc.topdir = filepath.Clean(filepath.FromSlash(lines[2]))
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

// hooksIn lists the hooks git would run from a repository: those in its hooks
// directory, or in core.hooksPath when that is set.
//
// A hooks directory inside the working tree is the project's own files, which
// the review panel lists as changes like any other, so only the setting is
// reported for it. One in the git directory, or anywhere else, is not shown
// there, so every hook in it is listed with a hash of its contents.
func hooksIn(loc gitLocations, hooksPath *configEntry, submodule string) ([]Program, error) {
	dirPath := filepath.Join(loc.common, "hooks")
	machine := false
	if hooksPath != nil {
		dirPath = expandHome(hooksPath.value)
		if !filepath.IsAbs(dirPath) {
			base := loc.topdir
			if base == "" {
				base = loc.gitDir
			}
			dirPath = filepath.Join(base, filepath.FromSlash(dirPath))
		}
		dirPath = filepath.Clean(dirPath)
		machine = hooksPath.machine
		if loc.topdir != "" && within(loc.topdir, dirPath) && !within(loc.gitDir, dirPath) && !within(loc.common, dirPath) {
			return nil, nil
		}
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read hooks in %s: %w", dirPath, err)
	}
	var items []Program
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".sample") || !hookNames[name] {
			continue
		}
		full := filepath.Join(dirPath, name)
		fi, err := os.Stat(full)
		if err != nil || !fi.Mode().IsRegular() || !executable(fi) {
			continue
		}
		sum, err := hashFile(full)
		if err != nil {
			return nil, fmt.Errorf("read hook %s: %w", full, err)
		}
		items = append(items, Program{Kind: "hook", Name: name, Sum: sum, Where: dirPath, Submodule: submodule, Machine: machine})
	}
	return items, nil
}

// executable reports whether git would start the file as a hook. Windows has
// no execute bit; git there starts any file with a hook's name.
func executable(fi fs.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxHashed)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
}

// submoduleGitDirs finds the git directories git would descend into: every
// submodule the index records, wherever its .git file points (a repository
// can say "gitdir: ../x" for a submodule, and git then reads x's
// configuration), and every directory under .git/modules.
func submoduleGitDirs(dir string, loc gitLocations) ([]submodule, error) {
	seen := map[string]bool{}
	var subs []submodule
	add := func(gd, rel string) {
		key := filepath.Clean(gd)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] || len(subs) >= maxSubmodules {
			return
		}
		seen[key] = true
		subs = append(subs, submodule{gitDir: filepath.Clean(gd), path: rel})
	}

	if loc.topdir != "" {
		out, err := run(dir, "ls-files", "--stage", "-z")
		if err != nil {
			return nil, err
		}
		for _, rec := range strings.Split(out, "\x00") {
			meta, path, ok := strings.Cut(rec, "\t")
			if !ok || !strings.HasPrefix(meta, "160000 ") {
				continue
			}
			gd, ok := resolveGitDir(filepath.Join(loc.topdir, filepath.FromSlash(path)))
			if ok {
				add(gd, path)
			}
		}
	}
	for _, base := range []string{filepath.Join(loc.gitDir, "modules"), filepath.Join(loc.common, "modules")} {
		walkModules(base, add)
	}
	return subs, nil
}

// walkModules finds the git directories under a modules directory: each is a
// directory holding a HEAD file, and may have a modules directory of its own.
func walkModules(base string, add func(gd, rel string)) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		gd := filepath.Join(base, e.Name())
		if _, err := os.Stat(filepath.Join(gd, "HEAD")); err == nil {
			add(gd, "")
			walkModules(filepath.Join(gd, "modules"), add)
		} else {
			// A submodule named with a slash lives in a nested directory.
			walkModules(gd, add)
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

// scanSubmodule is ScanPrograms for a submodule's own git directory.
func scanSubmodule(sub submodule) ([]Program, error) {
	name := sub.path
	if name == "" {
		name = sub.gitDir
	}
	cfg := filepath.Join(sub.gitDir, "config")
	var items []Program
	var hooksPath *configEntry
	if _, err := os.Stat(cfg); err == nil {
		entries, err := listConfig(sub.gitDir, sub.gitDir, &cfg)
		if err != nil {
			return nil, err
		}
		for i := range entries {
			e := entries[i]
			if e.key == "core.hookspath" && strings.TrimSpace(e.value) != "" {
				hooksPath = &entries[i]
			}
			if runsProgram(e.key, e.value) {
				items = append(items, Program{Kind: "setting", Name: e.key, Value: e.value, Where: e.origin, Submodule: name})
			}
		}
	}
	hooks, err := hooksIn(gitLocations{gitDir: sub.gitDir, common: sub.gitDir}, hooksPath, name)
	if err != nil {
		return nil, err
	}
	return append(items, hooks...), nil
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
	// New are the programs not in what was accepted: all of them on first sight.
	New []Program
	// Narrowed is set when what was accepted has shrunk and nothing is new, so
	// the record can be brought down to match without asking.
	Narrowed bool
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
//     risk, so a scan with fewer is recorded without asking.
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
	if len(added) > 0 {
		return Verdict{Warn: true, New: added}
	}
	return Verdict{Narrowed: len(r.Items) < len(known.IDs)}
}

// Warning is the plain text of a warning for the programs in a verdict.
func (v Verdict) Warning(action string) string {
	var b strings.Builder
	if v.First {
		b.WriteString("This repository's git configuration or hooks make git run a program, and Flockdeck has not asked you about them before. ")
	} else {
		b.WriteString("This repository's git configuration or hooks were changed to run a program since you last accepted them. ")
	}
	b.WriteString("Flockdeck is about to run git " + action + ", which would run:")
	for _, p := range v.New {
		b.WriteString("\n- " + p.Line())
	}
	b.WriteString("\nIf you or your tools set these up, continue. If you did not, an agent may have written them: look at the files named before you go on.")
	return b.String()
}
