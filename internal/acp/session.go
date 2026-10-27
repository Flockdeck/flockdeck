package acp

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Update kinds that carry a session's own state, as opposed to its
// conversation. The agent sends them as it wishes: opencode and Codex change
// settings through config options, Gemini through modes and models.
const (
	UpdateAvailableCommands = "available_commands_update"
	UpdateCurrentMode       = "current_mode_update"
	UpdateConfigOption      = "config_option_update"
	UpdateSessionInfo       = "session_info_update"
	// UpdatePlanUpdate is the plan some agents (Codex) send as markdown instead
	// of the baseline "plan" entries.
	UpdatePlanUpdate = "plan_update"
	// UpdateCompaction and UpdateCompactionUpdate are the notices an agent sends
	// when it compacts its context. No agent tried so far has been seen sending
	// one (opencode does behind a client capability this client does not claim),
	// so the fields read are a best guess; see Compaction.
	UpdateCompaction       = "compaction"
	UpdateCompactionUpdate = "compaction_update"
	// UpdateNotice is a Flockdeck adapter's request for a fixed notice. It carries
	// a code, never text: the bridge owns the wording (NoticeText in the bridge).
	UpdateNotice = "flockdeck_notice"
)

// Command is one slash command an agent offers.
type Command struct {
	Name        string
	Description string
	// Hint is the placeholder for the command's argument, when it takes one.
	Hint string
}

func (c *Command) UnmarshalJSON(data []byte) error {
	var w struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Input       *struct {
			Hint string `json:"hint"`
		} `json:"input"`
	}
	_ = json.Unmarshal(data, &w)
	*c = Command{Name: w.Name, Description: w.Description}
	if w.Input != nil {
		c.Hint = w.Input.Hint
	}
	return nil
}

// ConfigValue is one choice of a select config option. Group is the name of the
// group it was listed under, if the agent grouped its choices.
type ConfigValue struct {
	Value       string
	Name        string
	Description string
	Group       string
}

// ConfigOption is one setting a session exposes (model, effort, mode and so
// on). Current is the value as text: a boolean option reads "true" or "false".
type ConfigOption struct {
	ID          string
	Name        string
	Description string
	// Category is the agent's own grouping: mode, model, thought_level, or one
	// it made up.
	Category string
	// Type is "select" in the current schema; Values is only filled for it.
	Type    string
	Current string
	Values  []ConfigValue
}

func (o *ConfigOption) UnmarshalJSON(data []byte) error {
	var w struct {
		ID           string          `json:"id"`
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		Category     string          `json:"category"`
		Type         string          `json:"type"`
		CurrentValue json.RawMessage `json:"currentValue"`
		Options      json.RawMessage `json:"options"`
	}
	_ = json.Unmarshal(data, &w)
	*o = ConfigOption{ID: w.ID, Name: w.Name, Description: w.Description, Category: w.Category, Type: w.Type,
		Current: scalarText(w.CurrentValue)}
	// options is a flat list of choices, or a list of groups that each hold one.
	var items []struct {
		Value       json.RawMessage `json:"value"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Group       string          `json:"group"`
		Options     []struct {
			Value       json.RawMessage `json:"value"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
		} `json:"options"`
	}
	if json.Unmarshal(w.Options, &items) != nil {
		return nil
	}
	for _, it := range items {
		if len(it.Options) > 0 {
			group := it.Name
			if group == "" {
				group = it.Group // a group with no display name is shown by its id
			}
			for _, v := range it.Options {
				o.Values = append(o.Values, ConfigValue{Value: scalarText(v.Value), Name: v.Name, Description: v.Description, Group: group})
			}
			continue
		}
		o.Values = append(o.Values, ConfigValue{Value: scalarText(it.Value), Name: it.Name, Description: it.Description, Group: it.Group})
	}
	return nil
}

// scalarText is a JSON string, number or boolean as text, and "" for anything
// else.
func scalarText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	case 't', 'f':
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			return strconv.FormatBool(b)
		}
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var n json.Number
		if json.Unmarshal(raw, &n) == nil {
			return n.String()
		}
	}
	return ""
}

// Mode is one session mode an agent lists in session/new's modes block.
type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Modes is session/new's modes block (Gemini). Agents that use config options
// for the mode (opencode, Codex) have none.
type Modes struct {
	Current   string `json:"currentModeId"`
	Available []Mode `json:"availableModes"`
}

// Model is one model an agent lists in session/new's models block.
type Model struct {
	ID          string `json:"modelId"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Models is session/new's models block (Gemini and others that predate config
// options).
type Models struct {
	Current   string  `json:"currentModelId"`
	Available []Model `json:"availableModels"`
}

// SessionNew is the agent's reply to session/new: the id, and what the session
// exposes. Modes and Models are nil when the agent sent no such block.
type SessionNew struct {
	SessionID     string
	Modes         *Modes
	Models        *Models
	ConfigOptions []ConfigOption
}

// PromptCapabilities says what a prompt may carry besides text.
type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// MCPCapabilities says which MCP server transports the agent takes besides
// stdio.
type MCPCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

// AgentCapabilities is what the agent said it can do at initialize. A flag the
// agent did not send is false. Raw is the whole block, so a capability this
// client does not read yet is not lost.
type AgentCapabilities struct {
	LoadSession bool
	Prompt      PromptCapabilities
	MCP         MCPCapabilities
	// List, Resume and Fork are the session capabilities (sessionCapabilities in
	// the schema): the agent has the method when the key is present.
	List, Resume, Fork bool

	Raw json.RawMessage
}

func parseCapabilities(raw json.RawMessage) AgentCapabilities {
	c := AgentCapabilities{Raw: append(json.RawMessage(nil), raw...)}
	var w struct {
		LoadSession        bool               `json:"loadSession"`
		PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
		MCPCapabilities    MCPCapabilities    `json:"mcpCapabilities"`
		SessionCaps        map[string]any     `json:"sessionCapabilities"`
	}
	// Fields of the wrong type are skipped one by one, as with updates.
	_ = json.Unmarshal(raw, &w)
	c.LoadSession, c.Prompt, c.MCP = w.LoadSession, w.PromptCapabilities, w.MCPCapabilities
	_, c.List = w.SessionCaps["list"]
	_, c.Resume = w.SessionCaps["resume"]
	_, c.Fork = w.SessionCaps["fork"]
	return c
}

// Compaction is a notice that an agent compacted, or is compacting, its
// context. Status and Message are whatever the update carried under those
// names or under "text"; either may be empty.
type Compaction struct {
	Status  string
	Message string
}

func decodeSessionNew(raw json.RawMessage) SessionNew {
	var w struct {
		SessionID     string          `json:"sessionId"`
		Modes         json.RawMessage `json:"modes"`
		Models        json.RawMessage `json:"models"`
		ConfigOptions json.RawMessage `json:"configOptions"`
	}
	_ = json.Unmarshal(raw, &w)
	s := SessionNew{SessionID: w.SessionID}
	if m := bytes.TrimSpace(w.Modes); len(m) > 0 && m[0] == '{' {
		var v Modes
		_ = json.Unmarshal(m, &v)
		s.Modes = &v
	}
	if m := bytes.TrimSpace(w.Models); len(m) > 0 && m[0] == '{' {
		var v Models
		_ = json.Unmarshal(m, &v)
		s.Models = &v
	}
	s.ConfigOptions = decodeConfigOptions(w.ConfigOptions)
	return s
}

// decodeConfigOptions reads a configOptions array. It returns nil when the
// value is not an array and an empty slice for an empty one, so an update that
// clears the options can be told from one that has none.
func decodeConfigOptions(raw json.RawMessage) []ConfigOption {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	out := []ConfigOption{}
	_ = json.Unmarshal(raw, &out)
	return out
}
