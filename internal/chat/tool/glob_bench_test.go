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

// BenchmarkGlobUnderOneDirectory looks for the Go files under src in a tree
// whose node_modules holds ten thousand files, which is the shape of the
// patterns a model writes -- a directory, then ** -- in the trees it works in.
func BenchmarkGlobUnderOneDirectory(b *testing.B) {
	dir := b.TempDir()
	mk := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	for i := range 50 {
		mk(fmt.Sprintf("src/pkg%02d/a.go", i))
	}
	for i := range 200 {
		for j := range 50 {
			mk(fmt.Sprintf("node_modules/m%03d/f%02d.js", i, j))
		}
	}
	set, err := New(dir)
	if err != nil {
		b.Fatal(err)
	}
	glob, _ := set.Lookup("glob")
	args := json.RawMessage(`{"pattern": "src/**/*.go"}`)
	b.ReportAllocs()
	for b.Loop() {
		out, err := glob.Run(context.Background(), args)
		if err != nil || strings.Count(out, "\n") != 50 || !strings.Contains(out, "src/pkg00/a.go") {
			b.Fatalf("glob = %q, %v", out, err)
		}
	}
}
