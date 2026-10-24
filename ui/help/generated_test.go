package help

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The interface reads its help and key table from files; they are what the
// pages and the table in this package make now, or the test fails and says how
// to write them again.
func TestTheGeneratedFilesAreCurrent(t *testing.T) {
	for name, make := range map[string]func() ([]byte, error){
		HelpFile: func() ([]byte, error) { return JSON(true) },
		KeysFile: KeysJSON,
	} {
		want, err := make()
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join("..", "src", name))
		if err != nil {
			t.Errorf("%s: %v; run `go generate ./help` in ui/", name, err)
			continue
		}
		if !bytes.Equal(bytes.TrimRight(got, "\n"), want) {
			t.Errorf("ui/src/%s is out of date: run `go generate ./help` in ui/", name)
		}
	}
}
