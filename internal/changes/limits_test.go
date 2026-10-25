package changes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// Git output past the limit is not held: the command fails with ErrTooLarge.
func TestGitOutputPastTheLimitIsRefused(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 300; i++ {
		write(t, dir, filepath.Join("many", strings.Repeat("d", 40)+"_"+string(rune('a'+i%26))+strings.Repeat("x", i%7), "f"+strings.Repeat("y", 30)+".txt"), "x\n")
	}
	if _, err := gitx.ReadOnlyCapped(context.Background(), dir, 512, "ls-files", "--others", "--exclude-standard", "-z"); !errors.Is(err, gitx.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	out, err := gitx.ReadOnlyCapped(context.Background(), dir, 1<<20, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil || out == "" {
		t.Fatalf("under the limit: %v, %d bytes", err, len(out))
	}
}

// A line count is read once and then kept while the file stays as it was.
func TestUntrackedLineCountsAreKeptWhileTheFileIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "1\n2\n3\n")
	path := filepath.Join(dir, "a.txt")
	budget := int64(1 << 20)
	var f File
	countUntracked(path, &f, &budget)
	if f.Added != 3 || budget != 1<<20-6 {
		t.Fatalf("first read: %d lines, budget %d", f.Added, budget)
	}
	var g File
	countUntracked(path, &g, &budget)
	if g.Added != 3 || budget != 1<<20-6 {
		t.Errorf("second read: %d lines, budget %d, want the cache to answer for free", g.Added, budget)
	}
	write(t, dir, "a.txt", "1\n2\n3\n4\n5\n")
	var h File
	countUntracked(path, &h, &budget)
	if h.Added != 5 {
		t.Errorf("after a change: %d lines, want 5", h.Added)
	}
}

// Past the budget a file is listed with its size and no count.
func TestUntrackedFilesPastTheByteBudgetAreNotRead(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "b.txt", strings.Repeat("line\n", 100))
	budget := int64(10)
	var f File
	countUntracked(filepath.Join(dir, "b.txt"), &f, &budget)
	if f.Added != 0 || f.SizeAfter == nil || *f.SizeAfter != 500 {
		t.Errorf("file = %+v, want no count and a size of 500", f)
	}
}

// A link is never read, whatever it points at.
func TestAnUntrackedLinkIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "l.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	budget := int64(1 << 20)
	var f File
	countUntracked(link, &f, &budget)
	if f.Added != 0 || budget != 1<<20 {
		t.Errorf("link: %+v, budget %d, want it left alone", f, budget)
	}
}
