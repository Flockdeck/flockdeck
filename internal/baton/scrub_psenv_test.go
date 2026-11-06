package baton

import (
	"strings"
	"testing"
)

// PowerShell sets an environment variable through the env: drive with the name
// and the value as separate words, which no = or flag rule sees.
func TestScrubHidesTheValueOfAPowerShellEnvAssignment(t *testing.T) {
	sc := NewScrubber()
	for _, in := range []string{
		"Set-Item env:API_TOKEN 'zzzz1111aaaa'",
		"Set-Item env:API_TOKEN zzzz1111aaaa",
		"Set-Item -Path env:API_TOKEN -Value zzzz1111aaaa",
		"Set-Item -Path env:API_TOKEN -Value \"zzzz1111aaaa\"; ./deploy",
		"set-content env:DB_PASSWORD zzzz1111aaaa",
		"sc env:DB_PASSWORD zzzz1111aaaa",
		"New-Item -Path env:CLIENT_SECRET -Value zzzz1111aaaa",
		"[Environment]::SetEnvironmentVariable('DB_PASSWORD','zzzz1111aaaa')",
		"[System.Environment]::SetEnvironmentVariable(\"API_TOKEN\", \"zzzz1111aaaa\", \"User\")",
	} {
		if got, _ := sc.Scrub(in); strings.Contains(got, "zzzz1111aaaa") {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
	}
	// What is not a secret is left alone.
	for _, in := range []string{"Set-Item env:EDITOR code", "Set-Item env:API_TOKEN_FILE ./token", "disc env:DB_PASSWORD x"} {
		if got, _ := sc.Scrub(in); got != in {
			t.Errorf("Scrub(%q) = %q, want it unchanged", in, got)
		}
	}
}
