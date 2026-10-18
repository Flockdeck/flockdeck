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

func pemBody(lines int) string {
	var b []string
	for i := 0; i < lines; i++ {
		b = append(b, strings.Repeat("MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC", 1)+"abcdefghij"[:i%10]+strings.Repeat("Zx", 6)[:11-i%10])
	}
	return strings.Join(b, "\n")
}

func TestScrubTakesOutPrivateKeysWithOrWithoutTheirBeginLine(t *testing.T) {
	body := pemBody(12)
	whole := "before\n-----BEGIN PRIVATE KEY-----\n" + body + "\n-----END PRIVATE KEY-----\nProceed? (y/n)"
	if got := Scrub(whole); strings.Contains(got, "MIIEvQ") || !strings.Contains(got, "Proceed? (y/n)") {
		t.Errorf("whole key: %q", got)
	}
	// The window of output that is read starts in the middle of the key, with
	// part of a line before the body, and the BEGIN line is not in it.
	cut := "AQEFAASCBKcwggSjAgEAAoIBAQCabcdefghijklmn\n" + body + "\n-----END PRIVATE KEY-----\nProceed? (y/n)"
	got := Scrub(cut)
	if strings.Contains(got, "MIIEvQ") || strings.Contains(got, "AQEFAASCBKcw") {
		t.Errorf("a key cut from its BEGIN line was sent: %q", got)
	}
	if !strings.Contains(got, "Proceed? (y/n)") {
		t.Errorf("the question was lost: %q", got)
	}
	// Two lines are a hash and a path, not a key.
	two := strings.Join(strings.Split(body, "\n")[:2], "\n")
	if got := Scrub(two); got != two {
		t.Errorf("two lines of base64 were changed: %q", got)
	}
}

func TestScrubTakesOutURLCredentialsAndJWTs(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnopqrstuv"
	got := Scrub("clone https://deploy:s3cr3tpw@git.example.com/x.git\nAuth ok " + jwt + " done")
	for _, leaked := range []string{"s3cr3tpw", jwt} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q survived: %q", leaked, got)
		}
	}
	if !strings.Contains(got, "git.example.com") {
		t.Errorf("the host was lost: %q", got)
	}
}

func TestScrubKnowsMoreTokenShapes(t *testing.T) {
	for name, tok := range map[string]string{
		"stripe live":  "sk_live_" + "4eC39HqLyjWDarjtT1zdp7dc",
		"stripe test":  "rk_test_" + "4eC39HqLyjWDarjtT1zdp7dc",
		"hugging face": "hf_" + "abcdefghijklmnopqrstuvwxyzABCDEFGH",
		"digitalocean": "dop_v1_" + strings.Repeat("0123456789abcdef", 4),
		"twilio":       "SK" + "0123456789abcdef0123456789abcdef",
		"npm":          "npm_" + "abcdefghijklmnopqrstuvwxyz0123456789",
		"google":       "AIza" + "SyA-abcdefghijklmnopqrstuvwxyz01234",
		"github oauth": "gho_" + "abcdefghijklmnopqrstuvwxyz0123456789",
		"github user":  "ghu_" + "abcdefghijklmnopqrstuvwxyz0123456789",
		"github app":   "ghs_" + "abcdefghijklmnopqrstuvwxyz0123456789",
		"github renew": "ghr_" + "abcdefghijklmnopqrstuvwxyz0123456789",
	} {
		got := Scrub("token " + tok + " ok")
		if strings.Contains(got, tok[4:]) || got != "token [redacted] ok" {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestScrubLeavesOrdinaryProseAndCodeAlone(t *testing.T) {
	for _, in := range []string{
		"The task_test_runner and mask_live_data helpers return SKU lists; hf_ is a prefix.",
		"SKIP_THIS_TEST=1 go test ./... -run TestSkLive",
		"func (s *Store) Get(key string) (string, error) { return s.m[key], nil }",
		"see https://example.com/docs/get-started for the quick start",
		"abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789ab\nsha256 of one file",
		"cat README.md; ls src/app/main.go; go test ./...",
		"-----------------------------------------------------------------------------------\nPASS ok pkg 0.4s",
	} {
		if got := Scrub(in); got != in {
			t.Errorf("Scrub changed ordinary text:\n in: %q\nout: %q", in, got)
		}
	}
}

func TestScrubSecretFileNamesWithPunctuation(t *testing.T) {
	for _, in := range []string{"loaded .env: ok", "see .env, then", "(.env)", "wrote ~/.ssh/id_rsa.", "open `.env`"} {
		got := Scrub(in + " tail")
		if strings.Contains(got, ".env") || strings.Contains(got, "id_rsa") {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestScrubJoinsATokenTheTerminalWrapped(t *testing.T) {
	key := "sk-ant-api03-" + strings.Repeat("A1b2C3d4", 8)
	for _, nl := range []string{"\n", "\r\n"} {
		in := "export ANTHROPIC_AUTH_KEY_FOR_THIS_SESSION_IS_" + key[:30] + nl + key[30:] + nl + "Proceed? (y/n)"
		got := Scrub(in)
		if strings.Contains(got, "A1b2C3d4") {
			t.Errorf("part of a wrapped key was sent: %q", got)
		}
		if !strings.Contains(got, "export ") || !strings.Contains(got, "Proceed? (y/n)") {
			t.Errorf("ordinary text was lost: %q", got)
		}
	}
	// A long word at the end of a line that is not a token is left alone.
	in := "a path " + strings.Repeat("segment-", 6) + "\ncontinues-here and more"
	if got := Scrub(in); got != in {
		t.Errorf("changed: %q", got)
	}
}
