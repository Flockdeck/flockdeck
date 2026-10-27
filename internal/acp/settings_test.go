package acp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

func TestSetModeAndModelReachTheAgent(t *testing.T) {
	log := filepath.Join(t.TempDir(), "set.log")
	c, sid := start(t, newRecorder(), acptest.WithModes(), acptest.WithSettingLog(log))
	if err := c.SetMode(ctxT(t), sid, "yolo"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := c.SetModel(ctxT(t), sid, "gem-flash"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	got := readLog(t, log)
	for _, want := range []string{`session/set_mode {"modeId":"yolo","sessionId":"sess-1"}`, `session/set_model {"modelId":"gem-flash","sessionId":"sess-1"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("the agent was sent\n%s\nwant a line %s", got, want)
		}
	}
}

func TestSetConfigOptionSendsTheValueAndReturnsTheList(t *testing.T) {
	log := filepath.Join(t.TempDir(), "set.log")
	c, sid := start(t, newRecorder(), acptest.WithConfigOptions(), acptest.WithBooleanOption(), acptest.WithSettingLog(log))
	opts, err := c.SetConfigOption(ctxT(t), sid, "model", "small", false)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(opts) != 3 || opts[0].ID != "model" || opts[0].Current != "small" {
		t.Errorf("options = %+v, want the list with model small", opts)
	}
	if _, err := c.SetConfigOption(ctxT(t), sid, "fast", "true", true); err != nil {
		t.Fatalf("boolean: %v", err)
	}
	got := readLog(t, log)
	if !strings.Contains(got, `"configId":"model"`) || !strings.Contains(got, `"value":"small"`) {
		t.Errorf("select request = %s", got)
	}
	// A boolean goes as a JSON boolean with its type, not as the text "true".
	if !strings.Contains(got, `"type":"boolean"`) || !strings.Contains(got, `"value":true`) || strings.Contains(got, `"value":"true"`) {
		t.Errorf("boolean request = %s", got)
	}
}

func TestSetConfigOptionWithABareReplyReturnsNil(t *testing.T) {
	c, sid := start(t, newRecorder(), acptest.WithConfigOptions(), acptest.WithBareSettingReplies())
	opts, err := c.SetConfigOption(ctxT(t), sid, "mode", "plan", false)
	if err != nil || opts != nil {
		t.Errorf("opts, err = %v, %v; want nil, nil", opts, err)
	}
}

func TestAnAgentWithoutTheMethodIsUnsupported(t *testing.T) {
	c, sid := start(t, newRecorder()) // no shape: the fake has none of the methods
	if err := c.SetMode(ctxT(t), sid, "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetMode = %v, want ErrUnsupported", err)
	}
	if err := c.SetModel(ctxT(t), sid, "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetModel = %v, want ErrUnsupported", err)
	}
	if _, err := c.SetConfigOption(ctxT(t), sid, "x", "y", false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetConfigOption = %v, want ErrUnsupported", err)
	}
}

func TestAnAgentErrorIsFixedText(t *testing.T) {
	c, sid := start(t, newRecorder(), acptest.WithModes())
	err := c.SetMode(ctxT(t), sid, "nope")
	if !errors.Is(err, ErrSettingRefused) {
		t.Fatalf("err = %v, want ErrSettingRefused", err)
	}
	var rpc *RPCError
	if errors.As(err, &rpc) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("the agent's error leaks through: %v", err)
	}
}

func TestSettingValuesAreCheckedBeforeSending(t *testing.T) {
	log := filepath.Join(t.TempDir(), "set.log")
	c, sid := start(t, newRecorder(), acptest.WithModes(), acptest.WithSettingLog(log))
	for _, bad := range []string{"", strings.Repeat("a", maxSettingLen+1), "a\nb", "a\x00b", "\xff"} {
		if err := c.SetMode(ctxT(t), sid, bad); !errors.Is(err, ErrSettingRefused) {
			t.Errorf("SetMode(%q) = %v, want ErrSettingRefused", bad, err)
		}
	}
	if _, err := c.SetConfigOption(ctxT(t), sid, "fast", "yes", true); !errors.Is(err, ErrSettingRefused) {
		t.Errorf("a boolean option with the value yes: %v", err)
	}
	if got := readLog(t, log); got != "" {
		t.Errorf("the agent was sent %q for values that were refused", got)
	}
}
