// Package tool is the set of tools the native API agent runs.
//
// `flockdeck chat` talks to a model API directly, so nothing else supplies the
// model with a way to read or change the project: these are it. Each one is
// confined to the pane's working directory, and the three that change
// something -- write_file, edit_file and run_command -- have a question for the
// person at the terminal, which the chat loop puts to them before the call
// runs, raising a Notification so the pane turns amber and whoever is watching
// the window learns which pane wants them.
//
// The tools sit behind the Tool interface so the chat loop and the tools can
// be built and tested apart: the loop knows how to describe a tool to a wire
// format, when to ask, and what to do with the answer, and knows nothing about
// what any particular tool does.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Tool is one thing the model can do. It is the contract in section 8 of
// design/multi-agent.md, and the only thing the chat loop sees.
type Tool interface {
	Name() string
	// Describe is the tool as the model is told about it, in the small corner
	// of JSON Schema every wire format understands.
	Describe() Schema
	// Approval is the question to put to the user before the call runs, or ""
	// when the call needs no permission. A call that will be refused outright
	// -- a path leading out of the pane's directory, arguments that do not
	// parse -- answers "" as well, because refusing is not the user's decision
	// to make and asking about it would only teach them to say yes.
	Approval(args json.RawMessage) string
	// Run performs the call and returns what the model should be told. An
	// error is a result too: it is handed back as the tool's output so the
	// model can correct itself rather than being left to guess.
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Schema is one tool as the model is told about it. Every wire format Flockdeck
// speaks -- Anthropic's `input_schema`, OpenAI's `function`, Gemini's
// `functionDeclarations` -- carries the same three things in a different
// envelope, so this is the shape they are all translated from.
type Schema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Params      Params `json:"parameters"`
}

// Params is a JSON Schema object describing a tool's arguments.
type Params struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property is one argument.
type Property struct {
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Items       *Property `json:"items,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
}

// object builds a parameter schema, which is always an object because every
// wire format requires the top level of a tool's arguments to be one.
func object(props map[string]Property, required ...string) Params {
	return Params{Type: "object", Properties: props, Required: required}
}

// ErrOutsideRoot is a path that leads out of the pane's working directory: a
// call that will not be attempted, which is Flockdeck's own decision rather
// than a failure of the underlying operation.
var ErrOutsideRoot = errors.New("path is outside the pane's working directory")

// Set is the tools one chat session offers, all confined to one directory.
type Set struct {
	tools  []Tool
	byName map[string]Tool
}

// New builds the v1 tools for a pane working in cwd.
//
// The directory is resolved through any symbolic links once, here, so that
// every later containment check compares like with like: on macOS a temporary
// directory reached through /tmp is really under /private/tmp, and a pane whose
// root was recorded by the shorter name would refuse every path in it.
func New(cwd string) (*Set, error) {
	root, err := NewRoot(cwd)
	if err != nil {
		return nil, err
	}
	s := &Set{}
	s.tools = []Tool{
		&readFile{root: root},
		&writeFile{root: root},
		&editFile{root: root},
		&listDir{root: root},
		&globTool{root: root},
		&grepTool{root: root},
		&runCommand{root: root},
	}
	s.byName = make(map[string]Tool, len(s.tools))
	for _, t := range s.tools {
		s.byName[t.Name()] = t
	}
	return s, nil
}

// Tools returns the set in a stable order, which is the order the model is
// told about them in and so worth keeping the same from one turn to the next.
func (s *Set) Tools() []Tool {
	out := make([]Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// HideEnv keeps the named variables out of the environment of every command
// run_command runs. It is for the key Flockdeck handed the pane: that is in the
// chat's own environment for the chat to use, and a model that runs `env` has
// no business printing it into the conversation.
func (s *Set) HideEnv(names ...string) {
	for _, t := range s.tools {
		if rc, ok := t.(*runCommand); ok {
			rc.hide = append(rc.hide, names...)
		}
	}
}

// Lookup finds a tool by the name the model called.
func (s *Set) Lookup(name string) (Tool, bool) {
	t, ok := s.byName[name]
	return t, ok
}

// decode parses a call's arguments, treating an absent argument object as an
// empty one: models routinely omit it for a tool whose arguments are all
// optional, and failing that call would be pedantry.
func decode(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("arguments are not valid JSON for this tool: %w", err)
	}
	return nil
}

// sortedStrings is used wherever a tool lists what it found, because a model
// comparing two runs of the same tool should not have to wonder whether the
// order meant something.
func sortedStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}
