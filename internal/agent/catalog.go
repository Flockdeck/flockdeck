package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// DefaultAgentID is the agent a pane gets when nothing -- not the pane, not the
// project, not the user's defaults -- has said which one it wants. It is Claude
// because that is what every pane was before agents were a choice, and a saved
// layout from that build has to come back exactly as it went away.
const DefaultAgentID = "claude"

// Catalog is the agents this run can offer: the built-ins with the user's
// agents.json merged over them, together with the defaults from that file.
type Catalog struct {
	// Specs are every agent, in picker order and including hidden ones.
	Specs []Spec
	// Defaults apply where a project has nothing to say.
	Defaults Defaults
	// Projects are the per-project defaults, keyed as the user wrote them.
	Projects map[string]Defaults
	// Notice is empty unless the user's file could not be used in full. It is
	// something for the interface to show, never a reason not to start: a
	// mistyped agents.json leaves somebody with the built-in agents, not with
	// an application that will not open.
	Notice string
}

// Load reads the catalog from Flockdeck's state directory.
//
// It never fails. Where the state directory itself cannot be found there is
// nowhere for an agents.json to be, and the built-ins are the whole answer.
func Load() *Catalog {
	// A catalog read again may say something new about any agent -- a program
	// installed a moment ago, an entry pointed at another one -- so what was
	// probed under the old one is forgotten. Reading it again is what the
	// picker's opening does, which is the moment Refresh was written for and
	// which never called it: an agent just installed stayed greyed out.
	Refresh()
	path, err := ConfigPath()
	if err != nil {
		return &Catalog{Specs: normalizeAll(Builtins())}
	}
	return LoadFrom(filepath.Dir(path))
}

// LoadFrom reads the catalog from a named directory, which is what the tests
// use and what a future --state-dir would use.
func LoadFrom(dir string) *Catalog {
	f, err := ReadConfig(dir)
	if err != nil {
		c := &Catalog{Specs: normalizeAll(Builtins())}
		c.Notice = err.Error()
		return c
	}
	return Merge(f)
}

// Merge overlays a parsed agents.json on the built-in catalog.
//
// A user entry whose id is a built-in's is merged field by field over it: a
// field the user set wins, a field they left out keeps the built-in's answer.
// That is why the entries arrive as raw JSON -- it is the only way left, by
// then, to tell "" the user wrote from "" the decoder filled in.
func Merge(f *File) *Catalog {
	c := &Catalog{Specs: Builtins()}
	if f == nil {
		c.Specs = normalizeAll(c.Specs)
		return c
	}
	c.Defaults = f.Defaults
	c.Projects = f.Projects

	index := make(map[string]int, len(c.Specs))
	for i, s := range c.Specs {
		index[s.ID] = i
	}

	var problems []string
	for n, raw := range f.Agents {
		var head struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &head); err != nil || head.ID == "" {
			problems = append(problems, fmt.Sprintf("agent %d has no id", n+1))
			continue
		}
		problems = append(problems, unknownFields(head.ID, raw)...)
		if i, known := index[head.ID]; known {
			builtin := c.Specs[i]
			merged := builtin
			// Decoding an array into a slice reuses the slice's elements, and a
			// struct element keeps every field the new one leaves out: a user's
			// `"models": [{"id": "opus"}]` came out named "Default", and an
			// `"args"` entry of a plain value inherited the built-in group it
			// landed on and vanished inside it. So the lists of structs start
			// empty, and get the built-in's back only where the entry named none.
			merged.Args, merged.ResumeArgs, merged.Models = nil, nil, nil
			if err := json.Unmarshal(raw, &merged); err != nil {
				// One unusable entry costs the user that entry's changes and
				// nothing else: the built-in it was written over stays as it
				// was, and every other agent in the file is still merged.
				problems = append(problems, fmt.Sprintf("agent %q: %v", head.ID, err))
				continue
			}
			// An empty array decodes as an empty slice rather than nil, so an
			// entry that deliberately clears a list still clears it.
			merged.Args = orBuiltin(merged.Args, builtin.Args)
			merged.ResumeArgs = orBuiltin(merged.ResumeArgs, builtin.ResumeArgs)
			merged.Models = orBuiltin(merged.Models, builtin.Models)
			problems = append(problems, checkRunner(&merged)...)
			problems = append(problems, checkTokens(&merged)...)
			c.Specs[i] = merged
			continue
		}
		var fresh Spec
		if err := json.Unmarshal(raw, &fresh); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q: %v", head.ID, err))
			continue
		}
		// Ids are matched exactly, as they are everywhere else -- a layout
		// records them -- so "Claude" is a new agent, not the built-in. On
		// Windows its program is found as the same claude.exe, and the picker
		// showed two Claudes while the entry changed neither.
		for known := range index {
			if strings.EqualFold(known, head.ID) {
				problems = append(problems, fmt.Sprintf("agent %q is a new agent, not %q: ids are matched exactly", head.ID, known))
				break
			}
		}
		problems = append(problems, checkRunner(&fresh)...)
		problems = append(problems, checkTokens(&fresh)...)
		index[fresh.ID] = len(c.Specs)
		c.Specs = append(c.Specs, fresh)
	}
	c.Specs = normalizeAll(c.Specs)
	problems = append(problems, c.missingDefaults()...)
	if len(problems) > 0 {
		c.Notice = ConfigName + ": " + strings.Join(problems, "; ")
	}
	return c
}

// unknownFields names the keys of an entry that no agent has.
//
// The decoder passes over them without a word, so a field written in the wrong
// place did nothing and said nothing: a "baseURL" beside the id rather than
// inside "api" left the endpoint greyed out in the picker with no clue why. The
// entry is still used -- a key this build does not know may be one a later
// build wrote -- but the notice says which keys went unread.
func unknownFields(id string, raw json.RawMessage) []string {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return nil // not an object; decoding it properly reports that
	}
	var problems []string
	for key := range keys {
		if specFields[key] {
			continue
		}
		msg := fmt.Sprintf("agent %q: %q is not something an agent has", id, key)
		if apiFields[key] {
			msg = fmt.Sprintf("agent %q: %q belongs inside \"api\"", id, key)
		}
		problems = append(problems, msg)
	}
	sort.Strings(problems)
	return problems
}

var (
	specFields = jsonFields(reflect.TypeFor[Spec]())
	apiFields  = jsonFields(reflect.TypeFor[APISpec]())
)

// jsonFields is the set of keys a struct decodes, read from its own tags so
// the two cannot drift apart.
func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// missingDefaults names each default, the installation's or a project's, that
// names no agent in the catalog.
//
// Such a default opens Claude instead (DefaultsFor), and without a word that
// looks like the choice simply did not take: a hand-typed "Codex" or a
// misspelt project entry said nothing at all.
func (c *Catalog) missingDefaults() []string {
	var problems []string
	if d := c.Defaults.Agent; d != "" {
		if _, ok := c.Find(d); !ok {
			problems = append(problems, fmt.Sprintf("the default agent %q is not one of the agents here", d))
		}
	}
	projects := make([]string, 0, len(c.Projects))
	for p := range c.Projects {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	for _, p := range projects {
		if d := c.Projects[p].Agent; d != "" {
			if _, ok := c.Find(d); !ok {
				problems = append(problems, fmt.Sprintf("the default agent %q for %s is not one of the agents here", d, p))
			}
		}
	}
	return problems
}

// checkTokens names every token an entry's arguments refer to that is not one.
//
// A token with no value takes its group out of the command line, and an
// unknown one never has a value: an `"if": "modle"` quietly dropped the
// --model flag with it, and a "{{modle}}" in a value went through as written.
func checkTokens(s *Spec) []string {
	seen := map[string]bool{}
	var problems []string
	note := func(name string) {
		if name == "" || seen[name] || knownToken(name) {
			return
		}
		seen[name] = true
		problems = append(problems, fmt.Sprintf("agent %q: %q is not a token; the tokens are %s", s.ID, name, tokenNames))
	}
	var walk func([]Arg)
	walk = func(args []Arg) {
		for _, a := range args {
			if a.If != "" {
				note(strings.TrimSpace(strings.Trim(a.If, "{}")))
			}
			for _, m := range anyToken.FindAllStringSubmatch(a.Value, -1) {
				note(m[1])
			}
			walk(a.Args)
		}
	}
	walk(s.Args)
	walk(s.ResumeArgs)
	return problems
}

// anyToken matches anything written as a token, known or not.
var anyToken = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

// tokenNames lists the tokens for a notice that has to name them.
const tokenNames = "session, model, settings, prompt, cwd and pane"

// checkRunner settles a runner written by hand into one of the two there are.
//
// Anything else was taken at its word and matched neither: "API" in capitals
// started a pane whose command line began "--agent", with no program in front
// of it, which died on the spot with nothing to say why. Case is forgiven; a
// runner that is neither is named in the notice and decided the way a missing
// one is.
func checkRunner(s *Spec) []string {
	r := Runner(strings.ToLower(strings.TrimSpace(string(s.Runner))))
	switch r {
	case RunnerCLI, RunnerAPI, "":
		s.Runner = r
		return nil
	}
	s.Runner = ""
	return []string{fmt.Sprintf("agent %q: runner %q is neither \"cli\" nor \"api\"", s.ID, r)}
}

// orBuiltin is the list an entry set, or the built-in's where it set none.
func orBuiltin[T any](set, builtin []T) []T {
	if set == nil {
		return builtin
	}
	return set
}

// normalizeAll fills in what an entry can be trusted to have meant, so that the
// rest of Flockdeck never has to ask whether a field was left out.
func normalizeAll(specs []Spec) []Spec {
	for i := range specs {
		normalize(&specs[i])
	}
	return specs
}

func normalize(s *Spec) {
	if s.Name == "" {
		s.Name = s.ID
	}
	// An entry's environment replaces the inherited variable of the same
	// name as it is written, so "PATH=$HOME/bin:$PATH" gave the pane a PATH
	// of those very characters and nothing in it would run. The variables in
	// a value are read as a shell would read them; the name is left alone.
	for i, kv := range s.Env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			s.Env[i] = name + "=" + expandVars(value)
		}
	}
	if s.Runner == "" {
		// An entry that describes an endpoint means to talk to it; anything
		// else is a program to run.
		if s.API.Wire != "" || s.API.BaseURL != "" {
			s.Runner = RunnerAPI
		} else {
			s.Runner = RunnerCLI
		}
	}
	if s.Runner == RunnerCLI {
		if s.Exe == "" {
			// Somebody writing `{"id": "amp"}` by hand means the command of
			// that name, and guessing it is far friendlier than starting a
			// pane with no program in it at all.
			s.Exe = s.ID
		}
		s.Exe = expandExe(s.Exe)
		return
	}
	// An API entry is `flockdeck chat` whatever else it says, so an entry that
	// gives only an endpoint -- which is all the example in the design gives --
	// still starts the chat client properly and still reports its lifecycle.
	if s.API.Wire == "" {
		// An endpoint with no wire named is an OpenAI-compatible one: that is
		// what nearly every local server and gateway speaks.
		s.API.Wire = "openai"
	}
	// A local model's address is commonly kept in a variable -- OLLAMA_HOST --
	// and "http://$OLLAMA_HOST/v1" went to the chat client, and to the check
	// for whether it needs a key, as those very characters.
	s.API.BaseURL = expandVars(s.API.BaseURL)
	// Built from the entry as it now stands, wire and all, since the chat
	// client learns its endpoint from these and from nothing else.
	if len(s.Args) == 0 {
		s.Args = chatArgs(s.ID, s.API, false)
	}
	if len(s.ResumeArgs) == 0 {
		s.ResumeArgs = chatArgs(s.ID, s.API, true)
	}
	if s.Caps == (Caps{}) {
		s.Caps = chatCaps()
	}
	// An entry that lists its models and names no default -- the help page's
	// own example does exactly that -- started its panes asking for none,
	// which most endpoints refuse. The first model it lists is the one taken.
	// An entry that wants the endpoint's own choice says so the way Claude's
	// list does, with a model of empty id first, and keeps it.
	if s.DefaultModel == "" && len(s.Models) > 0 {
		s.DefaultModel = s.Models[0].ID
	}
}

// expandExe reads a program path the way the shell somebody copied it from
// would have: a leading "~" is their home, and "$NAME", "${NAME}" or "%NAME%"
// is that variable. It was looked up on PATH as written, so an entry saying
// "~/bin/mycli" or "%LOCALAPPDATA%\Tools\mycli.exe" was never found. Only a
// variable that is set is replaced, so a path that merely contains a "$" or
// a "%" keeps it.
func expandExe(exe string) string {
	if exe == "~" || strings.HasPrefix(exe, "~/") || strings.HasPrefix(exe, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			exe = home + exe[1:]
		}
	}
	return expandVars(exe)
}

// expandVars replaces "$NAME", "${NAME}" and "%NAME%" with the variable, where
// it is set, in Flockdeck's own environment -- which is the one a pane
// inherits, so "$PATH" means what the pane would have had.
func expandVars(s string) string {
	s = percentVar.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := os.LookupEnv(strings.Trim(m, "%")); ok {
			return v
		}
		return m
	})
	return os.Expand(s, func(name string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return "${" + name + "}"
	})
}

// percentVar matches a Windows %NAME% reference.
var percentVar = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_()]*%`)

// Find returns the spec with an id.
func (c *Catalog) Find(id string) (Spec, bool) {
	for _, s := range c.Specs {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}

// Visible returns the agents the picker offers, which is everything not hidden.
// Unavailable agents are still here: somebody who has not installed Codex
// should still learn that Flockdeck would run it.
func (c *Catalog) Visible() []Spec {
	out := make([]Spec, 0, len(c.Specs))
	for _, s := range c.Specs {
		if !s.Hidden {
			out = append(out, s)
		}
	}
	return out
}

// DefaultsFor returns the agent and model a project starts panes with: its own
// entry where it has one, otherwise the installation's.
func (c *Catalog) DefaultsFor(project string) Defaults {
	d := c.Defaults
	if project != "" {
		key := projectKey(project)
		for recorded, pd := range c.Projects {
			if projectKey(recorded) != key {
				continue
			}
			if pd.Agent != "" {
				d.Agent = pd.Agent
			}
			// An empty model is a real choice -- "whatever the agent is set
			// to" -- so a project entry naming an agent replaces the model
			// too, rather than leaving the installation's model attached to a
			// different agent entirely.
			if pd.Agent != "" || pd.Model != "" {
				d.Model = pd.Model
			}
			break
		}
	}
	if d.Agent == "" {
		d.Agent = DefaultAgentID
	}
	// A default naming an agent the catalog no longer has -- an entry since
	// deleted from agents.json -- is a pane that cannot start, and it is
	// every new pane rather than one: each asks for the default and was told
	// "no agent named ... is configured". Claude opens instead, and the model
	// chosen for the other agent does not come with it.
	if _, ok := c.Find(d.Agent); !ok && len(c.Specs) > 0 {
		d = Defaults{Agent: DefaultAgentID}
	}
	return d
}

// Resolve answers what a new pane should start: the spec and the model id.
//
// Both arguments are what the pane asked for and may be empty, which is the
// ordinary case -- splitting with the default agent is one keystroke and asks
// for nothing. A restored pane is not resolved this way: it recorded its agent
// and model when it was created, and Find is what gives those back to it,
// because a default changed since then must not quietly move a conversation
// onto another model.
//
// The last return is false only when the catalog holds no usable agent at all,
// which takes a user file that hides every built-in.
func (c *Catalog) Resolve(project, agentID, model string) (Spec, string, bool) {
	d := c.DefaultsFor(project)
	id := agentID
	if id == "" {
		id = d.Agent
	}
	spec, ok := c.Find(id)
	if !ok {
		// The pane, or the defaults, named an agent this catalog no longer
		// has: an entry deleted from agents.json, or a layout saved when it
		// was still there. Falling back to Claude, and then to whatever else
		// is offered, opens the pane; refusing would lose it.
		if spec, ok = c.Find(DefaultAgentID); !ok {
			visible := c.Visible()
			if len(visible) == 0 {
				return Spec{}, "", false
			}
			spec = visible[0]
		}
	}
	if model == "" && spec.ID == d.Agent {
		model = d.Model
	}
	if model == "" {
		model = spec.DefaultModel
	}
	return spec, model, true
}

// StripEnv is every variable any agent in the catalog wants taken out of a
// pane's environment, in the order the entries give them and without
// duplicates.
//
// It is the union rather than the chosen agent's own list because the markers
// each of these tools leaves behind say "you are running inside me", and a
// Codex pane opened from inside a Claude session is no more a child of it than
// a Claude pane is. A pane is a clean top-level session whatever is running in
// it.
func (c *Catalog) StripEnv() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range c.Specs {
		for _, name := range s.StripEnv {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// projectKey puts a project path into the one form two spellings of it are
// compared in. The same repository reaches Flockdeck from a directory picker, the
// command line and saved state, so it arrives as "C:\Repo\App\" one run and
// "c:\repo\app" the next; without this a project default would be found only
// when it was written the same way it was read.
func projectKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(clean)
	}
	return clean
}
