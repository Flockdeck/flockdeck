package depth

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A package whose tests run their own test binary again (os.Executable, or
// os.Args[0], handed to exec) must have a test binary that guards against
// running as its own descendant: one of the calls below in its tests, or in
// the code it shares. Otherwise a lost variable makes the child run the whole
// suite and start the next one, which is how a machine was once brought down.
var (
	// acptest.Command uses os.Executable in a file that is not a test.
	runsItself = regexp.MustCompile(`os\.Executable\(\)|os\.Args\[0\]|acptest\.Command\(`)
	guarded    = regexp.MustCompile(`depth\.Guard\(\)|iso\.Main\(|testiso\.Main\(|RunIfAgent\(\)|os\.Exit\(Main\(m\)\)`)
)

func TestEveryPackageThatRunsItsTestBinaryAgainGuardsAgainstRecursion(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	uses := map[string][]string{} // package directory -> files that run the binary again
	guard := map[string]bool{}    // package directory -> has a guard
	walked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		walked++
		dir := filepath.Dir(path)
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "//") {
				continue
			}
			if runsItself.MatchString(line) {
				uses[dir] = append(uses[dir], filepath.Base(path))
			}
			if guarded.MatchString(line) {
				guard[dir] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if walked == 0 || len(guard) == 0 {
		t.Fatalf("walked %d test files and found %d guarded packages, so the walk is looking in the wrong place", walked, len(guard))
	}
	for dir, files := range uses {
		if !guard[dir] {
			t.Errorf("%s runs its test binary again (%s) and no test file in it calls depth.Guard, iso.Main, testiso.Main or acptest.RunIfAgent; call depth.Guard() first in its TestMain", dir, strings.Join(files, ", "))
		}
	}
}

// Linux cuts a process name (comm) to 15 characters, and the depth guard counts
// ancestors by that name, so two test binaries sharing their first 15 characters
// would be counted as one.
func TestNoTwoTestBinariesShareTheirFirst15Characters(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{} // comm -> package directory
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		comm := filepath.Base(dir) + ".test"
		if len(comm) > 15 {
			comm = comm[:15]
		}
		if other, ok := seen[comm]; ok && other != dir {
			t.Errorf("test binaries of %s and %s both show as %q on Linux", other, dir, comm)
		}
		seen[comm] = dir
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("no test packages found")
	}
}
