package structured

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jmwri/flockdeck/internal/acp"
)

var (
	// ErrNoSuchSetting is returned by SetMode, SetModel and SetConfig for a
	// mode, model, option or value that is not in the lists the pane last
	// pushed. A value the list had a moment ago, and the agent has since
	// replaced, is refused the same way, so the caller shows what is on offer
	// now.
	ErrNoSuchSetting = errors.New("structured: that setting is not on offer")
	// ErrSettingFailed is returned when the agent did not take the change. Its
	// own error text is not kept.
	ErrSettingFailed = errors.New("structured: the agent did not change the setting")
)

// maxSettingCalls is how many setting requests may wait on the agent at once.
// More are refused, so a caller that sends them faster than the agent answers
// does not pile up goroutines.
const maxSettingCalls = 4

// live returns the client and session id to send a setting to.
func (r *Runtime) live() (*acp.Client, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.phase != PhaseReady || r.client == nil || r.session == "" {
		return nil, "", ErrNotRunning
	}
	if r.settingCalls >= maxSettingCalls {
		return nil, "", ErrSettingFailed
	}
	r.settingCalls++
	return r.client, r.session, nil
}

// same says the session a setting was checked against is still the pane's. The
// lists are read after live and before the request, so a session replaced in
// between (a restart) would otherwise be checked against one session's lists
// and sent to another.
func (r *Runtime) same(c *acp.Client, sid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.client == c && r.session == sid && !r.closed && r.phase == PhaseReady
}

// settingDone ends the call live began.
func (r *Runtime) settingDone() {
	r.mu.Lock()
	r.settingCalls--
	r.mu.Unlock()
}

func settingFailed(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, acp.ErrClosed) {
		return ErrNotRunning
	}
	return ErrSettingFailed
}

// SetMode switches the session to a mode from the modes block the pane pushed.
// It returns ErrNoSuchSetting for any other id. It waits for the agent, so it
// must not be called from a goroutine that must not block.
func (r *Runtime) SetMode(id string) error {
	c, sid, err := r.live()
	if err != nil {
		return err
	}
	defer r.settingDone()
	m := r.br.Session().Modes
	if m == nil || !slices.ContainsFunc(m.Available, func(v acp.Mode) bool { return v.ID == id }) || !r.same(c, sid) {
		return ErrNoSuchSetting
	}
	if err := c.SetMode(r.ctx, sid, id); err != nil {
		return settingFailed(err)
	}
	r.br.ModeSet(id)
	r.notify()
	return nil
}

// SetModel switches the session to a model from the models block the pane
// pushed. Errors are as for SetMode.
func (r *Runtime) SetModel(id string) error {
	c, sid, err := r.live()
	if err != nil {
		return err
	}
	defer r.settingDone()
	m := r.br.Session().Models
	if m == nil || !slices.ContainsFunc(m.Available, func(v acp.Model) bool { return v.ID == id }) || !r.same(c, sid) {
		return ErrNoSuchSetting
	}
	if err := c.SetModel(r.ctx, sid, id); err != nil {
		return settingFailed(err)
	}
	r.br.ModelSet(id)
	r.notify()
	return nil
}

// SetConfig sets the config option configID to value. The option has to be one
// the pane pushed, and the value one of its choices (or "true" or "false" for a
// boolean option). Errors are as for SetMode.
func (r *Runtime) SetConfig(configID, value string) error {
	c, sid, err := r.live()
	if err != nil {
		return err
	}
	defer r.settingDone()
	opts := r.br.Session().ConfigOptions
	i := slices.IndexFunc(opts, func(o acp.ConfigOption) bool { return o.ID == configID })
	if i < 0 {
		return ErrNoSuchSetting
	}
	boolean := false
	switch opts[i].Type {
	case "select":
		if !slices.ContainsFunc(opts[i].Values, func(v acp.ConfigValue) bool { return v.Value == value }) {
			return ErrNoSuchSetting
		}
	case "boolean":
		if value != "true" && value != "false" {
			return ErrNoSuchSetting
		}
		boolean = true
	default:
		return ErrNoSuchSetting
	}
	if !r.same(c, sid) {
		return ErrNoSuchSetting
	}
	got, err := c.SetConfigOption(r.ctx, sid, configID, value, boolean)
	if err != nil {
		return settingFailed(err)
	}
	if got == nil {
		// The agent did not send its options back: the change goes into the
		// list as it is now, which an update may have replaced meanwhile.
		r.br.ConfigValueSet(configID, value)
	} else {
		r.br.ConfigSet(got)
	}
	r.notify()
	return nil
}

// LooksUnasked says a mode or option value, by its id, name and description,
// reads as one that lets the agent run commands or edit files without asking.
// It is a guess from words: an agent may name such a mode anything. A value
// whose id or name is an accept-edits mode is not counted, since that mode
// still asks for commands.
func LooksUnasked(id, name, description string) bool {
	head := strings.ToLower(id + " " + name)
	for _, ok := range []string{"accept-edits", "acceptedits", "accept_edits", "accept edits"} {
		if strings.Contains(head, ok) {
			return false
		}
	}
	all := head + " " + strings.ToLower(description)
	for _, w := range unaskedWords {
		if strings.Contains(all, w) {
			return true
		}
	}
	return false
}

var unaskedWords = []string{"yolo", "bypass", "dangerous", "auto-approve", "auto approve", "autoapprove", "accept-all", "accept all",
	"acceptall", "full-access", "full access", "full_access", "fullaccess", "dontask", "dont-ask", "don't ask", "without asking",
	"without approval", "without permission", "without prompting", "never ask", "no approval", "skip permission"}

// settingsUnasked says the mode the agent is in now, or the current value of
// one of its select options, reads as one that runs without asking. It follows
// the agent's own changes as well as the person's.
func (r *Runtime) settingsUnasked() bool {
	if r.br == nil {
		return false
	}
	s := r.br.Session()
	if m := s.Modes; m != nil {
		for _, v := range m.Available {
			if v.ID == m.Current && LooksUnasked(v.ID, v.Name, v.Description) {
				return true
			}
		}
	}
	for _, o := range s.ConfigOptions {
		if o.Type != "select" {
			continue
		}
		for _, v := range o.Values {
			if v.Value == o.Current && LooksUnasked(v.Value, v.Name, v.Description) {
				return true
			}
		}
	}
	return false
}
