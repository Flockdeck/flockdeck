package baton

import "testing"

func TestAnAuthorizationHeaderInJSONKeepsItsQuoteAndBrace(t *testing.T) {
	got, _ := NewScrubber().Scrub(`{"Authorization": "Bearer abc123def456ghi"}`)
	if got != `{"Authorization": "Bearer [REDACTED: auth-credential]"}` {
		t.Errorf("got %q", got)
	}
	got, _ = NewScrubber().Scrub(`curl -H 'Authorization: Bearer abc123def456ghi' x`)
	if got != `curl -H 'Authorization: Bearer [REDACTED: bearer-token]' x` {
		t.Errorf("got %q", got)
	}
}

func TestAFlagThatTakesNothingDoesNotTakeTheNextLine(t *testing.T) {
	untouched(t, "docker login -u me --password-stdin\nregistry.example.com")
	untouched(t, "tool --token-file /etc/tool/token")
	untouched(t, "tool --password\nsecondword")
	taken(t, "tool --password hunter2hunter2 --verbose", []string{"hunter2hunter2"}, []string{"--verbose"})
}

func TestTypesBooleansAndNamesAreNotSecretValues(t *testing.T) {
	for _, in := range []string{
		"password: required", "secret: false", "Token: string", "password: null",
		"token_type: bearer", "password:\n  type: string", "password: true", "secret: none",
		"api_key: your-key-here", "client_secret: <required>",
	} {
		untouched(t, in)
	}
	// What a real value looks like is still taken.
	taken(t, "password: Hunter2Hunter2", []string{"Hunter2Hunter2"}, nil)
	taken(t, "token: bearer abc123def456ghi", []string{"abc123def456ghi"}, nil)
}

func TestCookieValuesWithPunctuationAndMoreBareCredentials(t *testing.T) {
	taken(t, "Cookie: a=Zm9vYmFy!baz#qux123456789abcdef", []string{"Zm9vYmFy"}, nil)
	taken(t, `dbpass := "hunter2hunter2"`, []string{"hunter2hunter2"}, nil)
	taken(t, `rootpass = 'Zx9Qw8Er7Ty6'`, []string{"Zx9Qw8Er7Ty6"}, nil)
	untouched(t, `bypass := "hunter2hunter2"`)
	taken(t, "curl user:Sup3rS3cret@host.example/x", []string{"Sup3rS3cret"}, []string{"user:"})
	untouched(t, "send it to mailto:alice@example.com today")
	untouched(t, "git clone git@github.com:org/repo.git")
}
