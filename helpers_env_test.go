package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/helpers"
)

// flockdeckEnvNames collects the variables Flockdeck itself reads or sets: every
// FLOCKDECK_ or PERCH_ name in the usage text, and every name paneEnv is asked
// for, in both spellings. It is driven from the program so that a variable
// added later is covered without anyone editing this test.
func flockdeckEnvNames(t *testing.T) []string {
	t.Helper()
	names := map[string]bool{}

	var usageText bytes.Buffer
	var c cliFlags
	fs := flockdeckFlagSet(&c)
	fs.SetOutput(&usageText)
	usage(fs)
	for _, m := range regexp.MustCompile(`\b(?:FLOCKDECK|PERCH)_[A-Z0-9_]+`).FindAllString(usageText.String(), -1) {
		names[m] = true
	}

	pane := regexp.MustCompile(`paneEnv\("([A-Z0-9_]+)"\)`)
	var sources []string
	for _, dir := range []string{".", "internal/chat"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		sources = append(sources, files...)
	}
	for _, f := range sources {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pane.FindAllStringSubmatch(string(src), -1) {
			names["FLOCKDECK_"+m[1]] = true
			names["PERCH_"+m[1]] = true
		}
	}

	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestHelperEnvironmentHasNoFlockdeckVariables(t *testing.T) {
	names := flockdeckEnvNames(t)
	// A scrape that finds almost nothing would pass for the wrong reason.
	for _, want := range []string{"FLOCKDECK_API_KEY", "FLOCKDECK_TOKEN", "FLOCKDECK_API", "FLOCKDECK_PANE", "PERCH_TOKEN", "FLOCKDECK_DIR"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Fatalf("%s was not found in the usage text or paneEnv calls; got %v", want, names)
		}
	}

	var parent []string
	for _, n := range names {
		parent = append(parent, n+"=do-not-leak", strings.ToLower(n)+"=do-not-leak")
	}
	parent = append(parent, "PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=do-not-leak", "TYPESAFE_API_KEY=do-not-leak",
		"OPENAI_API_KEY=do-not-leak", "GITHUB_TOKEN=do-not-leak")

	entry, _ := helpers.Lookup("lens")
	env, err := helpers.BuildEnv(entry, parent, helpers.EnvVars{Port: 8123, Host: "127.0.0.1", DataDir: "/d", AllowedHosts: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(name)
		if strings.Contains(val, "do-not-leak") || strings.HasPrefix(up, "FLOCKDECK_") || strings.HasPrefix(up, "PERCH_") ||
			strings.HasSuffix(up, "_API_KEY") || up == "GITHUB_TOKEN" {
			t.Errorf("%s reached the helper's environment", name)
		}
	}
	got := map[string]bool{}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		got[name] = true
	}
	for _, want := range []string{"PORT", "HOST", "DATA_DIR", "ALLOWED_HOSTS", "LOG_FILE", "PATH", "HOME"} {
		if !got[want] {
			t.Errorf("%s is missing from the helper's environment", want)
		}
	}
}
