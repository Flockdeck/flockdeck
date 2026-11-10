package bridge

import (
	"bytes"
	"encoding/base64"
	"hash/maphash"
	"image"
	_ "image/gif"  // register the decoders DecodeConfig reads sizes with
	_ "image/jpeg" //
	_ "image/png"  //

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// What an agent may return as a picture. Everything in it is hostile, so each
// of these is a bound, and a refused picture is dropped with no entry and no
// error.
const (
	// maxImageBytes is the most a picture is, decoded. Its base64 then fits in
	// one message of the control socket (1 MiB) with room for the envelope.
	maxImageBytes = 700 << 10
	// maxImagesPerTool is how many pictures one tool entry makes entries for.
	maxImagesPerTool = 4
	// maxImages is how many image entries a session holds.
	maxImages = 100
)

// imageTypes are the media types drawn: those a browser draws without running
// anything. It matches IMAGE_TYPES in the page's conv/Image.js.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// base64Clean says s is only base64 characters, with up to two = at the end and
// no line breaks or spaces. The page checks the same before it draws.
func base64Clean(s string) bool {
	n := len(s)
	for k := 0; k < 2 && n > 0 && s[n-1] == '='; k++ {
		n--
	}
	for i := 0; i < n; i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/') {
			return false
		}
	}
	return true
}

// addImage makes an image entry from an agent's image block, or does nothing
// when the block is not one to keep. tool is the tool entry the block came in,
// or nil for a reply. Call with b.mu held.
func (b *Bridge) addImage(c *acp.ContentBlock, tool *item) {
	if b.images >= maxImages || c == nil {
		return
	}
	// A longer string than the cap's base64 is refused before it is decoded.
	if !imageTypes[c.MimeType] || c.Data == "" || len(c.Data) > base64.StdEncoding.EncodedLen(maxImageBytes) || !base64Clean(c.Data) {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(c.Data)
	if err != nil || len(raw) == 0 || len(raw) > maxImageBytes {
		return
	}
	if tool != nil {
		h := maphash.String(b.seed, c.Data)
		if _, dup := tool.imgs[h]; dup || len(tool.imgs) >= maxImagesPerTool {
			return
		}
		if tool.imgs == nil {
			tool.imgs = map[uint64]struct{}{}
		}
		tool.imgs[h] = struct{}{}
	}
	e := transcript.Entry{Kind: transcript.KindImage, MediaType: c.MimeType, Bytes: len(raw), HasDetail: true}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil && cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= 1<<16 && cfg.Height <= 1<<16 {
		e.Width, e.Height = cfg.Width, cfg.Height
	}
	it := b.add(e)
	it.data = c.Data
	it.detail = len(c.Data)
	b.detail += it.detail
	b.images++
	b.gainedDetail(it)
	b.trim()
}
