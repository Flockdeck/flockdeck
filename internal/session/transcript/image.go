package transcript

import (
	"bytes"
	"image"
	_ "image/gif" // registers gif with image.Decode/DecodeConfig, for dimensions only
	"image/jpeg"
	_ "image/png" // registers png with image.Decode/DecodeConfig
)

// allowedImageMediaTypes are the only media types the chat view ever sends a
// phone: it draws an image entry's detail straight into an <img
// src="data:...">, and anything else -- an svg above all, which is markup --
// would let a crafted transcript run script in the phone's page.
var allowedImageMediaTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

const (
	// maxImageInputBytes refuses an image far larger than any real screenshot
	// or file read should ever produce, before any decoding is attempted.
	maxImageInputBytes = 20 << 20
	// maxImageSendBytes is what is actually sent to the phone: comfortably
	// under the control socket's and the relay's 1 MiB message cap once
	// base64 (a third again as many bytes) and the surrounding JSON are
	// added.
	maxImageSendBytes = 700 << 10
	// maxImageDimension is the longest edge an oversized image is downscaled
	// to.
	maxImageDimension = 2000
)

// prepareImage validates an image block's bytes for sending to the phone,
// downscaling a png or jpeg that is too large or too big on a side -- the
// only two formats the standard library can both decode and re-encode
// without cgo. A gif or webp that is already small enough passes through
// unchanged; one that is not is refused rather than sent oversized, since
// there is no cheap way here to shrink it.
//
// ok is false for a media type the phone must never be asked to draw, for
// bytes too large to consider at all, or for an oversized gif or webp -- any
// of which means no image entry is sent for this block, the same silent drop
// as scaffolding the design says to leave out of the chat view.
func prepareImage(mediaType string, data []byte) (outType string, outData []byte, width, height int, ok bool) {
	if !allowedImageMediaTypes[mediaType] || len(data) == 0 || len(data) > maxImageInputBytes {
		return "", nil, 0, 0, false
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		width, height = cfg.Width, cfg.Height
	}
	fits := len(data) <= maxImageSendBytes && width <= maxImageDimension && height <= maxImageDimension
	canTranscode := mediaType == "image/png" || mediaType == "image/jpeg"
	if fits || !canTranscode {
		if len(data) > maxImageSendBytes {
			return "", nil, 0, 0, false
		}
		return mediaType, data, width, height, true
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", nil, 0, 0, false
	}
	out, w, h, ok2 := encodeJPEGUnderLimit(img, maxImageSendBytes)
	if !ok2 {
		return "", nil, 0, 0, false
	}
	return "image/jpeg", out, w, h, true
}

// encodeJPEGUnderLimit downscales and re-encodes img, trying successively
// lower quality and, failing that, smaller dimensions, until the result fits
// under limit or the budget below runs out.
func encodeJPEGUnderLimit(img image.Image, limit int) (data []byte, width, height int, ok bool) {
	dim := maxImageDimension
	for round := 0; round < 3; round++ {
		scaled := downscale(img, dim)
		b := scaled.Bounds()
		for _, q := range []int{85, 70, 55, 40, 25} {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: q}); err != nil {
				return nil, 0, 0, false
			}
			if buf.Len() <= limit {
				return buf.Bytes(), b.Dx(), b.Dy(), true
			}
		}
		dim /= 2
	}
	return nil, 0, 0, false
}

// downscale returns img unchanged if it already fits within maxDim on both
// sides, or a nearest-neighbour resize that does -- cheap, and good enough
// for a chat thumbnail and its full-screen view, without pulling in an image
// library beyond the standard one.
func downscale(img image.Image, maxDim int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim || w == 0 || h == 0 {
		return img
	}
	scale := float64(maxDim) / float64(w)
	if h > w {
		scale = float64(maxDim) / float64(h)
	}
	nw := max(1, int(float64(w)*scale))
	nh := max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	return dst
}
