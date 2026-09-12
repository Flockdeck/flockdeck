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
	f, err := os.Open(abs)
	if err != nil {
		return "", t.root.explain(err)
	}
	defer f.Close()
	// The file is read a line at a time rather than whole, because the model
	// pages through a large log with offset and limit and a read of two
	// thousand lines should not cost the memory of the whole file.
	r := bufio.NewReaderSize(f, 64<<10)
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
		noun := "lines"
		if remaining == 1 {
			noun = "line"
		}
		fmt.Fprintf(&b, "\n[%d more %s; read again with offset %d]\n", remaining, noun, shown+1)
	}
	return b.String(), nil
}

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
	if err != nil {
		return ""
	}
	// The question shows what is being written, not only how much of it: a
	// size is not something anybody can say yes or no to.
	rel := t.root.Rel(abs)
	newSize := humanBytes(int64(len(a.Content)))
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		old, _ := os.ReadFile(abs)
		q := fmt.Sprintf("Overwrite %s (%s, %s) with %s, %s?", rel,
			humanBytes(info.Size()), linesOf(string(old)), newSize, linesOf(a.Content))
		if at := firstChange(string(old), a.Content); at == 0 {
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

// excerptLines is how much of a write the question shows.
const excerptLines = 6

// excerpt is up to excerptLines lines of s from line from on.
func excerpt(s string, from int) string {
	lines := textLines(s)
	if from < 1 || from > len(lines) {
		return ""
	}
	return marked(lines[from-1:], "│")
}

// textLines splits s into lines without inventing an empty last one for the
// newline that ends it.
func textLines(s string) []string {
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}

// marked draws up to excerptLines of lines, each on a line of its own behind
// mark and cut to a width a pane shows, with a count of the rest.
func marked(lines []string, mark string) string {
	var b strings.Builder
	shown := lines
	if len(shown) > excerptLines {
		shown = shown[:excerptLines]
	}
	for _, l := range shown {
		r := []rune(l)
		if len(r) > 100 {
			r = append(r[:100], '…')
		}
		fmt.Fprintf(&b, "  %s %s\n", mark, string(r))
	}
	if rest := len(lines) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  %s … %d more %s", mark, rest, plural(rest, "line", "lines"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// firstChange is the line at which b first differs from a, or 0 when the two
// are the same.
func firstChange(a, b string) int {
	if a == b {
		return 0
	}
	al := strings.Split(strings.ReplaceAll(a, "\r\n", "\n"), "\n")
	bl := strings.Split(strings.ReplaceAll(b, "\r\n", "\n"), "\n")
	for i := range bl {
		if i >= len(al) || al[i] != bl[i] {
			return i + 1
		}
	}
	// b is a's opening lines and no more: what changed is that the rest went.
	return len(bl)
}

func linesOf(s string) string {
	n := countLines(s)
	return fmt.Sprintf("%d %s", n, plural(n, "line", "lines"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return "", fmt.Errorf("%s is a directory", t.root.Rel(abs))
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", t.root.explain(err)
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return "", t.root.explain(err)
	}
	return fmt.Sprintf("Wrote %s (%d lines, %s).", t.root.Rel(abs), countLines(a.Content), humanBytes(int64(len(a.Content)))), nil
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
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", "", 0, t.root.explain(err)
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

// withCRLF writes every line ending in s as a carriage return and a newline.
func withCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
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
	where := "1 occurrence"
	if count != 1 {
		where = fmt.Sprintf("%d occurrences", count)
	}
	// The two texts are shown as the lines they are, marked the way a diff
	// marks them: joined into one line, an edit of more than one line could
	// not be read closely enough to agree to.
	added := marked(textLines(a.NewString), "+")
	if a.NewString == "" {
		added = "  + (nothing: the text is removed)"
	}
	return fmt.Sprintf("Edit %s, replacing %s?\n%s\n%s",
		t.root.Rel(abs), where, marked(textLines(a.OldString), "-"), added)
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
	noun := "occurrence"
	if count != 1 {
		noun = "occurrences"
	}
	return fmt.Sprintf("Edited %s, replacing %d %s.", t.root.Rel(abs), count, noun), nil
}

// looksBinary reports whether data is something a model should be shown as
// text. A NUL byte near the start is the cheap, and in practice reliable,
// signal: no source file has one and almost every binary format does.
func looksBinary(data []byte) bool {
	head := data
	if len(head) > 8<<10 {
		head = head[:8<<10]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// nextLine reads one line without its ending, keeping at most max bytes of it
// and reporting how many were dropped. A file's last line may have no newline,
// in which case it comes back with io.EOF.
//
// A line has a ceiling of its own because one line of a minified bundle can be
// megabytes long, and handing it over whole would spend the context window on
// one read however few lines were asked for.
func nextLine(r *bufio.Reader, max int) (string, int, error) {
	var kept []byte
	dropped := 0
	for {
		chunk, err := r.ReadSlice('\n')
		if err != bufio.ErrBufferFull {
			// The line's own ending is not text: it is neither kept nor
			// counted among what the reader did not see.
			chunk = bytes.TrimRight(chunk, "\r\n")
		}
		if room := max - len(kept); len(chunk) > room {
			kept, dropped = append(kept, chunk[:room]...), dropped+len(chunk)-room
		} else {
			kept = append(kept, chunk...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if dropped == 0 {
			// A CRLF split across two reads leaves its carriage return here.
			kept = bytes.TrimRight(kept, "\r")
		}
		// Cutting by byte count can land in the middle of a rune.
		return strings.ToValidUTF8(string(kept), ""), dropped, err
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// humanBytes is a size as it should be read aloud, not as it is stored.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
