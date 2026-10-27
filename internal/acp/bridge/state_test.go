package bridge

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// feed decodes a recorded update the way the client does and gives it to the
// bridge. The client's decoder is not exported, so the updates are built with
// the same fields it fills.
func commands(names ...string) acp.Update {
	u := acp.Update{Kind: acp.UpdateAvailableCommands, Commands: []acp.Command{}}
	for _, n := range names {
		u.Commands = append(u.Commands, acp.Command{Name: n, Description: "d " + n})
	}
	return u
}

func TestCommandsReplaceTheLastList(t *testing.T) {
	b := New(Config{})
	b.Update(commands("compact", "review"))
	if got := b.Session().Commands; len(got) != 2 || got[0].Name != "compact" || got[1].Description != "d review" {
		t.Fatalf("commands = %+v", got)
	}
	b.Update(commands("init"))
	if got := b.Session().Commands; len(got) != 1 || got[0].Name != "init" {
		t.Errorf("the second list did not replace the first: %+v", got)
	}
	b.Update(commands())
	if got := b.Session().Commands; len(got) != 0 {
		t.Errorf("an empty list did not clear them: %+v", got)
	}
	// An update with no list at all changes nothing.
	b.Update(commands("keep"))
	b.Update(acp.Update{Kind: acp.UpdateAvailableCommands})
	if got := b.Session().Commands; len(got) != 1 {
		t.Errorf("an update with no list changed the commands: %+v", got)
	}
}

func TestSessionStateIsCleanedAndBounded(t *testing.T) {
	b := New(Config{})
	var many []string
	for i := 0; i < maxCommands+50; i++ {
		many = append(many, "c"+strconv.Itoa(i))
	}
	u := commands(many...)
	u.Commands = append(u.Commands, acp.Command{Name: ""}, acp.Command{Name: "x\x1b[31mred\x1b[0m‮", Description: strings.Repeat("d", 5000), Hint: "h\nh"})
	b.Update(u)
	got := b.Session().Commands
	if len(got) != maxCommands {
		t.Errorf("kept %d commands, want %d", len(got), maxCommands)
	}

	b2 := New(Config{})
	b2.Update(acp.Update{Kind: acp.UpdateAvailableCommands, Commands: []acp.Command{
		{Name: "", Description: "no name"},
		{Name: "x\x1b[31mred\x1b[0m‮", Description: strings.Repeat("d", 5000), Hint: "h\nh"},
	}})
	c := b2.Session().Commands
	if len(c) != 1 || c[0].Name != "xred" || len([]rune(c[0].Description)) > 401 || strings.ContainsAny(c[0].Hint, "\n") {
		t.Errorf("commands = %+v", c)
	}

	// Config options: the count, the values and every text are bounded.
	var opts []acp.ConfigOption
	for i := 0; i < maxConfigOptions+5; i++ {
		o := acp.ConfigOption{ID: "o" + strconv.Itoa(i), Name: "N\x07", Current: strings.Repeat("v", 1000)}
		for j := 0; j < maxConfigValues+5; j++ {
			o.Values = append(o.Values, acp.ConfigValue{Value: "v" + strconv.Itoa(j)})
		}
		opts = append(opts, o)
	}
	b2.Update(acp.Update{Kind: acp.UpdateConfigOption, ConfigOptions: opts})
	co := b2.Session().ConfigOptions
	if len(co) != maxConfigOptions || len(co[0].Values) != maxConfigValues || co[0].Name != "N" || len([]rune(co[0].Current)) > labelCap+1 {
		t.Errorf("config options: %d options, %d values, name %q, current %d runes", len(co), len(co[0].Values), co[0].Name, len([]rune(co[0].Current)))
	}
}

func TestModeUpdateAfterTheModesBlock(t *testing.T) {
	b := New(Config{})
	b.SessionNew(acp.SessionNew{SessionID: "s", Modes: &acp.Modes{Current: "default", Available: []acp.Mode{{ID: "default", Name: "Default"}, {ID: "yolo"}}},
		Models: &acp.Models{Current: "pro", Available: []acp.Model{{ID: "pro", Name: "Pro"}}}})
	s := b.Session()
	if s.Modes == nil || s.Modes.Current != "default" || len(s.Modes.Available) != 2 || s.Models == nil || s.Models.Current != "pro" {
		t.Fatalf("state = %+v %+v", s.Modes, s.Models)
	}
	b.Update(acp.Update{Kind: acp.UpdateCurrentMode, ModeID: "yolo"})
	s = b.Session()
	if s.Modes.Current != "yolo" || len(s.Modes.Available) != 2 {
		t.Errorf("modes = %+v", s.Modes)
	}
	// A mode update before any modes block still records the mode.
	b2 := New(Config{})
	b2.Update(acp.Update{Kind: acp.UpdateCurrentMode, ModeID: "plan"})
	if m := b2.Session().Modes; m == nil || m.Current != "plan" || m.Available == nil {
		t.Errorf("modes = %+v", m)
	}
	// An empty id changes nothing.
	b.Update(acp.Update{Kind: acp.UpdateCurrentMode})
	if b.Session().Modes.Current != "yolo" {
		t.Error("an empty mode id changed the mode")
	}
}

// The session/new reply can be handled after an update that followed it. The
// older reply must not put its values back.
func TestSessionNewDoesNotOverwriteALaterUpdate(t *testing.T) {
	b := New(Config{})
	b.Update(acp.Update{Kind: acp.UpdateConfigOption, ConfigOptions: []acp.ConfigOption{{ID: "model", Current: "small"}}})
	b.Update(acp.Update{Kind: acp.UpdateCurrentMode, ModeID: "yolo"})
	b.SessionNew(acp.SessionNew{
		ConfigOptions: []acp.ConfigOption{{ID: "model", Current: "big"}},
		Modes:         &acp.Modes{Current: "default", Available: []acp.Mode{{ID: "default"}, {ID: "yolo"}}},
	})
	s := b.Session()
	if s.ConfigOptions[0].Current != "small" || s.Modes.Current != "yolo" {
		t.Errorf("state = %+v %+v", s.ConfigOptions, s.Modes)
	}
}

func TestConfigOptionsReplaceAndKeepValues(t *testing.T) {
	b := New(Config{})
	b.SessionNew(acp.SessionNew{ConfigOptions: []acp.ConfigOption{{ID: "model", Name: "Model", Category: "model", Type: "select", Current: "big",
		Values: []acp.ConfigValue{{Value: "big", Name: "Big"}, {Value: "small", Name: "Small", Group: "g"}}}}})
	o := b.Session().ConfigOptions
	if len(o) != 1 || o[0].Current != "big" || len(o[0].Values) != 2 || o[0].Values[1].Group != "g" {
		t.Fatalf("options = %+v", o)
	}
	b.Update(acp.Update{Kind: acp.UpdateConfigOption, ConfigOptions: []acp.ConfigOption{{ID: "model", Current: "small"}, {ID: "effort", Current: "high"}}})
	o = b.Session().ConfigOptions
	if len(o) != 2 || o[0].Current != "small" || o[1].ID != "effort" {
		t.Errorf("options = %+v", o)
	}
	// What Session returns is a copy.
	o[0].Values = append(o[0].Values, acp.ConfigValue{Value: "zzz"})
	o[0].Current = "mutated"
	if b.Session().ConfigOptions[0].Current != "small" {
		t.Error("changing the returned options changed the bridge's")
	}
}

func TestSessionInfoPlanMarkdownAndCapabilities(t *testing.T) {
	b := New(Config{})
	title := "Fix\x1b[2J the parser\nplease"
	b.Update(acp.Update{Kind: acp.UpdateSessionInfo, InfoTitle: &title})
	if got := b.Session().Title; got != "Fix the parser" && got != "Fix the parser please" {
		t.Errorf("title = %q", got)
	}
	empty := ""
	b.Update(acp.Update{Kind: acp.UpdateSessionInfo, InfoTitle: &empty})
	if b.Session().Title != "" {
		t.Errorf("a cleared title stayed: %q", b.Session().Title)
	}
	other := "kept"
	b.Update(acp.Update{Kind: acp.UpdateSessionInfo, InfoTitle: &other})
	b.Update(acp.Update{Kind: acp.UpdateSessionInfo})
	if b.Session().Title != "kept" {
		t.Errorf("an update with no title changed it: %q", b.Session().Title)
	}

	b.Update(acp.Update{Kind: acp.UpdatePlanUpdate, PlanMarkdown: "# Plan\n1. a\x1b[31m\n" + strings.Repeat("x", maxPlanMarkdown)})
	md := b.Session().PlanMarkdown
	if !strings.HasPrefix(md, "# Plan\n1. a") || strings.Contains(md, "\x1b") || len(md) > maxPlanMarkdown {
		t.Errorf("plan markdown: %d bytes, starts %.20q", len(md), md)
	}
	if len(b.Plan()) != 0 {
		t.Error("a markdown plan became plan rows")
	}

	b.Capabilities(acp.AgentCapabilities{LoadSession: true, Prompt: acp.PromptCapabilities{Image: true}, Raw: json.RawMessage(`{"secret":1}`)})
	c := b.Session().Capabilities
	if !c.LoadSession || !c.Prompt.Image || c.Raw != nil {
		t.Errorf("capabilities = %+v", c)
	}
}

func TestCompactionIsANotice(t *testing.T) {
	b := New(Config{})
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: "before"}})
	b.Update(acp.Update{Kind: acp.UpdateCompaction, Compaction: &acp.Compaction{Status: "completed", Message: "Compacted 80\x1b[0m messages."}})
	b.Update(acp.Update{Kind: acp.UpdateCompactionUpdate, Compaction: &acp.Compaction{Status: "running"}})
	b.Update(acp.Update{Kind: acp.UpdateCompaction})
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: "after"}})
	var got []string
	for _, e := range b.Snapshot() {
		got = append(got, e.Kind+":"+e.Text+e.Markdown)
	}
	want := []string{"reply:before", "notice:Compacted 80 messages.", "notice:Context compaction: running", "notice:Context compacted.", "reply:after"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries = %q, want %q", got, want)
	}
}

func TestAMessageOutsideATurnIsAPrompt(t *testing.T) {
	b := New(Config{})
	chunk := func(s string) acp.Update {
		return acp.Update{Kind: acp.UpdateUserMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: s}}
	}
	// A replayed history: user and agent messages, with the user's in pieces.
	b.Update(chunk("Fix "))
	b.Update(chunk("the bug"))
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: "Done."}})
	b.Update(chunk("Thanks"))
	var got []string
	for _, e := range b.Snapshot() {
		got = append(got, e.Kind+":"+e.Text+e.Markdown)
	}
	want := []string{"prompt:Fix the bug", "reply:Done.", "prompt:Thanks"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries = %q, want %q", got, want)
	}
}

// An agent that echoes our prompt must not make it appear twice.
func TestAnEchoedPromptInATurnIsIgnored(t *testing.T) {
	b := New(Config{})
	b.TurnStart("hello")
	b.Update(acp.Update{Kind: acp.UpdateUserMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: "hello"}})
	b.Update(acp.Update{Kind: acp.UpdateAgentMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: "hi"}})
	b.TurnEnd("end_turn")
	var prompts int
	for _, e := range b.Snapshot() {
		if e.Kind == transcript.KindPrompt {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("%d prompt entries, want 1", prompts)
	}
}

func TestAHugeUserMessageIsCapped(t *testing.T) {
	b := New(Config{})
	big := strings.Repeat("u", maxText/2)
	for i := 0; i < 4; i++ {
		b.Update(acp.Update{Kind: acp.UpdateUserMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: big}})
	}
	if n := len(b.Snapshot()[0].Text); n > maxText {
		t.Errorf("prompt holds %d bytes, cap is %d", n, maxText)
	}
}
