package gitx

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Tip is the commit a local branch is at, or an error when there is no such branch.
func Tip(repoDir, branch string) (string, error) {
	if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") {
		return "", &gitError{fmt.Sprintf("%q is not a branch", branch)}
	}
	out, err := run(repoDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Head is the commit a checkout is at.
func Head(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DeleteBranchAt deletes a local branch only if it is still at the commit given: git
// is asked to delete the ref "if it is still this", so a branch that has moved since
// is left alone, as are its commits. Unlike DeleteBranch it never discards work that
// was not there when the caller looked.
func DeleteBranchAt(repoDir, branch, commit string) error {
	if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") || strings.TrimSpace(commit) == "" {
		return &gitError{fmt.Sprintf("%q is not a branch that can be deleted at %q", branch, commit)}
	}
	_, err := run(repoDir, "update-ref", "-d", "refs/heads/"+branch, commit)
	return err
}

// IsClean reports whether a checkout has no changes at all: nothing modified, nothing
// staged, and no file git does not know of, ignored files included (git status leaves
// them out unless it is asked).
func IsClean(dir string) (bool, error) {
	out, err := run(dir, "status", "--porcelain", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// Show is the content of a file at a revision (rev:path), or an error when it is not
// there. It reads the object, not the working tree.
func Show(repoDir, rev, path string) (string, error) {
	if strings.HasPrefix(rev, "-") || strings.TrimSpace(rev) == "" || strings.TrimSpace(path) == "" {
		return "", &gitError{"nothing to show"}
	}
	return run(repoDir, "show", rev+":"+path)
}

// AssumeUnchanged lists up to limit tracked files of a checkout that git is told not to look
// at: any whose tag in ls-files -v is a lower case letter (assume-unchanged, alone or with
// skip-worktree), and those tagged S (skip-worktree) unless the repository uses a sparse
// checkout, where that is how files that are left out are marked. git status does not report
// changes to any of them.
func AssumeUnchanged(dir string, limit int) ([]string, error) {
	out, err := run(dir, "ls-files", "-v", "-z")
	if err != nil {
		return nil, err
	}
	sparse := false
	if v, err := run(dir, "config", "--get", "core.sparseCheckout"); err == nil && strings.TrimSpace(v) == "true" {
		sparse = true
	}
	var names []string
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) < 3 || rec[1] != ' ' || len(names) >= limit {
			continue
		}
		if tag := rec[0]; tag >= 'a' && tag <= 'z' || tag == 'S' && !sparse {
			names = append(names, rec[2:])
		}
	}
	return names, nil
}

// Extras lists what a checkout holds beyond the files git has checked out for it: every
// path on disk (except the .git entry at the top) that is not a tracked file, a folder
// that holds one, or the empty folder of a submodule. It finds what git status leaves
// out, ignored files and folders, empty folders, and what a hook wrote. It returns up to
// limit names and how many there were in all. A checkout that cannot be read is an
// error, and the caller takes that to mean it is not known to be empty.
func Extras(dir string, limit int) (names []string, total int, err error) {
	out, err := run(dir, "ls-files", "-z", "--stage")
	if err != nil {
		return nil, 0, err
	}
	tracked := map[string]bool{}
	folders := map[string]bool{}
	links := map[string]bool{}
	for _, rec := range strings.Split(out, "\x00") {
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		mode, path := rec[:strings.IndexByte(rec, ' ')], rec[tab+1:]
		if mode == "160000" {
			links[path] = true
		}
		tracked[path] = true
		for p := path; ; {
			i := strings.LastIndexByte(p, '/')
			if i < 0 {
				break
			}
			p = p[:i]
			folders[p] = true
		}
	}
	note := func(name string) {
		total++
		if len(names) < limit {
			names = append(names, name)
		}
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if links[rel] {
				// A submodule's folder is empty until somebody fills it.
				entries, derr := os.ReadDir(path)
				if derr != nil {
					return derr
				}
				if len(entries) > 0 {
					note(rel + "/")
				}
				return filepath.SkipDir
			}
			if folders[rel] {
				return nil
			}
			note(rel + "/")
			return filepath.SkipDir
		}
		if !tracked[rel] {
			note(rel)
		}
		return nil
	})
	return names, total, err
}

// TreeEntry is one entry of a tree in the object store.
type TreeEntry struct {
	Mode, Type, Sha, Name string
}

// TreeEntries lists the entries of a tree (a revision or rev:folder), one level, without
// reading any blob. An error says that git could not list it, which is not the same as there
// being no entries.
func TreeEntries(repoDir, treeish string) ([]TreeEntry, error) {
	if strings.HasPrefix(treeish, "-") || strings.TrimSpace(treeish) == "" {
		return nil, &gitError{"nothing to list"}
	}
	out, err := run(repoDir, "ls-tree", "-z", treeish)
	if err != nil {
		return nil, err
	}
	var entries []TreeEntry
	for _, rec := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		entries = append(entries, TreeEntry{Mode: f[0], Type: f[1], Sha: f[2], Name: name})
	}
	return entries, nil
}

// BlobLimited is the content of a blob that somebody else committed and that is read to decide
// something: an error says that it is over max bytes (and it is not read), or that git could
// not give it.
func BlobLimited(repoDir, sha string, max int) ([]byte, error) {
	if len(sha) < 7 || strings.ContainsAny(sha, " \t\n-") {
		return nil, &gitError{"not an object name"}
	}
	sizeText, err := run(repoDir, "cat-file", "-s", sha)
	if err != nil {
		return nil, err
	}
	var size int
	if _, err := fmt.Sscanf(strings.TrimSpace(sizeText), "%d", &size); err != nil {
		return nil, &gitError{"git did not say how large an object is"}
	}
	if size > max {
		return nil, &gitError{"it is too large"}
	}
	text, err := run(repoDir, "cat-file", "blob", sha)
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
}

// ObjectType is the kind of object a name refers to (blob, tree, commit).
func ObjectType(repoDir, name string) (string, error) {
	if strings.HasPrefix(name, "-") || strings.TrimSpace(name) == "" {
		return "", &gitError{"not an object name"}
	}
	out, err := run(repoDir, "cat-file", "-t", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
