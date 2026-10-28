package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

func commandCard(t *testing.T, command string) Card {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newBridge(Config{})
	return b.PermissionOpen(acp.PermissionRequest{RequestID: "r", SessionID: "s",
		ToolCall: acp.ToolCall{ToolCallID: "c", Title: "t", Kind: "execute", RawInput: raw}})
}

// The card's Command is the first line of the command, so a command of two lines
// shows as one. CommandText is the whole of it, and is the only field a reader
// that has to show everything that will run may go by.
func TestCommandTextKeepsTheWholeCommand(t *testing.T) {
	for name, tc := range map[string]struct {
		in      string
		text    string
		altered bool
	}{
		"one line":         {"echo hi", "echo hi", false},
		"two lines":        {"echo hi\nrm -rf x", "echo hi\nrm -rf x", false},
		"tab":              {"a\tb", "a\tb", false},
		"escape sequence":  {"echo \x1b[31mred", "echo red", true},
		"bidi override":    {"echo \u202egpj.txt", "echo gpj.txt", true},
		"zero width space": {"ec\u200bho", "echo", true},
		"carriage return":  {"echo a\r\necho b", "echo a\necho b", true},
		"unicode":          {"echo 日本語 é", "echo 日本語 é", false},
		"nothing":          {"", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			c := commandCard(t, tc.in)
			if c.CommandText != tc.text || c.CommandAltered != tc.altered {
				t.Errorf("CommandText = %q altered %v, want %q altered %v", c.CommandText, c.CommandAltered, tc.text, tc.altered)
			}
		})
	}
	c := commandCard(t, "echo hi\nrm -rf x")
	if c.Command != "echo hi" {
		t.Errorf("Command = %q, the first line was expected (this is why CommandText exists)", c.Command)
	}
}

func TestCommandTextIsCutAndSaysSo(t *testing.T) {
	at := strings.Repeat("a", cardCommandCap)
	if c := commandCard(t, at); c.CommandText != at || c.CommandAltered {
		t.Errorf("a command at the cap: altered %v, len %d", c.CommandAltered, len(c.CommandText))
	}
	over := at + "b"
	c := commandCard(t, over)
	if !c.CommandAltered || len(c.CommandText) != cardCommandCap {
		t.Errorf("a command over the cap: altered %v, len %d", c.CommandAltered, len(c.CommandText))
	}
	// The cut is at a character.
	multi := strings.Repeat("é", cardCommandCap) // two bytes each
	c = commandCard(t, multi)
	if !c.CommandAltered || len(c.CommandText) > cardCommandCap || !strings.HasSuffix(c.CommandText, "é") {
		t.Errorf("a multibyte command over the cap: altered %v, len %d", c.CommandAltered, len(c.CommandText))
	}
}

func TestACardWithoutACommandHasNoCommandText(t *testing.T) {
	b, _ := newBridge(Config{})
	c := b.PermissionOpen(acp.PermissionRequest{RequestID: "r", SessionID: "s",
		ToolCall: acp.ToolCall{ToolCallID: "c", Title: "t", Kind: "execute", RawInput: json.RawMessage(`{"command":["ls","-l"]}`)}})
	if c.Command != "" || c.CommandText != "" || c.CommandAltered {
		t.Errorf("a command that is not a string: %+v", c)
	}
	c = b.PermissionOpen(acp.PermissionRequest{RequestID: "r", SessionID: "s",
		ToolCall: acp.ToolCall{ToolCallID: "c", Title: "t", Kind: "execute"}})
	if c.CommandText != "" || c.CommandAltered {
		t.Errorf("no input: %+v", c)
	}
}
