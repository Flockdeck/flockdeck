package jev

import (
	"strings"
	"testing"
)

const (
	fakeAPIKey = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	fakeBearer = "abcdefghijklmnopqrstuvwxyz012345"
)

func TestScrubTakesOutSecrets(t *testing.T) {
	in := strings.Join([]string{
		"key is " + fakeAPIKey,
		"curl -H 'Authorization: Bearer " + fakeBearer + "' https://x.test",
		"export DB_PASSWORD=hunter2-correct-horse",
		`read /home/me/project/.env and C:\Users\me\.ssh\id_rsa and ~/.aws/credentials`,
		"Proceed? (y/n)",
	}, "\n")
	got := Scrub(in)
	for _, leaked := range []string{fakeAPIKey, fakeBearer, "hunter2", ".env", "id_rsa", ".aws", "credentials"} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q survived: %q", leaked, got)
		}
	}
	for _, kept := range []string{"key is", "curl", "export DB_PASSWORD=", "read", "Proceed? (y/n)"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was lost: %q", kept, got)
		}
	}
}

func TestScrubLeavesOrdinaryTextAlone(t *testing.T) {
	in := "Run the tests? (y/n)\n  src/app/main.go:42: ok\n$ git status"
	if got := Scrub(in); got != in {
		t.Errorf("Scrub changed ordinary text: %q", got)
	}
}

func TestScrubOfNothingButSecretsIsEmpty(t *testing.T) {
	for _, in := range []string{"", "  \n", fakeAPIKey, " " + fakeAPIKey + "\n\n", "/home/me/.env"} {
		if got := Scrub(in); got != "" {
			t.Errorf("Scrub(%q) = %q, want empty", in, got)
		}
	}
}
