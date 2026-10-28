package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

func raw(s string) []json.RawMessage { return []json.RawMessage{json.RawMessage(s)} }

// optedIn is a catalog with the beta turned on, as the workspace builds it from
// the preferences.
func optedIn(f *File) *Catalog {
	c := Merge(f)
	c.Structured = true
	return c
}

func ids(specs []Spec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}

// The structured entries ship: opencode listed once opted in, Gemini, Codex and
// Claude hidden.
func TestBuiltinStructuredEntries(t *testing.T) {
	c := optedIn(&File{})
	if s, ok := c.Find("opencode-acp"); !ok || s.Runner != RunnerACP || s.ACP.Adapter != "opencode" || s.ACP.Auth != AuthNone {
		t.Fatalf("opencode-acp = %+v, %v", s, ok)
	}
	for _, id := range []string{"gemini-acp", "codex-acp", "claude-acp"} {
		s, ok := c.Find(id)
		if !ok || !s.Hidden || s.Runner != RunnerACP {
			t.Errorf("%s should exist and be hidden: %+v, %v", id, s, ok)
		}
		if slices.Contains(ids(c.Visible()), id) {
			t.Errorf("%s is offered although it is hidden", id)
		}
	}
	// Claude's entry starts Flockdeck's own adapter and no other, and is the
	// only structured entry that names Claude.
	for _, s := range c.Specs {
		if s.Runner == RunnerACP && s.ID != "claude-acp" && (strings.Contains(s.ID, "claude") || strings.Contains(s.ACP.Adapter, "claude")) {
			t.Errorf("%s: only claude-acp is a structured Claude entry", s.ID)
		}
	}
}

// claude-acp is startable by id (a saved layout and the canary name it) though
// it is not listed, starts the hidden adapter `claude`, sends the Flockdeck MCP
// server, and passes only the login locations of the environment.
func TestClaudeACPEntry(t *testing.T) {
	c := optedIn(&File{})
	s, ok := c.Find("claude-acp")
	if !ok {
		t.Fatal("claude-acp is not in the catalog")
	}
	if s.Runner != RunnerACP || s.ACP.Adapter != "claude" || s.ACP.Auth != AuthExternal || !s.ACP.MCP {
		t.Errorf("claude-acp = %+v", s)
	}
	if !strings.Contains(s.ACP.Login, "claude") || !strings.Contains(s.ACP.Login, "API key") {
		t.Errorf("login text %q names neither the subscription login nor a key", s.ACP.Login)
	}
	if !slices.Equal(s.ACP.PassEnv, homeEnv()) {
		t.Errorf("passEnv = %v", s.ACP.PassEnv)
	}
	got := s.ACPPassEnv([]string{"HOME=/h", "ANTHROPIC_API_KEY=sk-from-the-shell", "FLOCKDECK_TOKEN=t"})
	for _, kv := range got {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "FLOCKDECK_") {
			t.Errorf("%q is passed from Flockdeck's own environment", kv)
		}
	}
	if !slices.Contains(got, "HOME=/h") {
		t.Errorf("HOME is not passed: %v", got)
	}
	if _, ok := s.ACPAdapter(); !ok {
		t.Error("the adapter is not in the manifest")
	}
	if _, ok := c.offered("claude-acp"); ok {
		t.Error("offered() accepted a hidden entry")
	}
	// The terminal Claude entry is unchanged and takes no structured key.
	cli, _ := c.Find("claude")
	if cli.Runner == RunnerACP || cli.ACPKeyEnv() != "" {
		t.Errorf("claude = %+v", cli)
	}
}

// claude-acp is available when claude is installed, and only then: the adapter
// is a subcommand of the running program, so a flockdeck on PATH proves nothing.
func TestClaudeACPAvailability(t *testing.T) {
	s, _ := Merge(&File{}).Find("claude-acp")
	stubProbes(t, map[string]bool{"flockdeck": true})
	if Available(s) {
		t.Error("available with flockdeck on PATH and no claude")
	}
	stubProbes(t, map[string]bool{"claude": true})
	if !Available(s) {
		t.Error("unavailable with claude on PATH and no flockdeck on PATH")
	}
	// Neither one stands in for the other.
	cx, _ := Merge(&File{}).Find("codex-acp")
	stubProbes(t, map[string]bool{"claude": true})
	if Available(cx) {
		t.Error("codex-acp became available because claude is installed")
	}
	stubProbes(t, map[string]bool{"codex": true})
	if Available(s) {
		t.Error("claude-acp became available because codex is installed")
	}
}

// codex-acp is available when codex is installed, and only then, for the same
// reason: its command is the flockdeck binary.
func TestCodexACPAvailability(t *testing.T) {
	s, _ := Merge(&File{}).Find("codex-acp")
	stubProbes(t, map[string]bool{"flockdeck": true})
	if Available(s) {
		t.Error("available with flockdeck on PATH and no codex")
	}
	stubProbes(t, map[string]bool{"codex": true})
	if !Available(s) {
		t.Error("unavailable with codex on PATH and no flockdeck on PATH")
	}
}

// The hidden rule: structured entries are not offered unless the user opted in,
// and they are still found by id, since a saved layout names them.
func TestStructuredEntriesAreListedOnlyOnOptIn(t *testing.T) {
	off := Merge(&File{})
	if slices.Contains(ids(off.Visible()), "opencode-acp") {
		t.Error("opencode-acp is offered without the opt-in")
	}
	if _, ok := off.Find("opencode-acp"); !ok {
		t.Error("opencode-acp should still be found by id")
	}
	if _, ok := off.offered("opencode-acp"); ok {
		t.Error("offered() should refuse a structured entry without the opt-in")
	}
	on := optedIn(&File{})
	if !slices.Contains(ids(on.Visible()), "opencode-acp") {
		t.Error("opencode-acp should be offered with the opt-in")
	}
	// The terminal opencode is not touched by either.
	for _, c := range []*Catalog{off, on} {
		if !slices.Contains(ids(c.Visible()), "opencode") {
			t.Error("the terminal opencode entry should always be offered")
		}
	}
}

// agents.json does not carry the opt-in: a copy of it from another machine
// cannot turn the beta on.
func TestAgentsJSONCannotSwitchTheBetaOn(t *testing.T) {
	var f File
	if err := json.Unmarshal([]byte(`{"version":1,"structuredPanes":true}`), &f); err != nil {
		t.Fatal(err)
	}
	if Merge(&f).Structured {
		t.Fatal("the opt-in was read from agents.json")
	}
	if _, kept := f.Extra["structuredPanes"]; !kept {
		t.Error("an unknown key should still be kept as an extra")
	}
}

// A user's catalog can carry an ACP entry of its own, and can edit a built-in
// one field by field.
func TestACPEntryParseAndMerge(t *testing.T) {
	c := optedIn(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id":"mine","name":"Mine","runner":"ACP","acp":{"adapter":"gemini","auth":"none","passEnv":["HOME","MY_VAR"]}}`),
		json.RawMessage(`{"id":"opencode-acp","acp":{"passEnv":["HOME"]}}`),
	}})
	if c.Notice != "" {
		t.Fatalf("notice: %s", c.Notice)
	}
	mine, ok := c.Find("mine")
	if !ok || mine.Runner != RunnerACP || mine.ACP.Adapter != "gemini" || mine.ACP.Auth != AuthNone || !slices.Equal(mine.ACP.PassEnv, []string{"HOME", "MY_VAR"}) {
		t.Fatalf("mine = %+v, %v", mine, ok)
	}
	if mine.Exe != "" {
		t.Errorf("a structured entry has no Exe, got %q", mine.Exe)
	}
	oc, _ := c.Find("opencode-acp")
	if oc.ACP.Adapter != "opencode" || oc.ACP.Auth != AuthNone || !oc.ACP.MCP {
		t.Errorf("the merge lost the built-in's fields: %+v", oc.ACP)
	}
	if !slices.Equal(oc.ACP.PassEnv, []string{"HOME"}) {
		t.Errorf("the entry's list should replace the built-in's, got %v", oc.ACP.PassEnv)
	}
	// The built-in itself is not changed by the edit.
	if fresh := opencodeACPSpec(); len(fresh.ACP.PassEnv) != len(homeEnv()) {
		t.Errorf("an edit reached the shipped list: %v", fresh.ACP.PassEnv)
	}
}

// An entry that names a runner nobody has, or an adapter the manifest lacks, is
// refused with a message that says why.
func TestUnknownRunnerAndAdapterAreRefused(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id":"a","runner":"mcp"}`),
		json.RawMessage(`{"id":"b","runner":"acp","acp":{"adapter":"nothing"}}`),
		json.RawMessage(`{"id":"c","runner":"acp"}`),
		json.RawMessage(`{"id":"opencode-acp","acp":{"adapter":"nothing"}}`),
	}})
	for _, want := range []string{
		`runner "mcp" is none of "cli" and "acp"`,
		`"nothing" is not an adapter this build knows`,
		`agent "c": a structured agent needs`,
	} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should say %s", c.Notice, want)
		}
	}
	if _, ok := c.Find("b"); ok {
		t.Error("an entry naming an unknown adapter should not be added")
	}
	if oc, _ := c.Find("opencode-acp"); oc.ACP.Adapter != "opencode" {
		t.Errorf("a bad edit should leave the built-in as it was: %+v", oc.ACP)
	}
}

// The environment is exactly what the entry lists, from what is there, and
// never a FLOCKDECK_ variable, whatever the entry says.
func TestACPEnvironmentIsExactlyTheAllowlist(t *testing.T) {
	environ := []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=secret", "FLOCKDECK_TOKEN=tok", "APPDATA=C:\\a", "BROKEN"}
	s := Spec{ACP: ACPSpec{PassEnv: []string{"HOME", "APPDATA"}}}
	got := s.ACPPassEnv(environ)
	if !slices.Equal(got, []string{"HOME=/h", "APPDATA=C:\\a"}) {
		t.Errorf("got %v", got)
	}
	if len(Spec{}.ACPPassEnv(environ)) != 0 {
		t.Error("an entry that lists nothing passes nothing")
	}

	c := Merge(&File{Agents: raw(`{"id":"x","runner":"acp","acp":{"adapter":"opencode","passEnv":["HOME","FLOCKDECK_TOKEN","flockdeck_relay","PERCH_TOKEN","perch_api","A B","1X"]}}`)})
	x, _ := c.Find("x")
	if !slices.Equal(x.ACP.PassEnv, []string{"HOME"}) {
		t.Errorf("PassEnv = %v", x.ACP.PassEnv)
	}
	for _, want := range []string{`"FLOCKDECK_TOKEN" is dropped`, `"flockdeck_relay" is dropped`, `"PERCH_TOKEN" is dropped`, `"perch_api" is dropped`, `"A B" is not a variable name`, `"1X" is not a variable name`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should say %s", c.Notice, want)
		}
	}
	for _, s := range Builtins() {
		if s.Runner != RunnerACP {
			continue
		}
		for _, name := range s.ACP.PassEnv {
			up := strings.ToUpper(name)
			if strings.HasPrefix(up, "FLOCKDECK_") || strings.HasPrefix(up, "PERCH_") || strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") {
				t.Errorf("%s passes %s", s.ID, name)
			}
		}
	}
}

// A catalog written before structured entries existed loads as it did.
func TestAnOldCatalogLoadsUnchanged(t *testing.T) {
	old := &File{Version: 1, Defaults: Defaults{Agent: "codex"}, Agents: raw(`{"id":"codex","defaultModel":"gpt-5"}`)}
	c := Merge(old)
	if c.Notice != "" || c.Structured {
		t.Fatalf("notice %q, structured %v", c.Notice, c.Structured)
	}
	for _, id := range ids(c.Visible()) {
		if s, _ := c.Find(id); s.Runner == RunnerACP {
			t.Errorf("%s is structured but was offered without the opt-in", id)
		}
	}
	if s, _ := c.Find("codex"); s.Runner != RunnerCLI || s.DefaultModel != "gpt-5" {
		t.Errorf("codex = %+v", s)
	}
}

// Availability of a structured entry is whether the manifest's command is on PATH.
func TestStructuredAvailabilityFollowsTheManifestCommand(t *testing.T) {
	var asked string
	old := lookPath
	lookPath = func(name string) (string, error) {
		asked = name
		return "", errors.New("not found")
	}
	defer func() { lookPath = old }()
	if probe(opencodeACPSpec()) {
		t.Error("available although nothing is installed")
	}
	if asked != "opencode" {
		t.Errorf("looked for %q, want the manifest's command", asked)
	}
}

// ACPPassEnv holds the line itself: a Spec built any other way than through
// the catalog is still refused the reserved names, whatever their case.
func TestACPPassEnvEnforcesTheRulesOnItsOwn(t *testing.T) {
	environ := []string{"HOME=/h", "FLOCKDECK_TOKEN=a", "PERCH_TOKEN=b", "perch_api=c", "Flockdeck_x=d", "ANTHROPIC_API_KEY=k", "PATH=/bin"}
	s := Spec{Runner: RunnerACP, ACP: ACPSpec{Adapter: "opencode", PassEnv: []string{"HOME", "FLOCKDECK_TOKEN", "PERCH_TOKEN", "perch_api", "Flockdeck_x", "PATH"}}}
	got := s.ACPPassEnv(environ)
	if !slices.Equal(got, []string{"HOME=/h"}) {
		t.Errorf("got %v, want only HOME (PATH is MinimalEnv's)", got)
	}
}

// Case counts on Linux and macOS and not on Windows.
func TestACPPassEnvCaseFollowsThePlatform(t *testing.T) {
	s := Spec{ACP: ACPSpec{PassEnv: []string{"path_extra", "Home"}}}
	got := s.ACPPassEnv([]string{"PATH_EXTRA=1", "HOME=/h", "Home=/w"})
	want := []string{"Home=/w"}
	if runtime.GOOS == "windows" {
		want = []string{"PATH_EXTRA=1", "HOME=/h", "Home=/w"}
	}
	if !slices.Equal(got, want) {
		t.Errorf("on %s got %v, want %v", runtime.GOOS, got, want)
	}
}

// Names that MinimalEnv already gives are not passed again, and the list here
// is MinimalEnv's own.
func TestACPPassEnvSkipsWhatMinimalEnvGives(t *testing.T) {
	for _, n := range minimalEnvNames {
		t.Setenv(n, "x")
	}
	var fromAcp []string
	for _, kv := range acp.MinimalEnv() {
		name, _, _ := strings.Cut(kv, "=")
		fromAcp = append(fromAcp, name)
	}
	slices.Sort(fromAcp)
	have := slices.Clone(minimalEnvNames)
	slices.Sort(have)
	// On Windows the two spellings of SystemRoot are one variable.
	if runtime.GOOS != "windows" && !slices.Equal(fromAcp, slices.Compact(have)) {
		t.Errorf("acp.MinimalEnv gives %v, minimalEnvNames is %v", fromAcp, have)
	}
	s := Spec{ACP: ACPSpec{PassEnv: minimalEnvNames}}
	if got := s.ACPPassEnv(os.Environ()); len(got) != 0 {
		t.Errorf("passed %v", got)
	}
}

func TestPassEnvIsCapped(t *testing.T) {
	var names []string
	for i := 0; i < maxPassEnv+10; i++ {
		names = append(names, fmt.Sprintf("V%d", i))
	}
	entry, _ := json.Marshal(map[string]any{"id": "big", "runner": "acp", "acp": map[string]any{"adapter": "opencode", "passEnv": names}})
	c := Merge(&File{Agents: raw(string(entry))})
	big, _ := c.Find("big")
	if len(big.ACP.PassEnv) != maxPassEnv || !strings.Contains(c.Notice, "at most 64 names") {
		t.Errorf("kept %d, notice %q", len(big.ACP.PassEnv), c.Notice)
	}
	// And in the function itself, for a Spec made another way.
	var environ []string
	for _, n := range names {
		environ = append(environ, n+"=1")
	}
	if got := (Spec{ACP: ACPSpec{PassEnv: names}}).ACPPassEnv(environ); len(got) != maxPassEnv {
		t.Errorf("ACPPassEnv read %d names", len(got))
	}
}

// The login advice is shown to the person, so it is cleaned.
func TestLoginAdviceIsCleaned(t *testing.T) {
	dirty := "run \x1b[2Jthis\x07\nnow " + strings.Repeat("x", 1<<20)
	entry, _ := json.Marshal(map[string]any{"id": "l", "runner": "acp", "acp": map[string]any{"adapter": "opencode", "login": dirty}})
	c := Merge(&File{Agents: raw(string(entry))})
	l, _ := c.Find("l")
	if strings.ContainsAny(l.ACP.Login, "\x1b\x07\n") || len([]rune(l.ACP.Login)) > 300 {
		t.Errorf("login has a control character or is %d runes", len([]rune(l.ACP.Login)))
	}
	if !strings.HasPrefix(l.ACP.Login, "run [2Jthisnow ") || !strings.Contains(c.Notice, "login was cleaned") {
		t.Errorf("login starts %q, notice %q", l.ACP.Login[:20], c.Notice)
	}
	// Advice that needs nothing done is left alone, with no notice.
	if g := Merge(&File{}); g.Notice != "" {
		t.Errorf("the built-ins gave a notice: %s", g.Notice)
	}
}

// The authentication mode is settled into one of two.
func TestAuthModeIsSettled(t *testing.T) {
	for _, tc := range []struct{ in, want, notice string }{
		{`""`, AuthExternal, ""},
		{`"EXTERNAL "`, AuthExternal, ""},
		{`" None"`, AuthNone, ""},
		{`"bogus"`, AuthExternal, `acp auth "bogus" is neither`},
	} {
		c := Merge(&File{Agents: raw(`{"id":"a","runner":"acp","acp":{"adapter":"opencode","auth":` + tc.in + `}}`)})
		a, _ := c.Find("a")
		if a.ACP.Auth != tc.want {
			t.Errorf("auth %s became %q, want %q", tc.in, a.ACP.Auth, tc.want)
		}
		if tc.notice == "" && c.Notice != "" || tc.notice != "" && !strings.Contains(c.Notice, tc.notice) {
			t.Errorf("auth %s: notice %q, want %q", tc.in, c.Notice, tc.notice)
		}
	}
}

// Format characters are removed from the login advice too: a bidi override
// would show the advice in an order other than the one written.
func TestLoginAdviceLosesFormatCharacters(t *testing.T) {
	entry, _ := json.Marshal(map[string]any{"id": "f", "runner": "acp", "acp": map[string]any{
		"adapter": "opencode", "login": "run " + string(rune(0x202e)) + "txet" + string(rune(0x202c)) + " the" + string(rune(0x200b)) + "command" + string(rune(0xfeff))}})
	c := Merge(&File{Agents: raw(string(entry))})
	f, _ := c.Find("f")
	if f.ACP.Login != "run txet thecommand" {
		t.Errorf("login = %q", f.ACP.Login)
	}
	if !strings.Contains(c.Notice, "login was cleaned") {
		t.Errorf("notice %q", c.Notice)
	}
}

// The same name in two cases: both pass on Windows, only the exact one
// elsewhere.
func TestACPPassEnvTwoCasesOfOneName(t *testing.T) {
	environ := []string{"HOME=a", "Home=b"}
	lower := Spec{ACP: ACPSpec{PassEnv: []string{"home"}}}.ACPPassEnv(environ)
	exact := Spec{ACP: ACPSpec{PassEnv: []string{"HOME"}}}.ACPPassEnv(environ)
	if runtime.GOOS == "windows" {
		if !slices.Equal(lower, environ) || !slices.Equal(exact, environ) {
			t.Errorf("on Windows: lower %v, exact %v, want both to pass both", lower, exact)
		}
		return
	}
	if len(lower) != 0 || !slices.Equal(exact, []string{"HOME=a"}) {
		t.Errorf("on %s: lower %v, exact %v", runtime.GOOS, lower, exact)
	}
}

func TestASpecKnowsFromTheManifestThatItRunsWithoutAsking(t *testing.T) {
	got := map[string]bool{}
	for _, s := range Builtins() {
		got[s.ID] = s.RunsWithoutAsking()
	}
	// opencode is started to ask, but a repository's own agent permissions win
	// over that, so it is still recorded.
	if !got["opencode-acp"] || got["gemini-acp"] || got["codex-acp"] || got["claude-acp"] || got["claude"] {
		t.Errorf("runs without asking = %v", got)
	}
}

func TestASpecKnowsTheEnvironmentTheManifestSetsForItsAdapter(t *testing.T) {
	got := map[string][]string{}
	for _, s := range Builtins() {
		got[s.ID] = s.ACPLaunchEnv()
	}
	want := `OPENCODE_CONFIG_CONTENT={"permission":{"bash":"ask","edit":"ask"}}`
	if len(got["opencode-acp"]) != 1 || got["opencode-acp"][0] != want {
		t.Errorf("opencode-acp: %v, want %s", got["opencode-acp"], want)
	}
	for id, env := range got {
		if id != "opencode-acp" && len(env) != 0 {
			t.Errorf("%s sets %v", id, env)
		}
	}
	if env := (Spec{ID: "x", Runner: RunnerCLI}).ACPLaunchEnv(); env != nil {
		t.Errorf("a terminal entry sets %v", env)
	}
}

// keyMethod is trimmed, and dropped with a notice when it is not a plain id.
func TestKeyMethodIsValidated(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id":"x","runner":"acp","acp":{"adapter":"opencode","keyMethod":" api-key "}}`),
		json.RawMessage(`{"id":"y","runner":"acp","acp":{"adapter":"opencode","keyMethod":"Bad Method"}}`),
		json.RawMessage(`{"id":"z","runner":"acp","acp":{"adapter":"opencode","keyMethod":"` + strings.Repeat("a", 65) + `"}}`),
	}})
	if x, _ := c.Find("x"); x.ACP.KeyMethod != "api-key" {
		t.Errorf("x keyMethod = %q", x.ACP.KeyMethod)
	}
	for _, id := range []string{"y", "z"} {
		if s, _ := c.Find(id); s.ACP.KeyMethod != "" {
			t.Errorf("%s keyMethod = %q, want it dropped", id, s.ACP.KeyMethod)
		}
		if !strings.Contains(c.Notice, `agent "`+id+`": acp keyMethod`) {
			t.Errorf("notice %q does not mention %s", c.Notice, id)
		}
	}
	if g, _ := Merge(&File{}).Find("gemini-acp"); g.ACP.KeyMethod != "gemini-api-key" {
		t.Errorf("gemini-acp keyMethod = %q", g.ACP.KeyMethod)
	}
	if cx, _ := Merge(&File{}).Find("codex-acp"); !cx.ACP.MCP {
		t.Error("codex-acp takes MCP servers")
	}
}

// A Spec has shown-input keys only when the adapter it starts is one of
// Flockdeck's own; the entry's name does not give them.
func TestACPShownInputIsOwnAdaptersOnly(t *testing.T) {
	spec := func(adapter string) Spec {
		return Spec{ID: "claude", Runner: RunnerACP, ACP: ACPSpec{Adapter: adapter}}
	}
	if got := spec("claude").ACPShownInput(); len(got) != 6 || got[0] != "command" {
		t.Errorf("claude: %v", got)
	}
	if got := spec("codex-go").ACPShownInput(); len(got) != 2 || got[1] != "cwd" {
		t.Errorf("codex: %v", got)
	}
	for _, a := range []string{"opencode", "nothing", ""} {
		if got := spec(a).ACPShownInput(); got != nil {
			t.Errorf("%q: %v", a, got)
		}
	}
}
