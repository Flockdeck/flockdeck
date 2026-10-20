package webui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// nextTag finds a build tag list that names next: -tags next, -tags=a,next,
// -tags "a next", and the same set through GOFLAGS.
var nextTag = regexp.MustCompile(`(?:-tags[= ]|GOFLAGS[^\n]*-tags[= ])\s*["']?[^\n"']*\bnext\b`)

func TestNextTagMatcher(t *testing.T) {
	for _, s := range []string{
		`go build -tags next .`,
		`go build -tags=next .`,
		`go build -tags "acp_canary next" .`,
		`go build -tags=netgo,next .`,
		`GOFLAGS=-tags=next go build .`,
		`GOFLAGS: -tags next`,
	} {
		if !nextTag.MatchString(s) {
			t.Errorf("not caught: %s", s)
		}
	}
	for _, s := range []string{
		`go build -trimpath .`,
		`go test -tags acp_canary ./...`,
		`go build -tags nextgen .`,
		`# the next release`,
	} {
		if nextTag.MatchString(s) {
			t.Errorf("caught by mistake: %s", s)
		}
	}
}

// A release is built without the new shell. The commands that build one are in
// the release workflow, the image build, the Makefile and cmd/release, and none
// may carry -tags with next in it. The test workflow runs in front of every
// release (release.yml calls it), so a release stops here.
func TestReleaseBuildsDoNotUseTheNextTag(t *testing.T) {
	root := filepath.Join("..", "..")
	var checked int
	for _, rel := range []string{
		".github/workflows/release.yml",
		".github/workflows/docker-image.yml",
		".github/workflows/linux-packages.yml",
		"Dockerfile",
		"Makefile",
		"cmd/release/main.go",
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		checked++
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			if nextTag.MatchString(line) {
				t.Errorf("%s:%d builds with the next tag: %s", rel, i+1, trimmed)
			}
		}
	}
	if checked < 6 {
		t.Errorf("only %d of the release build files were read", checked)
	}
}

// cmd/release builds the desktop binaries by running go build; it must not be
// passed a tag at all, since a tag there would end up in every release.
func TestReleaseToolPassesNoBuildTags(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "release", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"-tags"`) || strings.Contains(string(b), "-tags=") {
		t.Error("cmd/release passes build tags; a release is built with none")
	}
}
