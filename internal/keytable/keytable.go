// Package keytable is the shape of the key table: one action, its binding and
// where it belongs. The table itself is data owned by the front end and handed
// to the server's packages (server.Config.Keys, workspace.SetKeys), so none of
// them imports where it lives.
package keytable

import (
	"encoding/json"
	"strings"
)

// Key is one action, its binding if it has one, and where it belongs in the
// shortcut tables.
type Key struct {
	// ID is stable and is what the front end hangs its implementation off;
	// it is also what `[[key:id]]` in a help page refers to.
	ID string `json:"id"`
	// Keys is the binding as it is written for a reader, or "" for actions
	// that are only reachable from the command palette.
	Keys string `json:"keys"`
	// Label is the one line shown in the palette and in the shortcut tables.
	Label string `json:"label"`
	// Short is the one name the action goes by where a surface is named --
	// the rail, the palette's rows, Go to: "Agents" for a label that reads
	// "All agents across projects". The label stays, and stays searchable,
	// because the docs/keys.md table and the help pages are written from it.
	// Empty where the label's own name (the part before its dash) will do.
	Short string `json:"short,omitempty"`
	// Section groups the action in the shortcut tables.
	Section string `json:"section"`
	// Page is the help page that explains the action, if one does.
	Page string `json:"page,omitempty"`
	// NoPalette keeps navigation that only makes sense from the keyboard out
	// of the palette — opening the palette from the palette, most of all.
	NoPalette bool `json:"noPalette,omitempty"`
	// Confirm is the question to ask before running, for the actions that
	// stop other people's work.
	Confirm string `json:"confirm,omitempty"`
}

// Name is the action as prose calls it: the label up to the dash that
// introduces the gloss the palette needs and a sentence does not. "Detach —
// close the window, leave agents running" is the palette entry; "Detach" is
// what a page says.
func (k Key) Name() string {
	if i := strings.Index(k.Label, " — "); i > 0 {
		return k.Label[:i]
	}
	return k.Label
}

// Table is a list of actions, in the order the shortcut tables show them.
type Table []Key

// Lookup returns the action with the given id.
func (t Table) Lookup(id string) (Key, bool) {
	for _, k := range t {
		if k.ID == id {
			return k, true
		}
	}
	return Key{}, false
}

// From turns a table written in another package's own Key type into a Table,
// through the JSON both share. The program uses it to hand the server the
// front end's table; it panics if the shapes have drifted apart, which the
// first run shows.
func From(v any) Table {
	b, err := json.Marshal(v)
	if err != nil {
		panic("keytable: " + err.Error())
	}
	var t Table
	if err := json.Unmarshal(b, &t); err != nil {
		panic("keytable: " + err.Error())
	}
	return t
}
