package transcript

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"runtime"
	"testing"
)

// hugeBlankPNG writes a png w by h, one bit a pixel and every pixel black,
// straight to bytes rather than through image/png, which would first have to
// hold the whole image: the point is a file a few hundred kilobytes long that
// decodes to hundreds of megabytes.
func hugeBlankPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, data []byte) {
		_ = binary.Write(&out, binary.BigEndian, uint32(len(data)))
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(data)
		out.WriteString(kind)
		out.Write(data)
		_ = binary.Write(&out, binary.BigEndian, crc.Sum32())
	}
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = 1, 0 // one bit a pixel, greyscale
	chunk("IHDR", ihdr[:])

	var idat bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&idat, zlib.BestCompression)
	row := make([]byte, 1+(w+7)/8) // a filter byte of none, then the pixels
	for y := 0; y < h; y++ {
		if _, err := zw.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	chunk("IDAT", idat.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

// TestPrepareImageRefusesADecompressionBomb covers an image block whose file
// is small but whose picture is enormous. Every transcript is read this way,
// watched or not, so decoding it whole to downscale it put hundreds of
// megabytes on the heap -- gigabytes, for a slightly larger one -- for an
// image no model was ever shown at that size.
func TestPrepareImageRefusesADecompressionBomb(t *testing.T) {
	bomb := hugeBlankPNG(t, 20000, 20000)
	if len(bomb) > maxImageSendBytes {
		t.Fatalf("the test image is %d bytes; it should be small enough to pass the size check", len(bomb))
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, _, _, ok := prepareImage("image/png", bomb)
	runtime.ReadMemStats(&after)

	if ok {
		t.Error("a 20000x20000 png was accepted; it should be refused before it is decoded")
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Errorf("looking at a %d-byte png allocated %d MB", len(bomb), grew>>20)
	}
}
