package server

import (
	"strings"
	"testing"
)

// bs is one backslash. The prefixes below are built from it so none is written
// as a doubled backslash that an editor or a tool might fold into one.
const bs = "\x5c"

// GetFinalPathNameByHandle answers with the verbatim form, \?\C:\...
// realPath hands back an ordinary path.
func TestRealPathHasNoVerbatimPrefix(t *testing.T) {
	got := realPath(t.TempDir())
	if strings.HasPrefix(got, bs+bs) {
		t.Errorf("realPath kept a verbatim or UNC prefix: %q", got)
	}
}

func TestTrimVerbatimPrefix(t *testing.T) {
	verbatim := bs + bs + "?" + bs
	for in, want := range map[string]string{
		verbatim + `C:` + bs + `Users` + bs + `x`:               `C:` + bs + `Users` + bs + `x`,
		verbatim + `UNC` + bs + `srv` + bs + `share` + bs + `d`: bs + bs + `srv` + bs + `share` + bs + `d`,
		`C:` + bs + `Users` + bs + `x`:                          `C:` + bs + `Users` + bs + `x`,
		bs + bs + `srv` + bs + `share` + bs + `d`:               bs + bs + `srv` + bs + `share` + bs + `d`,
	} {
		if got := trimVerbatimPrefix(in); got != want {
			t.Errorf("trimVerbatimPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
