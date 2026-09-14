package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// This file is the desktop side of the phone's attach-a-picture flow: the
// attachImage command and its reply. A picture the phone downscales
// on-device and sends over the control socket is saved under Flockdeck's own
// state directory (store.Dir()), never into the user's own checkout, so a
// prompt can point an agent at a real file on disk without adding anything to
// the repository it is working in.

// attachImageResultMsg answers an attachImage command: the saved path, or an
// error the phone shows in place of the chip it was about to draw.
type attachImageResultMsg struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

// maxAttachedImageBytes bounds a decoded picture kept on disk. The phone
// downscales to comfortably under the control socket's own 1 MiB message cap
// before it ever sends one (see docs/plans/phone-conversation-view.md and the
// relay's own hub.go), so this is a generous backstop against a stale or
// misbehaving client rather than a limit anything real bumps into.
const maxAttachedImageBytes = 8 << 20

// attachedImageMaxAge is how long a saved picture is kept before the sweep at
// startup removes it: long enough to survive a conversation that runs into
// the next day, short enough that a machine used for years does not fill up
// with them.
const attachedImageMaxAge = 7 * 24 * time.Hour

// attachImageRateLimit and attachImageRateWindow bound how often one window
// may call attachImage -- see controlClient.attachImageLimit. Each call
// decodes and writes up to maxAttachedImageBytes to disk, with nothing else
// to slow a window sending them as fast as it can encode base64.
const (
	attachImageRateLimit  = 10
	attachImageRateWindow = 10 * time.Second
)

// attachImage saves a picture the phone sent for a pane's prompt, and answers
// with the path an agent in that pane can read it back from.
func (s *Server) attachImage(c *controlClient, paneID, name, mediaType, dataB64 string) {
	if !c.attachImageLimit.allow(attachImageRateLimit, attachImageRateWindow) {
		c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Error: "too many pictures attached too quickly"})
		return
	}
	go func() {
		defer s.surviveFor(c, "attaching a picture")
		if paneID == "" {
			return
		}
		// Scoped exactly as conversationOpen is: a client asking to attach to
		// a pane it cannot see -- closed since, or never open to it at all --
		// is answered plainly here, since this reply drives a chip the phone
		// is actively waiting on, unlike a background conversation stream
		// nobody may still be watching.
		if r := s.resolvePane(paneID); !r.found {
			c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Error: paneGone})
			return
		}
		path, err := saveAttachedImage(paneID, name, mediaType, dataB64)
		if err != nil {
			c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Error: err.Error()})
			return
		}
		c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Path: path})
	}()
}

// saveAttachedImage decodes, validates and writes one picture, and returns the
// path it was saved at.
func saveAttachedImage(paneID, name, mediaType, dataB64 string) (string, error) {
	_ = mediaType // the claimed type is never trusted over the bytes themselves
	if dataB64 == "" {
		return "", fmt.Errorf("no picture was sent")
	}
	// Bounding the encoded form first means a wildly oversized request never
	// pays for a full base64 decode before it is turned away.
	if len(dataB64) > base64.StdEncoding.EncodedLen(maxAttachedImageBytes) {
		return "", fmt.Errorf("that picture is too large to attach")
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return "", fmt.Errorf("that picture's data could not be read")
	}
	if len(data) > maxAttachedImageBytes {
		return "", fmt.Errorf("that picture is too large to attach")
	}
	ext, ok := sniffImageExt(data)
	if !ok {
		return "", fmt.Errorf("that file is not a picture flockdeck can attach")
	}

	dir, err := store.Dir()
	if err != nil {
		return "", fmt.Errorf("find where to save it: %w", err)
	}
	paneDir := filepath.Join(dir, "uploads", paneID)
	if err := os.MkdirAll(paneDir, 0o700); err != nil {
		return "", fmt.Errorf("create a place to save it: %w", err)
	}
	filename := fmt.Sprintf("%d-%s.%s", time.Now().UnixNano(), sanitizeAttachName(name), ext)
	path := filepath.Join(paneDir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("save it: %w", err)
	}
	return path, nil
}

// sanitizeAttachName reduces a name the phone sent to something safe to put
// in a path: no directory separators, nothing but the characters a filename
// needs, and never empty. The real extension comes from sniffImageExt, not
// from anything in name, so a claimed ".png" on a jpeg cannot mislabel the
// file that is actually written.
func sanitizeAttachName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "photo"
	}
	s := b.String()
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	return s
}

// sniffImageExt reads a file's own magic bytes rather than trusting what a
// client claims it is sending: png, jpeg, gif and webp are the whole list,
// the same four the chat view's own transcript images are limited to.
func sniffImageExt(data []byte) (ext string, ok bool) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return "png", true
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "jpg", true
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "gif", true
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp", true
	}
	return "", false
}

// cleanupAttachedImages removes an attached picture once it is older than
// maxAge, and any pane's now-empty upload folder along with it. Run once at
// startup, the same as store.SweepSessions: a picture attached in one run is
// not lost mid-conversation by a run that happens to restart soon after, but
// nothing here is meant to be kept for good.
func cleanupAttachedImages(maxAge time.Duration) {
	dir, err := store.Dir()
	if err != nil {
		return
	}
	root := filepath.Join(dir, "uploads")
	panes, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, pd := range panes {
		if !pd.IsDir() {
			continue
		}
		paneDir := filepath.Join(root, pd.Name())
		files, err := os.ReadDir(paneDir)
		if err != nil {
			continue
		}
		remaining := 0
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(paneDir, f.Name()))
				continue
			}
			remaining++
		}
		if remaining == 0 {
			_ = os.Remove(paneDir)
		}
	}
}
