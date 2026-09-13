package gitx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRefreshIndexRecordsWhatStatusLeaves covers a checkout whose files were
// touched without changing -- a build, a checkout by another tool. The pane
// headers' status takes no lock, so it never writes down what it found, and
// every status after it looks at those files again. RefreshIndex writes it
// down.
func TestRefreshIndexRecordsWhatStatusLeaves(t *testing.T) {
	repo := newRepo(t)
	index := filepath.Join(repo, ".git", "index")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(repo, "README.md"), later, later); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StatusWithin(repo, commandTimeout); err != nil {
		t.Fatalf("status: %v", err)
	}
	if after, _ := os.Stat(index); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the header's status wrote the index, which it must leave to the agents")
	}
	if err := RefreshIndex(repo); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if after, _ := os.Stat(index); !after.ModTime().After(before.ModTime()) {
		t.Error("the index was not written after its files were looked at again")
	}
}
