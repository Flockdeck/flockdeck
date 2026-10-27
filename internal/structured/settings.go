package structured

import (
	"context"
	"errors"
	"slices"

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

// live returns the client and session id to send a setting to.
func (r *Runtime) live() (*acp.Client, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.phase != PhaseReady || r.client == nil || r.session == "" {
		return nil, "", ErrNotRunning
	}
	return r.client, r.session, nil
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
	m := r.br.Session().Modes
	if m == nil || !slices.ContainsFunc(m.Available, func(v acp.Mode) bool { return v.ID == id }) {
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
	m := r.br.Session().Models
	if m == nil || !slices.ContainsFunc(m.Available, func(v acp.Model) bool { return v.ID == id }) {
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
	got, err := c.SetConfigOption(r.ctx, sid, configID, value, boolean)
	if err != nil {
		return settingFailed(err)
	}
	if got == nil {
		// The agent did not send its options back: keep ours with the one change.
		opts[i].Current = value
		got = opts
	}
	r.br.ConfigSet(got)
	r.notify()
	return nil
}
