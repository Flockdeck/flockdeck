package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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

// IsTrusted reports whether Claude Code has been told this directory is
// trusted.
func IsTrusted(dir string) bool {
	cfg, err := readClaudeConfig()
	if err != nil {
		return false
	}
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		return false
	}
	for _, key := range trustKeys(dir) {
		entry, _ := projects[key].(map[string]any)
		if entry == nil {
			continue
		}
		if ok, _ := entry["hasTrustDialogAccepted"].(bool); ok {
			return true
		}
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
func writeClaudeConfig(cfg map[string]any) error {
	path := configPath()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Claude configuration: %w", err)
	}
	tmp := path + ".flockdeck.tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write Claude configuration: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace Claude configuration: %w", err)
	}
	return nil
}
