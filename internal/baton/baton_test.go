package baton

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func sample() Baton {
	return Baton{
		ID:         "20261001-090000-0a1b2c",
		Title:      "Add a retry to the fetch client",
		FromPane:   "pane-1",
		FromAgent:  "claude",
		FromModel:  "opus",
		Cwd:        `C:\work\shop`,
		Branch:     "feat/retry",
		BaseCommit: "a1b2c3d (feat/retry, 2 commits ahead of main)",
		Created:    time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		Derived:    []string{"20260930-120000-ffffff"},
		Redactions: []Redaction{{"aws-key", 2}},
		Sections: map[Section]string{
			Goal:      "Add a retry.\n\nKeep the public API.",
			Decisions: "- Backoff is capped at 5 s: the proxy times out after 6.",
			Commands:  "- `go test ./...` - FAILED",
		},
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	b := sample()
	got, err := Parse(Render(b))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Errorf("round trip changed it:\n got %+v\nwant %+v", got, b)
	}
}

func TestRenderLeavesEmptySectionsOut(t *testing.T) {
	out := Render(sample())
	if strings.Contains(out, "## Open questions") || !strings.Contains(out, "## Goal") {
		t.Errorf("sections: %s", out)
	}
}

func TestParseKeepsAnEditorsOwnHeadings(t *testing.T) {
	text := Render(sample()) + "\n## Notes from me\n\nremember the proxy\n"
	got, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Section(NotInCheckout), "## Notes from me") {
		// The last known section is the one the unknown heading falls into.
		if !strings.Contains(got.Section(Commands), "## Notes from me") {
			t.Errorf("an unknown heading was dropped: %+v", got.Sections)
		}
	}
}

func TestASectionHeadingInsideASectionSurvives(t *testing.T) {
	b := sample().Set(Decisions, "pasted:\n## Goal\nnot a new section")
	got, err := Parse(Render(b))
	if err != nil {
		t.Fatal(err)
	}
	if got.Section(Decisions) != b.Section(Decisions) || got.Section(Goal) != b.Section(Goal) {
		t.Errorf("sections = %+v", got.Sections)
	}
}

func TestParseRefusesWhatIsNotABaton(t *testing.T) {
	if _, err := Parse("# just notes\n"); err != ErrNotBaton {
		t.Errorf("err = %v", err)
	}
}

func TestForkIsANewBatonThatRemembersTheOld(t *testing.T) {
	b := sample()
	f := b.Fork(b.Created.Add(time.Hour))
	if f.ID == b.ID || !ValidID(f.ID) {
		t.Errorf("fork id = %q", f.ID)
	}
	if got := f.Derived; len(got) != 2 || got[1] != b.ID {
		t.Errorf("derived = %v", got)
	}
	f = f.Set(Goal, "changed")
	if b.Section(Goal) == "changed" {
		t.Error("editing the fork changed the original")
	}
}

func TestNewIDShapeAndOnlyThat(t *testing.T) {
	id := NewID(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	if !ValidID(id) || !strings.HasPrefix(id, "20261001-090000-") {
		t.Errorf("id = %q", id)
	}
	for _, bad := range []string{"", "../x", `..\x`, "self", "20261001-090000-zzzzzz", "pane-1"} {
		if ValidID(bad) {
			t.Errorf("%q is a valid id", bad)
		}
	}
}
