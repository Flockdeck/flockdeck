package tool

import (
	"strings"
	"testing"
)

func TestGrep(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a.go", "package main\n\nfunc Start() {}\nfunc stop() {}\n")
	write(t, root, "sub/b.go", "package sub\n\nfunc Start() {}\n")
	write(t, root, "notes.md", "Start here.\n")
	write(t, root, "bin.dat", "Start\x00Start\n")
	write(t, root, ".git/config", "Start\n")
	tl := &grepTool{root: root}

	tests := []struct {
		name    string
		args    map[string]any
		want    []string
		absent  []string
		wantErr string
	}{
		{
			name:   "a match reports the path and the line number",
			args:   map[string]any{"pattern": "func Start"},
			want:   []string{"a.go:3: func Start() {}", "sub/b.go:3: func Start() {}"},
			absent: []string{"notes.md"},
		},
		{
			name:   "a glob narrows it to one kind of file",
			args:   map[string]any{"pattern": "Start", "glob": "**/*.md"},
			want:   []string{"notes.md:1: Start here."},
			absent: []string{"a.go"},
		},
		{
			name: "case can be ignored",
			args: map[string]any{"pattern": "^func stop", "ignore_case": true},
			want: []string{"a.go:4"},
		},
		{
			name:   "only the paths, when that is all that was asked for",
			args:   map[string]any{"pattern": "func Start", "files_only": true},
			want:   []string{"a.go", "sub/b.go"},
			absent: []string{"func Start() {}"},
		},
		{
			name:   "a subdirectory, reported from the root",
			args:   map[string]any{"pattern": "Start", "path": "sub"},
			want:   []string{"sub/b.go:3"},
			absent: []string{"a.go"},
		},
		{
			name: "one file",
			args: map[string]any{"pattern": "Start", "path": "notes.md"},
			want: []string{"notes.md:1"},
		},
		{
			name:   "binary files and version control stores are skipped",
			args:   map[string]any{"pattern": "Start"},
			absent: []string{"bin.dat", ".git/config"},
			want:   []string{"a.go"},
		},
		{
			name: "no match says so",
			args: map[string]any{"pattern": "nowhere at all"},
			want: []string{"No matches"},
		},
		{
			name:    "a pattern that is not a regular expression",
			args:    map[string]any{"pattern": "func Start(("},
			wantErr: "not a valid regular expression",
		},
		{
			name:    "no pattern",
			args:    map[string]any{},
			wantErr: "pattern is required",
		},
		{
			name:    "a search rooted outside the working directory",
			args:    map[string]any{"pattern": "x", "path": "../.."},
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
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q:\n%s", w, got)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("should not contain %q:\n%s", a, got)
				}
			}
		})
	}
}

// TestGrepStopsAtTheLimit keeps a search over a large checkout from filling the
// model's context with one pattern's worth of noise.
func TestGrepStopsAtTheLimit(t *testing.T) {
	root := newRoot(t)
	write(t, root, "many.txt", strings.Repeat("needle\n", 50))
	tl := &grepTool{root: root}

	got, err := call(t, tl, map[string]any{"pattern": "needle", "max_results": 5})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "many.txt:"); n != 5 {
		t.Errorf("got %d matches, want 5:\n%s", n, got)
	}
	if !strings.Contains(got, "stopped at 5 matches") {
		t.Errorf("a truncated search must say it was truncated:\n%s", got)
	}
}

func TestClipShortensALongLine(t *testing.T) {
	long := strings.Repeat("x", grepMaxLine+50)
	got := clip(long)
	if len([]rune(got)) != grepMaxLine+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("clip returned %d runes", len([]rune(got)))
	}
	if got := clip("short"); got != "short" {
		t.Errorf("clip(%q) = %q", "short", got)
	}
}
