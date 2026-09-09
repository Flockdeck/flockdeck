package chat

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		env   map[string]string
		check func(t *testing.T, o Options)
	}{
		{
			name: "the command line the design writes",
			args: []string{"--agent", "openai", "--model", "gpt-5", "--session", "abc", "--resume"},
			check: func(t *testing.T, o Options) {
				if o.Agent != "openai" || o.Model != "gpt-5" || o.Session != "abc" || !o.Resume {
					t.Errorf("options = %+v", o)
				}
			},
		},
		{
			name: "the task is whatever is left",
			args: []string{"--model", "m", "have", "a", "look", "at", "the", "build"},
			check: func(t *testing.T, o Options) {
				if o.Task != "have a look at the build" {
					t.Errorf("task = %q", o.Task)
				}
			},
		},
		{
			name: "a task beginning with a dash survives the separator",
			args: []string{"--", "-p is not what I meant"},
			check: func(t *testing.T, o Options) {
				if o.Task != "-p is not what I meant" {
					t.Errorf("task = %q", o.Task)
				}
			},
		},
		{
			name: "the endpoint can arrive in the environment instead",
			env: map[string]string{
				"PERCH_WIRE":     "openai",
				"PERCH_BASE_URL": "http://127.0.0.1:11434/v1",
				"PERCH_KEY_ENV":  "OLLAMA_KEY, OPENAI_API_KEY",
				"PERCH_AGENT":    "local",
				"PERCH_MODEL":    "qwen3-coder",
			},
			check: func(t *testing.T, o Options) {
				if o.Wire != "openai" || o.BaseURL != "http://127.0.0.1:11434/v1" {
					t.Errorf("endpoint = %+v", o)
				}
				if len(o.KeyEnv) != 2 || o.KeyEnv[0] != "OLLAMA_KEY" || o.KeyEnv[1] != "OPENAI_API_KEY" {
					t.Errorf("key names = %q", o.KeyEnv)
				}
				if o.Agent != "local" || o.Model != "qwen3-coder" {
					t.Errorf("agent and model = %+v", o)
				}
			},
		},
		{
			name: "a flag beats the environment",
			env:  map[string]string{"PERCH_MODEL": "from-the-pane"},
			args: []string{"--model", "from-the-flag"},
			check: func(t *testing.T, o Options) {
				if o.Model != "from-the-flag" {
					t.Errorf("model = %q", o.Model)
				}
			},
		},
		{
			name: "the pane is the conversation when no session is named",
			env: map[string]string{
				"PERCH_PANE":  "pane-7",
				"PERCH_API":   "http://127.0.0.1:9/hook",
				"PERCH_TOKEN": "t",
			},
			check: func(t *testing.T, o Options) {
				if o.Session != "pane-7" {
					t.Errorf("session = %q, want the pane id", o.Session)
				}
				if o.API == "" || o.Token != "t" {
					t.Errorf("callback = %+v", o)
				}
			},
		},
		{
			name: "the names an earlier build used still work",
			env:  map[string]string{"AGENT_WRAPPER_PANE": "old-pane"},
			check: func(t *testing.T, o Options) {
				if o.Session != "old-pane" {
					t.Errorf("session = %q", o.Session)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{
				"PERCH_AGENT", "PERCH_MODEL", "PERCH_WIRE", "PERCH_BASE_URL", "PERCH_KEY_ENV",
				"PERCH_PANE", "PERCH_API", "PERCH_TOKEN", "AGENT_WRAPPER_PANE",
			} {
				t.Setenv(name, "")
			}
			for name, v := range tc.env {
				t.Setenv(name, v)
			}
			o, err := ParseArgs(tc.args, io.Discard)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			tc.check(t, o)
		})
	}
}

func TestParseArgsSaysNothingMoreAfterTheUsage(t *testing.T) {
	var out strings.Builder
	if _, err := ParseArgs([]string{"-h"}, &out); !errors.Is(err, ErrHelpShown) {
		t.Errorf("error = %v, want ErrHelpShown", err)
	}
	if !strings.Contains(out.String(), "perch chat") {
		t.Errorf("usage did not describe the command: %q", out.String())
	}
	if _, err := ParseArgs([]string{"--nonsense"}, io.Discard); !errors.Is(err, ErrBadFlags) {
		t.Errorf("error = %v, want the flag set's own complaint to stand alone", err)
	}
}
