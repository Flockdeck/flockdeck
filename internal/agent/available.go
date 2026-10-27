package agent

import (
	"net"
	"net/url"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// Availability is whether an agent could be started on this machine right now:
// whether its program is on PATH, or for an ACP agent its adapter's command.
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

// lookPath is a variable so that a test can describe a machine
// with agents installed on it without installing any.
var (
	lookPath = exec.LookPath
)

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
	if s.Runner == RunnerACP {
		// The command the manifest starts is what has to be there. Node, where
		// the adapter needs it, is checked when the pane starts.
		a, ok := s.ACPAdapter()
		if !ok {
			return false
		}
		_, err := lookPath(a.Command)
		return err == nil
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

// FallbackKeyEnv is Flockdeck's own variable, which any agent that takes a key
// reads it from when nothing of its own holds one.
const FallbackKeyEnv = "FLOCKDECK_API_KEY"

// KeyNames are the environment variables an agent's key is looked for in,
// split where the key stored for it with `flockdeck keys set` is looked at:
// first before it, last after it. It is the one order the
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
// gateway -- the endpoint was changed on its own, with nothing else changed -- so an OPENAI_API_KEY exported for OpenAI was
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
// sent. It is the one rule the picker and the keys dialog
// decide that by.
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

// wireKeyEnv is the variable each vendor's own tools read a key from, which an
// entry that names none falls back on.
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
