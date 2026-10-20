package store

import "slices"

// Scopes of a wake switch. See WakePrefs.
const (
	WakeGlobal = "global"
	WakeAgent  = "agent"
	WakePane   = "pane"
)

// maxWakeOff is the most agent types, and the most panes, a WakePrefs lists.
// The desk sets these, so the cap is for a damaged file or a runaway script.
const maxWakeOff = 500

// WakePrefs are the off switches for typed wake, the line Flockdeck types into
// an idle agent's input when it has unread mail. Everything is on until one is
// switched off, so the zero value is the default and the file stays free of it.
// Any switch that is off wins: there is no way to turn a pane on that its type
// or the global switch has turned off.
type WakePrefs struct {
	// Off turns wake off for every pane.
	Off bool `json:"off,omitempty"`
	// Agents are the agent ids (claude, aider and so on) wake is off for.
	Agents []string `json:"agents,omitempty"`
	// Panes are the pane ids wake is off for.
	Panes []string `json:"panes,omitempty"`
}

// WakeEnabled says whether wake may type into the pane. agentID is the pane's
// agent type and may be empty.
func (p Prefs) WakeEnabled(agentID, paneID string) bool {
	w := p.Wake
	if w.Off {
		return false
	}
	if agentID != "" && slices.Contains(w.Agents, agentID) {
		return false
	}
	return paneID == "" || !slices.Contains(w.Panes, paneID)
}

// SetWake switches wake on or off for a scope and reports whether that changed
// anything. scope is WakeGlobal (id is ignored), WakeAgent (an agent id) or
// WakePane (a pane id); anything else, or an empty id where one is needed, changes
// nothing. Turning a scope on only clears that scope: a pane whose type is off
// stays off. The lists are replaced and not written into, because a Prefs is
// copied by value and the copies share them.
func (p *Prefs) SetWake(scope, id string, on bool) bool {
	switch scope {
	case WakeGlobal:
		if p.Wake.Off == !on {
			return false
		}
		p.Wake.Off = !on
		return true
	case WakeAgent:
		list, changed := withWakeOff(p.Wake.Agents, id, on)
		if changed {
			p.Wake.Agents = list
		}
		return changed
	case WakePane:
		list, changed := withWakeOff(p.Wake.Panes, id, on)
		if changed {
			p.Wake.Panes = list
		}
		return changed
	}
	return false
}

// withWakeOff returns list with id added (on false) or removed (on true), and
// whether that differs from list.
func withWakeOff(list []string, id string, on bool) ([]string, bool) {
	if id == "" || len(id) > 128 {
		return list, false
	}
	has := slices.Contains(list, id)
	switch {
	case on && has:
		return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == id }), true
	case !on && !has && len(list) < maxWakeOff:
		return append(slices.Clone(list), id), true
	}
	return list, false
}
