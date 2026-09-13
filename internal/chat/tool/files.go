package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// readDefaultLines is how much of a file one read returns when the model
	// does not say. It is generous enough that most source files arrive whole
	// and small enough that a log file does not fill the context window.
	readDefaultLines = 2000
	// readMaxBytes bounds one read's output whatever the line count says,
	// because a single minified line can be larger than everything else the
	// turn contains.
	readMaxBytes = 256 << 10
	// readMaxLine bounds one line of what a read returns, for the same reason
	// applied to the one line of a minified file that is larger than the rest
	// of the project.
	readMaxLine = 4 << 10
)

// readFile hands the model the contents of one file, with line numbers,
// because everything it does next -- an edit, a report to the user, a
// reference in its own prose -- is easier to state against a line number than
// against a quotation.
type readFile struct{ root *Root }

func (t *readFile) Name() string { return "read_file" }

func (t *readFile) Describe() Schema {
	return Schema{
		Name: t.Name(),
		Description: "Read a file from the working directory. Returns the file's lines, each " +
			"prefixed with its 1-based line number and a tab; the numbers are not part of the " +
			"file. Use offset and limit to page through a large file.",
		Params: object(map[string]Property{
			"path":   {Type: "string", Description: "Path to the file, relative to the working directory."},
			"offset": {Type: "integer", Description: "1-based line to start at. Defaults to the first line."},
			"limit":  {Type: "integer", Description: "How many lines to return. Defaults to 2000."},
		}, "path"),
	}
}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// Approval is empty: reading inside the pane's own directory is what the pane
// exists to do, and a path outside it is refused rather than asked about.
func (t *readFile) Approval(json.RawMessage) string { return "" }

func (t *readFile) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a readArgs
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := t.root.ResolveFile(a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", t.root.explain(err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use list_dir", t.root.Rel(abs))
	}
	if err := regularFile(t.root, abs, info); err != nil {
		return "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", t.root.explain(err)
	}
	defer f.Close()
	// The file is read a line at a time rather than whole, because the model
	// pages through a large log with offset and limit and a read of two
	// thousand lines should not cost the memory of the whole file.
	r, _ := utf16Text(bufio.NewReaderSize(f, 64<<10))
	if head, _ := r.Peek(8 << 10); looksBinary(head) {
		return "", fmt.Errorf("%s looks like a binary file (%s)", t.root.Rel(abs), humanBytes(info.Size()))
	}
	if info.Size() == 0 {
		return fmt.Sprintf("%s is empty.", t.root.Rel(abs)), nil
	}

	offset := a.Offset
	if offset < 1 {
		offset = 1
	}
	limit := a.Limit
	if limit <= 0 {
		limit = readDefaultLines
	}

	var b strings.Builder
	lines, shown, full := 0, offset-1, false
	for {
		line, dropped, err := nextLine(r, readMaxLine)
		if err != nil && err != io.EOF {
			return "", err
		}
		if err == io.EOF && line == "" && dropped == 0 {
			break
		}
		lines++
		if lines >= offset && lines < offset+limit && !full {
			if dropped > 0 {
				line += fmt.Sprintf(" [... %s more on this line]", humanBytes(int64(dropped)))
			}
			entry := fmt.Sprintf("%d\t%s\n", lines, line)
			if b.Len() > 0 && b.Len()+len(entry) > readMaxBytes {
				full = true
			} else {
				b.WriteString(entry)
				shown = lines
			}
		}
		if err == io.EOF {
			break
		}
	}
	if offset > lines {
		return "", fmt.Errorf("%s has %d lines; offset %d is past the end", t.root.Rel(abs), lines, offset)
	}
	if remaining := lines - shown; remaining > 0 {
		fmt.Fprintf(&b, "\n[%d more %s; read again with offset %d]\n", remaining, plural(remaining, "line", "lines"), shown+1)
	}
	return b.String(), nil
}

// editFamily is the standing permission write_file and edit_file offer: every
// change to a file in the pane's directory, for the rest of the session. It is
// three words so that it can never be a run_command prefix, which is two.
//
// It is offered because a turn that writes five files otherwise asks five
// questions, and somebody who has read the first two and decided has nothing
// to gain from being asked the rest.
const editFamily = "edits to files"

// writeFile creates or replaces a file whole. It is the tool for a new file
// and for a rewrite; changing part of an existing file is edit_file's job,
// which is worth saying in the description because a model handed only this
// one will happily rewrite a thousand-line file to change a constant.
type writeFile struct{ root *Root }

func (t *writeFile) Name() string { return "write_file" }

func (t *writeFile) Describe() Schema {
	return Schema{
		Name: t.Name(),
		Description: "Create a file, or replace one entirely, in the working directory. Missing " +
			"parent directories are created. To change part of an existing file use edit_file " +
			"instead. The user is asked before anything is written.",
		Params: object(map[string]Property{
			"path":    {Type: "string", Description: "Path to the file, relative to the working directory."},
			"content": {Type: "string", Description: "The complete new contents of the file."},
		}, "path", "content"),
	}
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *writeFile) Approval(args json.RawMessage) string {
	var a writeArgs
	if err := decode(args, &a); err != nil {
		return ""
	}
	abs, err := t.root.ResolveFile(a.Path)
	if err != nil || deviceName(t.root, abs) != nil {
		return ""
	}
	// The question shows what is being written, not only how much of it: a
	// size is not something anybody can say yes or no to.
	rel := t.root.Rel(abs)
	newSize := humanBytes(int64(len(a.Content)))
	info, err := os.Stat(abs)
	if err == nil && (info.IsDir() || regularFile(t.root, abs, info) != nil) {
		// Run refuses it, and a question the answer to which makes no
		// difference only teaches the user to stop reading them.
		return ""
	}
	if err == nil {
		old, _ := os.ReadFile(abs)
		q := fmt.Sprintf("Overwrite %s (%s, %s) with %s, %s?", rel,
			humanBytes(info.Size()), linesOf(string(old)), newSize, linesOf(a.Content))
		// Compared with what Run will write, not what was asked for. A file
		// with Windows endings anywhere in it is written back with them on
		// every line, so the same text read_file showed is no change; but a
		// file with none is written as given, and a question calling a
		// rewrite of every line ending "the same" would be agreed to unread.
		content := a.Content
		if strings.Contains(string(old), "\r\n") {
			content = withCRLF(content)
		}
		// A byte-order mark is kept by the write, so it is not a change.
		if at := firstChange(strings.TrimPrefix(string(old), utf8BOM), strings.TrimPrefix(content, utf8BOM)); at == 0 {
			q += "\n  (it is the same as what the file holds now)"
		} else {
			q += fmt.Sprintf("\n  the first change is at line %d:\n%s", at, excerpt(a.Content, at))
		}
		return q
	}
	// Missing directories are made along with the file, which is part of what
	// is being agreed to and so part of the question.
	also := ""
	if dir := firstMissingDir(filepath.Dir(abs)); dir != "" {
		also = fmt.Sprintf(", making the directory %s/", t.root.Rel(dir))
	}
	return fmt.Sprintf("Create %s (%s, %s)%s?\n%s", rel, newSize, linesOf(a.Content), also, excerpt(a.Content, 1))
}

// Prefix is the standing permission on offer for a write, which is every edit.
func (t *writeFile) Prefix(args json.RawMessage) string {
	var a writeArgs
	if err := decode(args, &a); err != nil {
		return ""
	}
	return editPrefix(t.root, a.Path)
}

// editPrefix is the standing permission on offer for changing path: every
// edit, except to a file inside a repository's own directory. A hook there
// runs at the next commit, and a config there names programs to run -- a
// pager, an editor, an ssh command -- so "always" for edits would be standing
// permission to run anything the next time anybody used git in the pane.
//
// The path is checked as it was written and as the file system resolves it,
// because a name is not the only way to reach a directory: a link can lead
// there, and on Windows .git. and ".git " (Windows drops the trailing dot or
// space), .git::$INDEX_ALLOCATION (the directory's own stream) and GIT~1 (its
// short name) are all .git.
func editPrefix(root *Root, path string) string {
	abs, err := root.ResolveFile(path)
	if err != nil {
		return ""
	}
	for _, p := range []string{abs, evalExisting(abs)} {
		for _, part := range strings.Split(root.Rel(p), "/") {
			if repoPart(part) {
				return ""
			}
		}
	}
	return editFamily
}

// repoPart reports whether one part of a path is, or on Windows might be, a
// repository's own directory. A name spelled one of Windows' other ways --
// ending in a dot or a space, naming a stream, or short like GIT~1 -- is
// counted as one even where no such directory exists yet, because the one
// made by writing through it would be .git itself.
func repoPart(part string) bool {
	if strings.EqualFold(part, ".git") || strings.EqualFold(part, ".hg") {
		return true
	}
	if runtime.GOOS != "windows" {
		return false
	}
	lower := strings.ToLower(part)
	return strings.TrimRight(part, ". ") != part || strings.Contains(part, ":") ||
		strings.HasPrefix(lower, "git~") || strings.HasPrefix(lower, "hg~")
}

func (t *writeFile) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a writeArgs
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := t.root.ResolveFile(a.Path)
	if err != nil {
		return "", err
	}
	if err := deviceName(t.root, abs); err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return "", fmt.Errorf("%s is a directory", t.root.Rel(abs))
	} else if err == nil {
		if err := regularFile(t.root, abs, info); err != nil {
			return "", err
		}
	}
	// read_file shows a file without its carriage returns, so a file with
	// Windows line endings is rewritten with bare newlines, and every one of
	// its lines would then show as changed. A rewrite keeps the file's own
	// endings, as an edit does.
	if old, err := os.ReadFile(abs); err == nil {
		if bytes.Contains(old, []byte("\r\n")) {
			a.Content = withCRLF(a.Content)
		}
		// So is a byte-order mark at its start, which a model writing the file
		// out again leaves off: a script PowerShell 5.1 then read in the
		// system's code page rather than as UTF-8, its accented text garbled,
		// and a first line shown as changed that was not.
		if bytes.HasPrefix(old, []byte(utf8BOM)) && !strings.HasPrefix(a.Content, utf8BOM) {
			a.Content = utf8BOM + a.Content
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", t.root.explain(err)
	}
	// Looked at again now that its directory is there. Before, in a directory
	// not yet made, a device's name had nothing behind it to see; deviceName
	// knows the names, and this is for whatever other spelling of one there is.
	if info, err := os.Stat(abs); err == nil {
		if err := regularFile(t.root, abs, info); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return "", t.root.explain(err)
	}
	return fmt.Sprintf("Wrote %s (%s, %s).", t.root.Rel(abs), linesOf(a.Content), humanBytes(int64(len(a.Content)))), nil
}

// utf8BOM is the byte-order mark a UTF-8 file may start with.
const utf8BOM = "\xef\xbb\xbf"

// firstMissingDir is the outermost directory on the way to dir that does not
// exist yet, or "" when dir is there already.
func firstMissingDir(dir string) string {
	missing := ""
	for {
		if _, err := os.Stat(dir); err == nil {
			return missing
		}
		missing = dir
		parent := filepath.Dir(dir)
		if parent == dir {
			return missing
		}
		dir = parent
	}
}

// editFile replaces one exact stretch of text with another.
//
// Insisting the old text occur exactly once, unless the model says otherwise,
// is what makes the edit safe to approve: the user is shown a change that can
// only land in one place, and an ambiguous edit is sent back to the model to
// be made specific rather than applied to whichever occurrence came first.
type editFile struct{ root *Root }

func (t *editFile) Name() string { return "edit_file" }

func (t *editFile) Describe() Schema {
	return Schema{
		Name: t.Name(),
		Description: "Replace an exact piece of text in a file. old_string must match the file " +
			"exactly, including indentation, and must occur exactly once unless replace_all is " +
			"true. Read the file first so the match is exact. The user is asked before anything " +
			"is changed.",
		Params: object(map[string]Property{
			"path":        {Type: "string", Description: "Path to the file, relative to the working directory."},
			"old_string":  {Type: "string", Description: "The text to replace, exactly as it appears in the file."},
			"new_string":  {Type: "string", Description: "The text to put in its place."},
			"replace_all": {Type: "boolean", Description: "Replace every occurrence instead of requiring exactly one."},
		}, "path", "old_string", "new_string"),
	}
}

type editArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// plan checks an edit and works out what the file would become. Approval and
// Run both need all of it, and the question the user is asked has to describe
// the edit that will actually be made, so the two go through the same code.
func (t *editFile) plan(a editArgs) (abs, updated string, count int, err error) {
	abs, err = t.root.ResolveFile(a.Path)
	if err != nil {
		return "", "", 0, err
	}
	if a.OldString == "" {
		return "", "", 0, fmt.Errorf("old_string is empty; use write_file to create a file")
	}
	if a.OldString == a.NewString {
		return "", "", 0, fmt.Errorf("old_string and new_string are identical, so the edit would change nothing")
	}
	if info, err := os.Stat(abs); err == nil {
		if err := regularFile(t.root, abs, info); err != nil {
			return "", "", 0, err
		}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", "", 0, t.root.explain(err)
	}
	if hasUTF16BOM(data) {
		// read_file shows it as text, and an edit would have to be written
		// back in its own encoding, which nothing here does.
		return "", "", 0, fmt.Errorf("%s is UTF-16 text, which edit_file cannot change in place; write_file can rewrite it whole, as UTF-8", t.root.Rel(abs))
	}
	if looksBinary(data) {
		return "", "", 0, fmt.Errorf("%s looks like a binary file", t.root.Rel(abs))
	}
	old := string(data)
	from, to := a.OldString, a.NewString
	if strings.Contains(old, "\r\n") {
		// read_file shows a file without its carriage returns, so an edit to
		// one with Windows line endings arrives written with bare newlines. It
		// is made in the file's own endings, rather than refused for not
		// matching or left as a file that mixes the two.
		to = withCRLF(to)
		if !strings.Contains(old, from) {
			from = withCRLF(from)
		}
	}
	count = strings.Count(old, from)
	switch {
	case count == 0:
		// The commonest way an edit misses is spacing: a tab written as
		// spaces, a line re-indented. Told only that the text is not there, a
		// model guesses; told where it nearly is, it reads those lines again.
		if at := looseMatch(old, from); at > 0 {
			return "", "", 0, fmt.Errorf("old_string does not appear in %s as written, but does at line %d with different spacing or indentation; read those lines again and copy them exactly", t.root.Rel(abs), at)
		}
		return "", "", 0, fmt.Errorf("old_string does not appear in %s", t.root.Rel(abs))
	case count > 1 && !a.ReplaceAll:
		return "", "", 0, fmt.Errorf("old_string appears %d times in %s; include more surrounding text so it is unique, or set replace_all", count, t.root.Rel(abs))
	}
	if a.ReplaceAll {
		updated = strings.ReplaceAll(old, from, to)
	} else {
		updated = strings.Replace(old, from, to, 1)
		count = 1
	}
	return abs, updated, count, nil
}

func (t *editFile) Approval(args json.RawMessage) string {
	var a editArgs
	if err := decode(args, &a); err != nil {
		return ""
	}
	abs, _, count, err := t.plan(a)
	if err != nil {
		return ""
	}
	// The two texts are shown as the lines they are, marked the way a diff
	// marks them: joined into one line, an edit of more than one line could
	// not be read closely enough to agree to.
	added := marked(textLines(a.NewString), "+")
	if a.NewString == "" {
		added = "  + (nothing: the text is removed)"
	}
	return fmt.Sprintf("Edit %s, replacing %d %s?\n%s\n%s", t.root.Rel(abs), count,
		plural(count, "occurrence", "occurrences"), marked(textLines(a.OldString), "-"), added)
}

// Prefix is the standing permission on offer for an edit, which is every edit.
func (t *editFile) Prefix(args json.RawMessage) string {
	var a editArgs
	if err := decode(args, &a); err != nil {
		return ""
	}
	return editPrefix(t.root, a.Path)
}

func (t *editFile) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a editArgs
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, updated, count, err := t.plan(a)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", t.root.explain(err)
	}
	// The file's own permissions are kept: an edit is not the moment to decide
	// that a script should stop being executable.
	if err := os.WriteFile(abs, []byte(updated), info.Mode().Perm()); err != nil {
		return "", t.root.explain(err)
	}
	return fmt.Sprintf("Edited %s, replacing %d %s.", t.root.Rel(abs), count, plural(count, "occurrence", "occurrences")), nil
}
