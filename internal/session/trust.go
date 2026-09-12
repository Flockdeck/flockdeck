package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Claude Code asks whether a folder is trusted the first time it runs in one,
// and records the answer in its own configuration. A fan-out creates a fresh
// worktree per child, so without this every child would stop on that question
// before doing any work.
//
// Nothing here decides that a directory should be trusted. It only reads the
// answer already given for one directory and copies it to another, which the
// caller offers as an explicit, explained choice.

// TrustedFor reports whether the trust question an agent asks has already been
// answered for a directory.
//
// An agent with no such question is reported as trusted, because there is
// nothing there to stop it: the point of asking is to know whether a fan-out
// would strand its children on a dialog, and an agent that never shows one
// never will.
func TrustedFor(spec agent.Spec, dir string) bool {
	if !spec.Caps.Trust {
		return true
	}
	return IsTrusted(dir)
}

// InheritTrustFor carries an agent's answer for one directory to another, for
// an agent that has an answer to carry. For one that does not it is a no-op
// rather than an error: nothing was asked, so nothing has to be arranged.
func InheritTrustFor(spec agent.Spec, from, to string) error {
	if !spec.Caps.Trust {
		return nil
	}
	return InheritTrust(from, to)
}

// Everything below reads and writes Claude Code's own configuration, which is
// the only agent configuration Flockdeck knows the shape of. That is why the
// capability is declared on the Spec rather than assumed: a second agent that
// claims Trust needs its own reader here, and until somebody who has that agent
// installed writes one, claiming it would quietly answer the wrong question in
// the wrong file.

// configPath is the file Claude Code keeps its per-directory answers in.
func configPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// trustKeys returns the forms a directory may be recorded under. Claude Code
// keys by the working directory as the process reports it, and both separator
// styles appear in practice.
func trustKeys(dir string) []string {
	clean := filepath.Clean(dir)
	keys := []string{clean}
	if slashed := filepath.ToSlash(clean); slashed != clean {
		keys = append(keys, slashed)
	}
	return keys
}

// claudeProjectKey is the key Claude Code looks a directory up under in its
// projects, which on Windows is written with forward slashes: its own
// normaliser is `if(M()==="windows")return n.replaceAll("\\","/")`. An
// answer recorded under the backslashed path is one Claude Code never reads,
// so a worktree given the project's trust that way still stopped on the
// trust question.
func claudeProjectKey(dir string) string {
	key := filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		key = filepath.ToSlash(key)
	}
	return key
}

// IsTrusted reports whether Claude Code would treat this directory as trusted,
// asking the question the way Claude Code itself does. That is not only the
// directory's own answer: Claude Code first looks under the project the
// directory belongs to -- the repository root, or for a linked worktree the
// root of the repository it was made from -- and then walks up from the
// directory to its repository root, or to the top of the disk outside a
// repository, accepting an answer given for any folder on the way. Asking only
// about the exact directory reported a subfolder of a trusted repository, and
// every worktree of one, as untrusted, and fan-out then refused to carry over
// an answer the user had in fact given.
//
// These rules were read from Claude Code 2.1.269: its trust check (nO), the
// key it files a project under (Pb and Dqe) and its walk up the folders (DH
// and NH). A later version that changes them changes what this has to do.
//
// This is the answer that decides whether InheritTrust writes trust into
// Claude Code's configuration, so it fails safe: anything that cannot be read,
// followed or matched exactly counts for nothing, which can only ever make a
// directory less trusted than Claude Code would find it, never more. A
// directory on another machine is reported untrusted without being looked at.
func IsTrusted(dir string) bool {
	if dir == "" || networkPath(dir) {
		return false
	}
	cfg, err := readClaudeConfig()
	if err != nil {
		return false
	}
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		return false
	}
	accepted := func(dir string) bool {
		for _, key := range trustKeys(dir) {
			entry, _ := projects[key].(map[string]any)
			if ok, _ := entry["hasTrustDialogAccepted"].(bool); ok {
				return true
			}
		}
		return false
	}

	dir, err = filepath.Abs(dir)
	if err != nil || !staysLocal(dir) {
		return false
	}
	root := repoRoot(dir)
	if root != "" && accepted(canonicalRoot(root)) {
		return true
	}
	for {
		if accepted(dir) {
			return true
		}
		parent := filepath.Dir(dir)
		if dir == root || parent == dir {
			return false
		}
		dir = parent
	}
}

// repoRoot is the nearest folder at or above dir holding a .git entry --
// a directory in a repository, a file in a linked worktree -- or "" outside
// any repository.
func repoRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// canonicalRoot is the project a repository root belongs to in Claude Code's
// eyes. For a linked worktree that is the repository the worktree was made
// from, found by following the worktree's .git file to its administrative
// folder and checking that the folder points back at it.
//
// Read from Claude Code 2.1.269 (Re and xt), and checked at least as strictly
// as it checks: a .git, commondir or gitdir that is not a plain file, a path
// that leads off the machine, and a comparison that is not exact all leave the
// worktree as its own project -- which is what Claude Code does with a step
// that fails -- so it is judged only on answers given for itself.
func canonicalRoot(root string) string {
	dotGit := filepath.Join(root, ".git")
	line, ok := readPointer(dotGit)
	if !ok || !strings.HasPrefix(line, "gitdir:") {
		return root // a .git folder: an ordinary repository
	}
	admin, ok := pointerTarget(root, strings.TrimSpace(strings.TrimPrefix(line, "gitdir:")))
	if !ok {
		return root
	}
	common, ok := readPointer(filepath.Join(admin, "commondir"))
	if !ok {
		return root
	}
	commonDir, ok := pointerTarget(admin, common)
	if !ok || filepath.Dir(admin) != filepath.Join(commonDir, "worktrees") {
		return root
	}
	back, ok := readPointer(filepath.Join(admin, "gitdir"))
	if !ok {
		return root
	}
	if backPath, ok := pointerTarget(admin, back); !ok || backPath != dotGit {
		return root
	}
	if filepath.Base(commonDir) != ".git" {
		// A bare repository: it is the project unless it has a .git of its own.
		if !staysLocal(commonDir) {
			return root
		}
		if _, err := os.Lstat(filepath.Join(commonDir, ".git")); err == nil {
			return root
		}
		return commonDir
	}
	return filepath.Dir(commonDir)
}

// readPointer reads one of the small files git ties a worktree together with,
// refusing anything that is not a plain file on this machine: a link could
// send the read anywhere.
func readPointer(path string) (string, bool) {
	if !staysLocal(path) {
		return "", false
	}
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4<<10))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// pointerTarget resolves a path found in one of those files against the folder
// it was found in. One that is empty, names another machine, or is only half
// absolute -- "C:x" or "\x" on Windows, which git never writes -- is refused.
func pointerTarget(base, p string) (string, bool) {
	if p == "" || networkPath(p) {
		return "", false
	}
	if !filepath.IsAbs(p) {
		if filepath.VolumeName(p) != "" || os.IsPathSeparator(p[0]) {
			return "", false
		}
		p = filepath.Join(base, p)
	}
	p = filepath.Clean(p)
	return p, !networkPath(p)
}

// networkPath reports whether a path names a location on another machine,
// where merely looking makes the system reach out to it -- on Windows, handing
// that machine the user's credentials. That is a UNC or device path on Windows
// (\\server\share, \\?\..., in either slash), and the automounted /net and
// /Network folders elsewhere: the places Claude Code 2.1.269 refuses to follow
// a worktree's links into (wl, Ire and xg).
func networkPath(p string) bool {
	if runtime.GOOS == "windows" {
		return len(p) >= 2 && os.IsPathSeparator(p[0]) && os.IsPathSeparator(p[1])
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(filepath.Clean(p), "/"), "/")
	return strings.EqualFold(first, "net") || strings.EqualFold(first, "network")
}

// staysLocal reports whether an absolute path can be followed without leaving
// the machine. Each link met on the way is read, not followed, and the walk
// goes on from where it points; a link to another machine, or a chain of links
// too long to be anything but a loop, ends it. Claude Code walks a worktree's
// paths the same way before it reads them (J, in 2.1.269).
func staysLocal(p string) bool {
	for hops := 0; hops < 40; hops++ {
		if networkPath(p) || !filepath.IsAbs(p) {
			return false
		}
		vol := filepath.VolumeName(p)
		parts := strings.FieldsFunc(p[len(vol):], func(r rune) bool { return r < 128 && os.IsPathSeparator(byte(r)) })
		cur := vol + string(filepath.Separator)
		next := ""
		for i, name := range parts {
			cur = filepath.Join(cur, name)
			fi, err := os.Lstat(cur)
			if err != nil {
				return true // nothing there to follow; reading it fails on its own
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(cur)
			if err != nil || target == "" || networkPath(target) {
				return false
			}
			if !filepath.IsAbs(target) {
				if filepath.VolumeName(target) != "" {
					return false
				}
				if os.IsPathSeparator(target[0]) {
					target = vol + target
				} else {
					target = filepath.Join(filepath.Dir(cur), target)
				}
			}
			next = filepath.Join(append([]string{target}, parts[i+1:]...)...)
			break
		}
		if next == "" {
			return true
		}
		p = filepath.Clean(next)
	}
	return false
}

// InheritTrust records that a directory is trusted, given that another already
// is. It refuses when the source is not itself trusted, so this can only ever
// carry an answer the user has already given.
func InheritTrust(from, to string) error {
	if !IsTrusted(from) {
		return fmt.Errorf("%s is not itself trusted", filepath.Base(from))
	}
	if IsTrusted(to) {
		return nil
	}

	cfg, err := readClaudeConfig()
	if err != nil {
		return err
	}
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		cfg["projects"] = projects
	}

	key := claudeProjectKey(to)
	entry, _ := projects[key].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
	// Trust is not the only question a fresh checkout is asked. A project
	// whose CLAUDE.md imports files from outside it is also asked "Allow
	// external CLAUDE.md file imports?", and a worktree is a project Claude
	// Code has never seen, so every child of a fan-out stopped on it again. The
	// answer the user gave the project itself is carried over as it stands --
	// yes or no -- and nothing is written where they were never asked.
	for _, k := range trustKeys(from) {
		source, _ := projects[k].(map[string]any)
		for _, answer := range []string{"hasClaudeMdExternalIncludesApproved", "hasClaudeMdExternalIncludesWarningShown"} {
			if v, ok := source[answer].(bool); ok {
				entry[answer] = v
			}
		}
	}
	projects[key] = entry

	return writeClaudeConfig(cfg)
}

func readClaudeConfig() (map[string]any, error) {
	path := configPath()
	if path == "" {
		return nil, fmt.Errorf("cannot locate the Claude Code configuration")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read Claude configuration: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("the Claude configuration could not be read: %w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

// writeClaudeConfig writes the file back, preserving every field it did not
// touch. Claude Code owns this file, so the whole document is rewritten from
// what was read rather than assembled from a partial view of it.
//
// It is written the way Claude Code 2.1.269 writes it, read from its
// executable: a temporary file renamed into place, through a symbolic link
// rather than over it, keeping the file's permissions (0600 for a new one).
// Renaming over the link replaced it with a plain file, and a configuration
// kept elsewhere and linked into place -- a dotfiles checkout, say -- quietly
// stopped being the one in use.
func writeClaudeConfig(cfg map[string]any) error {
	path := configPath()
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Claude configuration: %w", err)
	}
	tmp := path + ".flockdeck.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write Claude configuration: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace Claude configuration: %w", err)
	}
	return nil
}
