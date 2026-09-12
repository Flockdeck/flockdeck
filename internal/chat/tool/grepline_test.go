package tool

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// One line as long as the file -- a minified bundle, a log written without
// newlines -- is searched without being held in memory whole.
func TestGrepDoesNotHoldAHugeLineInMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.min.js")
	line := "needle" + strings.Repeat("x", 32<<20)
	if err := os.WriteFile(path, []byte(line+"\nneedle again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	line = ""

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	hits, err := matchFile(path, regexp.MustCompile("needle"), false, 10)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || !strings.HasPrefix(hits[0], "1: needle") || hits[1] != "2: needle again" {
		t.Errorf("hits = %.80q", hits)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Errorf("searching allocated %d MB for a 32 MB line", grew>>20)
	}
}
