package tool

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

// BenchmarkReadUTF16 decodes eight megabytes of a UTF-16 log -- what Windows
// PowerShell's > writes -- which read_file and grep read as text.
func BenchmarkReadUTF16(b *testing.B) {
	line := "2026-09-13 10:00:00 INFO the build step finished in 1.2s — ok\r\n"
	units := utf16.Encode([]rune(strings.Repeat(line, (4<<20)/len(line))))
	data := make([]byte, 2, 2+2*len(units))
	data[0], data[1] = 0xff, 0xfe
	for _, u := range units {
		data = append(data, byte(u), byte(u>>8))
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		r, ok := utf16Text(bufio.NewReaderSize(bytes.NewReader(data), 64<<10))
		if !ok {
			b.Fatal("not read as UTF-16")
		}
		if _, err := io.Copy(io.Discard, r); err != nil {
			b.Fatal(err)
		}
	}
}
