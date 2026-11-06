package record

import (
	"strings"
	"testing"
)

// A quoted value that is never closed on its line, as when the hook clipped the
// text at its limit, is still redacted.
func TestRedactsAQuotedValueWithNoClosingQuote(t *testing.T) {
	for _, in := range []string{
		"out\nDB_PASSWORD=\"hunter2-real-pass",
		"password: \"abc\nnext line",
		"DB_PASSWORD='hunter2-real-pass…[clipped 90 bytes]",
	} {
		if got := Redact(in); strings.Contains(got, "hunter2") || strings.Contains(got, "abc\n") || strings.Contains(got, "\"abc") {
			t.Errorf("Redact(%q) = %q", in, got)
		}
	}
}
