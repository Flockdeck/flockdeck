package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	// previewRunes bounds how much of a string is quoted back in an approval
	// question or an error, so that neither can push the rest of the terminal
	// off the screen.
	previewRunes = 240
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
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use list_dir", t.root.Rel(abs))
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if looksBinary(data) {
		return "", fmt.Errorf("%s looks like a binary file (%s)", t.root.Rel(abs), humanBytes(info.Size()))
	}
	if len(data) == 0 {
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
	lines := splitLines(data)
	if offset > len(lines) {
		return "", fmt.Errorf("%s has %d lines; offset %d is past the end", t.root.Rel(abs), len(lines), offset)
	}
	end := offset - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	shown := offset - 1
	for i := offset - 1; i < end; i++ {
		if b.Len() >= readMaxBytes {
			break
		}
		fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
		shown = i + 1
	}
	if remaining := len(lines) - shown; remaining > 0 {
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
	rel := t.root.Rel(abs)
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return fmt.Sprintf("Overwrite %s (%s) with %s?", rel, humanBytes(info.Size()), humanBytes(int64(len(a.Content))))
	}
	return fmt.Sprintf("Create %s (%s)?", rel, humanBytes(int64(len(a.Content))))
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
		return "", err
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return "", err
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
		return "", "", 0, err
	}
	if looksBinary(data) {
		return "", "", 0, fmt.Errorf("%s looks like a binary file", t.root.Rel(abs))
	}
	old := string(data)
	count = strings.Count(old, a.OldString)
	switch {
	case count == 0:
		return "", "", 0, fmt.Errorf("old_string does not appear in %s", t.root.Rel(abs))
	case count > 1 && !a.ReplaceAll:
		return "", "", 0, fmt.Errorf("old_string appears %d times in %s; include more surrounding text so it is unique, or set replace_all", count, t.root.Rel(abs))
	}
	if a.ReplaceAll {
		updated = strings.ReplaceAll(old, a.OldString, a.NewString)
	} else {
		updated = strings.Replace(old, a.OldString, a.NewString, 1)
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
	where := "1 occurrence"
	if count != 1 {
		where = fmt.Sprintf("%d occurrences", count)
	}
	return fmt.Sprintf("Edit %s, replacing %s?\n- %s\n+ %s",
		t.root.Rel(abs), where, preview(a.OldString), preview(a.NewString))
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
		return "", err
	}
	// The file's own permissions are kept: an edit is not the moment to decide
	// that a script should stop being executable.
	if err := os.WriteFile(abs, []byte(updated), info.Mode().Perm()); err != nil {
		return "", err
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

// splitLines splits a file into lines without inventing a trailing empty one
// for the newline that ends a well-formed text file.
func splitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// preview is a short, single-line rendering of a string for a question or an
// error. A newline becomes a marker rather than wrapping, so that an approval
// question stays one glance long however much text the edit moves.
func preview(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " / ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > previewRunes {
		return string(r[:previewRunes]) + "..."
	}
	return s
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
