package agent

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// Availability is whether an agent could be started on this machine right now:
// for a command-line agent, whether its program is on PATH; for an API agent,
// whether there is a key to use or an endpoint that needs none.
//
// It is not a permission. An unavailable agent is still offered by the picker,
// greyed, with its Install line beside it, because somebody who has not
// installed Codex should still learn that Flockdeck would run it.

// probeTTL is how long an answer is trusted. The picker asks for every agent
// each time it opens and the header asks on every render, and a PATH lookup for
// a program that is not installed walks every directory on PATH -- on Windows,
// with an extension list, several times over. A few seconds is short enough
// that installing an agent and opening the picker again shows it, and long
// enough that one open costs one lookup per agent.
const probeTTL = 5 * time.Second

// lookPath and keysFile are variables so that a test can describe a machine
// with agents installed on it without installing any.
var (
	lookPath = exec.LookPath
	keysFile = defaultKeysFile
)

// KeyProbe reports whether an API agent has a key available. It is a variable
// because key resolution belongs to internal/creds, which imports this package
// for Spec and so cannot be imported back from it; creds installs its own from
// an init if it wants to. The default below asks the same two questions creds
// does -- the environment, then the key store -- but only ever asks whether
// there is a key, never what it is.
var KeyProbe = keyIsSet

type probeResult struct {
	ok bool
	at time.Time
}

var probes = struct {
	sync.Mutex
	seen map[string]probeResult
}{seen: map[string]probeResult{}}

// now is the clock, replaceable so a test can watch an answer expire without
// waiting for it.
var now = time.Now

// Available reports whether a spec can be started here, remembering the answer
// for probeTTL.
func Available(s Spec) bool {
	at := now()
	probes.Lock()
	if got, ok := probes.seen[s.ID]; ok && at.Sub(got.at) < probeTTL {
		probes.Unlock()
		return got.ok
	}
	probes.Unlock()

	// The probe itself is deliberately outside the lock: it touches the disk,
	// and holding the lock across it would have every pane header waiting on
	// one slow PATH lookup. Two probes racing simply do the same work twice
	// and agree.
	ok := probe(s)

	probes.Lock()
	probes.seen[s.ID] = probeResult{ok: ok, at: at}
	probes.Unlock()
	return ok
}

// Refresh forgets what was probed, so the next look is a fresh one. The picker
// opening is the moment worth spending that on: it is where somebody who has
// just installed an agent goes to find it.
func Refresh() {
	probes.Lock()
	probes.seen = map[string]probeResult{}
	probes.Unlock()
}

// ProbeAll returns availability for a whole catalog, keyed by id, which is what
// the picker and the startup probe both want.
func ProbeAll(specs []Spec) map[string]bool {
	out := make(map[string]bool, len(specs))
	for _, s := range specs {
		out[s.ID] = Available(s)
	}
	return out
}

func probe(s Spec) bool {
	if s.Runner == RunnerAPI {
		return NeedsNoKey(s) || KeyProbe(s)
	}
	if s.Exe == "" {
		return false
	}
	_, err := lookPath(s.Exe)
	return err == nil
}

// NeedsNoKey reports whether an endpoint can be talked to without one. A model
// server on this machine -- Ollama, LM Studio, vLLM -- almost never asks for a
// key, and refusing to offer it until somebody invents one to store would be
// obtuse.
func NeedsNoKey(s Spec) bool {
	if s.API.BaseURL == "" {
		return false
	}
	u, err := url.Parse(s.API.BaseURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// keyIsSet is the default KeyProbe: the environment names the spec lists, then
// the key store. It reads nothing out -- the answer is set or not set, which is
// all availability, and all the interface, is ever told.
func keyIsSet(s Spec) bool {
	// The names are the ones the chat client tries, in its order: the entry's
	// own, then the vendor's usual one for the wire, then Flockdeck's own. Asking
	// only the first left an entry naming none of its own -- a gateway given
	// just a baseURL, with OPENAI_API_KEY exported -- greyed out in the picker
	// while it would have started perfectly well.
	//
	// An entry with neither an endpoint nor a variable of its own is the
	// OpenAI-compatible one as it ships, with no address filled in yet; a key
	// exported for OpenAI proper is not a reason to offer it.
	names := s.API.KeyEnv
	if len(s.API.KeyEnv) > 0 || s.API.BaseURL != "" {
		names = append(append(append([]string{}, names...), wireKeyEnv(s.API.Wire)...), "FLOCKDECK_API_KEY")
	}
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	path := keysFile()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var keys map[string]string
	if err := json.Unmarshal(data, &keys); err != nil {
		return false
	}
	return strings.TrimSpace(keys[s.ID]) != ""
}

// wireKeyEnv is the variable each vendor's own tools read a key from, which the
// chat client falls back on for an entry that names none. It is written out
// again here, rather than shared, because the chat client imports this package.
func wireKeyEnv(wire string) []string {
	switch strings.ToLower(strings.TrimSpace(wire)) {
	case "openai", "openai-compatible":
		return []string{"OPENAI_API_KEY"}
	case "gemini", "google":
		return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	default:
		return []string{"ANTHROPIC_API_KEY"}
	}
}

// KeysName is the file internal/creds keeps API keys in. It is named here only
// so that availability can ask whether one is set before creds exists.
const KeysName = "keys.json"

func defaultKeysFile() string {
	dir, err := store.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, KeysName)
}
