package creds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/store"
)

// keysFile is the name of the store under the state directory. Its shape is
// flat — agent id to key — because that is all it has to be, and a file a user
// may have to look at in a hurry should be readable at a glance.
const keysFile = "keys.json"

// fileMode is what the store is written with, here and on every rewrite. The
// state directory is already 0700, so this is the second of the two locks; it
// is what protects the file if it is ever copied somewhere less careful.
const fileMode = 0o600

// path returns where the store lives.
func path() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keysFile), nil
}

// load reads the store. A missing file is an empty store rather than an error,
// because not having set a key yet is the ordinary case.
func load() (map[string]string, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", keysFile, err)
	}
	// Notepad, and PowerShell's own Set-Content, start a UTF-8 file with a
	// byte-order mark, which the JSON decoder takes for a syntax error: a
	// store edited by hand on Windows would otherwise read as broken and
	// refuse every later `keys set`.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var keys map[string]string
	if err := json.Unmarshal(data, &keys); err != nil {
		// The parse error is not wrapped. encoding/json quotes the offending
		// input in its message, and the offending input here is a file of
		// secrets; a syntax error a few bytes into a key would print it.
		return nil, fmt.Errorf("%s is not valid JSON", p)
	}
	if keys == nil {
		keys = map[string]string{}
	}
	return keys, nil
}

// stored returns the key held for an agent id, or "".
//
// Every failure reads as "no key". This is on the path of starting a pane and
// of every availability probe, and a store that cannot be read is exactly the
// same outcome for the user as one with nothing in it: the agent shows as
// needing a key. Saying so through an error here would only give a dozen
// callers a way to print one.
func stored(agentID string) string {
	if agentID == "" {
		return ""
	}
	keys, err := load()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(keys[agentID])
}

// Has reports whether a key is stored for an agent id, without reading it.
func Has(agentID string) bool { return stored(agentID) != "" }

// Names lists the agent ids that have a stored key, sorted. It is what lets
// the interface show a key it has no Spec for — an agent removed from
// agents.json leaves its key behind, and a key nobody can see is a key nobody
// can clear.
func Names() ([]string, error) {
	keys, err := load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(keys))
	for id, v := range keys {
		if strings.TrimSpace(v) != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Set stores a key for an agent id, replacing whatever was there.
//
// A store that cannot be parsed is refused rather than replaced: rewriting it
// would throw away every other key in it, and a hand-edited file with a
// trailing comma is a much better problem to have than a silently emptied one.
func Set(agentID, key string) error {
	agentID = strings.TrimSpace(agentID)
	key = strings.TrimSpace(key)
	if agentID == "" {
		return errors.New("which agent the key is for is missing")
	}
	if key == "" {
		return errors.New("the key is empty")
	}
	keys, err := load()
	if err != nil {
		return err
	}
	keys[agentID] = key
	return save(keys)
}

// Clear removes an agent's key and reports whether there was one to remove.
func Clear(agentID string) (bool, error) {
	keys, err := load()
	if err != nil {
		return false, err
	}
	if _, ok := keys[agentID]; !ok {
		return false, nil
	}
	delete(keys, agentID)
	return true, save(keys)
}

// save writes the store, created 0600 before anything is in it.
//
// The write goes to a temporary file in the same directory and is renamed over
// the old one, so an interrupted write cannot leave a truncated file where a
// set of keys used to be. os.CreateTemp makes the file 0600 already; the
// Chmod is for the second and later writes on a system where it was somehow
// created otherwise, and its failure is not worth refusing the write over on a
// file system that has no modes at all.
func save(keys map[string]string) error {
	p, err := path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		// Reaching here means a key would not encode, and the value is in the
		// error json returns. It is replaced rather than wrapped.
		return errors.New("could not encode the keys")
	}
	data = append(data, '\n')

	dir, base := filepath.Split(p)
	f, err := os.CreateTemp(dir, base+".tmp*")
	if err != nil {
		return fmt.Errorf("write %s: %w", keysFile, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // does nothing once the rename below has succeeded

	_ = f.Chmod(fileMode)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", keysFile, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", keysFile, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", keysFile, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("write %s: %w", keysFile, err)
	}
	return nil
}
