package record

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recFolder makes a project folder under a fresh state dir, laid out as the
// recorder does, and returns the dir function for List and the folder.
func recFolder(t *testing.T) (func() (string, error), string) {
	t.Helper()
	dir := t.TempDir()
	folder := filepath.Join(dir, recordingsDir, "shop-0a1b2c3d")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	return func() (string, error) { return dir, nil }, folder
}

func startLine(v int) string {
	return `{"v":` + string(rune('0'+v)) + `,"seq":1,"time":"2026-10-01T10:15:01.5Z","session":"s","agent":"claude","project":"shop","type":"recording_started","text":"start"}` + "\n"
}

func TestListReportsTheFormatVersion(t *testing.T) {
	dir, folder := recFolder(t)
	files := map[string]string{
		"20261001T101530Z-00000002.jsonl": startLine(2),
		"20261001T101530Z-00000003.jsonl": startLine(3),
		"20261001T101530Z-00000001.jsonl": startLine(1),
		"20261001T101530Z-0000000x.jsonl": `{"v":2,"type":"assistant_message"}` + "\n",
		"20261001T101530Z-0000000y.jsonl": "not json\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(folder, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(files) {
		t.Fatalf("listed %d, want %d: files are listed and flagged, not hidden", len(got), len(files))
	}
	for _, in := range got {
		name := filepath.Base(in.Path)
		want := strings.HasSuffix(name, "00000002.jsonl")
		if in.Supported() != want {
			t.Errorf("%s: Supported = %v, want %v (version %d, started %q)", name, in.Supported(), want, in.Version, in.Started)
		}
	}
	for _, in := range got {
		if strings.HasSuffix(in.Path, "00000003.jsonl") && in.Version != 3 {
			t.Errorf("version of the v3 file = %d", in.Version)
		}
	}
}

func TestListContextStopsWhenTheContextIsDone(t *testing.T) {
	dir, folder := recFolder(t)
	if err := os.WriteFile(filepath.Join(folder, "20261001T101530Z-00000002.jsonl"), []byte(startLine(2)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := ListContext(ctx, dir)
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Errorf("ListContext on a done context = %v, %v", got, err)
	}
}

func TestListLeavesOutALinkNamedAsARecording(t *testing.T) {
	dir, folder := recFolder(t)
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(startLine(2)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(folder, "20261001T101530Z-00000009.jsonl")); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	got, err := List(dir)
	if err != nil || len(got) != 0 {
		t.Errorf("a link was listed: %v, %v", got, err)
	}
}
