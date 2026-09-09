package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Claude Code asks whether a folder is trusted the first time it runs in one,
// and records the answer in its own configuration. A fan-out creates a fresh
// worktree per child, so without this every child would stop on that question
// before doing any work.
//
// Nothing here decides that a directory should be trusted. It only reads the
// answer already given for one directory and copies it to another, which the
// caller offers as an explicit, explained choice.

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

	key := filepath.Clean(to)
	entry, _ := projects[key].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
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
	tmp := path + ".agent-wrapper.tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write Claude configuration: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace Claude configuration: %w", err)
	}
	return nil
}
