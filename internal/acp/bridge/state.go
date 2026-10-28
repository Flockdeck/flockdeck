package bridge

import (
	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Limits on what the agent's session state may hold. Like every other string
// from the agent, the text is cleaned and cut; these bound how much of it is
// kept.
const (
	maxCommands      = 200
	maxModes         = 32
	maxModels        = 500
	maxConfigOptions = 32
	maxConfigValues  = 500
	// maxPlanMarkdown bounds the plan an agent sends as markdown. It is pushed to
	// a window with the rest of the state, so it is kept small.
	maxPlanMarkdown = 64 << 10
)

// SessionState is what a window needs to draw the session's controls: the slash
// commands, the mode and model on offer and the settings the agent exposes.
// Every string has been cleaned and cut. The slices are the Bridge's own copies.
type SessionState struct {
	// Commands is the agent's latest list of slash commands.
	Commands []acp.Command
	// Modes is the mode block of session/new (Gemini), with the current mode
	// kept up to date by current_mode_update. Nil when the agent has none and
	// has not named a current mode.
	Modes *acp.Modes
	// Models is the models block of session/new. Nil when the agent has none.
	Models *acp.Models
	// ConfigOptions is the agent's settings (opencode and Codex: model, effort,
	// mode), as of the last config_option_update.
	ConfigOptions []acp.ConfigOption
	// PlanMarkdown is the plan an agent sent as markdown (plan_update).
	PlanMarkdown string
	// Title is the session's title, when the agent set one.
	Title string
	// Capabilities is what the agent said it can do at initialize. Raw is not
	// kept.
	Capabilities acp.AgentCapabilities
}

// seeded says which parts of the state an update has already set, so that the
// session/new reply, which can be handled after an update that followed it,
// does not put older values back.
type seeded struct {
	commands, modes, config bool
}

// Capabilities records what the agent said at initialize.
func (b *Bridge) Capabilities(c acp.AgentCapabilities) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c.Raw = nil
	b.state.Capabilities = c
}

// SessionNew records the modes, models and config options the agent listed in
// its session/new reply. A part that an update has already set is left alone.
func (b *Bridge) SessionNew(s acp.SessionNew) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.Modes != nil {
		m := cleanModes(s.Modes)
		if b.seeded.modes && b.state.Modes != nil {
			m.Current = b.state.Modes.Current
		}
		b.state.Modes = m
	}
	if s.Models != nil {
		b.state.Models = cleanModels(s.Models)
	}
	if s.ConfigOptions != nil && !b.seeded.config {
		b.state.ConfigOptions = cleanConfig(s.ConfigOptions)
	}
}

// ModeSet records a mode the agent has accepted, as current_mode_update would.
func (b *Bridge) ModeSet(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionUpdate(acp.Update{Kind: acp.UpdateCurrentMode, ModeID: id})
}

// ModelSet records a model the agent has accepted. There is no update for it in
// the protocol, so the model block is the only place it shows.
func (b *Bridge) ModelSet(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state.Models != nil {
		if id = line(id, labelCap); id != "" {
			b.state.Models.Current = id
		}
	}
}

// ConfigSet records the list of config options an agent sent after accepting a
// change, as config_option_update would. A nil list changes nothing.
func (b *Bridge) ConfigSet(opts []acp.ConfigOption) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionUpdate(acp.Update{Kind: acp.UpdateConfigOption, ConfigOptions: opts})
}

// ConfigValueSet gives the config option id the current value, in the list as it
// stands. An option that is not there is left out.
func (b *Bridge) ConfigValueSet(id, value string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.state.ConfigOptions {
		if b.state.ConfigOptions[i].ID == id {
			b.state.ConfigOptions[i].Current = line(value, labelCap)
		}
	}
}

// Session returns the session state as it stands.
func (b *Bridge) Session() SessionState {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state
	s.Commands = append([]acp.Command(nil), s.Commands...)
	s.ConfigOptions = make([]acp.ConfigOption, len(b.state.ConfigOptions))
	for i, o := range b.state.ConfigOptions {
		o.Values = append([]acp.ConfigValue(nil), o.Values...)
		s.ConfigOptions[i] = o
	}
	if m := b.state.Modes; m != nil {
		c := *m
		c.Available = append([]acp.Mode{}, m.Available...)
		s.Modes = &c
	}
	if m := b.state.Models; m != nil {
		c := *m
		c.Available = append([]acp.Model{}, m.Available...)
		s.Models = &c
	}
	return s
}

// sessionUpdate folds the state-carrying updates in. It reports whether the
// kind was one of them. Call with b.mu held.
func (b *Bridge) sessionUpdate(u acp.Update) bool {
	switch u.Kind {
	case acp.UpdateAvailableCommands:
		if u.Commands == nil {
			return true
		}
		b.seeded.commands = true
		n := min(len(u.Commands), maxCommands)
		b.state.Commands = make([]acp.Command, 0, n)
		for _, c := range u.Commands[:n] {
			name := line(c.Name, labelCap)
			if name == "" {
				continue
			}
			b.state.Commands = append(b.state.Commands, acp.Command{Name: name, Description: line(c.Description, 400), Hint: line(c.Hint, labelCap)})
		}
	case acp.UpdateCurrentMode:
		id := line(u.ModeID, labelCap)
		if id == "" {
			return true
		}
		b.seeded.modes = true
		if b.state.Modes == nil {
			b.state.Modes = &acp.Modes{Available: []acp.Mode{}}
		}
		b.state.Modes.Current = id
	case acp.UpdateConfigOption:
		if u.ConfigOptions == nil {
			return true
		}
		b.seeded.config = true
		b.state.ConfigOptions = cleanConfig(u.ConfigOptions)
	case acp.UpdateSessionInfo:
		if u.InfoTitle != nil {
			b.state.Title = line(*u.InfoTitle, labelCap)
		}
	case acp.UpdatePlanUpdate:
		b.state.PlanMarkdown = clean(capBytes(u.PlanMarkdown, maxPlanMarkdown))
	case acp.UpdateCompaction, acp.UpdateCompactionUpdate:
		text := "Context compacted."
		if c := u.Compaction; c != nil {
			switch {
			case c.Message != "":
				text = line(c.Message, labelCap)
			case c.Status != "":
				text = "Context compaction: " + line(c.Status, 40)
			}
		}
		b.closeMerges()
		b.add(transcript.Entry{Kind: transcript.KindNotice, Text: text})
	case acp.UpdateNotice:
		// The agent names a notice by code; the words are Flockdeck's. An
		// unknown code is dropped.
		if text, ok := noticeTexts[u.NoticeCode]; ok {
			b.closeMerges()
			b.add(transcript.Entry{Kind: transcript.KindNotice, Text: text})
		}
	default:
		return false
	}
	return true
}

// noticeTexts are the notices an adapter can ask for with a flockdeck_notice
// update, by code.
var noticeTexts = map[string]string{
	"untested_version": "This Claude version has not been tested with Flockdeck",
	"new_conversation": "Started a new conversation.",
	// Sent by an adapter that replays a stored conversation (the Claude adapter's
	// session/load) when it could not show all of it.
	"history_cut_earlier": "Earlier parts of this conversation are not shown.",
	"history_cut_later":   "Later parts of this conversation are not shown.",
	"history_cut_gap":     "Some parts of this conversation are not shown.",
}

func cleanModes(m *acp.Modes) *acp.Modes {
	out := &acp.Modes{Current: line(m.Current, labelCap), Available: []acp.Mode{}}
	for _, v := range m.Available[:min(len(m.Available), maxModes)] {
		if id := line(v.ID, labelCap); id != "" {
			out.Available = append(out.Available, acp.Mode{ID: id, Name: line(v.Name, labelCap), Description: line(v.Description, 400)})
		}
	}
	return out
}

func cleanModels(m *acp.Models) *acp.Models {
	out := &acp.Models{Current: line(m.Current, labelCap), Available: []acp.Model{}}
	for _, v := range m.Available[:min(len(m.Available), maxModels)] {
		if id := line(v.ID, labelCap); id != "" {
			out.Available = append(out.Available, acp.Model{ID: id, Name: line(v.Name, labelCap), Description: line(v.Description, 400)})
		}
	}
	return out
}

func cleanConfig(in []acp.ConfigOption) []acp.ConfigOption {
	out := make([]acp.ConfigOption, 0, min(len(in), maxConfigOptions))
	for _, o := range in[:min(len(in), maxConfigOptions)] {
		id := line(o.ID, labelCap)
		if id == "" {
			continue
		}
		c := acp.ConfigOption{ID: id, Name: line(o.Name, labelCap), Description: line(o.Description, 400),
			Category: line(o.Category, 40), Type: line(o.Type, 40), Current: line(o.Current, labelCap)}
		for _, v := range o.Values[:min(len(o.Values), maxConfigValues)] {
			c.Values = append(c.Values, acp.ConfigValue{Value: line(v.Value, labelCap), Name: line(v.Name, labelCap),
				Description: line(v.Description, 400), Group: line(v.Group, labelCap)})
		}
		out = append(out, c)
	}
	return out
}
