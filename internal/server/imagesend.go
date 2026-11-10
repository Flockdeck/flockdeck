package server

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "image/gif"  // registers gif with image.DecodeConfig, for dimensions only
	_ "image/jpeg" // registers jpeg
	_ "image/png"  // registers png

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/structured"
)

// This file is the desktop side of sending pictures with a prompt. A window
// attaches a picture (imageattach.go), is told the path it was saved at, and
// then names that path in paneSend's images. The path comes from the window and
// is not trusted: the file is read only if it is a regular file directly inside
// the uploads folder of the pane being sent to.

// imageRef is one picture a paneSend names.
type imageRef struct {
	Path string `json:"path"`
}

// Plain sentences for a window to show. They say nothing about where the file
// was or why a path was refused.
const (
	imageNotUploaded = "That picture was not attached to this pane, so it was not sent"
	imageGone        = "That picture is no longer available, so it was not sent. Attach it again"
	imageTooMany     = "A prompt takes at most 4 pictures"
	imageUnreadable  = "That picture could not be read, so it was not sent"
)

// uploadsRoot is the folder attached pictures are saved under, one folder per
// pane (see saveAttachedImage).
func uploadsRoot() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "uploads"), nil
}

// loadPaneImages reads the pictures refs name for pane paneID, under root, the
// uploads folder. Every path must name a regular file directly inside
// root/<paneID> once links are resolved, so a path that climbs out with "..",
// goes through a link to somewhere else, lies in another pane's folder, or is a
// drive-relative, rooted or UNC path on Windows, is refused. Each file is then
// checked by its own bytes: only png, jpeg, gif and webp pass. The same picture
// named twice is sent once.
func loadPaneImages(root, paneID string, refs []imageRef) ([]structured.Image, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > structured.MaxImages {
		return nil, errors.New(imageTooMany)
	}
	// The pane id is one path element: anything else would pick another folder.
	if paneID == "" || paneID == "." || paneID == ".." || strings.ContainsAny(paneID, `/\:`+"\x00") || filepath.Base(paneID) != paneID {
		return nil, errors.New(imageNotUploaded)
	}
	paneDir, err := filepath.EvalSymlinks(filepath.Join(root, paneID))
	if err != nil {
		if os.IsNotExist(err) {
			// The pane has no folder: nothing it attached is left (the sweep
			// removes an empty one). Only a path that would have been in it is
			// told so; any other is not this pane's.
			for _, ref := range refs {
				if ref.Path == "" || !sameUploadDir(filepath.Dir(ref.Path), filepath.Join(root, paneID)) {
					return nil, errors.New(imageNotUploaded)
				}
			}
			return nil, errors.New(imageGone)
		}
		return nil, errors.New(imageUnreadable)
	}
	var out []structured.Image
	seen := map[string]bool{}
	for _, ref := range refs {
		name, err := uploadName(paneDir, ref.Path)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		im, err := readUpload(paneDir, name)
		if err != nil {
			return nil, err
		}
		out = append(out, im)
	}
	return out, nil
}

// promptPictureKey splits the detail key of one picture of a prompt, "e12#0":
// the prompt entry's id and the picture's place in its images.
func promptPictureKey(key string) (entry string, n int, ok bool) {
	entry, num, found := strings.Cut(key, "#")
	if !found || entry == "" || num == "" || len(num) > 2 {
		return "", 0, false
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return "", 0, false
		}
	}
	n = 0
	for _, r := range num {
		n = n*10 + int(r-'0')
	}
	return entry, n, true
}

// maxPromptPictureDetail is the most a picture is, decoded, to be sent back in one
// conversationDetail (about 1.4 MB of base64). The window scales what it sends to
// 600 KB, so this does not bite for a picture it attached; a larger one that
// another client sent is answered as gone rather than as one big message.
const maxPromptPictureDetail = 1 << 20

// promptPictureDetail is the bytes of picture n of the prompt entry id, read
// from the pane's uploads folder. It reports false for an entry that is not a
// prompt, a place that holds no picture, and a file that is gone or not one
// readUpload accepts. The name comes from Flockdeck's own record of the prompt,
// and readUpload checks its shape again.
func promptPictureDetail(root, paneID string, entries []transcript.Entry, id string, n int) (transcript.Detail, bool) {
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		if e.Kind != transcript.KindPrompt || n >= len(e.Images) || e.Images[n].Upload == "" {
			return transcript.Detail{}, false
		}
		if paneID == "" || paneID == "." || paneID == ".." || strings.ContainsAny(paneID, `/\:`+"\x00") || filepath.Base(paneID) != paneID {
			return transcript.Detail{}, false
		}
		paneDir, err := filepath.EvalSymlinks(filepath.Join(root, paneID))
		if err != nil {
			return transcript.Detail{}, false
		}
		im, err := readUpload(paneDir, e.Images[n].Upload)
		if err != nil || len(im.Data) > maxPromptPictureDetail {
			return transcript.Detail{}, false
		}
		return transcript.Detail{Data: base64.StdEncoding.EncodeToString(im.Data)}, true
	}
	return transcript.Detail{}, false
}

// uploadName is the file name of path inside paneDir (already resolved), or an
// error when path is not a file directly in it.
func uploadName(paneDir, path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		// A drive-relative path ("C:x") and a rooted one with no drive ("\x") are
		// not absolute on Windows, and so land here.
		return "", errors.New(imageNotUploaded)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			// Where it would have been, so a path outside the folder is not told
			// apart from one inside that has been swept.
			if dir, derr := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(path))); derr == nil && sameUploadDir(dir, paneDir) {
				return "", errors.New(imageGone)
			}
			return "", errors.New(imageNotUploaded)
		}
		return "", errors.New(imageNotUploaded)
	}
	if !sameUploadDir(filepath.Dir(resolved), paneDir) {
		return "", errors.New(imageNotUploaded)
	}
	name := filepath.Base(resolved)
	if !uploadFileName.MatchString(name) {
		return "", errors.New(imageNotUploaded)
	}
	return name, nil
}

// uploadFileName is the shape of a name saveAttachedImage gives a file: a
// number, a dash, a name of letters, digits, dashes and underscores, and the
// extension of the sniffed type. Nothing else in the folder is read. This
// leaves out a colon (a Windows alternate data stream, "x.png:stream") and a
// reserved device name ("CON", "NUL", "COM1"), which cannot have this shape.
var uploadFileName = regexp.MustCompile(`^[0-9]+-[A-Za-z0-9_-]{1,60}\.(png|jpg|gif|webp)$`)

// sameUploadDir says a and b name the same folder. Both are cleaned; on Windows the
// comparison ignores case, as the file system does.
func sameUploadDir(a, b string) bool {
	rel, err := filepath.Rel(filepath.Clean(a), filepath.Clean(b))
	return err == nil && rel == "."
}

// readUpload reads one saved picture, name in paneDir, and checks it.
func readUpload(paneDir, name string) (structured.Image, error) {
	if !uploadFileName.MatchString(name) {
		return structured.Image{}, errors.New(imageNotUploaded)
	}
	full := filepath.Join(paneDir, name)
	// Lstat before open: only a regular file is opened, so a link, a device or a
	// folder is never opened to find out what it is.
	li, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return structured.Image{}, errors.New(imageGone)
		}
		return structured.Image{}, errors.New(imageUnreadable)
	}
	if !li.Mode().IsRegular() {
		return structured.Image{}, errors.New(imageNotUploaded)
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return structured.Image{}, errors.New(imageGone)
		}
		return structured.Image{}, errors.New(imageUnreadable)
	}
	defer f.Close()
	// The file opened is the one that was checked: a swap between the Lstat and
	// the open is refused.
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(li, st) {
		return structured.Image{}, errors.New(imageNotUploaded)
	}
	if st.Size() == 0 || st.Size() > structured.MaxImageBytes {
		return structured.Image{}, fmt.Errorf("That picture is %s, and a prompt takes pictures of up to 5 MB. Attach a smaller one", sizeWords(st.Size()))
	}
	data, err := io.ReadAll(io.LimitReader(f, structured.MaxImageBytes+1))
	if err != nil || len(data) > structured.MaxImageBytes {
		return structured.Image{}, errors.New(imageUnreadable)
	}
	ext, ok := sniffImageExt(data)
	if !ok {
		return structured.Image{}, errors.New("That file is not a picture Flockdeck can send")
	}
	im := structured.Image{Image: acp.Image{MediaType: imageMediaType(ext), Data: data}, Upload: name}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		im.Width, im.Height = cfg.Width, cfg.Height
	}
	return im, nil
}

func sizeWords(n int64) string {
	if n == 0 {
		return "empty"
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// imageMediaType is the media type of a sniffed extension from sniffImageExt.
func imageMediaType(ext string) string {
	switch ext {
	case "jpg":
		return "image/jpeg"
	default:
		return "image/" + ext
	}
}
