package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// The checks below read an agents.json entry the way somebody who wrote it by
// hand would want it read, and say what they found in the catalog's notice:
// a key in the wrong place, a token that is not one, a runner that is neither,
// a default naming no agent. None of them refuses the entry.

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
		// The decoder matches a key to a field without regard to case, so
		// "defaultmodel" is the default model: named here as unread, it was
		// a notice saying a setting that had taken effect had not.
		if specFields[strings.ToLower(key)] {
			continue
		}
		msg := fmt.Sprintf("agent %q: %q is not something an agent has", id, key)
		if apiFields[strings.ToLower(key)] {
			msg = fmt.Sprintf("agent %q: %q belongs inside \"api\"", id, key)
		}
		problems = append(problems, msg)
	}
	// The keys inside "api" are read the same way and pass over a stranger
	// just as quietly: an "api" given a "url" rather than a "baseURL" left the
	// endpoint the vendor's own, and the agent greyed out, with nothing said.
	for key, raw := range keys {
		var api map[string]json.RawMessage
		if !strings.EqualFold(key, "api") || json.Unmarshal(raw, &api) != nil {
			continue
		}
		for inner := range api {
			switch lower := strings.ToLower(inner); {
			case apiFields[lower]:
			case specFields[lower]:
				problems = append(problems, fmt.Sprintf("agent %q: %q belongs beside \"api\", not inside it", id, inner))
			default:
				problems = append(problems, fmt.Sprintf("agent %q: %q in \"api\" is not something an endpoint has; it takes wire, baseURL and keyEnv", id, inner))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

var (
	specFields = jsonFields(reflect.TypeFor[Spec]())
	apiFields  = jsonFields(reflect.TypeFor[APISpec]())
)

// jsonFields is the set of keys a struct decodes, in lower case, read from its
// own tags so the two cannot drift apart.
func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}

// missingDefaults names each default, the installation's or a project's, that
// names no agent in the catalog.
//
// Such a default gives way to the installation's own, or to Claude
// (DefaultsFor), and without a word that
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
	for _, m := range anyToken.FindAllStringSubmatch(s.Switch, -1) {
		note(m[1])
	}
	return problems
}

// checkTiers settles each model's tier into one of the three there are.
//
// Case is forgiven, as it is for a runner. Anything else is named in the
// notice and taken out, which leaves the model's tier unknown: a model routing
// never moves work to or from. Left in, a "large" would have read as unknown
// all the same, and nothing would have said why routing never chose it.
func checkTiers(s *Spec) []string {
	var problems []string
	for i, m := range s.Models {
		tier := strings.ToLower(strings.TrimSpace(m.Tier))
		if tier == "" || TierRank(tier) > 0 {
			s.Models[i].Tier = tier
			continue
		}
		s.Models[i].Tier = ""
		problems = append(problems, fmt.Sprintf("agent %q: model %q has tier %q; the tiers are small, mid and top", s.ID, m.ID, m.Tier))
	}
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
