// Package store persists the window layout between runs.
//
// Only the shape of the workspace is saved — tabs, splits, working directories
// and pane ids. Conversation history lives in Claude Code's own session store
// and is reattached with `claude --resume <id>`, which is why pane ids are
// generated as UUIDs.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Version is the schema version of the persisted state.
const Version = 1

// State is the whole persisted workspace.
type State struct {
	Version int    `json:"version"`
	Tabs    []Tab  `json:"tabs"`
	Active  int    `json:"active"`
	Root    string `json:"root,omitempty"` // directory the workspace was opened on
}

// Tab is one tab and its pane tree.
type Tab struct {
	Title string `json:"title,omitempty"`
	Focus string `json:"focus,omitempty"`
	Root  *Node  `json:"root"`
}

// Node mirrors layout.Node in a form that survives a round trip through JSON.
type Node struct {
	Pane     *Pane   `json:"pane,omitempty"`
	Dir      string  `json:"dir,omitempty"` // "h" or "v"
	Weight   float64 `json:"weight,omitempty"`
	Children []*Node `json:"children,omitempty"`
}

// Pane is a single restored pane.
type Pane struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "claude" or "shell"
	Cwd  string `json:"cwd"`
	Name string `json:"name,omitempty"`
	// Task is what a spawned pane was asked to do. It is kept so a restored
	// agent can still be told why its pane exists.
	Task string `json:"task,omitempty"`
}

// Dir returns the per-user directory holding wrapper state.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	dir := filepath.Join(base, "agent-wrapper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// SessionsDir returns the directory holding generated per-session settings.
func SessionsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", fmt.Errorf("create sessions dir: %w", err)
	}
	return sub, nil
}

// path returns the state file path for a workspace root. Each root gets its
// own layout, so opening two repositories does not clobber one another.
func path(root string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "layout-"+hashRoot(root)+".json"), nil
}

// normalizeRoot puts a workspace path into the one form used for comparing and
// hashing roots. Roots reach us from a directory picker, the command line and
// saved state, so the same project can arrive as "C:\Repo\App\" one run and
// "C:\repo\app" the next; without this each spelling would get its own layout
// file while sameRoot still treated them as one project.
func normalizeRoot(root string) string {
	clean := filepath.Clean(root)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(clean)
	}
	return clean
}

// Load reads the saved state for a workspace root. A missing file is not an
// error: it simply means there is nothing to restore.
func Load(root string) (*State, error) {
	p, err := path(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read layout: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// A corrupt layout must never stop the app from starting.
		return nil, nil
	}
	if s.Version != Version {
		return nil, nil
	}
	return &s, nil
}

// Save writes the state atomically so an interrupted write cannot leave a
// truncated layout behind.
func Save(root string, s *State) error {
	p, err := path(root)
	if err != nil {
		return err
	}
	s.Version = Version
	s.Root = root
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode layout: %w", err)
	}
	if err := writeAtomic(p, data); err != nil {
		return fmt.Errorf("write layout: %w", err)
	}
	return nil
}

// writeAtomic replaces a file with new contents, leaving either the old file
// or the new one behind and never a half-written mixture of the two.
//
// The temporary file gets a unique name because two wrappers can be running at
// once: with a shared "<name>.tmp" one process could rename the other's
// half-written file into place. It is flushed to disk before the rename, since
// a rename that reaches the disk ahead of the contents it names would leave an
// empty file after a crash.
func writeAtomic(path string, data []byte) error {
	dir, base := filepath.Split(path)
	f, err := os.CreateTemp(dir, base+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below has succeeded

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// os.CreateTemp already makes the file 0600, which is what these state
	// files want: they carry the local server's auth token.
	return renameWithRetry(tmp, path)
}

// renameWithRetry replaces dst with src, retrying briefly on failure.
//
// On Windows replacing a file fails outright with a sharing violation while
// anything else holds it open, and these files are opened constantly — by the
// other wrapper instance saving at the same moment, and by the virus scanner
// and search indexer that follow every write in the user's AppData directory.
// Those holds last microseconds, so a few retries turn a lost save into a
// slightly slower one. On other platforms the first attempt always decides it.
func renameWithRetry(src, dst string) error {
	delay := time.Millisecond
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		// A missing source is our own bug, not contention: retrying it would
		// only turn an immediate error into a delayed one.
		if errors.Is(err, fs.ErrNotExist) {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
	return err
}

// hashRoot turns a path into a short stable filename component.
func hashRoot(root string) string {
	root = normalizeRoot(root)
	// FNV-1a, inlined to keep the dependency surface small.
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(root); i++ {
		h ^= uint64(root[i])
		h *= prime
	}
	return fmt.Sprintf("%016x", h)
}

// SweepSessions removes generated per-session settings files left behind by
// runs that were killed rather than closed, and returns how many it deleted.
//
// Files are only removed once they are older than maxAge, because a second
// wrapper may be running concurrently and its panes' settings must not be
// pulled out from under it. Normal shutdown deletes these files as panes
// close, so anything this finds is genuinely orphaned.
func SweepSessions(maxAge time.Duration) (int, error) {
	dir, err := SessionsDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read sessions dir: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".settings.json") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}

// BrowserProfileDir returns the directory holding the browser profile used for
// the application window. Keeping it separate from the user's own profile
// means the window opens clean and does not disturb their browsing session.
func BrowserProfileDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, "window")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", fmt.Errorf("create window profile dir: %w", err)
	}
	return sub, nil
}

// Project is a directory the user has opened.
type Project struct {
	Root     string    `json:"root"`
	LastUsed time.Time `json:"lastUsed"`
}

// recentsFile is the global list of projects, kept separately from the
// per-project layouts so the picker can offer them before any is opened.
const recentsFile = "projects.json"

// maxRecents caps the remembered list.
const maxRecents = 40

// Recents returns known projects, most recently used first.
func Recents() ([]Project, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, recentsFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read projects: %w", err)
	}
	var list []Project
	if json.Unmarshal(data, &list) != nil {
		// A damaged list is not worth failing over; it is only a convenience.
		return nil, nil
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].LastUsed.After(list[j].LastUsed) })
	return list, nil
}

// TouchRecent records that a project was opened, moving it to the front.
func TouchRecent(root string) error {
	// The rewrite below replaces the whole file, so a list we could not read
	// has to stop us: carrying on would quietly discard every other project
	// the user has opened. A damaged or absent list reads as empty, which is
	// the case where starting again is the right answer.
	list, err := Recents()
	if err != nil {
		return err
	}
	out := make([]Project, 0, len(list)+1)
	out = append(out, Project{Root: root, LastUsed: time.Now()})
	for _, p := range list {
		if !sameRoot(p.Root, root) {
			out = append(out, p)
		}
	}
	if len(out) > maxRecents {
		out = out[:maxRecents]
	}
	return writeRecents(out)
}

// ForgetRecent drops a project from the remembered list.
func ForgetRecent(root string) error {
	list, err := Recents()
	if err != nil {
		return err
	}
	out := make([]Project, 0, len(list))
	for _, p := range list {
		if !sameRoot(p.Root, root) {
			out = append(out, p)
		}
	}
	return writeRecents(out)
}

func writeRecents(list []Project) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("encode projects: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, recentsFile), data); err != nil {
		return fmt.Errorf("write projects: %w", err)
	}
	return nil
}

// sameRoot compares project paths, case-insensitively on platforms whose file
// systems are.
func sameRoot(a, b string) bool {
	return normalizeRoot(a) == normalizeRoot(b)
}

// Session is the set of projects that were open when the application last
// exited, so a restart brings the whole workspace back rather than only the
// directory named on the command line.
type Session struct {
	Open   []string `json:"open"`
	Active string   `json:"active"`
}

const sessionFile = "session.json"

// LoadSession returns the last workspace, or nil when there is none.
func LoadSession() (*Session, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, sessionFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session: %w", err)
	}
	var s Session
	if json.Unmarshal(data, &s) != nil {
		return nil, nil // a damaged session must not stop startup
	}
	return &s, nil
}

// SaveSession records which projects are open.
func SaveSession(s *Session) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, sessionFile), data); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

// Instance records a running agent-wrapper so a second launch can attach to it
// instead of starting a rival server, and so agents can outlive the window.
type Instance struct {
	PID     int       `json:"pid"`
	URL     string    `json:"url"`
	Token   string    `json:"token"`
	Started time.Time `json:"started"`
}

const instanceFile = "instance.json"

func instancePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, instanceFile), nil
}

// LoadInstance returns the recorded running instance, or nil when there is
// none. A stale record left by a crash is not detected here: the caller has to
// probe the address, which is the only reliable test.
func LoadInstance() (*Instance, error) {
	path, err := instancePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read instance: %w", err)
	}
	var inst Instance
	if json.Unmarshal(data, &inst) != nil || inst.URL == "" {
		return nil, nil
	}
	return &inst, nil
}

// SaveInstance records this process as the running instance.
func SaveInstance(inst *Instance) error {
	path, err := instancePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instance: %w", err)
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write instance: %w", err)
	}
	return nil
}

// ClearInstance removes the record, and does nothing if it is already gone.
func ClearInstance() error {
	path, err := instancePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
