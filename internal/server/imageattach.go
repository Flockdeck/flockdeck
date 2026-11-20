package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
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
// error the phone shows in place of the chip it was about to draw. Req is the
// request's own req, echoed (since API 1.33), so a window sending several
// pictures matches each result to its chip.
type attachImageResultMsg struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Req   string `json:"req,omitempty"`
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

// The disk a picture may take. A sealed device is let attach attachDeviceBytes
// in attachDeviceWindow (it is a paired phone with Control agents, but a stolen
// one must not fill the disk); each pane's folder is kept to attachPaneFiles and
// attachPaneBytes, the oldest going first; and the age sweep runs at most every
// attachSweepEvery while pictures are being attached, not only at startup.
const (
	attachDeviceBytes  = 48 << 20
	attachDeviceWindow = time.Hour
	attachPaneFiles    = 20
	attachPaneBytes    = 64 << 20
	attachSweepEvery   = time.Hour
)

// lastAttachSweep is when the age sweep last ran, as Unix nanoseconds.
var lastAttachSweep atomic.Int64

// reserveUpload says whether the device may attach n more bytes now, and counts
// them if so.
func (d *sealedDevice) reserveUpload(n int64, now time.Time) bool {
	d.dmu.Lock()
	defer d.dmu.Unlock()
	if now.Sub(d.upSince) > attachDeviceWindow {
		d.upSince, d.upBytes = now, 0
	}
	if d.upBytes+n > attachDeviceBytes {
		return false
	}
	d.upBytes += n
	return true
}

// attachImage saves a picture the phone sent for a pane's prompt, and answers
// with the path an agent in that pane can read it back from.
func (s *Server) attachImage(c *controlClient, paneID, req, name, mediaType, dataB64 string) {
	if !c.attachImageLimit.allow(attachImageRateLimit, attachImageRateWindow) {
		c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Req: req, Error: "too many pictures attached too quickly"})
		return
	}
	go func() {
		defer s.surviveFor(c, "attaching a picture")
		if paneID == "" {
			return
		}
		if now := time.Now().UnixNano(); now-lastAttachSweep.Load() > int64(attachSweepEvery) {
			lastAttachSweep.Store(now)
			cleanupAttachedImages(attachedImageMaxAge)
		}
		// Scoped exactly as conversationOpen is: a client asking to attach to
		// a pane it cannot see -- closed since, or never open to it at all --
		// is answered plainly here, since this reply drives a chip the phone
		// is actively waiting on, unlike a background conversation stream
		// nobody may still be watching.
		if r := s.resolvePane(paneID); !r.found {
			c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Req: req, Error: paneGone})
			return
		}
		// A sealed device has a byte quota; the check is on the encoded size, an
		// upper bound of what is written. It is charged only now, after the pane is
		// known to be one the device may attach to: a request for a pane that is not
		// there costs the device nothing, and writes nothing.
		owner := ""
		if c.sealed != nil {
			if !s.sealed.device(c.device).reserveUpload(int64(len(dataB64))*3/4, time.Now()) {
				c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Req: req, Error: "too many pictures attached; try again later"})
				return
			}
			owner = uploadOwner(c.device)
		}
		path, err := saveAttachedImageFor(owner, paneID, name, mediaType, dataB64)
		if err != nil {
			c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Req: req, Error: err.Error()})
			return
		}
		c.sendJSON(attachImageResultMsg{Type: "attachImageResult", ID: paneID, Req: req, Path: path})
	}()
}

// uploadOwner is the tag a sealed device's pictures carry in their file names
// ("dv", eight hex digits of a hash of the device id, an underscore), so that the
// cap on a pane's folder, and what is trimmed to keep it, is that device's own
// pictures and never the desk's or another device's. The desk's own are untagged.
func uploadOwner(device string) string {
	sum := sha256.Sum256([]byte(device))
	return "dv" + hex.EncodeToString(sum[:4]) + "_"
}

// uploadTag matches the tag uploadOwner gives, after a file's leading number.
var uploadTag = regexp.MustCompile(`^[0-9]+-dv[0-9a-f]{8}_`)

// ownedUpload says whether a file in a pane's folder is owner's: a tagged name
// carries that owner's tag; an owner of "" (the desk) owns the untagged ones.
func ownedUpload(name, owner string) bool {
	if owner == "" {
		return !uploadTag.MatchString(name)
	}
	i := strings.IndexByte(name, '-')
	return i >= 0 && strings.HasPrefix(name[i+1:], owner)
}

// saveAttachedImage is saveAttachedImageFor the desk's own pictures.
func saveAttachedImage(paneID, name, mediaType, dataB64 string) (string, error) {
	return saveAttachedImageFor("", paneID, name, mediaType, dataB64)
}

// saveAttachedImageFor decodes, validates and writes one picture for owner (""
// for the desk, else uploadOwner of a device), and returns the path it was saved
// at. Only owner's own pictures in the pane's folder are counted or removed to
// make room.
func saveAttachedImageFor(owner, paneID, name, mediaType, dataB64 string) (string, error) {
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
		return "", fmt.Errorf("that file is not a picture Flockdeck can attach")
	}

	dir, err := store.Dir()
	if err != nil {
		return "", fmt.Errorf("find where to save it: %w", err)
	}
	paneDir := filepath.Join(dir, "uploads", paneID)
	if err := os.MkdirAll(paneDir, 0o700); err != nil {
		return "", fmt.Errorf("create a place to save it: %w", err)
	}
	trimPaneUploads(paneDir, owner, attachPaneFiles-1, attachPaneBytes-int64(len(data)))
	// The tag takes part of the 60 characters a name may have (uploadFileName).
	clean := sanitizeAttachName(name)
	if r := []rune(clean); len(r) > 60-len(owner) {
		clean = string(r[:60-len(owner)])
	}
	filename := fmt.Sprintf("%d-%s%s.%s", time.Now().UnixNano(), owner, clean, ext)
	path := filepath.Join(paneDir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("save it: %w", err)
	}
	return path, nil
}

// trimPaneUploads removes the oldest of owner's pictures in a pane's folder until
// owner holds at most maxFiles files and maxBytes bytes there, to make room for
// one more. Pictures that are not owner's are neither counted nor touched: a
// device's uploads must not push out the desk's own, or another device's, which
// a queued message may still point at.
func trimPaneUploads(paneDir, owner string, maxFiles int, maxBytes int64) {
	entries, err := os.ReadDir(paneDir)
	if err != nil {
		return
	}
	type pic struct {
		path string
		at   time.Time
		size int64
	}
	var pics []pic
	var total int64
	for _, e := range entries {
		if e.IsDir() || !ownedUpload(e.Name(), owner) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		pics = append(pics, pic{filepath.Join(paneDir, e.Name()), info.ModTime(), info.Size()})
		total += info.Size()
	}
	sort.Slice(pics, func(i, j int) bool { return pics[i].at.Before(pics[j].at) })
	for len(pics) > 0 && (len(pics) > maxFiles || total > maxBytes) {
		_ = os.Remove(pics[0].path)
		total -= pics[0].size
		pics = pics[1:]
	}
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
