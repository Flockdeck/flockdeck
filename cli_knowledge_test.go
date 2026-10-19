package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

func testKnowledgeEnv(inPane bool, vis string, visErr error) knowledgeEnv {
	return knowledgeEnv{
		inPane:     func() bool { return inPane },
		projectOf:  func(string) (string, error) { return "/work/proj", nil },
		visibility: func(string) (string, error) { return vis, visErr },
	}
}

func runK(t *testing.T, env knowledgeEnv, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runKnowledgeWith(env, args, &out)
	return out.String(), err
}

func TestKnowledgeOnAndOffPersistPerProject(t *testing.T) {
	isolateState(t)
	env := testKnowledgeEnv(false, "public", nil)
	if _, err := runK(t, env, "on"); err != nil {
		t.Fatal(err)
	}
	if got := store.LoadPrefs().KnowledgeSetting("/work/proj"); got != "on" {
		t.Fatalf("setting = %q after on", got)
	}
	if got := store.LoadPrefs().KnowledgeSetting("/work/other"); got != "" {
		t.Errorf("another project got %q", got)
	}
	if _, err := runK(t, env, "off"); err != nil {
		t.Fatal(err)
	}
	if got := store.LoadPrefs().KnowledgeSetting("/work/proj"); got != "off" {
		t.Errorf("setting = %q after off", got)
	}
}

func TestKnowledgeOnAndOffAreRefusedInsideAPane(t *testing.T) {
	isolateState(t)
	env := testKnowledgeEnv(true, "private", nil)
	for _, a := range []string{"on", "off"} {
		if _, err := runK(t, env, a); err == nil || !strings.Contains(err.Error(), "outside Flockdeck") {
			t.Errorf("%s in a pane: err = %v, want a refusal", a, err)
		}
	}
	if got := store.LoadPrefs().KnowledgeSetting("/work/proj"); got != "" {
		t.Errorf("a refused command changed the setting to %q", got)
	}
	if _, err := runK(t, env, "status"); err != nil {
		t.Errorf("status in a pane: %v", err)
	}
}

func TestKnowledgeStatusSaysWhereTheStateComesFrom(t *testing.T) {
	isolateState(t)
	cases := []struct {
		name string
		env  knowledgeEnv
		want string
	}{
		{"private default", testKnowledgeEnv(false, "private", nil), "pointer: on (default, repository is private)"},
		{"public default", testKnowledgeEnv(false, "public", nil), "pointer: ask, off until answered (repository is public)"},
		{"unknown default", testKnowledgeEnv(false, "", errors.New("offline")), "visibility is unknown"},
	}
	for _, c := range cases {
		out, err := runK(t, c.env)
		if err != nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: %q, %v, want %q", c.name, out, err, c.want)
		}
	}
	if _, err := runK(t, testKnowledgeEnv(false, "", nil), "off"); err != nil {
		t.Fatal(err)
	}
	out, _ := runK(t, testKnowledgeEnv(false, "private", nil), "status")
	if !strings.Contains(out, "pointer: off (set by you)") {
		t.Errorf("status after off = %q", out)
	}
}

func TestKnowledgeRejectsAnUnknownAction(t *testing.T) {
	isolateState(t)
	if _, err := runK(t, testKnowledgeEnv(false, "", nil), "maybe"); err == nil {
		t.Error("an unknown action was accepted")
	}
}

// A project can be a folder holding several repositories. The window keys the
// setting by that folder, so the command has to as well, not by the
// repository it happens to be run inside.
func TestKnowledgeUsesTheFolderTheProjectWasOpenedAs(t *testing.T) {
	isolateState(t)
	parent := t.TempDir()
	repo := filepath.Join(parent, "api")
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchRecents(parent); err != nil {
		t.Fatal(err)
	}
	store.FlushRecents()

	env := knowledgeEnv{
		inPane:     func() bool { return false },
		projectOf:  projectRootOf,
		visibility: func(string) (string, error) { return "private", nil },
	}
	if _, err := runK(t, env, "on", "-project", filepath.Join(repo, "pkg")); err != nil {
		t.Fatal(err)
	}
	if got := store.LoadPrefs().KnowledgeSetting(parent); got != "on" {
		t.Errorf("setting under the project folder = %q, want on", got)
	}
	out, err := runK(t, env, "status", "-project", repo)
	if err != nil || !strings.Contains(out, "project: "+parent) || !strings.Contains(out, "pointer: on (set by you)") {
		t.Errorf("status = %q, %v, want the project folder and the answer", out, err)
	}
}

func TestKnowledgeStatusCallsAnEmptyVisibilityUnknown(t *testing.T) {
	isolateState(t)
	out, err := runK(t, testKnowledgeEnv(false, "", nil))
	if err != nil || !strings.Contains(out, "visibility is unknown") {
		t.Errorf("status = %q, %v", out, err)
	}
}
