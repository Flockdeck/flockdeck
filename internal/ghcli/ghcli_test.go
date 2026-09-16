package ghcli

import (
	"errors"
	"os/exec"
	"testing"
)

func TestInstalledAsksLookPath(t *testing.T) {
	old := lookPath
	defer func() { lookPath = old }()

	lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	if !Installed() {
		t.Error("Installed() = false, want true when lookPath finds gh")
	}
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if Installed() {
		t.Error("Installed() = true, want false when lookPath does not find gh")
	}
}

func TestDetectInstallerPicksTheFirstManagerFound(t *testing.T) {
	old := lookPath
	defer func() { lookPath = old }()

	lookPath = func(name string) (string, error) {
		if name == "dnf" {
			return "/usr/bin/dnf", nil
		}
		return "", exec.ErrNotFound
	}
	in := DetectInstaller()
	if in.Manager != "dnf" {
		t.Errorf("Manager = %q, want dnf", in.Manager)
	}
	if in.URL == "" {
		t.Error("URL should always be filled in, even when a manager was found")
	}
}

func TestDetectInstallerFallsBackToTheManualURL(t *testing.T) {
	old := lookPath
	defer func() { lookPath = old }()

	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	in := DetectInstaller()
	if in.Manager != "" || in.Command != nil {
		t.Errorf("got %+v, want no manager found", in)
	}
	if in.URL != ghDownloadsURL {
		t.Errorf("URL = %q, want %q", in.URL, ghDownloadsURL)
	}
}

func TestTryInstallRefusesWithNoManager(t *testing.T) {
	if err := TryInstall(t.Context(), Installer{URL: ghDownloadsURL}, nil); err == nil {
		t.Error("want an error when there is no command to run")
	}
}

func TestParseOneTimeCode(t *testing.T) {
	tests := []struct {
		line string
		want string
		ok   bool
	}{
		{"! First copy your one-time code: 1234-ABCD", "1234-ABCD", true},
		{"Press Enter to open github.com in your browser...", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := parseOneTimeCode(tt.line)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseOneTimeCode(%q) = %q, %v; want %q, %v", tt.line, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseAuthStatus(t *testing.T) {
	loggedIn := "github.com\n" +
		"  ✓ Logged in to github.com account octocat (keyring)\n" +
		"  - Active account: true\n" +
		"  - Git operations protocol: https\n" +
		"  - Token scopes: 'gist', 'read:org', 'repo', 'workflow'\n"
	st := parseAuthStatus(loggedIn)
	if !st.LoggedIn || st.Host != "github.com" || st.Account != "octocat" {
		t.Errorf("got %+v, want logged in as octocat on github.com", st)
	}
	if st.Scopes != "gist', 'read:org', 'repo', 'workflow" {
		// The surrounding quote characters are trimmed, but not the ones
		// gh puts around each scope in the middle of the list -- there is
		// nothing in the line that marks those as different from the ones on
		// the ends.
		t.Errorf("Scopes = %q", st.Scopes)
	}

	loggedOut := parseAuthStatus("")
	if loggedOut.LoggedIn {
		t.Errorf("got %+v, want not logged in for empty input", loggedOut)
	}
}

func TestIsNoneFound(t *testing.T) {
	if !isNoneFound(errors.New(`gh pr view: no pull requests found for branch "feature"`)) {
		t.Error("want true for gh's own not-found wording")
	}
	if isNoneFound(errors.New("gh pr view: HTTP 401: Bad credentials")) {
		t.Error("want false for an unrelated error")
	}
	if isNoneFound(nil) {
		t.Error("want false for nil")
	}
}

func TestPRNumberFromURL(t *testing.T) {
	tests := map[string]int{
		"https://github.com/acme/widgets/pull/123\n": 123,
		"https://github.com/acme/widgets/issues/7":   7,
		"":              0,
		"not-a-url":     0,
		"trailing/text": 0,
	}
	for in, want := range tests {
		if got := prNumberFromURL(in); got != want {
			t.Errorf("prNumberFromURL(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   Summary
	}{
		{"none", nil, Summary{Overall: "none"}},
		{
			"all passing",
			[]Check{{Bucket: "pass"}, {Bucket: "pass"}},
			Summary{Passing: 2, Overall: "passing"},
		},
		{
			"one failing wins over passing",
			[]Check{{Bucket: "pass"}, {Bucket: "fail"}},
			Summary{Passing: 1, Failing: 1, Overall: "failing"},
		},
		{
			"pending wins over everything, since the run is not over",
			[]Check{{Bucket: "fail"}, {Bucket: "pending"}},
			Summary{Failing: 1, Pending: 1, Overall: "pending"},
		},
		{
			"skipped checks count toward nothing",
			[]Check{{Bucket: "skipping"}},
			Summary{Overall: "none"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Summarize(tt.checks); got != tt.want {
				t.Errorf("Summarize(%v) = %+v, want %+v", tt.checks, got, tt.want)
			}
		})
	}
}

func TestGhLabelStopsBeforeAnArgumentThatCouldBeAWholeCommentBody(t *testing.T) {
	got := ghLabel([]string{"pr", "comment", "123", "--body-file", "-"})
	if got != "gh pr comment" {
		t.Errorf("ghLabel = %q, want %q", got, "gh pr comment")
	}
}

func TestFirstLinesKeepsOnlyTheLeadingLines(t *testing.T) {
	msg := "line1\nline2\nline3\nline4\nline5\nline6\nline7"
	got := firstLines(msg, 3)
	want := "line1\nline2\nline3\n…"
	if got != want {
		t.Errorf("firstLines = %q, want %q", got, want)
	}
	if got := firstLines("only one line", 3); got != "only one line" {
		t.Errorf("firstLines with fewer lines than n should be unchanged, got %q", got)
	}
}
