package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/record"
)

// recordingsState is a state directory of a test's own. The recordings command
// is given it, so nothing here reads or writes the real one.
func recordingsState(t *testing.T) func() (string, error) {
	t.Helper()
	dir := t.TempDir()
	return func() (string, error) { return dir, nil }
}

func TestRecordingsCommandListsAndNamesTheFolder(t *testing.T) {
	state := recordingsState(t)
	var out bytes.Buffer
	if err := listRecordings(nil, &out, state); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no recordings") {
		t.Errorf("an empty list says %q", out.String())
	}

	m := record.NewManager(state)
	t.Cleanup(m.Close)
	meta := record.Meta{Pane: "abcdef0123456789", PaneName: "api", Project: "shop", ProjectRoot: "/work/shop", Agent: "claude"}
	path, err := m.Start(meta, "turned on")
	if err != nil {
		t.Fatal(err)
	}
	m.Stop(meta, "turned off")

	out.Reset()
	if err := listRecordings(nil, &out, state); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "shop") || !strings.Contains(got, "api") || !strings.Contains(got, path) {
		t.Errorf("list = %q", got)
	}

	out.Reset()
	if err := listRecordings([]string{"-json"}, &out, state); err != nil {
		t.Fatal(err)
	}
	var info record.Info
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &info); err != nil || info.Path != path || info.Agent != "claude" {
		t.Errorf("json = %q (%v)", out.String(), err)
	}

	out.Reset()
	if err := listRecordings([]string{"-dir"}, &out, state); err != nil {
		t.Fatal(err)
	}
	dir, _ := state()
	if got := strings.TrimSpace(out.String()); got != filepath.Join(dir, "recordings") {
		t.Errorf("-dir = %q", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
}

func TestSpawnRecordFlagIsOffByDefault(t *testing.T) {
	req, err := parseSpawn([]string{"do the thing"})
	if err != nil || req.Record {
		t.Fatalf("default spawn: %+v, %v", req, err)
	}
	req, err = parseSpawn([]string{"-record", "do the thing"})
	if err != nil || !req.Record {
		t.Fatalf("-record: %+v, %v", req, err)
	}
	// After the task, like the other flags.
	if req, err = parseSpawn([]string{"do the thing", "--record"}); err != nil || !req.Record || req.Task != "do the thing" {
		t.Fatalf("trailing --record: %+v, %v", req, err)
	}
	if _, err := parseSpawn([]string{"-shell", "-record"}); err == nil {
		t.Error("a shell was allowed -record")
	}
}
