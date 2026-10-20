package main

import (
	"strings"
	"testing"
)

// A release is built with no build tags, so a GOFLAGS set on the runner, such as
// -tags=next for the new shell, must not reach it. The last GOFLAGS in the list
// is the one the go command uses.
func TestBuildEnvEmptiesGOFLAGS(t *testing.T) {
	env := buildEnv([]string{"PATH=/bin", "GOFLAGS=-tags=next"}, "linux", "amd64", "1")
	var last string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOFLAGS=") {
			last = kv
		}
	}
	if last != "GOFLAGS=" {
		t.Errorf("GOFLAGS in the build environment is %q, want it empty", last)
	}
	for _, want := range []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1", "PATH=/bin"} {
		found := false
		for _, kv := range env {
			found = found || kv == want
		}
		if !found {
			t.Errorf("%s is missing from %v", want, env)
		}
	}
}
