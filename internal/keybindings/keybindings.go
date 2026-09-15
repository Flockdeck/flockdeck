// Package keybindings lets a person remap Flockdeck's own window shortcuts --
// the ones internal/help/keys.go compiles in as ID, Keys, Label and Section --
// without touching the binary.
//
// This is a different layer from the Claude Code CLI's own keybindings.json
// (~/.claude/keybindings.json), which remaps keystrokes inside the terminal
// tool running in a pane. That file is read by the agent running in the pane;
// this one is read by the window around it, for the chords the window
// intercepts before a pane ever sees them.
//
// help.Keys stays the source of defaults and metadata -- label, section, help
// page, palette eligibility. This package holds only the overrides: a map of
// action id to a binding, absent meaning "use the built-in default." A future
// release that adds an action, or changes a default nobody has touched, needs
// no migration here.
package keybindings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/store"
)

// FileName is where the overrides are kept, in the state directory -- its own
// file for its own concern, following agents.json and remote.json rather than
// growing prefs.json.
const FileName = "keybindings.json"

// Version is the schema version written into the file. Nothing rejects a file
// for its version: the map is additive, and refusing to start with somebody's
// remapping because a newer build wrote a 2 into it would be a poor trade for
// a field nothing reads.
const Version = 1

// File is keybindings.json as it is written on disk.
type File struct {
	Version int `json:"version"`
	// Overrides maps an action's id (help.Key.ID) to the binding it has been
	// given instead of its built-in default. An id mapped to "" has
	// deliberately been given no binding at all -- reachable from the command
	// palette alone -- which is different from the id being absent, which
	// means the default.
	Overrides map[string]string `json:"overrides,omitempty"`
}

// mu is held across every read and write of keybindings.json in this process,
// as agents.json's configMu is: two remaps saved at once could each read the
// file, change their own entry and write it back, losing one.
var mu sync.Mutex

// path returns where keybindings.json lives.
func path() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the overrides. Absent or damaged, they are no overrides at all --
// every binding is the built-in default -- and never a startup failure, the
// same way ReadPrefs treats a missing or damaged prefs.json.
func Load() (*File, error) {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() (*File, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := store.ReadState(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &File{Version: Version}, nil
		}
		return nil, fmt.Errorf("read %s: %w", FileName, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &File{Version: Version}, nil
	}
	var f File
	if json.Unmarshal(data, &f) != nil {
		// Damaged, but a person's remapping is not worth a startup failure
		// over: every binding just goes back to its default until the file is
		// repaired or replaced by the next remap saved here.
		return &File{Version: Version}, nil
	}
	return &f, nil
}

// save writes the overrides. mu must be held.
func save(f *File) error {
	if f.Version == 0 {
		f.Version = Version
	}
	p, err := path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", FileName, err)
	}
	if err := store.WriteAtomic(p, data); err != nil {
		return fmt.Errorf("write %s: %w", FileName, err)
	}
	return nil
}

// ConflictError is returned when a binding would collide with another
// action's, whether that action's own default or its own override. Two
// actions can never share a chord: whichever ran second would simply never
// run.
type ConflictError struct {
	// Keys is the binding that was asked for.
	Keys string
	// With is the action that already has it.
	With help.Key
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s is already %s", e.Keys, e.With.Name())
}

// Effective returns help.Keys with every override applied: each entry's Keys
// replaced by what overrides gives its ID, when it gives one. help.Keys itself
// is never modified.
func Effective(overrides map[string]string) []help.Key {
	if len(overrides) == 0 {
		return help.Keys
	}
	out := make([]help.Key, len(help.Keys))
	copy(out, help.Keys)
	for i, k := range out {
		if v, ok := overrides[k.ID]; ok {
			out[i].Keys = v
		}
	}
	return out
}

// conflict reports the action, other than id itself, that overrides already
// gives keys -- by its own override or, absent one, its built-in default --
// or false when there is none.
func conflict(id, keys string, overrides map[string]string) (help.Key, bool) {
	sig := signatureOf(keys)
	if sig == "" {
		return help.Key{}, false
	}
	for _, k := range help.Keys {
		if k.ID == id {
			continue
		}
		effective := k.Keys
		if v, ok := overrides[k.ID]; ok {
			effective = v
		}
		if effective != "" && signatureOf(effective) == sig {
			return k, true
		}
	}
	return help.Key{}, false
}

// SetBinding gives action id the binding keys, or takes its binding away for
// keys == "" -- reachable from the command palette alone, like the actions
// that have always had no binding. It refuses a binding already given to
// another action, and one that cannot be expressed as a single chord (what
// help.Key.Keys marks with "…", for selectTab's Alt+1 … Alt+9 range, which is
// not remappable).
//
// A binding set back to exactly the built-in default is not kept as an
// override: a future release changing that default should reach somebody who
// never touched this action, the same way an absent field in prefs.json
// means the default rather than freezing whatever the default happened to be
// when the file was written.
func SetBinding(id, keys string) (*File, error) {
	k, ok := help.Lookup(id)
	if !ok {
		return nil, fmt.Errorf("no such action: %s", id)
	}
	if strings.Contains(k.Keys, "…") {
		return nil, fmt.Errorf("%s has no single binding to give it: it names a range, not a chord", k.Name())
	}
	keys = strings.TrimSpace(keys)
	if keys != "" && strings.Contains(keys, "…") {
		return nil, fmt.Errorf("%s has no single binding to give", k.Name())
	}
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return nil, err
	}
	if keys != "" {
		if with, has := conflict(id, keys, f.Overrides); has {
			return nil, &ConflictError{Keys: keys, With: with}
		}
	}
	if f.Overrides == nil {
		f.Overrides = map[string]string{}
	}
	if keys == k.Keys {
		delete(f.Overrides, id)
	} else {
		f.Overrides[id] = keys
	}
	if len(f.Overrides) == 0 {
		f.Overrides = nil
	}
	if err := save(f); err != nil {
		return nil, err
	}
	return f, nil
}

// ResetBinding takes id back to its built-in default, removing whatever
// override it had -- including one that deliberately gave it no binding.
func ResetBinding(id string) (*File, error) {
	if _, ok := help.Lookup(id); !ok {
		return nil, fmt.Errorf("no such action: %s", id)
	}
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return nil, err
	}
	if f.Overrides == nil {
		return f, nil
	}
	if _, had := f.Overrides[id]; !had {
		return f, nil
	}
	delete(f.Overrides, id)
	if len(f.Overrides) == 0 {
		f.Overrides = nil
	}
	if err := save(f); err != nil {
		return nil, err
	}
	return f, nil
}

// ResetAll takes every binding back to its built-in default.
func ResetAll() (*File, error) {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return nil, err
	}
	if len(f.Overrides) == 0 {
		return f, nil
	}
	f.Overrides = nil
	if err := save(f); err != nil {
		return nil, err
	}
	return f, nil
}
