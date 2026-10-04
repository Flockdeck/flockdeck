package transcript

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

const disagreeFixtureID = "44444444-5555-6666-7777-888888888888"

// disagreeDir is the one fixture folder whose replies deliberately disagree with
// themselves. TestClaudeFixturesRepeatUsageAcrossAReply skips it by this name
// and no other.
const disagreeDir = "claude_disagree"

// This documents the assumption in docs/recording-format.md ("Token usage and
// stop reason"): the entries of one reply repeat the same usage and stop reason,
// so writing them once, from the first line the reply produces, is exact. Here
// the entries of msg_a do not agree (a thinking entry with a partial count, a
// text entry, then a tool call with a larger count and another stop reason). The
// export writes the numbers of the first entry that produces a line (the text
// entry), once, and nothing from the later ones, because a line already written
// cannot be edited. Changing that (taking the last, summing, or anything else)
// is a decision about the recording format, not a refactor; this test is where
// that decision shows up.
func TestClaudeExportTakesUsageFromFirstLineWhenEntriesDisagree(t *testing.T) {
	home, err := filepath.Abs(filepath.Join("testdata", disagreeDir, "home"))
	if err != nil {
		t.Fatal(err)
	}
	spec := agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true}}
	before := ReplyDisagreements()
	evs, _, err := collect(t, spec, disagreeFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	var usages []ExportUsage
	var stops []string
	for _, e := range evs {
		if e.Usage != nil {
			usages = append(usages, *e.Usage)
		}
		if e.StopReason != "" {
			stops = append(stops, e.StopReason)
		}
		if e.Kind == ExportToolCall && (e.Usage != nil || e.StopReason != "") {
			t.Errorf("tool call carries %+v %q, but its reply's first line already did", e.Usage, e.StopReason)
		}
	}
	wantUsage := []ExportUsage{{InputTokens: Count(3), OutputTokens: Count(50), CacheCreationInputTokens: Count(0), CacheReadInputTokens: Count(2000)}, {InputTokens: Count(4), OutputTokens: Count(7), CacheCreationInputTokens: Count(0), CacheReadInputTokens: Count(3000)}}
	if !reflect.DeepEqual(usages, wantUsage) {
		t.Errorf("usage written %+v, want one per reply from its first line %+v", usages, wantUsage)
	}
	if want := []string{"tool_use", "end_turn"}; !reflect.DeepEqual(stops, want) {
		t.Errorf("stop reasons written %v, want one per reply from its first line %v", stops, want)
	}
	// Two later entries of msg_a disagree with the first one seen; msg_b has one.
	if got := ReplyDisagreements() - before; got != 2 {
		t.Errorf("counted %d disagreeing entries, want 2", got)
	}
}

// A conversation whose replies repeat their numbers is not counted as
// disagreeing.
func TestClaudeExportCountsNoDisagreementWhenRepliesAgree(t *testing.T) {
	before := ReplyDisagreements()
	if _, _, err := collect(t, moreSpec(t), moreFixtureID); err != nil {
		t.Fatal(err)
	}
	if got := ReplyDisagreements() - before; got != 0 {
		t.Errorf("counted %d disagreeing entries in a conversation whose replies agree", got)
	}
}

// This fails when the entries of one reply, in any fixture conversation here
// (they stand for what Claude Code writes), carry different usage or different
// stop reasons. That means Claude Code changed: the export writes each once,
// from the first line the reply produces, and cannot go back to a line it has
// written, so per-reply totals are exact only while the entries agree. Read the
// sentence in docs/recording-format.md under "Token usage and stop reason"
// before changing anything; the meaning of `usage` and `stopReason` may need to
// change, which is a format decision.
//
// An entry with no stop reason (null) before one that has it is not a
// disagreement: the docs describe that case and the export handles it.
func TestClaudeFixturesRepeatUsageAcrossAReply(t *testing.T) {
	checked := 0
	err := filepath.WalkDir("testdata", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == disagreeDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		type details struct{ usage, stop string }
		seen := map[string]details{}
		for n, ln := range bytes.Split(raw, []byte("\n")) {
			var e struct {
				Type    string `json:"type"`
				Message struct {
					ID         string          `json:"id"`
					StopReason string          `json:"stop_reason"`
					Usage      json.RawMessage `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(ln, &e) != nil || e.Type != "assistant" || e.Message.ID == "" {
				continue
			}
			var u map[string]any
			_ = json.Unmarshal(e.Message.Usage, &u)
			cur := details{stop: e.Message.StopReason}
			if u != nil {
				norm, _ := json.Marshal(u) // map keys come out sorted
				cur.usage = string(norm)
			}
			prev, ok := seen[e.Message.ID]
			if !ok {
				seen[e.Message.ID] = cur
				continue
			}
			checked++
			if cur.usage != "" && prev.usage != "" && cur.usage != prev.usage {
				t.Errorf("%s line %d: reply %s has different usage on its entries (%s, then %s). Claude Code no longer repeats a reply's usage on each entry, and the export writes it once, from the first line; see docs/recording-format.md, \"Token usage and stop reason\"", path, n+1, e.Message.ID, prev.usage, cur.usage)
			}
			if cur.stop != "" && prev.stop != "" && cur.stop != prev.stop {
				t.Errorf("%s line %d: reply %s has different stop reasons on its entries (%s, then %s). Claude Code no longer repeats a reply's stop reason on each entry, and the export writes it once, from the first line; see docs/recording-format.md, \"Token usage and stop reason\"", path, n+1, e.Message.ID, prev.stop, cur.stop)
			}
			if prev.usage == "" {
				prev.usage = cur.usage
			}
			if prev.stop == "" {
				prev.stop = cur.stop
			}
			seen[e.Message.ID] = prev
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no reply has more than one entry in the fixtures, so the check looks at nothing")
	}
}
