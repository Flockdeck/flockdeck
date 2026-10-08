package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGitSeenRoundTrip(t *testing.T) {
	isolateConfig(t)
	repo := filepath.Join(t.TempDir(), "r", ".git")

	if _, ok, err := LoadGitSeen(repo); ok || err != nil {
		t.Fatalf("nothing saved: ok=%v err=%v, want false and no error", ok, err)
	}
	if err := SaveGitSeen(repo, []string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadGitSeen(repo)
	if err != nil || !ok || !reflect.DeepEqual(got.IDs, []string{"b", "a"}) || got.Accepted.IsZero() {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	// Another repository is not affected, and saving an empty list is a record.
	other := filepath.Join(t.TempDir(), "o", ".git")
	if _, ok, _ := LoadGitSeen(other); ok {
		t.Error("a second repository read as accepted")
	}
	if err := SaveGitSeen(other, nil); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := LoadGitSeen(other); !ok || len(got.IDs) != 0 {
		t.Errorf("empty record read back as %+v ok=%v", got, ok)
	}
	if got, _, _ := LoadGitSeen(repo); len(got.IDs) != 2 {
		t.Errorf("first repository's record was disturbed: %+v", got)
	}
}

func TestGitSeenDamagedFileIsKept(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, gitSeenFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadGitSeen("/x/.git"); ok || err != nil {
		t.Fatalf("damaged file: ok=%v err=%v, want false and no error", ok, err)
	}
	if _, err := os.Stat(path + ".damaged"); err != nil {
		t.Errorf("damaged file was not kept aside: %v", err)
	}
}
