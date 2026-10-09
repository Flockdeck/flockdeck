package secretname

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// The readable lists the hash tables in hashes_gen.go are made from are in
// testdata/names.txt, not in Go source: a program that holds a list of
// credential and browser-store file names as text is what Windows Defender
// guessed was a stealer, and that goes for a test binary as much as the
// shipped one. Change the file, then run go generate in this directory.

type nameLists struct {
	files, folders, pairs, suffixes, prefixAny, prefixPrivate []string
}

var (
	listsOnce sync.Once
	listsVal  nameLists
	listsErr  error
)

func lists(t testing.TB) nameLists {
	t.Helper()
	listsOnce.Do(func() {
		b, err := os.ReadFile("testdata/names.txt")
		if err != nil {
			listsErr = err
			return
		}
		var cur *[]string
		for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "" || strings.HasPrefix(line, "#"):
			case line == "[files]":
				cur = &listsVal.files
			case line == "[folders]":
				cur = &listsVal.folders
			case line == "[pairs]":
				cur = &listsVal.pairs
			case line == "[suffixes]":
				cur = &listsVal.suffixes
			case line == "[prefix-any]":
				cur = &listsVal.prefixAny
			case line == "[prefix-private]":
				cur = &listsVal.prefixPrivate
			case cur == nil:
				listsErr = os.ErrInvalid
			default:
				*cur = append(*cur, line)
			}
		}
	})
	if listsErr != nil {
		t.Fatalf("testdata/names.txt: %v", listsErr)
	}
	return listsVal
}
