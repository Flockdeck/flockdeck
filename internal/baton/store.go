package baton

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/store"
)

// ErrExists is what Save returns for an id already stored. A baton is not
// changed once saved: an edit is a new baton with its own id (see Baton.Fork).
var ErrExists = errors.New("a baton with that id is already saved")

// ErrNotFound is what Load returns for an id that is not stored.
var ErrNotFound = errors.New("no such baton")

// maxStored bounds a baton file that is read back. Render never makes one
// anywhere near this; a bigger one is not ours.
const maxStored = 1 << 20

// Store keeps batons as markdown files in one folder, private to the user.
type Store struct {
	dir string
}

// NewStore returns a store in dir, which is made when the first baton is
// saved.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Open returns the store in Flockdeck's own state directory, under batons/.
// Batons are kept there and never inside a checkout.
func Open() (*Store, error) {
	dir, err := store.Dir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(dir, "batons")), nil
}

// Dir is the folder batons are kept in.
func (s *Store) Dir() string { return s.dir }

// Path is where a baton with this id is kept, or "" for an id that is not the
// shape NewID makes, so no other name can be joined to the folder.
func (s *Store) Path(id string) string {
	if !ValidID(id) {
		return ""
	}
	return filepath.Join(s.dir, id+".md")
}

// Save writes a baton under its id, and refuses to replace one.
func (s *Store) Save(b Baton) error {
	path := s.Path(b.ID)
	if path == "" {
		return fmt.Errorf("%q is not a baton id", b.ID)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	_, werr := f.WriteString(Render(b))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return werr
	}
	return nil
}

// Load reads a stored baton.
func (s *Store) Load(id string) (Baton, error) {
	path := s.Path(id)
	if path == "" {
		return Baton{}, ErrNotFound
	}
	b, err := readFile(path, nil)
	if errors.Is(err, fs.ErrNotExist) {
		return Baton{}, ErrNotFound
	}
	if err != nil {
		return Baton{}, err
	}
	if b.ID == "" {
		b.ID = id
	}
	return b, nil
}

// List returns every stored baton, newest first. A file that is not a baton is
// skipped.
func (s *Store) List() ([]Baton, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Baton
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !ValidID(id) {
			continue
		}
		if b, err := s.Load(id); err == nil {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// ReadFile reads a baton from a file anywhere. A file with no baton front
// matter is taken as free text and becomes a baton whose Where things stand
// section is that text, so a lead can hand over notes it wrote by hand.
func ReadFile(path string) (Baton, error) { return ReadFileVerified(path, nil) }

// ReadFileVerified is ReadFile with a check made on the file once it is open:
// verify is given the open file and may refuse it. The check on the name that
// came before the open is then not the only one, and a path that was changed to
// point somewhere else between the two is judged by where it led.
func ReadFileVerified(path string, verify func(*os.File) error) (Baton, error) {
	b, err := readFile(path, verify)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, ErrNotBaton) {
		return Baton{}, err
	}
	text, rerr := readText(path, verify)
	if rerr != nil {
		return Baton{}, rerr
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return Baton{
		Title:    CleanText(name),
		Sections: map[Section]string{Standing: strings.TrimSpace(CleanText(text))},
	}, nil
}

func readText(path string, verify func(*os.File) error) (string, error) {
	// The path is looked at before it is opened and the open file after, and
	// they have to be the same file. Stat on the open file follows a link, so
	// looking only there would let a link through; looking only at the path
	// leaves a gap between the look and the open.
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if after, err := f.Stat(); err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return "", fmt.Errorf("%s changed while it was being opened", filepath.Base(path))
	}
	if verify != nil {
		if err := verify(f); err != nil {
			return "", err
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStored+1))
	if err == nil && len(data) > maxStored {
		return "", fmt.Errorf("%s is too large to be a baton", filepath.Base(path))
	}
	return string(data), err
}

func readFile(path string, verify func(*os.File) error) (Baton, error) {
	text, err := readText(path, verify)
	if err != nil {
		return Baton{}, err
	}
	return Parse(text)
}

// sourcesName is the file in the store folder that records which agent each
// baton was made from.
const sourcesName = "sources.json"

// sourcesMu serialises changes to the sources file; the file is small and
// changed rarely.
var sourcesMu sync.Mutex

// Source is the id of the agent a baton was made from, as the application
// recorded it when the baton was made, or "" if it recorded none. It is kept
// outside the baton file on purpose. A baton's own header says which agent it is
// from, but a baton is text that anyone who can write a file can write, and a
// header that says "claude" is a claim and no proof; what decides whether a baton
// may be sent to another company must not be something a file can say.
func (s *Store) Source(id string) string {
	if !ValidID(id) {
		return ""
	}
	s.lockRecords(sourcesName)
	defer sourcesMu.Unlock()
	return s.readSources()[id]
}

// SetSource records the agent a baton was made from. A baton with no source
// recorded has an unknown one.
func (s *Store) SetSource(id, agent string) error {
	if !ValidID(id) {
		return fmt.Errorf("%q is not a baton id", id)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	s.lockRecords(sourcesName)
	defer sourcesMu.Unlock()
	m := s.readSources()
	// A sources file that is there and cannot be read (no permission, a folder in
	// its place) is left as it is and the record fails: it is not replaced with a
	// file that says nothing of what it held. One that is read but is not JSON has
	// what it held kept beside it, under a name that is not already taken
	// (sources.json.bad, then .bad.1 and on), before the new one replaces it.
	raw, err := os.ReadFile(filepath.Join(s.dir, sourcesName))
	switch {
	case err == nil:
		var probe map[string]string
		if json.Unmarshal(raw, &probe) != nil {
			if err := keepBad(filepath.Join(s.dir, sourcesName), raw); err != nil {
				return fmt.Errorf("the sources file is damaged and a copy could not be kept: %w", err)
			}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the sources file cannot be read, so it is left as it is: %w", err)
	}
	m[id] = agent
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".sources-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, sourcesName)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// keepBad writes raw to the first of path.bad, path.bad.1, path.bad.2 ... that
// does not exist, so a second damage does not overwrite the copy of the first.
func keepBad(path string, raw []byte) error {
	for i := 0; i < 1000; i++ {
		name := path + ".bad"
		if i > 0 {
			name = fmt.Sprintf("%s.bad.%d", path, i)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, werr := f.Write(raw)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return werr
	}
	return errors.New("too many damaged copies of the sources file are kept")
}

func (s *Store) readSources() map[string]string {
	m := map[string]string{}
	if _, damaged := readRecord(filepath.Join(s.dir, sourcesName), &m); damaged {
		m = map[string]string{}
	}
	if m == nil {
		m = map[string]string{}
	}
	return m
}
