package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jmwri/flockdeck/internal/store"
)

// ConfigName is the file, in Flockdeck's state directory, where the user's own
// agents and defaults live. It is read fresh every time the catalog is asked
// for, so editing it by hand takes effect without a restart.
const ConfigName = "agents.json"

// ConfigVersion is the schema version written into that file. Nothing rejects
// a file for its version: an agent list is additive, and refusing to start with
// somebody's agents because a newer build wrote a 2 into it would be a poor
// trade for a field nothing reads.
const ConfigVersion = 1

// Defaults are the agent and model chosen when a pane asks for neither: once
// for the whole installation, and once per project for somebody who works in
// two repositories that want different agents.
type Defaults struct {
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
}

// File is agents.json as it is written on disk.
//
// The agents are kept as the raw JSON they were written as, rather than as
// Specs, because merging them over the built-ins has to tell a field the user
// set from a field they left out -- and once decoded into a struct the two look
// exactly alike.
type File struct {
	Version  int                 `json:"version"`
	Defaults Defaults            `json:"defaults,omitempty"`
	Projects map[string]Defaults `json:"projects,omitempty"`
	Agents   []json.RawMessage   `json:"agents,omitempty"`
	// Extra is every other top-level key the file held. It is kept so that
	// writing a default back from the picker cannot quietly delete something a
	// later build, or the user, put there.
	Extra map[string]json.RawMessage `json:"-"`
}

// knownFields are the keys File decodes itself; everything else is Extra.
var knownFields = []string{"version", "defaults", "projects", "agents"}

func (f *File) UnmarshalJSON(data []byte) error {
	type plain File
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var rest map[string]json.RawMessage
	if err := json.Unmarshal(data, &rest); err != nil {
		return err
	}
	for _, name := range knownFields {
		delete(rest, name)
	}
	*f = File(p)
	if len(rest) > 0 {
		f.Extra = rest
	}
	return nil
}

func (f File) MarshalJSON() ([]byte, error) {
	type plain File
	data, err := marshalUnescaped(plain(f))
	if err != nil {
		return nil, err
	}
	if len(f.Extra) == 0 {
		// The common case keeps the struct's field order, which is the order
		// somebody reading the file expects to find them in.
		return data, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	for name, raw := range f.Extra {
		if _, taken := m[name]; !taken {
			m[name] = raw
		}
	}
	return marshalUnescaped(m)
}

// marshalUnescaped is json.Marshal without the HTML escaping, which an
// encoder further out cannot undo once it has been done in here.
func marshalUnescaped(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// ConfigPath returns where agents.json lives.
func ConfigPath() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigName), nil
}

// ReadConfig reads agents.json from a directory. A file that is not there is
// not an error -- it is the ordinary case, and means nothing but the built-ins.
func ReadConfig(dir string) (*File, error) {
	configMu.Lock()
	defer configMu.Unlock()
	return readConfig(dir)
}

// configMu is held across every read and write of agents.json in this
// process. Two defaults saved at once each read the file, changed their own
// entry and wrote it back, so one was lost; and Windows refuses to rename over
// a file that the other save, or a picker reading the catalog, has open --
// twenty saves at once lost nineteen and failed eleven with "Access is
// denied".
var configMu sync.Mutex

func readConfig(dir string) (*File, error) {
	data, err := os.ReadFile(filepath.Join(dir, ConfigName))
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Version: ConfigVersion}, nil
		}
		return nil, fmt.Errorf("read %s: %w", ConfigName, err)
	}
	// Notepad and Windows PowerShell both write UTF-8 with a byte-order mark
	// in front, which the decoder takes for a stray character: the whole file
	// was set aside, and the picker then refused to save a default over it.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	// An empty file -- what `touch` or an editor's New File leaves -- holds
	// nothing to lose, so it reads as no file at all. As a parse error it put a
	// notice up and, since a file that cannot be read is not written over,
	// stopped the picker saving any default into it.
	if len(bytes.TrimSpace(data)) == 0 {
		return &File{Version: ConfigVersion}, nil
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ConfigName, err)
	}
	return &f, nil
}

// WriteConfig replaces agents.json in a directory.
//
// The write is atomic, because this file is the user's own: a half-written one
// left behind by an interrupted save would lose every agent they had defined,
// and would do it at the moment Flockdeck was closing rather than somewhere they
// could see it happen.
func WriteConfig(dir string, f *File) error {
	configMu.Lock()
	defer configMu.Unlock()
	return writeConfig(dir, f)
}

func writeConfig(dir string, f *File) error {
	if f.Version == 0 {
		f.Version = ConfigVersion
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The file is the user's own, and what they wrote in it should come back
	// as they wrote it. The default escapes "&", "<" and ">" in every string,
	// so saving a default from the picker turned a baseURL's "&x=" into
	// "&x=" and an install line's "&&" into "&&".
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return fmt.Errorf("encode %s: %w", ConfigName, err)
	}
	data := buf.Bytes()
	path := filepath.Join(dir, ConfigName)
	tmp, err := os.CreateTemp(dir, ConfigName+".tmp*")
	if err != nil {
		return fmt.Errorf("write %s: %w", ConfigName, err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("write %s: %w", ConfigName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("write %s: %w", ConfigName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", ConfigName, err)
	}
	// The state directory is the user's alone, but the file is written 0600 as
	// well: an OpenAI-compatible entry may carry a private endpoint, and the
	// rest of Flockdeck's state is kept that way too.
	if err := os.Chmod(name, 0o600); err != nil && !os.IsNotExist(err) {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", ConfigName, err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", ConfigName, err)
	}
	return nil
}

// SetDefaults records the agent and model to use where nothing else says. An
// empty project sets them for the whole installation; a project path sets them
// for that project alone, which is the picker's "set as default for this
// project".
func SetDefaults(dir, project string, d Defaults) error {
	configMu.Lock()
	defer configMu.Unlock()
	f, err := readConfig(dir)
	if err != nil {
		// A file that cannot be read must not be written over: it is the only
		// copy of whatever agents the user defined, and a defaults change is
		// nowhere near worth losing them. Saying so gives the interface
		// something to show and leaves the file to be repaired by hand.
		return err
	}
	if project == "" {
		f.Defaults = d
	} else {
		if f.Projects == nil {
			f.Projects = map[string]Defaults{}
		}
		key := projectKey(project)
		// A project already recorded under another spelling of the same path
		// is replaced rather than joined, so the two cannot disagree.
		for existing := range f.Projects {
			if projectKey(existing) == key && existing != project {
				delete(f.Projects, existing)
			}
		}
		if d == (Defaults{}) {
			delete(f.Projects, project)
		} else {
			f.Projects[project] = d
		}
	}
	return writeConfig(dir, f)
}
