package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "internal/main.go", false},
		{"**/*.go", "internal/chat/tool/glob.go", true},
		// ** spans zero segments as well as many, so a pattern written
		// **/*.go -- which is what a model writes when it means "every Go
		// file" -- also finds the ones at the top.
		{"**/*.go", "main.go", true},
		{"**", "anything/at/all.txt", true},
		{"internal/**/*_test.go", "internal/chat/tool/dirs_test.go", true},
		{"internal/**/*_test.go", "cmd/main_test.go", false},
		{"internal/*/*.go", "internal/store/store.go", true},
		{"internal/*/*.go", "internal/store/sub/store.go", false},
		{"?.txt", "a.txt", true},
		{"?.txt", "ab.txt", false},
		{"./*.go", "main.go", true},
		{"docs/**", "docs/a/b/c.md", true},
		{"docs/**", "docs", true},
		{"docs/**", "other/a.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.name, func(t *testing.T) {
			if got := matchGlob(tt.pattern, tt.name); got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestListDir(t *testing.T) {
	root := newRoot(t)
	write(t, root, "b.txt", "hello")
	write(t, root, "sub/c.txt", "hello")
	if err := os.MkdirAll(filepath.Join(root.Dir(), "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	tl := &listDir{root: root}

	got, err := call(t, tl, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	// The first line names the directory, then the directories, then the files.
	want := []string{".:", "empty/", "sub/", "b.txt  5 B"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), got)
	}
	for i, w := range want {
		if strings.TrimSpace(lines[i]) != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}

	if got, err := call(t, tl, map[string]any{"path": "empty"}); err != nil || !strings.Contains(got, "is empty") {
		t.Errorf("an empty directory should say so: %q, %v", got, err)
	}
	if _, err := call(t, tl, map[string]any{"path": "b.txt"}); err == nil || !strings.Contains(err.Error(), "use read_file") {
		t.Errorf("a file should point at read_file: %v", err)
	}
}

func TestGlob(t *testing.T) {
	root := newRoot(t)
	write(t, root, "main.go", "package main")
	write(t, root, "internal/store/store.go", "package store")
	write(t, root, "internal/store/store_test.go", "package store")
	write(t, root, "README.md", "hello")
	write(t, root, ".git/objects/deadbeef", "not source")
	tl := &globTool{root: root}

	tests := []struct {
		name   string
		args   map[string]any
		want   []string
		absent []string
	}{
		{
			name:   "every Go file below the root",
			args:   map[string]any{"pattern": "**/*.go"},
			want:   []string{"internal/store/store.go", "internal/store/store_test.go"},
			absent: []string{"README.md"},
		},
		{
			name:   "the top level only",
			args:   map[string]any{"pattern": "*.go"},
			want:   []string{"main.go"},
			absent: []string{"internal/store/store.go"},
		},
		{
			name:   "within a subdirectory, reported from the root",
			args:   map[string]any{"pattern": "*_test.go", "path": "internal/store"},
			want:   []string{"internal/store/store_test.go"},
			absent: []string{"internal/store/store.go"},
		},
		{
			name:   "the version control store is not source",
			args:   map[string]any{"pattern": "**"},
			want:   []string{"main.go"},
			absent: []string{"deadbeef"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := call(t, tl, tt.args)
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

	if got, err := call(t, tl, map[string]any{"pattern": "**/*.rs"}); err != nil || !strings.Contains(got, "No files match") {
		t.Errorf("a pattern matching nothing should say so: %q, %v", got, err)
	}
	if _, err := call(t, tl, map[string]any{"pattern": "**", "path": ".."}); err == nil {
		t.Error("a search rooted outside the working directory must be refused")
	}
	if _, err := call(t, tl, map[string]any{}); err == nil {
		t.Error("a glob with no pattern must be refused")
	}
}
