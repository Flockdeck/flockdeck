package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/structured"
)

func settings(n int, current string) []acp.ConfigOption {
	var out []acp.ConfigOption
	for i := 0; i < n; i++ {
		out = append(out, acp.ConfigOption{ID: fmt.Sprintf("opt%02d", i), Current: current, Values: []acp.ConfigValue{{Value: current, Name: "Name " + current}}})
	}
	return out
}

func rowNames(text string) []string {
	var out []string
	for _, ln := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if at := strings.Index(ln, ": "); at > 0 {
			out = append(out, ln[:at])
		}
	}
	return out
}

func TestInfoTextListsSettingsAfterTheFixedRowsAndStopsAtThirtyTwo(t *testing.T) {
	got := infoText(structured.Info{Session: bridge.SessionState{ConfigOptions: settings(40, "v")}})
	names := rowNames(got)
	var nset int
	last := -1
	for i, n := range names {
		if strings.HasPrefix(n, "Setting ") {
			nset++
			if last == -1 {
				last = i
			}
		}
	}
	if nset != 32 {
		t.Errorf("%d setting rows, want 32\n%s", nset, got)
	}
	if names[last-1] != "Slash commands" {
		t.Errorf("the settings do not follow the fixed rows: %q", names[last-1])
	}
	if names[len(names)-1] != "Cut" || !strings.Contains(got, "Setting opt00: Name v (v)\n") {
		t.Errorf("rows:\n%s", got)
	}
	if few := infoText(structured.Info{Session: bridge.SessionState{ConfigOptions: settings(3, "v")}}); strings.Contains(few, "Cut:") {
		t.Errorf("a Cut row for three settings\n%s", few)
	}
}

func TestInfoTextReachesTheByteLimitWithoutLosingTheFixedRows(t *testing.T) {
	long := strings.Repeat("v", 190)
	got := infoText(structured.Info{Name: "A", MCP: []string{"flockdeck"}, Session: bridge.SessionState{
		Commands: []acp.Command{{Name: "a"}}, ConfigOptions: settings(32, long)}})
	if len(got) > 6000 || len(got) < 5500 {
		t.Errorf("%d bytes, want a text that reaches the limit", len(got))
	}
	for _, want := range []string{"Agent: A\n", "MCP servers: flockdeck\n", "Slash commands: 1\n", "Setting opt00: ", "\nCut: some settings are left out\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.HasSuffix(got, "left out\n") || strings.Contains(got, "Setting opt31:") {
		t.Errorf("the cut did not drop the last settings:\n%s", got[len(got)-300:])
	}
}

func TestInfoTextSaysWhatSignInNeedsAndFallsBackInChoiceNames(t *testing.T) {
	got := infoText(structured.Info{
		Setup: &structured.Setup{Kind: structured.SetupLogin, Message: "Sign in to continue", Login: "Run claude once"},
		Session: bridge.SessionState{
			Modes:  &acp.Modes{Current: "gone", Available: []acp.Mode{{ID: "plan", Name: "Plan"}}},
			Models: &acp.Models{Current: "same", Available: []acp.Model{{ID: "same", Name: "same"}}},
			ConfigOptions: []acp.ConfigOption{
				{ID: "a: b", Current: "x\nSession: forged", Values: []acp.ConfigValue{{Value: "x", Name: "X"}}},
				{ID: "empty"},
			},
		},
	})
	for _, want := range []string{"\nSign-in needed: Sign in to continue\n", "\nHow to sign in: Run claude once\n", "\nMode: gone\n", "\nModel: same\n", "\nSetting a b: x Session: forged\n", "\nSetting empty: not reported\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if n := strings.Count(got, "\nSession:"); n != 1 {
		t.Errorf("%d Session rows", n)
	}
}

func TestInfoTextStripsEscapeBidiSeparatorsAndC1FromEveryValue(t *testing.T) {
	hostile := "a\x1b[31mb‮c d e\u0085f​g\x00h"
	i := structured.Info{
		Name: hostile, Version: hostile, Catalog: hostile, Adapter: hostile, Cwd: hostile, SessionID: hostile,
		Auth: []string{hostile}, MCP: []string{hostile},
		Setup: &structured.Setup{Message: hostile, Login: hostile},
		Session: bridge.SessionState{
			Modes:         &acp.Modes{Current: hostile},
			Models:        &acp.Models{Current: hostile},
			ConfigOptions: []acp.ConfigOption{{ID: hostile, Current: hostile}},
		},
	}
	got := infoText(i)
	for _, r := range got {
		if r != '\n' && (r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x202e || r == 0x2028 || r == 0x2029 || r == 0x200b) {
			t.Fatalf("U+%04X is in\n%q", r, got)
		}
	}
	if !strings.Contains(got, "Directory: a[31mbc d efgh\n") {
		t.Errorf("the letters were lost:\n%s", got)
	}
}
