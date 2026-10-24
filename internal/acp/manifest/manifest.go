// Package manifest is the list of structured-agent adapters this Flockdeck
// release is tested with, the versions it expects, and the checks made before
// one is started: that Node is there when the adapter needs it, and that the
// adapter is new enough.
//
// The list is adapters.json, embedded. It names exact versions on purpose: the
// adapters for Codex and others change owner and ship previews almost daily, so
// a release pins what it was tested with rather than following latest. A weekly
// job (.github/workflows/acp-canary.yml) tries the pinned versions and the
// newest preview of each against the same handshake and reports, without
// blocking a merge. There is no adapter for Claude Code.
//
// Node is not bundled. When an adapter needs it and it is missing or too old,
// CheckNode returns a *NodeRequiredError, which the caller shows as a
// "Node required" state with the fix.
package manifest

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed adapters.json
var embedded []byte

// Version is the manifest format this build reads.
const Version = 1

// Adapter is one agent Flockdeck can talk to over ACP.
type Adapter struct {
	// ID is the short name: lower case letters, digits and hyphens.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Command and Args start the adapter. Command is a name looked up on PATH.
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// Package is the npm package that provides it, if there is one.
	Package string `json:"package,omitempty"`
	// Pin is the exact version this release installs. MinVersion is the oldest
	// version it accepts, for an adapter the user installs. An adapter has at
	// least one.
	Pin        string `json:"pin,omitempty"`
	MinVersion string `json:"minVersion,omitempty"`
	NeedsNode  bool   `json:"needsNode"`
	// MinNode is the oldest Node that runs it. Required when NeedsNode is set.
	MinNode string `json:"minNode,omitempty"`
	// TestedWith and TestedOn say which version the tests last ran, and when.
	TestedWith string `json:"testedWith"`
	TestedOn   string `json:"testedOn"`
	// Install is the text shown for how to get it.
	Install string `json:"install"`
	Note    string `json:"note,omitempty"`
	// RunsWithoutAsking says the agent, started the way Flockdeck starts it,
	// runs shell commands and edits files without sending a permission request,
	// so a permission card gates nothing for it. A window warns of it for the
	// life of the pane. It is what was observed of the tested version, not a
	// promise about others.
	RunsWithoutAsking bool `json:"runsWithoutAsking,omitempty"`
	// LaunchEnv is environment Flockdeck sets for the adapter when it starts it,
	// after whatever else the agent is given, so it wins. It is how an agent that
	// takes its settings from the environment is made to ask before it acts:
	// opencode reads OPENCODE_CONFIG_CONTENT. Values are fixed text in this file,
	// never read from the user's environment.
	LaunchEnv map[string]string `json:"launchEnv,omitempty"`
	// PermissionProbe says how to find out, when a pane starts, whether the
	// launchEnv made the agent ask in this working directory. See
	// PermissionProbe. When it is set, RunsWithoutAsking holds only until that
	// check has run, and the check's answer replaces it.
	PermissionProbe *PermissionProbe `json:"permissionProbe,omitempty"`
}

// PermissionProbe is a command that lists the agents and the permissions each
// resolved, run in the pane's working directory with the adapter's environment,
// so that the user's and the project's own settings are part of what it prints.
// Flockdeck runs it without the launchEnv setting, builds a value for the setting
// from the rules of Agent that keeps the user's denials and makes every allow
// ask, then runs it again with that value to see whether any agent, the one a
// session starts in or one a task starts, still allows. The output is read by
// internal/acp/permprobe.
type PermissionProbe struct {
	// Args follow the adapter's command.
	Args []string `json:"args"`
	// Agent is the agent whose rules the value is built from: the one the user's
	// and the project's own permissions reach without an agent's own. Every agent
	// the command lists is checked afterwards.
	Agent string `json:"agent"`
	// Setting is the launchEnv variable the value is set in.
	Setting string `json:"setting"`
	// Tools are the permissions that must ask: names of the agent's tools.
	Tools []string `json:"tools"`
}

// Manifest is the list.
type Manifest struct {
	Manifest int       `json:"manifest"`
	Adapters []Adapter `json:"adapters"`
}

var (
	idRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// pkgRe is an npm package name, scoped or not. It has no slash apart from the
	// one after a scope, no leading dot and nothing a shell reads.
	pkgRe = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
	// cmdRe is a command name: letters, digits, dot, underscore, hyphen; not an
	// option.
	cmdRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	// argRe is one argument: printable, no spaces or shell characters.
	argRe  = regexp.MustCompile(`^[A-Za-z0-9._=:@/+-]+$`)
	dateRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Parse reads and checks a manifest. An unknown field, an unknown format
// version, a duplicate id, a version that is not exact, or an adapter for
// Claude Code is an error.
func Parse(data []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("adapter manifest: %w", err)
	}
	if dec.More() {
		return Manifest{}, errors.New("adapter manifest: text after the object")
	}
	if m.Manifest != Version {
		return Manifest{}, fmt.Errorf("adapter manifest: format %d, this build reads %d", m.Manifest, Version)
	}
	if len(m.Adapters) == 0 {
		return Manifest{}, errors.New("adapter manifest: no adapters")
	}
	seen := map[string]bool{}
	for i, a := range m.Adapters {
		if err := a.check(); err != nil {
			return Manifest{}, fmt.Errorf("adapter manifest: adapters[%d] %q: %w", i, a.ID, err)
		}
		if seen[a.ID] {
			return Manifest{}, fmt.Errorf("adapter manifest: adapters[%d]: id %q twice", i, a.ID)
		}
		seen[a.ID] = true
	}
	return m, nil
}

func (a Adapter) check() error {
	switch {
	case !idRe.MatchString(a.ID):
		return errors.New("id must be lower case letters, digits and hyphens")
	case mentionsClaude(a):
		return errors.New("there is no adapter for Claude Code: id, name, command, package and args may not name claude or anthropic")
	case a.Name == "" || a.Command == "" || a.Install == "":
		return errors.New("name, command and install are required")
	case len(a.Name) > 60 || len(a.Install) > 400 || len(a.Note) > 400 || hasControl(a.Name+a.Install+a.Note):
		return errors.New("name, install or note is too long or holds a control character")
	case !cmdRe.MatchString(a.Command) || len(a.Command) > 60:
		return errors.New("command is a name looked up on PATH: letters, digits, dot, underscore and hyphen")
	case a.Package != "" && (len(a.Package) > 214 || !pkgRe.MatchString(a.Package)):
		return errors.New("package is not an npm package name")
	case a.Args == nil:
		return errors.New("args is required, [] if there are none")
	case a.Pin == "" && a.MinVersion == "":
		return errors.New("one of pin and minVersion is required")
	case a.Pin != "" && a.Package == "":
		return errors.New("pin needs the npm package it is a version of")
	case !dateRe.MatchString(a.TestedOn) || a.TestedWith == "":
		return errors.New("testedWith and testedOn (YYYY-MM-DD) are required")
	case a.NeedsNode && a.MinNode == "":
		return errors.New("needsNode needs minNode")
	case !a.NeedsNode && a.MinNode != "":
		return errors.New("minNode without needsNode")
	}
	if len(a.Args) > 16 {
		return errors.New("too many args")
	}
	for _, arg := range a.Args {
		if len(arg) > 100 || !argRe.MatchString(arg) {
			return fmt.Errorf("arg %q is not a plain argument", clipText(arg, 40))
		}
	}
	if err := checkLaunchEnv(a.LaunchEnv); err != nil {
		return err
	}
	if err := a.PermissionProbe.check(a); err != nil {
		return err
	}
	for name, v := range map[string]string{"pin": a.Pin, "minVersion": a.MinVersion, "minNode": a.MinNode, "testedWith": a.TestedWith} {
		if v == "" {
			continue
		}
		if _, err := ParseVersion(v); err != nil || !exactVersion(v) {
			return fmt.Errorf("%s %q is not an exact version (no ranges, no tags, no latest)", name, v)
		}
	}
	return nil
}

// check accepts a probe that names a launchEnv variable, a few plain arguments
// and a few tools. A nil probe is fine.
func (p *PermissionProbe) check(a Adapter) error {
	if p == nil {
		return nil
	}
	if _, ok := a.LaunchEnv[p.Setting]; !ok {
		return fmt.Errorf("permissionProbe setting %q is not in launchEnv", clipText(p.Setting, 40))
	}
	if !cmdRe.MatchString(p.Agent) || len(p.Agent) > 40 {
		return fmt.Errorf("permissionProbe agent %q is not an agent name", clipText(p.Agent, 40))
	}
	if len(p.Args) == 0 || len(p.Args) > 6 {
		return errors.New("permissionProbe takes 1 to 6 args")
	}
	for _, arg := range p.Args {
		if len(arg) > 40 || !argRe.MatchString(arg) {
			return fmt.Errorf("permissionProbe arg %q is not a plain argument", clipText(arg, 40))
		}
	}
	if len(p.Tools) == 0 || len(p.Tools) > 4 {
		return errors.New("permissionProbe takes 1 to 4 tools")
	}
	for _, tool := range p.Tools {
		if len(tool) > 40 || !cmdRe.MatchString(tool) {
			return fmt.Errorf("permissionProbe tool %q is not a tool name", clipText(tool, 40))
		}
	}
	if !a.RunsWithoutAsking {
		return errors.New("permissionProbe on an adapter that does not run without asking has nothing to replace")
	}
	return nil
}

var launchEnvRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// checkLaunchEnv accepts a few plain names and short values. A name that is
// Flockdeck's own (FLOCKDECK_, PERCH_) is refused, as the entry-level passEnv refuses it.
func checkLaunchEnv(env map[string]string) error {
	if len(env) > 4 {
		return errors.New("launchEnv takes at most 4 variables")
	}
	for name, v := range env {
		switch {
		case !launchEnvRe.MatchString(name) || len(name) > 60:
			return fmt.Errorf("launchEnv name %q is not a variable name in capitals", clipText(name, 40))
		case strings.HasPrefix(name, "FLOCKDECK_") || strings.HasPrefix(name, "PERCH_"):
			return fmt.Errorf("launchEnv may not set %s: those names are Flockdeck's own", name)
		case v == "" || len(v) > 400 || hasControl(v):
			return fmt.Errorf("launchEnv %s must be text of 1 to 400 characters with no control character", name)
		}
	}
	return nil
}

// LaunchEnvList is LaunchEnv as name=value, sorted by name.
func (a Adapter) LaunchEnvList() []string {
	var out []string
	for name, v := range a.LaunchEnv {
		out = append(out, name+"="+v)
	}
	sort.Strings(out)
	return out
}

// Default is the manifest this build was released with. It is checked by the
// tests; a build with a bad one does not start.
func Default() Manifest {
	m, err := Parse(embedded)
	if err != nil {
		panic(err)
	}
	return m
}

// Raw is the embedded manifest's text, for a job that reads it.
func Raw() []byte { return append([]byte(nil), embedded...) }

// Lookup finds an adapter by id.
func (m Manifest) Lookup(id string) (Adapter, bool) {
	for _, a := range m.Adapters {
		if a.ID == id {
			return a, true
		}
	}
	return Adapter{}, false
}

// IDs are the adapter ids, sorted.
func (m Manifest) IDs() []string {
	var out []string
	for _, a := range m.Adapters {
		out = append(out, a.ID)
	}
	sort.Strings(out)
	return out
}

// Want is the version an adapter's installed copy has to be: its pin, else its
// floor.
func (a Adapter) Want() string {
	if a.Pin != "" {
		return a.Pin
	}
	return a.MinVersion
}

// Accepts reports whether an installed version is one this release accepts: the
// pin exactly, or anything from the floor up.
func (a Adapter) Accepts(have string) (bool, error) {
	hv, err := ParseVersion(have)
	if err != nil {
		return false, err
	}
	if a.Pin != "" {
		pv, _ := ParseVersion(a.Pin)
		return Compare(hv, pv) == 0, nil
	}
	fv, _ := ParseVersion(a.MinVersion)
	return Compare(hv, fv) >= 0, nil
}

// SemVer is a version: numbers, and a pre-release part.
type SemVer struct {
	Major, Minor, Patch int
	Pre                 string
}

func (v SemVer) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

var versionRe = regexp.MustCompile(`v?(\d{1,6})\.(\d{1,6})\.(\d{1,6})(?:-([0-9A-Za-z.-]{1,64}))?(?:\+[0-9A-Za-z.-]+)?`)

// ParseVersion finds the first version in s, which may be a bare version, one
// with a leading v, or the output of a --version command ("Gemini CLI 0.64.0").
func ParseVersion(s string) (SemVer, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return SemVer{}, fmt.Errorf("no version in %q", clipText(s, 60))
	}
	n := func(x string) int { v, _ := strconv.Atoi(x); return v }
	return SemVer{n(m[1]), n(m[2]), n(m[3]), m[4]}, nil
}

// exactRe is semver 2.0 whole: no leading zeros in the numbers, and pre-release
// identifiers that are non-empty, with no leading zero in a numeric one.
var exactRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// exactVersion reports whether s is nothing but one semver 2.0 version.
func exactVersion(s string) bool {
	return len(s) <= 100 && exactRe.MatchString(s)
}

// Compare orders two versions: -1, 0 or 1. A pre-release is below its release.
func Compare(a, b SemVer) int {
	for _, p := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	return comparePre(a.Pre, b.Pre)
}

// comparePre orders pre-release parts the way semver does: dot-separated, a
// number below a word, numbers by value.
func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		xn, xe := strconv.Atoi(x)
		yn, ye := strconv.Atoi(y)
		switch {
		case xe == nil && ye == nil && xn != yn:
			if xn < yn {
				return -1
			}
			return 1
		case xe == nil && ye != nil:
			return -1
		case xe != nil && ye == nil:
			return 1
		case xe != nil && ye != nil && x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

// mentionsClaude reports whether any field that names or starts an adapter
// mentions Claude or Anthropic, in any letter case.
func mentionsClaude(a Adapter) bool {
	for _, f := range append([]string{a.ID, a.Name, a.Command, a.Package}, a.Args...) {
		f = strings.ToLower(f)
		if strings.Contains(f, "claude") || strings.Contains(f, "anthropic") {
			return true
		}
	}
	return false
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// Table is the "tested with" table as Markdown, one row for each adapter. The
// docs page holds it between markers and a test keeps the two equal.
func (m Manifest) Table() string {
	var b strings.Builder
	b.WriteString("| Adapter | Starts with | Needs Node | Accepts | Tested with | Tested on |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, a := range m.Adapters {
		start := a.Command
		if len(a.Args) > 0 {
			start += " " + strings.Join(a.Args, " ")
		}
		node := "no"
		if a.NeedsNode {
			node = "yes, " + a.MinNode + " or newer"
		}
		accepts := a.MinVersion + " or newer"
		if a.Pin != "" {
			accepts = a.Pin + " only"
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s | %s |\n", a.Name, start, node, accepts, a.TestedWith, a.TestedOn)
	}
	return b.String()
}
