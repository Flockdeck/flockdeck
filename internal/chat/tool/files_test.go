package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// call runs one tool with arguments written as a Go map, which is how every
// test in this package states a call: the wire carries JSON and so should the
// test, but nobody should have to read it as a quoted string.
func call(t *testing.T, tl Tool, args map[string]any) (string, error) {
	t.Helper()
	return tl.Run(context.Background(), rawArgs(t, args))
}

func rawArgs(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// write lays down a file in the root for a test to work on.
func write(t *testing.T, root *Root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root.Dir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestReadFile(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a.txt", "one\ntwo\nthree\n")
	write(t, root, "empty.txt", "")
	write(t, root, "bin.dat", "head\x00tail")
	tl := &readFile{root: root}

	tests := []struct {
		name    string
		args    map[string]any
		want    []string // substrings the answer must contain
		absent  []string
		wantErr string // substring of the error, when one is expected
	}{
		{
			name: "the whole file, numbered",
			args: map[string]any{"path": "a.txt"},
			want: []string{"1\tone", "2\ttwo", "3\tthree"},
		},
		{
			name:   "a window of it",
			args:   map[string]any{"path": "a.txt", "offset": 2, "limit": 1},
			want:   []string{"2\ttwo", "1 more line", "offset 3"},
			absent: []string{"3\tthree"},
		},
		{
			name: "an empty file says so rather than saying nothing",
			args: map[string]any{"path": "empty.txt"},
			want: []string{"empty.txt is empty"},
		},
		{
			name:    "a binary file is refused",
			args:    map[string]any{"path": "bin.dat"},
			wantErr: "binary",
		},
		{
			name:    "a directory points at the right tool",
			args:    map[string]any{"path": "."},
			wantErr: "use list_dir",
		},
		{
			name:    "a file that is not there",
			args:    map[string]any{"path": "nope.txt"},
			wantErr: "nope.txt",
		},
		{
			name:    "an offset past the end",
			args:    map[string]any{"path": "a.txt", "offset": 99},
			wantErr: "past the end",
		},
		{
			name:    "a path out of the root",
			args:    map[string]any{"path": "../secret.txt"},
			wantErr: ErrOutsideRoot.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := call(t, tl, tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %q, %v; want an error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("answer is missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("answer should not contain %q:\n%s", absent, got)
				}
			}
		})
	}
}

func TestReadFileNeedsNoApproval(t *testing.T) {
	root := newRoot(t)
	for _, tl := range []Tool{&readFile{root: root}, &listDir{root: root}, &globTool{root: root}, &grepTool{root: root}} {
		if q := tl.Approval(rawArgs(t, map[string]any{"path": "a.txt", "pattern": "x"})); q != "" {
			t.Errorf("%s should not ask: %q", tl.Name(), q)
		}
	}
}

func TestWriteFile(t *testing.T) {
	root := newRoot(t)
	tl := &writeFile{root: root}

	if q := tl.Approval(rawArgs(t, map[string]any{"path": "new.txt", "content": "hello"})); !strings.Contains(q, "Create new.txt") {
		t.Errorf("a new file should be described as created: %q", q)
	}
	if _, err := call(t, tl, map[string]any{"path": "sub/new.txt", "content": "hello\n"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root.Dir(), "sub", "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Errorf("wrote %q", data)
	}

	q := tl.Approval(rawArgs(t, map[string]any{"path": "sub/new.txt", "content": "goodbye"}))
	if !strings.Contains(q, "Overwrite sub/new.txt") {
		t.Errorf("an existing file should be described as overwritten: %q", q)
	}
}

// TestWriteFileOutsideTheRootIsRefusedNotAsked is the rule from section 8 of
// the design: a path that escapes is refused, never offered for approval.
func TestWriteFileOutsideTheRootIsRefusedNotAsked(t *testing.T) {
	root := newRoot(t)
	args := rawArgs(t, map[string]any{"path": "../escape.txt", "content": "x"})

	for _, tl := range []Tool{&writeFile{root: root}, &editFile{root: root}} {
		if q := tl.Approval(args); q != "" {
			t.Errorf("%s asked about a path outside the root: %q", tl.Name(), q)
		}
		if _, err := tl.Run(context.Background(), args); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("%s: got %v, want a refusal", tl.Name(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root.Dir()), "escape.txt")); err == nil {
		t.Fatal("the refusal must not have written anything")
	}
}

func TestEditFile(t *testing.T) {
	const original = "alpha\nbeta\nalpha\n"

	tests := []struct {
		name    string
		args    map[string]any
		want    string // the file afterwards
		wantErr string
	}{
		{
			name: "one occurrence",
			args: map[string]any{"path": "a.txt", "old_string": "beta", "new_string": "BETA"},
			want: "alpha\nBETA\nalpha\n",
		},
		{
			name: "every occurrence when asked",
			args: map[string]any{"path": "a.txt", "old_string": "alpha", "new_string": "A", "replace_all": true},
			want: "A\nbeta\nA\n",
		},
		{
			name:    "an ambiguous edit is sent back rather than guessed at",
			args:    map[string]any{"path": "a.txt", "old_string": "alpha", "new_string": "A"},
			wantErr: "appears 2 times",
		},
		{
			name:    "text that is not there",
			args:    map[string]any{"path": "a.txt", "old_string": "gamma", "new_string": "G"},
			wantErr: "does not appear",
		},
		{
			name:    "an edit that changes nothing",
			args:    map[string]any{"path": "a.txt", "old_string": "beta", "new_string": "beta"},
			wantErr: "identical",
		},
		{
			name:    "an empty old_string points at the right tool",
			args:    map[string]any{"path": "a.txt", "old_string": "", "new_string": "x"},
			wantErr: "use write_file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRoot(t)
			abs := write(t, root, "a.txt", original)
			tl := &editFile{root: root}

			got, err := call(t, tl, tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %q, %v; want an error containing %q", got, err, tt.wantErr)
				}
				// An edit that was refused must not have asked either, and
				// must have left the file alone.
				if q := tl.Approval(rawArgs(t, tt.args)); q != "" {
					t.Errorf("a refused edit was offered for approval: %q", q)
				}
				after, err := os.ReadFile(abs)
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != original {
					t.Errorf("the file changed: %q", after)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(abs)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tt.want {
				t.Errorf("file is %q, want %q", after, tt.want)
			}
		})
	}
}

// TestEditApprovalDescribesTheEditThatWillBeMade matters because the question
// is the only thing the user sees before saying yes.
func TestEditApprovalDescribesTheEditThatWillBeMade(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a.txt", "alpha\nbeta\nalpha\n")
	tl := &editFile{root: root}

	q := tl.Approval(rawArgs(t, map[string]any{"path": "a.txt", "old_string": "beta", "new_string": "BETA"}))
	for _, want := range []string{"a.txt", "1 occurrence", "- beta", "+ BETA"} {
		if !strings.Contains(q, want) {
			t.Errorf("question is missing %q:\n%s", want, q)
		}
	}

	q = tl.Approval(rawArgs(t, map[string]any{"path": "a.txt", "old_string": "alpha", "new_string": "A", "replace_all": true}))
	if !strings.Contains(q, "2 occurrences") {
		t.Errorf("question should say how many places it lands in:\n%s", q)
	}
}

func TestSizes(t *testing.T) {
	sizes := []struct {
		n    int64
		want string
	}{{0, "0 B"}, {512, "512 B"}, {2048, "2.0 kB"}, {3 << 20, "3.0 MB"}}
	for _, s := range sizes {
		if got := humanBytes(s.n); got != s.want {
			t.Errorf("humanBytes(%d) = %q, want %q", s.n, got, s.want)
		}
	}
}
