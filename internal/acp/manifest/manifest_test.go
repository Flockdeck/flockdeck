package manifest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTheEmbeddedManifestParses(t *testing.T) {
	m := Default()
	want := []string{"gemini", "opencode"}
	got := m.IDs()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for _, a := range m.Adapters {
		if strings.Contains(strings.ToLower(a.Package), "claude") {
			t.Errorf("%s: a third-party Claude package is listed", a.ID)
		}
	}
	gem, _ := m.Lookup("gemini")
	if len(gem.Args) != 1 || gem.Args[0] != "--acp" || gem.MinVersion != "0.64.0" || gem.Pin != "" {
		t.Errorf("gemini = %+v", gem)
	}
	oc, _ := m.Lookup("opencode")
	if oc.Args[0] != "acp" {
		t.Errorf("opencode = %+v", oc)
	}
	// Claude Code's adapter is Flockdeck's own and is not offered yet.
	cl, ok := m.Lookup("claude")
	if !ok || !cl.Hidden || cl.Command != "flockdeck" || len(cl.Args) != 2 || cl.Args[0] != "acp-adapter" || cl.Args[1] != "claude" || cl.Package != "" {
		t.Errorf("claude = %+v", cl)
	}
	// So is Codex's: no package, hidden.
	cx, ok := m.Lookup("codex-go")
	if !ok || !cx.Hidden || cx.Command != "flockdeck" || len(cx.Args) != 2 || cx.Args[0] != "acp-adapter" || cx.Args[1] != "codex" || cx.Package != "" || cx.Pin != "" {
		t.Errorf("codex-go = %+v", cx)
	}
	for _, id := range m.IDs() {
		if id == "claude" || id == "codex-go" {
			t.Error("a hidden adapter is in IDs")
		}
	}
	if strings.Contains(m.Table(), "Claude") {
		t.Error("a hidden adapter is in the docs table")
	}
}

func TestRawIsACopy(t *testing.T) {
	a := Raw()
	a[0] = 'X'
	if Raw()[0] == 'X' {
		t.Error("Raw shares the embedded bytes")
	}
}

const goodAdapter = `{"id":"x","name":"X","command":"x","args":[],"minVersion":"1.0.0","testedWith":"1.0.0","testedOn":"2026-10-19"}`

func wrap(adapters ...string) string {
	return `{"manifest":1,"adapters":[` + strings.Join(adapters, ",") + `]}`
}

func TestParseRefusesWhatItShould(t *testing.T) {
	if _, err := Parse([]byte(wrap(goodAdapter))); err != nil {
		t.Fatalf("a good manifest: %v", err)
	}
	cases := map[string]string{
		"not json":            `nope`,
		"trailing text":       wrap(goodAdapter) + ` {}`,
		"wrong format":        strings.Replace(wrap(goodAdapter), `"manifest":1`, `"manifest":2`, 1),
		"no adapters":         `{"manifest":1,"adapters":[]}`,
		"unknown field":       strings.Replace(wrap(goodAdapter), `"id":"x"`, `"id":"x","surprise":1`, 1),
		"duplicate id":        wrap(goodAdapter, goodAdapter),
		"bad id":              strings.Replace(wrap(goodAdapter), `"id":"x"`, `"id":"X Y"`, 1),
		"claude":              strings.Replace(wrap(goodAdapter), `"id":"x"`, `"id":"claude"`, 1),
		"claude in id":        strings.Replace(wrap(goodAdapter), `"id":"x"`, `"id":"claude-acp"`, 1),
		"command is a path":   strings.Replace(wrap(goodAdapter), `"command":"x"`, `"command":"/usr/bin/x"`, 1),
		"no args":             strings.Replace(wrap(goodAdapter), `"args":[],`, ``, 1),
		"no version rule":     strings.Replace(wrap(goodAdapter), `"minVersion":"1.0.0",`, ``, 1),
		"range floor":         strings.Replace(wrap(goodAdapter), `"minVersion":"1.0.0"`, `"minVersion":"^1.0.0"`, 1),
		"latest":              strings.Replace(wrap(goodAdapter), `"minVersion":"1.0.0"`, `"minVersion":"latest"`, 1),
		"v prefix":            strings.Replace(wrap(goodAdapter), `"minVersion":"1.0.0"`, `"minVersion":"v1.0.0"`, 1),
		"short version":       strings.Replace(wrap(goodAdapter), `"minVersion":"1.0.0"`, `"minVersion":"1.0"`, 1),
		"pin without package": strings.Replace(wrap(goodAdapter), `"minVersion":"1.0.0"`, `"pin":"1.0.0"`, 1),
		"bad date":            strings.Replace(wrap(goodAdapter), `2026-10-19`, `19 Oct`, 1),
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A pin with its package, and a pre-release pin, are fine.
	pinned := strings.Replace(wrap(goodAdapter), `"minVersion":"1.0.0"`, `"pin":"2.2.3-preview.15","package":"@a/b"`, 1)
	if _, err := Parse([]byte(pinned)); err != nil {
		t.Errorf("a pre-release pin: %v", err)
	}
}

func TestVersions(t *testing.T) {
	good := map[string]string{
		"0.64.0": "0.64.0", "v20.11.1": "20.11.1", "Gemini CLI 0.64.0\n": "0.64.0",
		"2.2.3-preview.15": "2.2.3-preview.15", "1.19.3+build.5": "1.19.3", "node v24.20.0": "24.20.0",
	}
	for in, want := range good {
		v, err := ParseVersion(in)
		if err != nil || v.String() != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %s", in, v, err, want)
		}
	}
	for _, in := range []string{"", "latest", "1.2", "version one"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded", in)
		}
	}
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "10.0.0"}
	for i := range order {
		for j := range order {
			a, _ := ParseVersion(order[i])
			b, _ := ParseVersion(order[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestAccepts(t *testing.T) {
	m := Default()
	gem, _ := m.Lookup("gemini")
	// No entry is pinned today, so a pinned one is made here.
	codex := Adapter{ID: "pinned", Name: "Pinned", Command: "pinned", Args: []string{}, Package: "@scope/pinned", Pin: "2.2.2", TestedWith: "2.2.2", TestedOn: "2026-10-19"}
	cases := []struct {
		a    Adapter
		have string
		want bool
	}{
		{gem, "0.64.0", true}, {gem, "0.64.1", true}, {gem, "1.0.0", true}, {gem, "0.63.9", false}, {gem, "0.65.0-nightly.1", true}, {gem, "0.64.0-nightly.1", false},
		{codex, "2.2.2", true}, {codex, "2.2.3", false}, {codex, "2.2.1", false}, {codex, "2.2.3-preview.15", false},
	}
	for _, c := range cases {
		got, err := c.a.Accepts(c.have)
		if err != nil || got != c.want {
			t.Errorf("%s accepts %s = %v, %v; want %v", c.a.ID, c.have, got, err, c.want)
		}
	}
	if _, err := gem.Accepts("unknown"); err == nil {
		t.Error("an unreadable version was accepted")
	}
	if codex.Want() != "2.2.2" || gem.Want() != "0.64.0" {
		t.Errorf("want = %s, %s", codex.Want(), gem.Want())
	}
}

// The "tested with" table on the docs page is the manifest's table, so the two
// cannot disagree.
func TestTheDocsTableIsTheManifestsTable(t *testing.T) {
	page, err := os.ReadFile("../../../docs/acp-adapters.md")
	if err != nil {
		t.Fatal(err)
	}
	const begin, end = "<!-- adapters:begin -->\n", "<!-- adapters:end -->"
	s := string(page)
	i, j := strings.Index(s, begin), strings.Index(s, end)
	if i < 0 || j < i {
		t.Fatal("the page has no table markers")
	}
	if got, want := s[i+len(begin):j], Default().Table(); got != want {
		t.Errorf("the table on docs/acp-adapters.md is out of date. It should be:\n%s", want)
	}
}

func TestExactVersionIsSemver2(t *testing.T) {
	for _, ok := range []string{"1.2.3", "0.0.0", "1.2.3-rc.1", "1.2.3-0.alpha-1", "1.2.3+build.5", "1.2.3-x+y"} {
		if !exactVersion(ok) {
			t.Errorf("%q should be exact", ok)
		}
	}
	for _, bad := range []string{"", "v1.2.3", "01.2.3", "1.02.3", "1.2.03", "1.2", "1.2.3-", "1.2.3-.a", "1.2.3-a..b", "1.2.3-01", "1.2.3+", "1.2.3 ", " 1.2.3", "^1.2.3", "1.2.3-rc.1\n", "latest", "1.2.x", "1.2.3 || 2.0.0"} {
		if exactVersion(bad) {
			t.Errorf("%q should not be exact", bad)
		}
	}
}

func TestParseChecksTheFieldsAProcessOrNpmReads(t *testing.T) {
	base := func() Adapter {
		return Adapter{ID: "pinned", Name: "Pinned", Command: "pinned", Args: []string{}, Package: "@scope/pinned", Pin: "2.2.2", TestedWith: "2.2.2", TestedOn: "2026-10-19"}
	}
	cases := map[string]func(*Adapter){
		"package with a path":      func(a *Adapter) { a.Package = "../evil" },
		"package with a space":     func(a *Adapter) { a.Package = "a b" },
		"package as a url":         func(a *Adapter) { a.Package = "https://example.com/x.tgz" },
		"package with a version":   func(a *Adapter) { a.Package = "pkg@1.0.0" },
		"command as a path":        func(a *Adapter) { a.Command = "bin/run" },
		"command as an option":     func(a *Adapter) { a.Command = "-rf" },
		"command with a space":     func(a *Adapter) { a.Command = "a b" },
		"arg with a space":         func(a *Adapter) { a.Args = []string{"a b"} },
		"arg with a newline":       func(a *Adapter) { a.Args = []string{"a\nb"} },
		"arg with a shell char":    func(a *Adapter) { a.Args = []string{"$(x)"} },
		"claude in the id":         func(a *Adapter) { a.ID = "Claude-helper" },
		"claude in the package":    func(a *Adapter) { a.ID = "helper"; a.Package = "@zed-industries/claude-agent-acp" },
		"anthropic package":        func(a *Adapter) { a.ID = "helper"; a.Package = "@anthropic-ai/claude-code" },
		"anthropic in the package": func(a *Adapter) { a.Package = "@Anthropic-AI/sdk" },
		"claude in the name":       func(a *Adapter) { a.Name = "My CLAUDE helper" },
		"anthropic in the name":    func(a *Adapter) { a.Name = "Anthropic Agent" },
		"claude as the command":    func(a *Adapter) { a.Command = "Claude" },
		"anthropic in the command": func(a *Adapter) { a.Command = "anthropic-cli" },
		"claude in an arg":         func(a *Adapter) { a.Args = []string{"--agent=ClaudeCode"} },
		"anthropic in an arg":      func(a *Adapter) { a.Args = []string{"anthropic"} },
		"too many args":            func(a *Adapter) { a.Args = make([]string, 17) },
		"name with an escape":      func(a *Adapter) { a.Name = "x\x1b[2J" },
		"pin with a leading zero":  func(a *Adapter) { a.Pin = "02.2.2" },
	}
	for name, mut := range cases {
		a := base()
		mut(&a)
		if err := a.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := base().check(); err != nil {
		t.Errorf("the pinned entry: %v", err)
	}
}

func TestOnlyOpencodeIsRecordedAsPossiblyRunningWithoutAsking(t *testing.T) {
	// Flockdeck starts opencode to ask (see launchEnv), but a repository's own
	// agent permissions win over that, so the warning stays.
	for _, a := range Default().Adapters {
		if want := a.ID == "opencode"; a.RunsWithoutAsking != want {
			t.Errorf("%s: runsWithoutAsking = %v, want %v", a.ID, a.RunsWithoutAsking, want)
		}
	}
	m, err := Parse([]byte(wrap(strings.Replace(goodAdapter, `"args":[]`, `"args":[],"runsWithoutAsking":true`, 1))))
	if err != nil || !m.Adapters[0].RunsWithoutAsking {
		t.Errorf("the field is not read: %v, %+v", err, m)
	}
}

// launchEnv is what Flockdeck sets for an adapter: opencode is told to ask.
func TestOpencodeIsLaunchedToAskAndNoOtherAdapterSetsEnvironment(t *testing.T) {
	for _, a := range Default().Adapters {
		if a.ID != "opencode" {
			if len(a.LaunchEnv) != 0 {
				t.Errorf("%s sets environment: %v", a.ID, a.LaunchEnv)
			}
			continue
		}
		if len(a.LaunchEnv) != 1 {
			t.Fatalf("opencode launchEnv = %v, want exactly OPENCODE_CONFIG_CONTENT", a.LaunchEnv)
		}
		var cfg struct {
			Permission map[string]string `json:"permission"`
		}
		if err := json.Unmarshal([]byte(a.LaunchEnv["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
			t.Fatalf("OPENCODE_CONFIG_CONTENT is not the config object: %v", err)
		}
		if len(cfg.Permission) != 2 || cfg.Permission["bash"] != "ask" || cfg.Permission["edit"] != "ask" {
			t.Errorf("permission = %v, want bash and edit set to ask and nothing else", cfg.Permission)
		}
	}
}

func TestLaunchEnvIsChecked(t *testing.T) {
	base := func(env string) string {
		return strings.Replace(goodAdapter, `"args":[]`, `"args":[],"launchEnv":`+env, 1)
	}
	for name, env := range map[string]string{
		"lower case name":  `{"opencode_x":"1"}`,
		"flockdeck name":   `{"FLOCKDECK_TOKEN":"1"}`,
		"perch name":       `{"PERCH_TOKEN":"1"}`,
		"name with equals": `{"A=B":"1"}`,
		"empty value":      `{"A":""}`,
		"control value":    `{"A":"x\u0007y"}`,
		"long value":       `{"A":"` + strings.Repeat("x", 401) + `"}`,
		"five variables":   `{"A":"1","B":"1","C":"1","D":"1","E":"1"}`,
	} {
		if _, err := Parse([]byte(wrap(base(env)))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	m, err := Parse([]byte(wrap(base(`{"B_TWO":"2","A_ONE":"1"}`))))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Adapters[0].LaunchEnvList(); len(got) != 2 || got[0] != "A_ONE=1" || got[1] != "B_TWO=2" {
		t.Errorf("LaunchEnvList = %v, want sorted by name", got)
	}
}

// permissionProbe is how a pane checks, at launch, that opencode asks.
func TestOnlyOpencodeHasAPermissionProbeAndItNamesTheLaunchSetting(t *testing.T) {
	for _, a := range Default().Adapters {
		if a.ID != "opencode" {
			if a.PermissionProbe != nil {
				t.Errorf("%s has a permissionProbe", a.ID)
			}
			continue
		}
		p := a.PermissionProbe
		if p == nil {
			t.Fatal("opencode has no permissionProbe")
		}
		if strings.Join(p.Args, " ") != "agent list" || p.Agent != "build" || p.Setting != "OPENCODE_CONFIG_CONTENT" ||
			strings.Join(p.Tools, " ") != "bash edit" {
			t.Errorf("probe = %+v", *p)
		}
	}
}

func TestABadPermissionProbeIsRefused(t *testing.T) {
	oc, _ := Default().Lookup("opencode")
	for name, mut := range map[string]func(*PermissionProbe){
		"a setting that is not in launchEnv": func(p *PermissionProbe) { p.Setting = "OTHER" },
		"no agent":                           func(p *PermissionProbe) { p.Agent = "" },
		"an agent with a space":              func(p *PermissionProbe) { p.Agent = "build it" },
		"no args":                            func(p *PermissionProbe) { p.Args = nil },
		"seven args":                         func(p *PermissionProbe) { p.Args = make([]string, 7) },
		"an arg with a space":                func(p *PermissionProbe) { p.Args = []string{"debug agent"} },
		"an arg with a shell character":      func(p *PermissionProbe) { p.Args = []string{"debug;rm"} },
		"no tools":                           func(p *PermissionProbe) { p.Tools = nil },
		"five tools":                         func(p *PermissionProbe) { p.Tools = []string{"a", "b", "c", "d", "e"} },
		"a tool that is not a name":          func(p *PermissionProbe) { p.Tools = []string{"bash edit"} },
	} {
		a := oc
		p := *oc.PermissionProbe
		mut(&p)
		a.PermissionProbe = &p
		if err := a.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A probe on an adapter that is not flagged has nothing to replace.
	a := oc
	a.RunsWithoutAsking = false
	if err := a.check(); err == nil {
		t.Error("a probe without runsWithoutAsking was accepted")
	}
	if err := oc.check(); err != nil {
		t.Errorf("the opencode entry: %v", err)
	}
}

// Flockdeck's own adapter may name the agent it drives; nothing else may.
func TestOnlyFlockdecksOwnAdapterMayNameClaude(t *testing.T) {
	own := `{"id":"claude","name":"Claude Code","command":"flockdeck","args":["acp-adapter","claude"],"drives":"claude","minVersion":"2.1.311","testedWith":"2.1.311","testedOn":"2026-10-27","hidden":true,"note":"n"}`
	if _, err := Parse([]byte(wrap(goodAdapter, own))); err != nil {
		t.Fatalf("the own adapter: %v", err)
	}
	for name, bad := range map[string]string{
		"not hidden":      strings.Replace(own, `"hidden":true,`, ``, 1),
		"another command": strings.Replace(own, `"command":"flockdeck"`, `"command":"claude-acp"`, 1),
		"an npm package":  strings.Replace(own, `"hidden":true`, `"hidden":true,"package":"@zed-industries/claude-agent-acp"`, 1),
		"a pin":           strings.Replace(strings.Replace(own, `"minVersion":"2.1.311"`, `"pin":"2.1.311"`, 1), `"hidden":true`, `"hidden":true,"package":"x"`, 1),
		"other args":      strings.Replace(own, `"args":["acp-adapter","claude"]`, `"args":["hook","claude"]`, 1),
		"extra args":      strings.Replace(own, `"args":["acp-adapter","claude"]`, `"args":["acp-adapter","claude","--x"]`, 1),
		"another id":      strings.Replace(own, `"id":"claude"`, `"id":"claude-own"`, 1),
		"a launch env":    strings.Replace(own, `"hidden":true,`, `"hidden":true,"runsWithoutAsking":true,"launchEnv":{"X":"1"},"permissionProbe":{"args":["a"],"agent":"b","setting":"X","tools":["t"]},`, 1),
	} {
		if _, err := Parse([]byte(wrap(goodAdapter, bad))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// drives is for Flockdeck's own adapters only: a command other than flockdeck,
// or a program name that is not plain, is refused.
func TestDrivesIsForOwnAdaptersOnly(t *testing.T) {
	own := `{"id":"codex-go","name":"Codex","command":"flockdeck","args":["acp-adapter","codex"],"drives":"codex","minVersion":"0.0.0","testedWith":"0.0.0","testedOn":"2026-10-27","hidden":true,"note":"n"}`
	if _, err := Parse([]byte(wrap(goodAdapter, own))); err != nil {
		t.Fatalf("the own adapter: %v", err)
	}
	for name, bad := range map[string]string{
		"another command": strings.Replace(own, `"command":"flockdeck"`, `"command":"other"`, 1),
		"a path":          strings.Replace(own, `"drives":"codex"`, `"drives":"../codex"`, 1),
		"a space":         strings.Replace(own, `"drives":"codex"`, `"drives":"co dex"`, 1),
	} {
		if _, err := Parse([]byte(wrap(goodAdapter, bad))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	m := Default()
	for id, want := range map[string]string{"claude": "claude", "codex-go": "codex"} {
		a, ok := m.Lookup(id)
		if !ok || a.Drives != want {
			t.Errorf("%s drives %q, want %q", id, a.Drives, want)
		}
	}
}

// Only Flockdeck's own adapters for claude and codex are believed when they say
// they can rewind.
func TestOwnRewindIsTheTwoOwnAdaptersOnly(t *testing.T) {
	m := Default()
	for id, want := range map[string]bool{"claude": true, "codex-go": true, "opencode": false} {
		a, ok := m.Lookup(id)
		if !ok {
			t.Fatalf("%s is not in the manifest", id)
		}
		if got := OwnRewind(a); got != want {
			t.Errorf("OwnRewind(%s) = %v, want %v", id, got, want)
		}
	}
	a, _ := m.Lookup("codex-go")
	for name, mod := range map[string]func(*Adapter){
		"another command":  func(a *Adapter) { a.Command = "other" },
		"another argument": func(a *Adapter) { a.Args = []string{"acp-adapter", "claude"} },
		"a package":        func(a *Adapter) { a.Package = "@x/y" },
		"launch env":       func(a *Adapter) { a.LaunchEnv = map[string]string{"A": "b"} },
		"another id":       func(a *Adapter) { a.ID = "codex-other" },
	} {
		b := a
		mod(&b)
		if OwnRewind(b) {
			t.Errorf("%s: believed", name)
		}
	}
}

// OwnAdapter is the phone approval's test of an option id: the same two adapters
// as OwnRewind, in the same exact shapes.
func TestOwnAdapterIsTheTwoOwnAdaptersOnly(t *testing.T) {
	m := Default()
	for id, want := range map[string]bool{"claude": true, "codex-go": true, "opencode": false, "gemini": false} {
		a, ok := m.Lookup(id)
		if !ok {
			t.Fatalf("%s is not in the manifest", id)
		}
		if got := OwnAdapter(a); got != want {
			t.Errorf("OwnAdapter(%s) = %v, want %v", id, got, want)
		}
	}
	for _, id := range []string{"claude", "codex-go"} {
		a, _ := m.Lookup(id)
		for name, mod := range map[string]func(*Adapter){
			"another command":  func(a *Adapter) { a.Command = "other" },
			"another argument": func(a *Adapter) { a.Args = []string{"acp-adapter", "gemini"} },
			"a package":        func(a *Adapter) { a.Package = "@x/y" },
			"a pin":            func(a *Adapter) { a.Pin = "1.0.0" },
			"launch env":       func(a *Adapter) { a.LaunchEnv = map[string]string{"A": "b"} },
			"a probe":          func(a *Adapter) { a.PermissionProbe = &PermissionProbe{} },
			"another id":       func(a *Adapter) { a.ID = id + "-other" },
			"not drives":       func(a *Adapter) { a.Drives = "" },
			"not hidden":       func(a *Adapter) { a.Hidden = false },
		} {
			b := a
			mod(&b)
			if OwnAdapter(b) {
				t.Errorf("%s, %s: believed", id, name)
			}
		}
	}
}
