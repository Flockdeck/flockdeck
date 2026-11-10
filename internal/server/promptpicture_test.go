package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func TestPromptPictureKey(t *testing.T) {
	for key, want := range map[string]struct {
		e  string
		n  int
		ok bool
	}{
		"e12#0":  {"e12", 0, true},
		"e12#3":  {"e12", 3, true},
		"e12":    {"", 0, false},
		"#1":     {"", 0, false},
		"e1#":    {"", 0, false},
		"e1#-1":  {"", 0, false},
		"e1#x":   {"", 0, false},
		"e1#123": {"", 0, false},
	} {
		e, n, ok := promptPictureKey(key)
		if e != want.e || n != want.n || ok != want.ok {
			t.Errorf("%q = %q %d %v, want %+v", key, e, n, ok, want)
		}
	}
}

func TestPromptPictureDetailServesTheUploadOrSaysGone(t *testing.T) {
	root := t.TempDir()
	data := testPNG(t, 2, 2)
	writeUpload(t, root, "pane-a", "1-shot.png", data)
	entries := []transcript.Entry{
		{ID: "e1", Kind: transcript.KindPrompt, Images: []transcript.PromptImage{
			{MediaType: "image/png", Bytes: len(data), Upload: "1-shot.png"},
			{MediaType: "image/png", Bytes: 5, Upload: "2-swept.png"},
			{MediaType: "image/png", Bytes: 5, Upload: "../../etc/passwd"},
			{MediaType: "image/png", Bytes: 5},
		}},
		{ID: "e2", Kind: transcript.KindReply, Markdown: "x"},
	}
	d, ok := promptPictureDetail(root, "pane-a", entries, "e1", 0)
	if !ok || d.Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("first picture: ok=%v", ok)
	}
	for _, c := range []struct {
		pane, id string
		n        int
	}{
		{"pane-a", "e1", 1},    // swept
		{"pane-a", "e1", 2},    // a name that is not an upload's
		{"pane-a", "e1", 3},    // no upload recorded
		{"pane-a", "e1", 4},    // past the list
		{"pane-a", "e2", 0},    // not a prompt
		{"pane-a", "e9", 0},    // no such entry
		{"pane-b", "e1", 0},    // another pane's folder
		{"../pane-a", "e1", 0}, // a pane id that climbs
	} {
		if _, ok := promptPictureDetail(root, c.pane, entries, c.id, c.n); ok {
			t.Errorf("%+v served, want gone", c)
		}
	}
	if err := os.Remove(filepath.Join(root, "pane-a", "1-shot.png")); err != nil {
		t.Fatal(err)
	}
	if _, ok := promptPictureDetail(root, "pane-a", entries, "e1", 0); ok {
		t.Error("a removed file was served")
	}
}
