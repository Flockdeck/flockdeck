package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
)

// isolateKeys points the state directory at one of this test's own, so nothing
// here can read or write the keys of whoever is running it.
func isolateKeys(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
	// The built-in API agents read these, and a key exported by the machine
	// running the tests would otherwise show up in the listing as set.
	for _, s := range keysAgents() {
		for _, name := range s.API.KeyEnv {
			t.Setenv(name, "")
		}
	}
	// Any API agent reads this one, after its stored key.
	t.Setenv("FLOCKDECK_API_KEY", "")
}

// keysAgent is one agent from the list the subcommand works from.
func keysAgent(t *testing.T, id string) agent.Spec {
	t.Helper()
	for _, s := range keysAgents() {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no agent %q among the agents that can want a key", id)
	return agent.Spec{}
}

// A variable nothing that starts a pane reads is not where a pane's key comes
// from, and the listing must not say that it is.
func TestKeysListReportsOnlyWhatAPaneWouldFind(t *testing.T) {
	isolateKeys(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-elsewhere")
	out, err := runKeysCmd(t, "", "list")
	if err != nil {
		t.Fatalf("keys list: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "anthropic ") && !strings.Contains(line, "not set") {
			t.Errorf("anthropic is listed as %q, from a variable its pane never reads", line)
		}
	}
}

// runKeysCmd drives the subcommand with nobody at the terminal, which is how a
// script uses it, and returns everything it printed.
func runKeysCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := keysCmd(args, keysIO{in: strings.NewReader(stdin), out: &out})
	return out.String(), err
}

func TestKeysSetReadsStdin(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		want  string
	}{
		{name: "a line", stdin: "sk-typed\n", want: "sk-typed"},
		{name: "no trailing newline", stdin: "sk-piped", want: "sk-piped"},
		{name: "surrounding space", stdin: "  sk-padded  \r\n", want: "sk-padded"},
		{name: "only the first line", stdin: "sk-first\nsk-second\n", want: "sk-first"},
		{name: "a byte-order mark in front", stdin: "\xef\xbb\xbfsk-marked\r\n", want: "sk-marked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateKeys(t)
			out, err := runKeysCmd(t, tt.stdin, "set", "anthropic")
			if err != nil {
				t.Fatalf("keys set: %v", err)
			}
			if strings.Contains(out, tt.want) {
				t.Errorf("the key was printed back: %q", out)
			}
			got := creds.Resolve(keysAgent(t, "anthropic"))
			if got.Secret() != tt.want {
				t.Errorf("stored %q, want %q", got.Secret(), tt.want)
			}
			if got.Source != creds.SourceStore {
				t.Errorf("source = %q, want the store", got.Source)
			}
		})
	}
}

func TestKeysSetRefusesNothing(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "   \n", "set", "anthropic"); err == nil {
		t.Fatal("an empty line was accepted as a key")
	}
	if creds.Has("anthropic") {
		t.Error("an empty key was stored anyway")
	}
}

func TestKeysSetNeedsAnAgent(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "sk-x", "set"); err == nil {
		t.Fatal("keys set with no agent named succeeded")
	}
}

// The listing is the one place a person looks to find out where a key is
// coming from, and it must answer that without ever showing one.
func TestKeysListSaysWhereWithoutSaying(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENAI_API_KEY", "sk-exported")
	if _, err := runKeysCmd(t, "sk-stored\n", "set", "anthropic"); err != nil {
		t.Fatalf("keys set: %v", err)
	}
	// A key left behind by an agent nobody has in their catalog any more is
	// listed too, because otherwise there is no way to be told it is there.
	if err := creds.Set("an-old-agent", "sk-orphan"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	out, err := runKeysCmd(t, "", "list")
	if err != nil {
		t.Fatalf("keys list: %v", err)
	}
	for _, secret := range []string{"sk-exported", "sk-stored", "sk-orphan"} {
		if strings.Contains(out, secret) {
			t.Errorf("the listing printed %s:\n%s", secret, out)
		}
	}
	for _, want := range []string{"anthropic", "openai", "google", "an-old-agent", "OPENAI_API_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not mention %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "not set") {
		t.Errorf("the listing never says an agent has no key:\n%s", out)
	}
}

func TestKeysClear(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "sk-stored\n", "set", "anthropic"); err != nil {
		t.Fatalf("keys set: %v", err)
	}
	out, err := runKeysCmd(t, "", "clear", "anthropic")
	if err != nil {
		t.Fatalf("keys clear: %v", err)
	}
	if !strings.Contains(out, "anthropic") {
		t.Errorf("clearing did not say what it cleared: %q", out)
	}
	if creds.Has("anthropic") {
		t.Error("the key survived being cleared")
	}
	out, err = runKeysCmd(t, "", "clear", "anthropic")
	if err != nil {
		t.Fatalf("clearing a key that is not there failed: %v", err)
	}
	if !strings.Contains(out, "no stored key") {
		t.Errorf("clearing nothing said %q", out)
	}
}

// `flockdeck chat` run by hand has no pane to hand it a stored key, and its
// error without one tells the user to run `flockdeck keys set`: the key that
// sets has to be the one it then finds.
func TestAChatStartedByHandFindsAStoredKey(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "sk-stored\n", "set", "anthropic"); err != nil {
		t.Fatalf("keys set: %v", err)
	}
	if got := storedKey("anthropic"); got != "sk-stored" {
		t.Errorf("storedKey = %q, want the stored key", got)
	}
	if got := storedKey("openai"); got != "" {
		t.Errorf("storedKey found %q for an agent with nothing stored", got)
	}
}

// Which ids exist is the one thing somebody setting a key for the first time
// cannot guess, and a mistyped one would otherwise be stored where nothing
// reads it without a word.
func TestKeysSaysWhichAgentsTakeAKey(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "sk-x\n", "set"); err == nil || !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("keys set with no agent = %v, want the ids listed", err)
	}
	_, err := runKeysCmd(t, "sk-x\n", "set", "anthropc")
	if err == nil || !strings.Contains(err.Error(), "no agent called anthropc") || !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("a mistyped id = %v, want it refused with the ids that take a key", err)
	}
	if creds.Has("anthropc") {
		t.Error("a key was stored under a mistyped id")
	}
	if _, err := runKeysCmd(t, "sk-x\n", "set", "anthropic"); err != nil {
		t.Errorf("a known id was refused: %v", err)
	}
}

// /model lists what the pane's agent offers, which comes from the catalog.
func TestAChatIsOfferedItsAgentsModels(t *testing.T) {
	isolateKeys(t)
	var ids []string
	for _, m := range catalogModels("anthropic") {
		ids = append(ids, m.ID)
	}
	if !strings.Contains(strings.Join(ids, " "), "claude-") {
		t.Errorf("the Claude API agent offers %q", ids)
	}
	if got := catalogModels("no-such-agent"); len(got) != 0 {
		t.Errorf("an unknown agent offers %+v", got)
	}
}

// A key typed at a terminal is hidden while it is read, the terminal is put
// back afterwards, and the prompt says which of the two it is.
func TestKeysSetHidesTheKeyAtATerminal(t *testing.T) {
	isolateKeys(t)
	hidden, restored := false, false
	var prompt bytes.Buffer
	err := keysCmd([]string{"set", "anthropic"}, keysIO{
		in: strings.NewReader("sk-secret\n"), out: &bytes.Buffer{}, prompt: &prompt,
		hide: func() func() {
			hidden = true
			return func() { restored = true }
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hidden || !restored {
		t.Errorf("hidden %v, restored %v; want both", hidden, restored)
	}
	if !strings.Contains(prompt.String(), "will not be shown") {
		t.Errorf("prompt = %q", prompt.String())
	}

	prompt.Reset()
	keysCmd([]string{"set", "anthropic"}, keysIO{
		in: strings.NewReader("sk-secret\n"), out: &bytes.Buffer{}, prompt: &prompt,
		hide: func() func() { return nil },
	})
	if !strings.Contains(prompt.String(), "will be visible") {
		t.Errorf("a terminal that could not hide the key was not said to show it: %q", prompt.String())
	}
}

// Clearing a key that comes from the environment clears nothing, and the one
// thing worth telling somebody trying is where it does come from.
func TestKeysClearSaysWhereAKeyItCannotClearComesFrom(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENAI_API_KEY", "sk-exported")
	out, err := runKeysCmd(t, "", "clear", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "OPENAI_API_KEY") || strings.Contains(out, "sk-exported") {
		t.Errorf("clear said %q", out)
	}
}

// A key set while a pane is running does not reach that pane at once, and
// somebody setting one needs to know when it will.
func TestKeysSetSaysWhenTheKeyIsUsed(t *testing.T) {
	isolateKeys(t)
	out, err := runKeysCmd(t, "sk-new\n", "set", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already running") {
		t.Errorf("keys set did not say when the key takes effect:\n%s", out)
	}
}

// A stored key reaches the pane in its environment; the variable carrying it
// is found so that commands the model runs do not inherit it. A key the user
// exported themselves, and nothing stored, is left alone.
func TestTheVariableCarryingAStoredKeyIsFound(t *testing.T) {
	isolateKeys(t)
	if got := storedKeyVars("anthropic"); got != nil {
		t.Errorf("found %q with nothing stored", got)
	}
	if err := creds.Set("anthropic", "sk-stored"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-stored")
	t.Setenv("OPENAI_API_KEY", "sk-users-own")
	got := strings.Join(storedKeyVars("anthropic"), " ")
	if !strings.Contains(got, "ANTHROPIC_API_KEY") || strings.Contains(got, "OPENAI_API_KEY") {
		t.Errorf("storedKeyVars = %q, want the variable carrying the stored key and no other", got)
	}
}

// Git Bash hands a program a pipe named after the pty behind it, and that name
// is what tells a person typing from a script piping a key in.
func TestAnMsysPtyIsToldFromAScriptsPipe(t *testing.T) {
	for name, want := range map[string]bool{
		`\msys-dd50a72ab4668b33-pty1-to-master`:   true,
		`\msys-dd50a72ab4668b33-pty0-from-master`: true,
		`\cygwin-e022582115c10879-pty4-to-master`: true,
		`\msys-dd50a72ab4668b33-pipe-0x1`:         false,
		`\mypipe`:                                 false,
		`C:\Users\someone\keys.txt`:               false,
	} {
		if got := isMsysPtyName(name); got != want {
			t.Errorf("isMsysPtyName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestKeysUsage(t *testing.T) {
	isolateKeys(t)
	for _, args := range [][]string{nil, {"-h"}, {"help"}} {
		out, err := runKeysCmd(t, "", args...)
		if err != nil {
			t.Fatalf("keys %v: %v", args, err)
		}
		if !strings.Contains(out, "flockdeck keys") {
			t.Errorf("keys %v printed no usage: %q", args, out)
		}
	}
	if out, err := runKeysCmd(t, "", "frobnicate"); err == nil {
		t.Errorf("an unknown command succeeded: %q", out)
	}
	// The bare command answers the question it is usually typed to ask.
	if out, _ := runKeysCmd(t, ""); !strings.Contains(out, "anthropic") || !strings.Contains(out, "not set") {
		t.Errorf("the bare command does not say which agents have a key:\n%s", out)
	}
}

// An agent is pointed at another address without agents.json being opened,
// and only the address changes: the wire, the names its key arrives in, and
// whatever else the file holds are left as they were.
func TestKeysEndpointChangesOnlyTheAddress(t *testing.T) {
	isolateKeys(t)
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"theme":"dark","agents":[`+
		`{"id":"anthropic","name":"Mine"},`+
		`{"id":"custom","name":"Custom","api":{"wire":"openai","baseURL":"http://10.0.0.1/v1"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := keysAgent(t, "anthropic")

	if out, err := runKeysCmd(t, "", "endpoint", "anthropic", "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("keys endpoint: %v\n%s", err, out)
	}
	after := keysAgent(t, "anthropic")
	if after.API.BaseURL != "http://127.0.0.1:8080" || after.API.Wire != before.API.Wire ||
		!slices.Equal(after.API.KeyEnv, before.API.KeyEnv) || after.Name != "Mine" {
		t.Errorf("anthropic is now %+v (was %+v)", after, before)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"theme"`) || !strings.Contains(string(data), "http://10.0.0.1/v1") {
		t.Errorf("the rest of agents.json was not kept:\n%s", data)
	}
	if out, _ := runKeysCmd(t, "", "endpoint", "anthropic"); !strings.Contains(out, "http://127.0.0.1:8080") {
		t.Errorf("the address is not shown: %q", out)
	}

	if out, err := runKeysCmd(t, "", "endpoint", "anthropic", "default"); err != nil {
		t.Fatalf("keys endpoint default: %v\n%s", err, out)
	}
	if got := keysAgent(t, "anthropic").API.BaseURL; got != before.API.BaseURL {
		t.Errorf("default left the address at %q, want %q", got, before.API.BaseURL)
	}

	for _, args := range [][]string{
		{"endpoint", "nosuch", "http://127.0.0.1"},
		{"endpoint", "anthropic", "ftp://127.0.0.1"},
		{"endpoint", "anthropic", "127.0.0.1:8080"},
		{"endpoint", "custom", "default"},
	} {
		if out, err := runKeysCmd(t, "", args...); err == nil {
			t.Errorf("keys %v succeeded: %q", args, out)
		}
	}
	// A password in the address is refused without being repeated.
	if _, err := runKeysCmd(t, "", "endpoint", "anthropic", "https://me:hunter2@example.com"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("an address with a password in it: %v", err)
	}
}

// Each entry for an agent is merged over the one before, so an address an
// earlier entry gives is the one used unless a later entry changes it. Going
// back to the vendor's endpoint took the address out of the last entry alone,
// said the vendor's was back, and left panes talking to the earlier address.
func TestKeysEndpointDefaultClearsEveryEntryForTheAgent(t *testing.T) {
	isolateKeys(t)
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"agents":[`+
		`{"id":"anthropic","api":{"baseURL":"http://10.0.0.9/v1"}},`+
		`{"id":"anthropic","name":"Mine"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := keysAgent(t, "anthropic").API.BaseURL; got != "http://10.0.0.9/v1" {
		t.Fatalf("the earlier entry's address is not in use to begin with: %q", got)
	}
	if out, err := runKeysCmd(t, "", "endpoint", "anthropic", "default"); err != nil {
		t.Fatalf("keys endpoint default: %v\n%s", err, out)
	}
	after := keysAgent(t, "anthropic")
	if after.API.BaseURL == "http://10.0.0.9/v1" {
		t.Errorf("default said the vendor's endpoint was back, and panes still use %q", after.API.BaseURL)
	}
	if after.Name != "Mine" {
		t.Errorf("the rest of the entries was not kept: %+v", after)
	}
}

// A key in the environment is used before a stored one, and storing a key
// while one is there says so, naming the variable and never either key.
func TestKeysSetSaysWhenTheEnvironmentWins(t *testing.T) {
	isolateKeys(t)
	name := keysAgent(t, "anthropic").API.KeyEnv[0]
	t.Setenv(name, "sk-from-the-environment")
	out, err := runKeysCmd(t, "sk-just-stored\n", "set", "anthropic")
	if err != nil {
		t.Fatalf("keys set: %v", err)
	}
	if !strings.Contains(out, name+" is set in this environment") || !strings.Contains(out, "unset "+name) {
		t.Errorf("the variable that wins is not named:\n%s", out)
	}
	if strings.Contains(out, "new panes use it") {
		t.Errorf("says new panes use a key they will not:\n%s", out)
	}
	if strings.Contains(out, "sk-from-the-environment") || strings.Contains(out, "sk-just-stored") {
		t.Errorf("a key was printed:\n%s", out)
	}
}

// The listing says where an agent talks to, where that is not its vendor --
// and says it once, for an agent that needs no key because the address is on
// this machine.
func TestKeysListSaysWhereAnAgentTalksTo(t *testing.T) {
	isolateKeys(t)
	anthropicLine := func() string {
		t.Helper()
		out, err := runKeysCmd(t, "", "list")
		if err != nil {
			t.Fatal(err)
		}
		found := ""
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "anthropic ") {
				found = line
			} else if strings.Contains(line, "talks to http") {
				t.Errorf("an agent on its vendor's endpoint is listed as %q", line)
			}
		}
		return found
	}

	if _, err := runKeysCmd(t, "", "endpoint", "anthropic", "https://gateway.example/v1"); err != nil {
		t.Fatal(err)
	}
	if line := anthropicLine(); !strings.Contains(line, "; talks to https://gateway.example/v1") {
		t.Errorf("anthropic is listed as %q", line)
	}

	if _, err := runKeysCmd(t, "", "endpoint", "anthropic", "http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	line := anthropicLine()
	if !strings.Contains(line, "on this machine, at http://127.0.0.1:8080") || strings.Count(line, "talks to") != 1 {
		t.Errorf("anthropic is listed as %q", line)
	}
}

// With somebody at the terminal, a key is checked with the agent's endpoint as
// it is stored, and whether it was taken is said -- without the key.
func TestKeysSetChecksTheKeyWithTheEndpoint(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key sk-bad-key"}}`))
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	if _, err := runKeysCmd(t, "", "endpoint", "anthropic", srv.URL); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"sk-bad-key": "refused", "sk-good-key": "accepted"} {
		var out, prompt bytes.Buffer
		if err := keysCmd([]string{"set", "anthropic"}, keysIO{in: strings.NewReader(key + "\n"), out: &out, prompt: &prompt}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s: the check was not said:\n%s", key, out.String())
		}
		// New panes are said to use a key only where the API did not refuse it.
		if refused, says := key == "sk-bad-key", strings.Contains(out.String(), "new panes use it"); refused == says {
			t.Errorf("%s: 'new panes use it' said: %v, after the API %s it:\n%s", key, says, want, out.String())
		}
		if strings.Contains(out.String()+prompt.String(), key) {
			t.Errorf("%s: the key was printed:\n%s", key, out.String())
		}
	}
}

// An endpoint that cannot be reached to check a key with is said to be that,
// in the address's words rather than a failed dial's, and the key is kept.
func TestKeysSetSaysWhenTheEndpointCannotBeReachedToCheck(t *testing.T) {
	isolateKeys(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "http://" + ln.Addr().String()
	ln.Close()
	if _, err := runKeysCmd(t, "", "endpoint", "anthropic", addr); err != nil {
		t.Fatal(err)
	}
	var out, prompt bytes.Buffer
	if err := keysCmd([]string{"set", "anthropic"}, keysIO{in: strings.NewReader("sk-some-key\n"), out: &out, prompt: &prompt}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "could not reach "+addr+" to check it; it is stored") {
		t.Errorf("the unreachable endpoint was not said:\n%s", out.String())
	}
	if strings.Contains(out.String(), "dial tcp") || strings.Contains(out.String(), "connectex") ||
		strings.Contains(out.String(), "sk-some-key") {
		t.Errorf("the dial error or the key was printed:\n%s", out.String())
	}
	if got := creds.Resolve(keysAgent(t, "anthropic")).Secret(); got != "sk-some-key" {
		t.Error("the key was not kept")
	}
}

// keys check asks the endpoint about the key a pane would use, says where it
// comes from, and never shows it.
func TestKeysCheckAsksAboutTheKeyAPaneWouldUse(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	if _, err := runKeysCmd(t, "", "endpoint", "anthropic", srv.URL); err != nil {
		t.Fatal(err)
	}

	// The test endpoint is on this machine, which needs no key, and is said to.
	out, err := runKeysCmd(t, "", "check", "anthropic")
	if err != nil || !strings.Contains(out, "not needed") {
		t.Errorf("with no key: %v\n%s", err, out)
	}

	// The agent now talks to somebody other than Anthropic, so the variable it
	// reads is Flockdeck's own: ANTHROPIC_API_KEY, the key for Anthropic, is
	// never sent to another address.
	name := "FLOCKDECK_API_KEY"
	for key, want := range map[string]string{"sk-bad-key": "refused", "sk-good-key": "accepted"} {
		t.Setenv(name, key)
		out, err := runKeysCmd(t, "", "check", "anthropic")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, want) || !strings.Contains(out, name) || strings.Contains(out, key) {
			t.Errorf("%s from %s: %q", want, name, out)
		}
	}
}

// keys check finds the key the way a pane does. A pane sends the key stored
// for its agent before FLOCKDECK_API_KEY, which is any agent's, and a built-in
// pointed at a gateway never sends its vendor's variable. The check went by
// another order: it asked the endpoint about FLOCKDECK_API_KEY -- which it
// takes, here -- while the pane went on sending the stored key, refused.
func TestKeysCheckFindsTheKeyTheWayAPaneDoes(t *testing.T) {
	isolateKeys(t)
	t.Setenv("FLOCKDECK_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	if _, err := runKeysCmd(t, "", "endpoint", "anthropic", srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := runKeysCmd(t, "sk-bad-key\n", "set", "anthropic"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOCKDECK_API_KEY", "sk-good-key")
	// The vendor's variable, which an agent at a gateway is never sent.
	t.Setenv("ANTHROPIC_API_KEY", "sk-good-key")

	out, err := runKeysCmd(t, "", "check", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "refused") || !strings.Contains(out, "stored with") {
		t.Errorf("keys check did not ask about the key a pane sends, the stored one:\n%s", out)
	}
	if strings.Contains(out, "sk-good-key") || strings.Contains(out, "sk-bad-key") {
		t.Errorf("a key was printed:\n%s", out)
	}
}
