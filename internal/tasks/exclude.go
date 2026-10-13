package tasks

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// ExcludeLine is the line added to a repository's own exclude file, so the board's
// directory is never committed by accident.
const ExcludeLine = ".flockdeck/"

// gitDirOf finds the git directory that holds the exclude file for a checkout:
// the .git directory of a main checkout, or the shared one a linked worktree
// points at. It reports false for a directory that is not a git checkout. It
// reads files and does not run git, so it works where git is not installed.
func gitDirOf(root string) (gitDir string, ok bool) {
	dot := filepath.Join(root, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dot, true
	}
	// A linked worktree or a submodule has a file: "gitdir: <path>".
	data, err := os.ReadFile(dot)
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	rest, found := strings.CutPrefix(line, "gitdir:")
	if !found {
		return "", false
	}
	dir := strings.TrimSpace(rest)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir = filepath.Clean(dir)
	if _, err := os.Stat(dir); err != nil {
		return "", false
	}
	return dir, true
}

// commonDirOf is the git directory shared by every worktree of a repository:
// the directory itself for a main checkout, and what its "commondir" file names
// for a linked worktree.
func commonDirOf(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	dir := strings.TrimSpace(string(data))
	if dir == "" {
		return gitDir
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(gitDir, dir)
	}
	return filepath.Clean(dir)
}

// MainCheckout is the main checkout of the repository a directory belongs to:
// the directory itself when it is one, or the one its linked worktree was cut
// from. For a directory that is not in a git repository, or a repository whose
// layout is not the usual one (a bare repository, a separate git directory), it
// is the directory itself.
func MainCheckout(root string) string {
	gitDir, ok := gitDirOf(root)
	if !ok {
		return root
	}
	common := commonDirOf(gitDir)
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return root
}

// EnsureExcluded adds ExcludeLine to the exclude file of the repository the
// checkout root belongs to, unless it is there already. It does not touch any
// .gitignore, which is tracked and shared. The first result says whether the
// file was changed; a directory that is not a git checkout is left alone and
// that is no error.
func EnsureExcluded(root string) (changed bool, err error) {
	gitDir, ok := gitDirOf(root)
	if !ok {
		return false, nil
	}
	path := filepath.Join(commonDirOf(gitDir), "info", "exclude")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		switch strings.TrimSpace(sc.Text()) {
		case ExcludeLine, "/" + ExcludeLine, ".flockdeck", "/.flockdeck":
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	var add []byte
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		add = append(add, '\n')
	}
	add = append(add, []byte("# Flockdeck task board\n"+ExcludeLine+"\n")...)
	// Appended, so nothing git or the user wrote there is rewritten.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(add); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}
