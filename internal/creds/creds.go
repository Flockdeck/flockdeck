// Package creds resolves the API key an agent needs to talk to a model API.
//
// A key is the one piece of state Flockdeck holds that is worth stealing, so this
// package is built around keeping it in as few places as possible. A key is
// looked for in the environment the user already keeps it in, and only then in
// Flockdeck's own store; whatever is found reaches exactly one place, the
// environment of the `flockdeck chat` process for the pane that needs it. Nothing
// else in the application is ever handed the value. The interface, the CLI and
// the snapshot code are given a Status instead, which says that there is a key
// and where it came from and never what it is.
//
// Keeping to that is the reason Key hides its value behind a method rather
// than exposing a field: a struct with a secret in an exported field ends up
// in a log line, a JSON snapshot or a wrapped error eventually, and no amount
// of care at the call sites prevents it. Here there is nothing to leak — fmt
// prints the redacted form, encoding/json sees no value to write, and the only
// way to the secret is to ask for it by name.
package creds

import (
	"fmt"
	"os"
	"strings"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Source says where a key was found, which is the only thing about a key that
// is safe to show.
type Source string

const (
	// SourceNone means no key was found.
	SourceNone Source = ""
	// SourceEnv means the key was already in the environment Flockdeck was
	// started from, under one of the Spec's own KeyEnv names.
	SourceEnv Source = "env"
	// SourceStore means the key came from Flockdeck's own keys.json.
	SourceStore Source = "store"
)

// Key is a resolved API key.
//
// The value is unexported on purpose; see the package comment. Secret is the
// only way to it, and the two formatting methods make sure that a Key printed
// by accident says how it was found rather than what it is.
type Key struct {
	secret string
	// Source says where it came from, and is empty when there is no key.
	Source Source
	// Env names the environment variable it was read from, for SourceEnv. It
	// is the variable's name, never its value.
	Env string
}

// Secret returns the key itself. Every call is a place the value can escape
// to, so there should be very few of them.
func (k Key) Secret() string { return k.secret }

// Set reports whether a key was found at all, which is what availability and
// the interface are actually asking.
func (k Key) Set() bool { return k.secret != "" }

// String is what a Key looks like anywhere it is printed. It is deliberately
// the same thing Status shows.
func (k Key) String() string {
	if !k.Set() {
		return "not set"
	}
	if k.Env != "" {
		return "set (" + k.Env + ")"
	}
	return "set (" + string(k.Source) + ")"
}

// GoString covers the one verb String does not: %#v prints a struct field by
// field, unexported fields included, which would otherwise put the secret in
// the debug output of anything holding a Key.
func (k Key) GoString() string { return "creds.Key{" + k.String() + "}" }

// Resolve finds the key for a Spec: each name in API.KeyEnv from the
// environment, in the order the Spec lists them, then Flockdeck's own store under
// the Spec's id, then nothing.
//
// The environment comes first because a key already exported there is the
// user's own arrangement — a secrets manager, a shell profile, a CI runner —
// and Flockdeck quietly preferring its own stale copy of it is a debugging session
// nobody enjoys.
func Resolve(spec agent.Spec) Key {
	for _, name := range spec.API.KeyEnv {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return Key{secret: v, Source: SourceEnv, Env: name}
		}
	}
	if v := stored(spec.ID); v != "" {
		return Key{secret: v, Source: SourceStore}
	}
	return Key{}
}

// Env returns the environment entries that carry the key to the one process
// that needs it, ready to append to that process's environment.
//
// A key already in the environment needs no entry: the pane inherits it, and
// writing it out again would only put a second copy of the value somewhere.
// A stored key is exported under the first name the Spec lists, which is the
// vendor's own conventional variable, so `flockdeck chat` finds it by resolving
// the same Spec at the other end rather than by learning a second convention.
//
// A Spec with no KeyEnv names has nowhere to put a stored key, and gets
// nothing rather than an invented variable name.
func Env(spec agent.Spec) []string {
	k := Resolve(spec)
	if !k.Set() || k.Source != SourceStore || len(spec.API.KeyEnv) == 0 {
		return nil
	}
	return []string{spec.API.KeyEnv[0] + "=" + k.Secret()}
}

// Status is everything about a key that may be shown, sent to the front end or
// written to a file: that there is one, and where it came from.
type Status struct {
	// Agent is the Spec id the key belongs to, which is also the name
	// `flockdeck keys set` takes and the key in keys.json.
	Agent string `json:"agent"`
	// Name is what the agent is called, for an interface that has the Spec to
	// hand.
	Name string `json:"name,omitempty"`
	Set  bool   `json:"set"`
	// Source and Env are where it was found. Env is a variable's name.
	Source Source `json:"source,omitempty"`
	Env    string `json:"env,omitempty"`
	// Vars lists the environment variables this agent's key may arrive in, so
	// somebody without one can be told where to put it instead of being sent
	// to the documentation.
	Vars []string `json:"vars,omitempty"`
}

// StatusOf reports on one Spec's key without reading it back.
func StatusOf(spec agent.Spec) Status {
	k := Resolve(spec)
	return Status{
		Agent:  spec.ID,
		Name:   spec.Name,
		Set:    k.Set(),
		Source: k.Source,
		Env:    k.Env,
		Vars:   spec.API.KeyEnv,
	}
}

// StatusAll reports on every Spec that could want a key, in the order given.
// A Spec that is not an API runner is skipped: a CLI keeps its own credentials
// and Flockdeck has no business offering to hold them.
func StatusAll(specs []agent.Spec) []Status {
	out := make([]Status, 0, len(specs))
	for _, s := range specs {
		if s.Runner != agent.RunnerAPI {
			continue
		}
		out = append(out, StatusOf(s))
	}
	return out
}

// Describe is the one-line form used by `flockdeck keys list` and by the notice
// the interface shows. It never contains a key.
func (s Status) Describe() string {
	switch {
	case !s.Set && len(s.Vars) > 0:
		return fmt.Sprintf("not set — export %s, or run `flockdeck keys set %s`",
			strings.Join(s.Vars, " or "), s.Agent)
	case !s.Set:
		return "not set"
	case s.Source == SourceEnv:
		return "set (from " + s.Env + ")"
	default:
		return "set (stored by flockdeck)"
	}
}
