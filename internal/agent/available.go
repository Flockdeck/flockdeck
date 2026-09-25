package agent

import (
	"bytes"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// ProbeAll returns availability for a whole catalog, keyed by id.
//
// The probes are made side by side. Each one that misses walks the whole of
// PATH, and one after another they took 270ms over the built-in catalog on a
// Windows machine with half the agents not installed, against 160ms together.
func ProbeAll(specs []Spec) map[string]bool {
	ok := make([]bool, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Go(func() { ok[i] = Available(s) })
	}
	wg.Wait()
	out := make(map[string]bool, len(specs))
	for i, s := range specs {
		out[s.ID] = ok[i]
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

// OpenAICompatibleID is the built-in entry for an OpenAI-compatible endpoint,
// which ships with no address and is no use until it is given one.
const OpenAICompatibleID = "openai-compatible"

// TakesAddress reports whether the picker offers to change an agent's
// address: the OpenAI-compatible entry, whose address is the one thing it
// needs, and any other API agent that has been given one -- a local model of
// the user's own, or a vendor's agent pointed at a gateway. An API agent still
// talking to its vendor is left alone, since there is nothing to change there
// that anybody asked to.
func TakesAddress(s Spec) bool {
	return s.Runner == RunnerAPI && (s.ID == OpenAICompatibleID || s.API.BaseURL != "")
}

// keyIsSet is the default KeyProbe: the environment names the spec lists, then
// the key store. It reads nothing out -- the answer is set or not set, which is
// all availability, and all the interface, is ever told.
func keyIsSet(s Spec) bool {
	first, last := KeyNames(s)
	for _, name := range append(first, last...) {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	path := keysFile()
	if path == "" {
		return false
	}
	// Read through the state directory's retry: on Windows a read made while
	// a save is replacing the store is refused as a sharing violation, and
	// the picker, which asks on every refresh, greyed the agent out as having
	// no key for as long as that answer was trusted.
	data, err := store.ReadState(path)
	if err != nil {
		return false
	}
	// A store saved by Notepad starts with a byte-order mark, which creds
	// reads past and the decoder does not: the key was used to start the
	// pane while the picker greyed the agent out for having none.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var keys map[string]string
	if err := json.Unmarshal(data, &keys); err != nil {
		return false
	}
	// An id starting "@" is reserved by creds for keys that are not an
	// agent's (the TypeSafe key), so no agent has a key there.
	if strings.HasPrefix(strings.TrimSpace(s.ID), "@") {
		return false
	}
	return strings.TrimSpace(keys[s.ID]) != ""
}

// FallbackKeyEnv is Flockdeck's own variable, which any API agent reads a key
// from when nothing of its own holds one.
const FallbackKeyEnv = "FLOCKDECK_API_KEY"

// KeyNames are the environment variables an API agent's key is looked for in,
// split where the key stored for it with `flockdeck keys set` is looked at:
// first before it, last after it. It is the one order the chat client, the
// keys dialog, `flockdeck keys` and the picker all go by:
//
//  1. the entry's own variables, less its vendor's where it talks to somebody
//     else (OwnKeyEnv), then the vendor's usual one where it talks to the
//     vendor;
//  2. the key stored for it;
//  3. FLOCKDECK_API_KEY.
//
// The stored key comes before Flockdeck's own variable because it is this
// agent's, and the variable is any agent's. The order used to depend on the
// entry: one with a variable of its own was handed its stored key under that
// variable, and so used it first, while one with none -- a built-in pointed at
// a gateway -- took FLOCKDECK_API_KEY first, and `flockdeck keys check` asked
// about one key while the pane sent the other.
//
// The vendor's variable is left out for an entry pointed anywhere else, a
// gateway above all: OPENAI_API_KEY is the user's key for OpenAI, and an entry
// given only a third party's address would have handed it to that party with
// every request, even with a key stored for the entry itself.
//
// An entry with neither an endpoint nor a variable of its own is the
// OpenAI-compatible one as it ships, with no address filled in yet; a key
// exported for OpenAI proper is not a reason to offer it, so it has only its
// own names, which are none, and its stored key.
func KeyNames(s Spec) (first, last []string) {
	if len(s.API.KeyEnv) == 0 && s.API.BaseURL == "" {
		return nil, nil
	}
	first = OwnKeyEnv(s.API)
	if VendorsOwn(s.API) {
		for _, name := range wireKeyEnv(s.API.Wire) {
			if !slices.Contains(first, name) {
				first = append(first, name)
			}
		}
	}
	if slices.Contains(first, FallbackKeyEnv) {
		return first, nil
	}
	return first, []string{FallbackKeyEnv}
}

// OwnKeyEnv are the variables an entry names for its key, as far as it may be
// given them: all of them where it talks to its wire's vendor, and all but that
// vendor's own where it talks to anybody else.
//
// A built-in keeps the vendor's variable in its entry when it is pointed at a
// gateway -- `flockdeck keys endpoint` and the picker's address field change
// the address and nothing else -- so an OPENAI_API_KEY exported for OpenAI was
// sent to the gateway with every request, ahead of the key stored for it.
// Everything that looks for a key, or tells a pane where to, goes through this.
func OwnKeyEnv(api APISpec) []string {
	if VendorsOwn(api) {
		return slices.Clone(api.KeyEnv)
	}
	vendor := wireKeyEnv(api.Wire)
	var names []string
	for _, name := range api.KeyEnv {
		// Windows reads a variable's name without regard to case, so a
		// differently cased spelling is the vendor's variable there too.
		if !slices.ContainsFunc(vendor, func(v string) bool { return strings.EqualFold(v, name) }) {
			names = append(names, name)
		}
	}
	return names
}

// VendorsOwn reports whether an API entry talks to the vendor its wire is
// named for -- at no address of its own, or at the vendor's own host over
// https -- which is the only place the vendor's usual key variable may be
// sent. It is the one rule the picker, the keys dialog and the chat client
// all decide that by.
func VendorsOwn(api APISpec) bool {
	base := strings.TrimSpace(api.BaseURL)
	if base == "" {
		return true
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && strings.EqualFold(u.Hostname(), vendorHost(api.Wire))
}

// vendorHost is the host each wire's vendor serves its API from.
func vendorHost(wire string) string {
	switch strings.ToLower(strings.TrimSpace(wire)) {
	case "openai", "openai-compatible":
		return "api.openai.com"
	case "gemini", "google":
		return "generativelanguage.googleapis.com"
	default:
		return "api.anthropic.com"
	}
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
