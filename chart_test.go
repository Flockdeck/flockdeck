package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/appwindow"
	"github.com/jmwri/flockdeck/internal/remote"
)

// The Dockerfile and the Helm chart under deploy/helm/flockdeck are written
// by hand against flockdeck's actual command line and environment. A flag
// renamed here, or an env var renamed or dropped, would leave them starting
// a binary that refuses its own arguments -- the exact drift
// cmd/flockdeck-relay/chart_test.go in the relay repository exists to catch
// for the relay's own chart. This is the same idea for flockdeck's Docker
// image and Helm chart, adapted the way
// flockdeck-planning/12-installation-simplification.md asks: because this
// package already defines every one of these names as a real Go identifier
// (dirEnv, startAgentEnv, ...), referencing them directly here is a stronger
// check than the relay's own regex-over-source-text approach -- a rename
// fails this file to compile, not just a test to run.

var chartDir = filepath.Join("deploy", "helm", "flockdeck")

// knownFlockdeckEnv is every FLOCKDECK_* (and the two others below)
// environment variable flockdeck itself reads, named by the constants that
// define them rather than by string literal, so a rename here is a compile
// error in this file rather than a silent gap in the check.
func knownFlockdeckEnv() map[string]bool {
	return map[string]bool{
		dirEnv:               true,
		startAgentEnv:        true,
		freshEnv:             true,
		shellFirstEnv:        true,
		noWindowEnv:          true,
		detachEnv:            true,
		soloEnv:              true,
		remoteJoinEnv:        true,
		remoteInviteEnv:      true,
		remoteNameEnv:        true,
		remote.RelayEnv:      true,
		updateEnv:            true,
		appwindow.BrowserEnv: true,
		agent.FallbackKeyEnv: true,
	}
}

// envNamesMentionedOnlyInProse are names the Dockerfile and the chart's
// README name in explanatory text without setting or reading them -- FLOCKDECK_DETACH
// in particular, which the container image deliberately never uses (-detach
// frees a terminal, which a container has none of to free; -no-window is
// the container's equivalent) but is worth naming in a comment explaining
// that choice.
var envNamesMentionedOnlyInProse = map[string]bool{
	detachEnv: true,
}

// topLevelFlags is every flag `flockdeck` itself (not a subcommand) defines,
// read from the same flag.FlagSet main() parses -- not regexed out of
// source text, since it is already a function this package exports to a
// test.
func topLevelFlags(t *testing.T) map[string]bool {
	t.Helper()
	fs := flockdeckFlagSet(&cliFlags{})
	flags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { flags[f.Name] = true })
	if !flags["C"] || !flags["no-window"] {
		t.Fatalf("reading flockdeck's own flags found %v, which is missing ones known to exist", flags)
	}
	return flags
}

func readChartFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(chartDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var envPattern = regexp.MustCompile(`FLOCKDECK_[A-Z0-9_]+`)

// Every FLOCKDECK_* name the Dockerfile mentions is one flockdeck actually
// reads.
func TestDockerfileEnvNamesAreKnown(t *testing.T) {
	known := knownFlockdeckEnv()
	dockerfile := readRepoFile(t, "Dockerfile")
	found := envPattern.FindAllString(dockerfile, -1)
	if len(found) < 5 {
		t.Fatalf("found only %d FLOCKDECK_* mentions in the Dockerfile; the pattern no longer matches how it documents them", len(found))
	}
	for _, name := range found {
		if !known[name] && !envNamesMentionedOnlyInProse[name] {
			t.Errorf("Dockerfile mentions %s, which flockdeck does not read (or this test does not know about); add its const to knownFlockdeckEnv or fix the Dockerfile", name)
		}
	}
}

// The flag CMD gives flockdeck by default is one flockdeck actually takes.
func TestDockerfileDefaultCMDFlagIsKnown(t *testing.T) {
	flags := topLevelFlags(t)
	dockerfile := readRepoFile(t, "Dockerfile")
	m := regexp.MustCompile(`(?m)^CMD \[(.+)\]\s*$`).FindStringSubmatch(dockerfile)
	if m == nil {
		t.Fatal("no CMD [...] line found in the Dockerfile")
	}
	for _, item := range regexp.MustCompile(`"-?([a-z][a-z0-9-]*)"`).FindAllStringSubmatch(m[1], -1) {
		if !flags[item[1]] {
			t.Errorf("the Dockerfile's default CMD passes -%s, which flockdeck does not take", item[1])
		}
	}
}

// Every FLOCKDECK_* name the chart sets is one flockdeck reads, and every
// top-level flag has flag written to it is one flockdeck defines. The chart
// deliberately has no way to set FLOCKDECK_NO_WINDOW, FLOCKDECK_DETACH: the
// image's own CMD already always runs -no-window (see the Dockerfile), and
// -detach makes no sense in a container that has no terminal to free, so
// both are left out on purpose rather than promoted to values.
var chartOmitsEnv = map[string]bool{
	noWindowEnv: true,
	detachEnv:   true,
	// The window's own browser choice: meaningless in a container, which
	// never opens one under -no-window.
	appwindow.BrowserEnv: true,
}

func TestChartEnvNamesAreKnown(t *testing.T) {
	known := knownFlockdeckEnv()
	var all strings.Builder
	err := filepath.WalkDir(chartDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		all.Write(data)
		all.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, name := range envPattern.FindAllString(all.String(), -1) {
		found[name] = true
	}
	if len(found) < 5 {
		t.Fatalf("found only %d distinct FLOCKDECK_* names across the chart; the walk found nothing to check", len(found))
	}
	for name := range found {
		if !known[name] {
			t.Errorf("the chart mentions %s, which flockdeck does not read", name)
		}
	}
	for name := range known {
		if !found[name] && !chartOmitsEnv[name] {
			t.Errorf("flockdeck reads %s, which the chart has no way to set; add it to values.yaml and deployment.yaml, or to chartOmitsEnv with the reason", name)
		}
	}
}

// Every flag the chart's Deployment passes in args is one flockdeck takes.
func TestChartArgsFlagsAreKnown(t *testing.T) {
	flags := topLevelFlags(t)
	deployment := readChartFile(t, "templates/deployment.yaml")
	passed := regexp.MustCompile(`(?m)^\s*-\s-([a-z][a-z0-9-]*)`).FindAllStringSubmatch(deployment, -1)
	if len(passed) == 0 {
		t.Fatal("found no -flag arguments in the chart's deployment; the pattern no longer matches how it passes them")
	}
	for _, m := range passed {
		if !flags[m[1]] {
			t.Errorf("the chart's deployment passes -%s, which flockdeck does not take", m[1])
		}
	}
}

// flockdeck, like the relay, is one process holding state that a second
// instance cannot share (a saved layout, a recent-projects list, one
// instance record) -- the chart runs exactly one, replaces it rather than
// rolling it, and offers no value that would change either.
func TestChartRunsOneFlockdeck(t *testing.T) {
	deployment := readChartFile(t, "templates/deployment.yaml")
	if n := len(regexp.MustCompile(`(?m)^\s*replicas: 1\s*$`).FindAllString(deployment, -1)); n != 1 {
		t.Errorf("the deployment says replicas: 1 %d times, want once", n)
	}
	if !regexp.MustCompile(`(?m)^\s*type: Recreate\s*$`).MatchString(deployment) {
		t.Error("the deployment does not replace its pod with Recreate")
	}
	err := filepath.WalkDir(chartDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, word := range []string{"replicaCount", "autoscaling", "HorizontalPodAutoscaler"} {
			if strings.Contains(string(data), word) {
				t.Errorf("%s mentions %s; flockdeck is one process and cannot be scaled", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The chart deliberately ships no Service or Ingress: self-hosted service
// mode's server binds 127.0.0.1 on a random port each start (no
// bind-address flag, no fixed port -- see internal/server.New), so neither
// object could ever route anything real. Shipping one that looks like it
// works and does not is worse than shipping neither; this test is a
// tripwire so that changes back, if self-hosted service mode ever grows a
// bind-address/fixed-port option, in the Go source, not by accident in the
// chart.
func TestChartHasNoServiceOrIngress(t *testing.T) {
	for _, base := range []string{"service.yaml", "ingress.yaml"} {
		if _, err := os.Stat(filepath.Join(chartDir, "templates", base)); err == nil {
			t.Errorf("templates/%s exists, but flockdeck's server has no fixed address for it to route to (see this test's own comment) -- confirm internal/server.New still has no bind-address/fixed-port option before adding it back", base)
		}
	}
}
