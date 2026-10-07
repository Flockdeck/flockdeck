package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"time"
)

// GitSeen is what a user last accepted of the programs a repository's git
// configuration and hooks would run: the IDs gitx.ScanPrograms gave them. It is
// kept here, in Flockdeck's own state, and not in the repository, where an
// agent that wrote the configuration could write the record of having accepted
// it too.
type GitSeen struct {
	IDs      []string  `json:"ids"`
	Accepted time.Time `json:"accepted"`
}

const (
	gitSeenFile = "git-programs.json"
	gitSeenWhat = "which git hooks and settings you have accepted"
)

// gitSeenMu serialises the read-modify-write of the file inside this process.
var gitSeenMu sync.Mutex

// LoadGitSeen returns what was accepted for a repository, and whether anything
// was. The repository is named by its common git directory.
func LoadGitSeen(repo string) (GitSeen, bool, error) {
	gitSeenMu.Lock()
	defer gitSeenMu.Unlock()
	all, err := readGitSeen()
	if err != nil {
		return GitSeen{}, false, err
	}
	s, ok := all[normalizeRoot(repo)]
	return s, ok, nil
}

// SaveGitSeen records what was accepted for a repository, replacing any earlier
// record.
func SaveGitSeen(repo string, ids []string) error {
	gitSeenMu.Lock()
	defer gitSeenMu.Unlock()
	all, err := readGitSeen()
	if err != nil {
		return err
	}
	if ids == nil {
		ids = []string{}
	}
	all[normalizeRoot(repo)] = GitSeen{IDs: ids, Accepted: time.Now().UTC().Round(time.Second)}
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", gitSeenWhat, err)
	}
	path := filepath.Join(dir, gitSeenFile)
	if err := keepUnread(path, gitSeenWhat); err != nil {
		return fmt.Errorf("write %s: %w", gitSeenWhat, err)
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write %s: %w", gitSeenWhat, err)
	}
	return nil
}

// readGitSeen reads the whole file. A file that does not parse is kept aside
// and read as empty, so the next commit asks again rather than going unchecked
// or failing.
func readGitSeen() (map[string]GitSeen, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, gitSeenFile)
	data, err := readState(path)
	noteRead(path, err)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]GitSeen{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", gitSeenWhat, err)
	}
	all := map[string]GitSeen{}
	if json.Unmarshal(data, &all) != nil || all == nil {
		quarantine(path, gitSeenWhat, KeptDamaged)
		return map[string]GitSeen{}, nil
	}
	return all, nil
}
