package gitx

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileChange is one modified file in a working tree.
type FileChange struct {
	Path string
	// Status is the two-letter porcelain code, e.g. " M", "A ", "??".
	Status string
	// Label is that code rendered for people: "modified", "new", …
	Label     string
	Staged    bool
	Unstaged  bool
	Untracked bool
	Added     int
	Removed   int
}

// Changes lists the files that differ from HEAD, with line counts.
func Changes(dir string) ([]FileChange, error) {
	// -z is what makes the names trustworthy: without it git quotes anything
	// with a space, a quote or a non-ASCII character and escapes the bytes, so
	// "café.txt" arrives as "caf\303\251.txt" and no longer names a real file.
	// It also puts a rename's old name in its own record rather than writing
	// "old -> new", which a file genuinely called "a -> b" was mistaken for.
	out, err := run(dir, "status", "--porcelain", "--untracked-files=all", "-z")
	if err != nil {
		return nil, err
	}

	// Line counts come from numstat, which status does not provide.
	staged := numstat(dir, true)
	unstaged := numstat(dir, false)

	var files []FileChange
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		entry := records[i]
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		path := entry[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // the name it came from follows as its own record
		}

		fc := FileChange{
			Path:      path,
			Status:    code,
			Label:     statusLabel(code),
			Staged:    code[0] != ' ' && code[0] != '?',
			Unstaged:  code[1] != ' ' && code[1] != '?',
			Untracked: code == "??",
		}
		if n, ok := staged[path]; ok {
			fc.Added, fc.Removed = n.added, n.removed
		}
		if n, ok := unstaged[path]; ok {
			fc.Added += n.added
			fc.Removed += n.removed
		}
		if fc.Untracked {
			fc.Added = countLines(filepath.Join(dir, path))
		}
		files = append(files, fc)
	}
	return files, nil
}

type lineCount struct{ added, removed int }

// numstat reads per-file line counts for the staged or unstaged diff.
//
// The keys have to match the names Changes reports, so this reads -z as well:
// otherwise a quoted name never matches, and a rename is keyed under
// "old => new", which matches nothing at all and left it counted as 0/0.
func numstat(dir string, cached bool) map[string]lineCount {
	args := []string{"diff", "--numstat", "-z"}
	if cached {
		args = append(args, "--cached")
	}
	out, err := run(dir, args...)
	if err != nil {
		return nil
	}
	counts := map[string]lineCount{}
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		fields := strings.SplitN(records[i], "\t", 3)
		if len(fields) < 3 {
			continue
		}
		// Binary files report "-" instead of a count.
		a, _ := strconv.Atoi(fields[0])
		r, _ := strconv.Atoi(fields[1])
		path := fields[2]
		if path == "" {
			// A rename leaves the path field empty and sends the old and new
			// names as the next two records; the new one is what is shown.
			if i+2 >= len(records) {
				break
			}
			path = records[i+2]
			i += 2
		}
		counts[path] = lineCount{added: a, removed: r}
	}
	return counts
}

// unmerged holds the porcelain codes git uses for a conflicted file. Several
// of them contain no "U" at all -- "AA" is both sides adding, "DD" both
// deleting -- so they have to be recognised as a set before the letters are
// read individually, or a conflict is labelled "added" or "deleted".
var unmerged = map[string]bool{
	"DD": true, "AU": true, "UD": true, "UA": true,
	"DU": true, "AA": true, "UU": true,
}

// statusLabel turns a porcelain code into a word.
func statusLabel(code string) string {
	switch {
	case code == "??":
		return "new"
	case unmerged[code]:
		return "conflict"
	case strings.ContainsAny(code, "D"):
		return "deleted"
	case strings.ContainsAny(code, "R"):
		return "renamed"
	case strings.ContainsAny(code, "A"):
		return "added"
	case strings.ContainsAny(code, "U"):
		return "conflict"
	default:
		return "modified"
	}
}

// looksBinary applies git's own test: a NUL byte near the start of the file.
func looksBinary(data []byte) bool {
	const sniff = 8000
	if len(data) > sniff {
		data = data[:sniff]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// countLines counts the lines in a file, used to size an untracked addition.
func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if len(data) == 0 || looksBinary(data) {
		return 0
	}
	text := string(data)
	n := strings.Count(text, "\n")
	// A file that does not end in a newline still has a final line.
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}

// maxDiffBytes bounds what is sent to the interface. A generated file can be
// enormous and nobody reads a million lines in a side panel.
const maxDiffBytes = 400 << 10

// Diff returns a unified diff for one file, including untracked files, whose
// content is shown as an addition.
func Diff(dir, path string) (string, error) {
	// The path arrives from the interface, so it has to be checked before it
	// is joined onto the working tree: "../../.ssh/id_rsa" is not a file
	// anyone asked the review panel about.
	if !insideTree(path) {
		return "", &gitError{"not a path inside the working tree: " + path}
	}
	full := filepath.Join(dir, path)
	if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
		if untracked(dir, path) {
			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			if looksBinary(data) {
				// git says this rather than printing the bytes, and so should
				// a panel that has to render them as text.
				return fmt.Sprintf("--- /dev/null\n+++ b/%s\nBinary file (%d bytes)\n", path, len(data)), nil
			}
			return renderAsAddition(path, string(data)), nil
		}
	}

	// HEAD covers staged and unstaged changes together, which is what someone
	// reviewing "what changed" wants to see.
	out, err := run(dir, "diff", "HEAD", "--", path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		out, err = run(dir, "diff", "--", path)
		if err != nil {
			return "", err
		}
	}
	return truncateDiff(out), nil
}

// insideTree reports whether a repository-relative path stays within the tree.
func insideTree(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return false
	}
	// git speaks in forward slashes; both separators have to be considered on
	// Windows, where a leading one is still a rooted path.
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func untracked(dir, path string) bool {
	out, err := run(dir, "ls-files", "--error-unmatch", "--", path)
	return err != nil || strings.TrimSpace(out) == ""
}

func renderAsAddition(path, content string) string {
	// A file's trailing newline terminates its last line rather than starting
	// an empty one, so splitting it off would show a phantom "+" at the end.
	// git marks the other case explicitly, and so do we.
	endsWithNewline := strings.HasSuffix(content, "\n")
	var lines []string
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	}

	var b strings.Builder
	b.WriteString("--- /dev/null\n+++ b/" + path + "\n")
	b.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
	for i, line := range lines {
		b.WriteString("+" + line + "\n")
		if !endsWithNewline && i == len(lines)-1 {
			b.WriteString("\\ No newline at end of file\n")
		}
		if b.Len() > maxDiffBytes {
			b.WriteString(fmt.Sprintf("… truncated, %d more lines\n", len(lines)-i-1))
			break
		}
	}
	return b.String()
}

// truncateDiff caps a diff at maxDiffBytes, on a line boundary: cutting at an
// exact byte count leaves half a line, which the panel colours as though it
// were a real one, and can slice a UTF-8 character in two.
func truncateDiff(s string) string {
	if len(s) <= maxDiffBytes {
		return s
	}
	cut := s[:maxDiffBytes]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		cut = cut[:i+1]
	} else {
		cut += "\n"
	}
	return cut + fmt.Sprintf("… truncated, %d more bytes\n", len(s)-len(cut))
}

// CommitAll stages everything and commits it.
func CommitAll(dir, message string) error {
	if strings.TrimSpace(message) == "" {
		return errEmptyMessage
	}
	if _, err := run(dir, "add", "--all"); err != nil {
		return err
	}
	_, err := run(dir, "commit", "-m", message)
	return err
}

// errEmptyMessage is returned rather than letting git open an editor, which
// would hang with nowhere to type.
var errEmptyMessage = &gitError{"a commit message is required"}

type gitError struct{ msg string }

func (e *gitError) Error() string { return e.msg }

// Push sends the current branch to its remote, setting the upstream the first
// time so a new worktree's branch does not need a separate command.
func Push(dir string) (string, error) {
	branch := CurrentBranch(dir)
	if branch == "" {
		return "", &gitError{"cannot push a detached HEAD"}
	}
	if StatusOf(dir).Upstream == "" {
		remote, err := pushRemote(dir)
		if err != nil {
			return "", err
		}
		return runVerbose(dir, "push", "--set-upstream", remote, branch)
	}
	return runVerbose(dir, "push")
}

// pushRemote picks where a branch with no upstream should go: "origin" by
// convention, or the only remote when the repository names it something else.
// More than one and no origin is a choice the user has to make themselves.
func pushRemote(dir string) (string, error) {
	remotes := Remotes(dir)
	switch {
	case len(remotes) == 0:
		return "", &gitError{"this repository has no remote to push to"}
	case len(remotes) == 1:
		return remotes[0], nil
	}
	for _, r := range remotes {
		if r == "origin" {
			return r, nil
		}
	}
	return "", &gitError{"no \"origin\" remote; push manually to one of: " + strings.Join(remotes, ", ")}
}

// Pull fast-forwards from the upstream. A merge that cannot fast-forward is
// left for the user to resolve deliberately rather than started here.
func Pull(dir string) (string, error) {
	return runVerbose(dir, "pull", "--ff-only")
}

// Fetch updates the remote-tracking branches.
func Fetch(dir string) (string, error) {
	return runVerbose(dir, "fetch", "--prune")
}

// HasRemote reports whether the repository has anywhere to push to. A
// substring test for "origin" used to be enough here, but it also matched a
// remote merely named "my-origin" and missed a repository whose only remote is
// called something else entirely.
func HasRemote(dir string) bool { return len(Remotes(dir)) > 0 }

// Remotes lists the configured remote names.
func Remotes(dir string) []string {
	out, err := run(dir, "remote")
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}
