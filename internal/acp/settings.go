package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// ErrSettingRefused is what SetMode, SetModel and SetConfigOption return when
// the agent answered with an error of its own. The agent's words are not in it:
// they are for the agent's log, not for a person.
var ErrSettingRefused = errors.New("acp: the agent did not accept the setting")

// maxSettingLen bounds an id or value sent to the agent. The ones sent come
// from lists the agent made, which the bridge has already cut to the same size.
const maxSettingLen = 256

func okSetting(parts ...string) bool {
	for _, p := range parts {
		if p == "" || len(p) > maxSettingLen || !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\r\n") {
			return false
		}
	}
	return true
}

// settingError turns what a setting call got back into the error the caller
// sees: a missing method is ErrUnsupported and any other error of the agent's
// is ErrSettingRefused. Failures that are not the agent's answer pass through.
func settingError(err error) error {
	var e *RPCError
	if errors.As(err, &e) {
		if e.Code == -32601 {
			return ErrUnsupported
		}
		return ErrSettingRefused
	}
	var a *AuthRequiredError
	if errors.As(err, &a) {
		return ErrSettingRefused
	}
	return err
}

// SetMode switches the session to a mode from its modes block
// (session/set_mode). It returns ErrUnsupported when the agent does not have the
// method and ErrSettingRefused when it answers with an error. It gives up after
// Config.SetupTimeout.
func (c *Client) SetMode(ctx context.Context, sessionID, modeID string) error {
	if !okSetting(sessionID, modeID) {
		return ErrSettingRefused
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	return settingError(c.call(ctx, "session/set_mode", map[string]any{"sessionId": sessionID, "modeId": modeID}, nil))
}

// SetModel switches the session to a model from its models block
// (session/set_model, which the protocol still lists as unstable). Errors are
// as for SetMode.
func (c *Client) SetModel(ctx context.Context, sessionID, modelID string) error {
	if !okSetting(sessionID, modelID) {
		return ErrSettingRefused
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	return settingError(c.call(ctx, "session/set_model", map[string]any{"sessionId": sessionID, "modelId": modelID}, nil))
}

// SetConfigOption sets one config option (session/set_config_option) and
// returns the whole list as the agent now has it, which can differ from the
// request: changing one option may change others. A boolean option is set with
// boolean true and value "true" or "false"; any other option takes the value
// of one of its choices. The returned list is nil when the agent did not send
// one. Errors are as for SetMode.
func (c *Client) SetConfigOption(ctx context.Context, sessionID, configID, value string, boolean bool) ([]ConfigOption, error) {
	if !okSetting(sessionID, configID, value) {
		return nil, ErrSettingRefused
	}
	params := map[string]any{"sessionId": sessionID, "configId": configID, "value": value}
	if boolean {
		if value != "true" && value != "false" {
			return nil, ErrSettingRefused
		}
		params["type"] = "boolean"
		params["value"] = value == "true"
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	var raw struct {
		ConfigOptions json.RawMessage `json:"configOptions"`
	}
	if err := c.call(ctx, "session/set_config_option", params, &raw); err != nil {
		return nil, settingError(err)
	}
	return decodeConfigOptions(raw.ConfigOptions), nil
}
