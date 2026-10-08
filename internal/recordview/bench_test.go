package recordview

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// benchFile builds a transcript from the header line and n lines made by mk.
func benchFile(n int, mk func(seq int) string) []byte {
	var b bytes.Buffer
	b.WriteString(started() + "\n")
	for i := 0; i < n; i++ {
		b.WriteString(mk(i+2) + "\n")
	}
	return b.Bytes()
}

// benchAll reads the whole file a page at a time, as a client would.
func benchAll(b *testing.B, data []byte) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cursor := 0
		for {
			pg, err := pageOf(context.Background(), bytes.NewReader(data), int64(len(data)), cursor, MaxPageEntries)
			if err != nil {
				b.Fatal(err)
			}
			if pg.Done {
				break
			}
			cursor = pg.Next
		}
	}
}

func prompt(text string) func(int) string {
	return func(i int) string { return line(i, "user_prompt", map[string]any{"text": text}) }
}

func toolInput(keys, valueBytes int) func(int) string {
	in := map[string]any{}
	for k := 0; k < keys; k++ {
		in[fmt.Sprintf("key%04d", k)] = strings.Repeat("v", valueBytes)
	}
	return func(i int) string { return line(i, "tool_call", map[string]any{"tool": "Bash", "input": in}) }
}

// The first four are the inputs a review found slow: lines over the cap are now
// skipped, so they cost the read only.
func BenchmarkReview130LinesOfSkDash(b *testing.B) {
	benchAll(b, benchFile(130, prompt(strings.Repeat("sk-", 10<<10))))
}

func BenchmarkReview130LinesOfPEMHeaders(b *testing.B) {
	benchAll(b, benchFile(130, prompt(strings.Repeat("-----BEGIN PRIVATE KEY-----", 1100))))
}

func BenchmarkReview8LinesOf960KBToolInput(b *testing.B) {
	benchAll(b, benchFile(8, toolInput(1900, 500)))
}

func BenchmarkReviewOneLineOf1MiB(b *testing.B) {
	benchAll(b, benchFile(1, prompt(strings.Repeat("sk-", 1<<20/3-300))))
}

// The same shapes at the size of the line cap, which are read and redacted.
func BenchmarkAtCap20LinesOfToolInput(b *testing.B) {
	benchAll(b, benchFile(20, toolInput(1900, 100)))
}

func BenchmarkAtCapOneLineOfSkDash(b *testing.B) {
	benchAll(b, benchFile(1, prompt(strings.Repeat("sk-", (MaxLineBytes-1000)/3))))
}

func BenchmarkAtCapOneLineOfEscapes(b *testing.B) {
	benchAll(b, benchFile(1, prompt(strings.Repeat("\x1b[", (MaxLineBytes-1000)/6))))
}

func BenchmarkAtCapOneLineWithTabs(b *testing.B) {
	benchAll(b, benchFile(1, prompt(strings.Repeat("ghp_\t", (MaxLineBytes-1000)/10))))
}

func BenchmarkOrdinaryTranscript(b *testing.B) {
	text := strings.Repeat("The build passed and the tests ran. ", 200)
	benchAll(b, benchFile(500, func(i int) string { return line(i, "assistant_message", map[string]any{"text": text}) }))
}
