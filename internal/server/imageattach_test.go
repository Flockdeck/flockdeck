package server

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// tinyPNGBase64Bytes is a real, decodable one-pixel PNG's own base64 -- the
// same fixture picture the transcript package's image tests use.
const tinyPNGBase64Bytes = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

func decodeAttachResult(t *testing.T, raw []byte) attachImageResultMsg {
	t.Helper()
	var msg attachImageResultMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal attachImageResult: %v", err)
	}
	return msg
}

// TestAttachImageSavesUnderStoreDir is the core test: without attachImage
// resolving the pane and writing the decoded bytes under store.Dir(), a
// picture the phone sends never reaches disk at all, and no path comes back
// for the prompt to reference.
func TestAttachImageSavesUnderStoreDir(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "claude")

	c := &controlClient{out: make(chan []byte, 8)}
	srv.attachImage(c, paneID, "IMG_0001.jpg", "image/png", tinyPNGBase64Bytes)
	msg := decodeAttachResult(t, nextRaw(t, c))
	if msg.Error != "" {
		t.Fatalf("attachImage refused a valid picture: %s", msg.Error)
	}
	if msg.Path == "" {
		t.Fatal("no path came back for a saved picture")
	}

	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(dir, "uploads", paneID)
	if filepath.Dir(msg.Path) != wantDir {
		t.Errorf("saved under %s, want under %s", filepath.Dir(msg.Path), wantDir)
	}
	if !strings.HasSuffix(msg.Path, ".png") {
		t.Errorf("path = %s, want a .png extension sniffed from the bytes", msg.Path)
	}

	got, err := os.ReadFile(msg.Path)
	if err != nil {
		t.Fatalf("reading the saved file: %v", err)
	}
	want, err := base64.StdEncoding.DecodeString(tinyPNGBase64Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("saved bytes do not match what was sent")
	}
}

// TestAttachImageRefusesAPaneItCannotSee covers the same scoping rule as
// conversationOpen: a phone must not be able to attach a picture to a pane it
// has no reason to see by naming its id.
func TestAttachImageRefusesAPaneItCannotSee(t *testing.T) {
	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.attachImage(c, "no-such-pane", "photo.jpg", "image/jpeg", tinyPNGBase64Bytes)
	msg := decodeAttachResult(t, nextRaw(t, c))
	if msg.Error == "" || msg.Path != "" {
		t.Errorf("attaching to an unknown pane should be refused, got %+v", msg)
	}
}

// TestAttachImageRefusesADisguisedFile covers validating by the bytes'
// magic number, not by whatever the phone claims: a client that lies about
// mediaType, or sends something that is not a picture at all, must not have
// it written to disk.
func TestAttachImageRefusesADisguisedFile(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "claude")

	notAPicture := base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\necho hi\n"))
	c := &controlClient{out: make(chan []byte, 8)}
	srv.attachImage(c, paneID, "totally-a.png", "image/png", notAPicture)
	msg := decodeAttachResult(t, nextRaw(t, c))
	if msg.Error == "" || msg.Path != "" {
		t.Errorf("a file that is not really a picture should be refused, got %+v", msg)
	}

	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", paneID)); err == nil {
		t.Error("nothing should have been written for a refused file")
	}
}

// TestAttachImageRefusesOversizeData covers the size cap: a decoded picture
// over the limit must be refused, not written to disk half-checked.
func TestAttachImageRefusesOversizeData(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "claude")

	huge := make([]byte, maxAttachedImageBytes+1)
	copy(huge, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	c := &controlClient{out: make(chan []byte, 8)}
	srv.attachImage(c, paneID, "big.png", "image/png", base64.StdEncoding.EncodeToString(huge))
	msg := decodeAttachResult(t, nextRaw(t, c))
	if msg.Error == "" || msg.Path != "" {
		t.Errorf("an oversized picture should be refused, got %+v", msg)
	}
}

// TestAttachImageIsRateLimitedPerWindow covers the backstop against a phone
// sending attachImage in a tight loop: each call decodes and writes up to
// maxAttachedImageBytes to disk, and nothing before this bounded how many of
// those one window could ask for in a burst.
func TestAttachImageIsRateLimitedPerWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "claude")
	c := &controlClient{out: make(chan []byte, 256)}

	for i := 0; i < attachImageRateLimit; i++ {
		srv.attachImage(c, paneID, "photo.png", "image/png", tinyPNGBase64Bytes)
		msg := decodeAttachResult(t, nextRaw(t, c))
		if msg.Error != "" {
			t.Fatalf("call %d: attachImage said %q, want it to succeed", i, msg.Error)
		}
	}

	srv.attachImage(c, paneID, "photo.png", "image/png", tinyPNGBase64Bytes)
	msg := decodeAttachResult(t, nextRaw(t, c))
	if msg.Error == "" || msg.Path != "" {
		t.Fatalf("call %d: attachImage said %+v, want a rate-limit refusal", attachImageRateLimit, msg)
	}

	// A second window is unaffected: the limit is per connection, not global.
	other := &controlClient{out: make(chan []byte, 8)}
	srv.attachImage(other, paneID, "photo.png", "image/png", tinyPNGBase64Bytes)
	msg = decodeAttachResult(t, nextRaw(t, other))
	if msg.Error != "" {
		t.Fatalf("a fresh window was refused by another window's rate limit: %q", msg.Error)
	}
}

// TestCleanupAttachedImagesRemovesOldFilesOnly covers the retention sweep:
// an old attachment goes, a fresh one stays, and a pane folder emptied by the
// sweep is removed too.
func TestCleanupAttachedImagesRemovesOldFilesOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	stateDir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	oldPaneDir := filepath.Join(stateDir, "uploads", "pane-old")
	freshPaneDir := filepath.Join(stateDir, "uploads", "pane-fresh")
	if err := os.MkdirAll(oldPaneDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(freshPaneDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldPaneDir, "old.png")
	freshFile := filepath.Join(freshPaneDir, "fresh.png")
	if err := os.WriteFile(oldFile, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freshFile, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	cleanupAttachedImages(7 * 24 * time.Hour)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("an attachment older than the retention window should have been removed")
	}
	if _, err := os.Stat(oldPaneDir); !os.IsNotExist(err) {
		t.Error("a pane's upload folder emptied by the sweep should be removed too")
	}
	if _, err := os.Stat(freshFile); err != nil {
		t.Errorf("a fresh attachment should survive the sweep: %v", err)
	}
}
