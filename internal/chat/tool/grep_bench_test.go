package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkGrepATree searches a hundred files of five thousand lines each for
// a word one line of each holds, which is the shape of most searches a model
// makes: a large tree, and very little in it that matches.
func BenchmarkGrepATree(b *testing.B) {
	dir := b.TempDir()
	body := strings.Repeat("the quick brown fox jumps over the lazy dog, twice over again\n", 5000)
	for i := range 100 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte(body+"a needle here\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	set, err := New(dir)
	if err != nil {
		b.Fatal(err)
	}
	grep, _ := set.Lookup("grep")
	args := json.RawMessage(`{"pattern": "needle"}`)
	b.ReportAllocs()
	for b.Loop() {
		out, err := grep.Run(context.Background(), args)
		if err != nil || !strings.Contains(out, "100 matches") {
			b.Fatalf("grep = %q, %v", out, err)
		}
	}
}
