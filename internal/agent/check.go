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
		if specFields[strings.ToLower(key)] || retiredFields[strings.ToLower(key)] {
			continue
		}
		problems = append(problems, fmt.Sprintf("agent %q: %q is not something an agent has", id, key))
	}
	sort.Strings(problems)
	return problems
}

// retiredAPI says why an entry written for the API runner is refused, or ""
// for any other. The runner started `flockdeck chat`, which is gone: read as a
// command-line agent, an entry with an "api" block or the runner "api" would
// start a program named for its id. It is refused whole and the built-in it was
// written over stays as it was.
//
// An entry that names one of the removed built-ins (anthropic, openai, google,
// openai-compatible) and gives no program is refused the same way: it was a
// setting for that built-in, and read on its own it would become a command-line
// agent that starts a program named for the id.
// removedBuiltins are the ids of the agents that ran on the API runner.
var removedBuiltins = map[string]bool{"anthropic": true, "openai": true, "google": true, "openai-compatible": true}

func retiredAPI(id string, raw json.RawMessage) string {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return ""
	}
	if _, hasExe := keys["exe"]; !hasExe && removedBuiltins[id] {
		return fmt.Sprintf("agent %q: the API runner (flockdeck chat) was removed, so this entry is ignored", id)
	}
	for key, v := range keys {
		var runner string
		if strings.EqualFold(key, "api") ||
			strings.EqualFold(key, "runner") && json.Unmarshal(v, &runner) == nil && strings.EqualFold(strings.TrimSpace(runner), "api") {
			return fmt.Sprintf("agent %q: the API runner (flockdeck chat) was removed, so this entry is ignored", id)
		}
	}
	return ""
}

// retiredFields are keys an agents.json entry may still carry that no build
// reads any more. They are passed over without a notice, as they were when they
// were fields: a file written for an older build, or by hand from older
// documentation, has not done anything wrong.
//
// "switch" was the command typed into a running agent to change its model,
// which nothing ever ran.
var retiredFields = map[string]bool{"switch": true}

var (
	specFields = jsonFields(reflect.TypeFor[Spec]())
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
	case RunnerCLI, RunnerACP, "":
		s.Runner = r
		return nil
	}
	s.Runner = ""
	return []string{fmt.Sprintf("agent %q: runner %q is none of \"cli\" and \"acp\"", s.ID, r)}
}
