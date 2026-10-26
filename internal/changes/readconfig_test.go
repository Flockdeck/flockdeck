package changes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A repository's configuration is written by whoever works in the checkout, so a
// clean filter it names must not run when the checkout is read, by Read or by the
// diff of a file. The branch is unreadable, with one fixed reason carrying the key.
func TestACheckoutWhoseConfigNamesAFilterIsNotRead(t *testing.T) {
	dir := repo(t)
	write(t, dir, ".gitattributes", "* filter=evil diff=evil\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "attributes")
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "one\ntwo changed\nthree\nfour\n")

	m := filepath.ToSlash(filepath.Join(t.TempDir(), "ran"))
	runGit(t, dir, "config", "filter.evil.clean", "touch "+m+" && cat")

	ctx := context.Background()
	b := Read(ctx, "p1", dir, MaxFiles)
	if !ConfigRefusal(b.Error) || !strings.Contains(b.Error, "filter.evil.clean") {
		t.Errorf("error = %q, want the config refusal naming filter.evil.clean", b.Error)
	}
	if _, err := git(ctx, dir, "status", "--porcelain"); err == nil {
		t.Error("a status of the working tree was run")
	}
	if _, err := git(ctx, dir, "diff", "--end-of-options", "HEAD", "--"); err == nil {
		t.Error("a diff against the working tree was run")
	}
	if _, err := os.Stat(m); err == nil {
		t.Error("the program ran")
	}

	// The check is made again on each read, so taking the key away reads the checkout.
	runGit(t, dir, "config", "--unset", "filter.evil.clean")
	if b := Read(ctx, "p1", dir, MaxFiles); b.Error != "" {
		t.Errorf("after removing the key: %q", b.Error)
	}
}

func TestACheckoutWithoutAProgramToRunOnAReadIsReadAsBefore(t *testing.T) {
	for name, set := range map[string]func(dir, m string){
		"nothing": func(dir, m string) {},
		"a text conversion": func(dir, m string) {
			runGit(t, dir, "config", "diff.evil.textconv", "touch "+m+" && cat")
		},
		"the standard filters of Git LFS": func(dir, m string) {
			runGit(t, dir, "config", "filter.lfs.clean", "git-lfs clean -- %f")
			runGit(t, dir, "config", "filter.lfs.smudge", "git-lfs smudge -- %f")
			runGit(t, dir, "config", "filter.lfs.process", "git-lfs filter-process")
		},
	} {
		dir := repo(t)
		write(t, dir, ".gitattributes", "*.txt diff=evil\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-q", "-m", "attributes")
		runGit(t, dir, "checkout", "-q", "-b", "feat")
		write(t, dir, "a.txt", "one\ntwo changed\nthree\nfour\n")
		m := filepath.ToSlash(filepath.Join(t.TempDir(), "ran"))
		set(dir, m)

		b := Read(context.Background(), "p1", dir, MaxFiles)
		if b.Error != "" {
			t.Errorf("%s: error %q", name, b.Error)
			continue
		}
		if a := byPath(b)["a.txt"]; a.Added != 2 || a.Removed != 1 {
			t.Errorf("%s: a.txt = %+v, want +2 -1", name, a)
		}
		if _, err := os.Stat(m); err == nil {
			t.Errorf("%s: the program ran", name)
		}
	}
}

func TestComparesWorkingTree(t *testing.T) {
	for args, want := range map[string]bool{
		"status --porcelain":                              true,
		"diff --raw -z --end-of-options abc":              true,
		"diff --raw -z --end-of-options abc --":           true,
		"diff --cached --name-only":                       false,
		"diff --end-of-options abc def --":                false,
		"diff --name-only --end-of-options abc -- x":      true,
		"ls-files --others --exclude-standard -z":         false,
		"ls-files -m":                                     true,
		"ls-files -mo --exclude-standard":                 true,
		"ls-files -om":                                    true,
		"ls-files --others --modified -z":                 true,
		"ls-files -z -- -m":                               false,
		"ls-files -s -z -- p":                             false,
		"log -1":                                          false,
		"show --no-textconv --end-of-options abc:x":       false,
		"update-index --refresh":                          true,
		"diff --no-color --end-of-options base head -- p": false,
	} {
		if got := comparesWorkingTree(strings.Fields(args)); got != want {
			t.Errorf("%q: %v, want %v", args, got, want)
		}
	}
}
