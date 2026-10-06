package baton

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Constants in a column are not a key wrapped over lines: no mark takes a line end,
// and what a second pass looks at does not make more of them.
func TestLinesOfConstantsAreNotJoinedIntoAKey(t *testing.T) {
	in := "cipher_X uint16 = 0x0002\ncipher_Y uint16 = 0x0003\ncipher_Z uint16 = 0x0004\ncipher_W uint16 = 0x0005\n"
	got, _ := NewScrubber().Scrub(in)
	if got != in {
		t.Errorf("Scrub(%q) = %q", in, got)
	}
	long := strings.Repeat("\tcipher_TLS_RSA_WITH_RC4_128_SHA              uint16 = 0x0005\n", 40)
	got, _ = NewScrubber().Scrub(long)
	if CountMarks(got) != 0 || strings.Count(got, "\n") != 40 {
		t.Errorf("a column of constants: %d marks, %d lines", CountMarks(got), strings.Count(got, "\n"))
	}
}

// A key wrapped over lines is marked a line at a time, so the lines stay lines.
func TestAWrappedKeyIsMarkedAtEachLineAndKeepsItsLineEnds(t *testing.T) {
	in := "the secret wJalr" + "XUtnFEMI/K7MDENG\n    /bPxRfiCYEXAMPLEKEY ends here\n"
	got, _ := NewScrubber().Scrub(in)
	if strings.Count(got, "\n") != strings.Count(in, "\n") || strings.Contains(got, "wJalr") || strings.Contains(got, "EXAMPLEKEY") {
		t.Errorf("Scrub(%q) = %q", in, got)
	}
}

// Openers with nothing to close them are made harmless at the same length, not
// into a longer mark: a text of them does not grow.
func TestUnclosedOpenersDoNotGrowAText(t *testing.T) {
	in := strings.Repeat("[REDACTED: ", 5000)
	got, reds := NewScrubber().Scrub(in)
	if len(got) > len(in) {
		t.Errorf("%d bytes became %d", len(in), len(got))
	}
	if len(reds) != 0 {
		t.Errorf("an opener was counted as a secret: %v", reds)
	}
	again, _ := NewScrubber().Scrub(got)
	if again != got {
		t.Error("scrubbing it again changed it")
	}
	if strings.Contains(got, "[REDACTED: ") {
		t.Error("an opener was left as it was")
	}
}

// The four inputs of a megabyte whose time doubled when marks were looked at again:
// each is within a few times what one pass takes.
func TestMarkHeavyInputsStayNearOnePass(t *testing.T) {
	if slowRun() {
		t.Skip("wall-clock limits do not hold under the race detector")
	}
	rep := func(s string, n int) string { return strings.Repeat(s, n) }
	for _, c := range []struct {
		name  string
		in    string
		limit time.Duration
	}{
		{"unterminated marks", rep("[REDACTED: ", 1048576/11), 4 * time.Second},
		{"a mark and a letter", rep("[REDACTED: secret-value]x", 1048576/25), 8 * time.Second},
		{"zero width password lines", rep("pass\u200bword=hunter2hunter2\n", 1048576/30), 4 * time.Second},
		{"url credentials", rep("https:"+"//user:pw12345@host.example/x ", 1048576/36), 4 * time.Second},
	} {
		start := time.Now()
		NewScrubber().Scrub(c.in)
		d := time.Since(start)
		t.Logf("%s: %d bytes in %s", c.name, len(c.in), d)
		if d > c.limit {
			t.Errorf("%s: took %s, over %s", c.name, d, c.limit)
		}
	}
}

// Two Go files whose lines look like a wrapped key to a pattern that is not careful
// are marked no more than before.
func TestGoFilesThatLookLikeWrappedKeysAreMarkedNoMoreThanBefore(t *testing.T) {
	root := runtime.GOROOT()
	for file, most := range map[string]int{
		filepath.Join("src", "crypto", "tls", "common_string.go"):              6,
		filepath.Join("src", "net", "http", "internal", "http2", "ciphers.go"): 136,
	} {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Skipf("no %s: %v", file, err)
		}
		got, _ := NewScrubber().Scrub(string(data))
		if n := CountMarks(got); n > most {
			t.Errorf("%s: %d marks, was at most %d", file, n, most)
		}
	}
}
