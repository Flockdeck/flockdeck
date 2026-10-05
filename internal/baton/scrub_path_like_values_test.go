package baton

import "testing"

// A URL's user name can end in a secret word. The password is still the secret, and
// only it goes: the scheme, the user and the host stay.
func TestURLCredentialsWithAUserEndingInASecretWord(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https:" + "//x-access-token:Zk39dLq02Mnb81xYtr4Vw7paQ5@github.com/o/r", "https://x-access-token:[REDACTED: url-credential]@github.com/o/r"},
		{"https:" + "//x-access-token:gh" + "s_0123456789abcdefghijklmnopqrstuvwxyz@github.com/o/r", "https://x-access-token:[REDACTED: url-credential]@github.com/o/r"},
		{"https:" + "//gitlab-ci-token:gl" + "pat-abcdefghij1234567890@gitlab.example.com/g/p.git", "https://gitlab-ci-token:[REDACTED: url-credential]@gitlab.example.com/g/p.git"},
		{"https:" + "//deploy-token:Tr0ub4dor6LxQ9@bitbucket.org/w/r.git", "https://deploy-token:[REDACTED: url-credential]@bitbucket.org/w/r.git"},
		{"https:" + "//x-token-auth:xo" + "xb-11702363773-765817263563-4xqfMAT0ndl2ra14fDcppgMK@bitbucket.org/w/r.git", "https://x-token-auth:[REDACTED: url-credential]@bitbucket.org/w/r.git"},
	} {
		got, _ := NewScrubber().Scrub(tc.in)
		if got != tc.want {
			t.Errorf("Scrub(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A secret under a secret's name with a slash, a colon or a dot in it is a secret.
// A value is a path only when the name says it is a place and the value starts like
// one; a few explicit words are nothing; the words people do use as passwords are
// not on that list.
func TestSecretsWithSlashesAndColonsAreNotTakenForPaths(t *testing.T) {
	for in, secret := range map[string]string{
		"DB_PASSWORD=ab3/Xk9/Zz":                          "ab3/Xk9/Zz",
		"password: hunter2/prod":                          "hunter2/prod",
		"token: sk_live/abc123def456":                     "sk_live/abc123def456",
		"GITLAB_TOKEN=gl" + "pat-abc/defghijklmnopqrst12": "gl" + "pat-abc/defghijklmnopqrst12",
		`password = "Aa1AYCiTsmbp7HwEbJ/n-/TBI"`:          "Aa1AYCiTsmbp7HwEbJ/n-/TBI",
		"api_key=Aa1ro1cvXdaxGH0RXhmbr:":                  "Aa1ro1cvXdaxGH0RXhmbr:",
		"private_key: path/to/file":                       "path/to/file",
		"token_file: /etc/x/Zk39dLq02Mnb81xYtr4Vw7paQ":    "Zk39dLq02Mnb81xYtr4Vw7paQ",
		"password: /run/secrets/Zk39dLq02Mnb81xYtr4V":     "Zk39dLq02Mnb81xYtr4V",
	} {
		taken(t, in, []string{secret}, nil)
	}
}

// The words people do use as passwords are redacted as values.
func TestTheWordsPeopleUseAsPasswordsAreRedactedAsValues(t *testing.T) {
	for _, in := range []string{"password: password", "PASSWORD=password", "password: changeme", "secret: admin", "token = secret"} {
		if got, _ := NewScrubber().Scrub(in); got == in {
			t.Errorf("Scrub(%q) left a word people use as a password", in)
		}
	}
}

func TestPlacesAndNothingsUnderSecretNamesStayUnmarked(t *testing.T) {
	for _, in := range []string{
		"password: /run/secrets/db_password", "DB_PASSWORD_FILE=/run/secrets/db_password", "private_key_path: ./keys/deploy",
		"ssh_key: ./keys/deploy", "tls_key_file: C:/certs/server", "token_file: ../tokens/ci", "service_url: https://api.example.com/v1",
		"password: none", "token: null", "secret: false", "api_key: true", "password: required", "Token: string", "secret_key: integer",
		"token_type: bearer", "api_key: your-key-here", "password=********", "secret_key_base: <%= ENV['SECRET'] %>",
		`password = os.Getenv("DB_PASSWORD")`, "token = ENV['TOKEN']", "auth_token_expires_at: 2026-12-31", "token_created_at: 2026-10-04T10:00:00Z",
		"num_tokens = 12", "max_tokens: 4096", "token_count=7", "TOKEN_BUCKET_SIZE=100", "token_ttl: 3600", "password_length: 16",
	} {
		untouched(t, in)
	}
}

// A mark with a kind this scrubber does not make is text that looks like a mark,
// and may have been put there to hide a secret: it is taken as a secret value.
func TestAMarkOfAnUnknownKindInUserTextIsASecretValueMark(t *testing.T) {
	got, _ := NewScrubber().Scrub("see [REDACTED: made-up-kind] for the value")
	if got != "see [REDACTED: secret-value] for the value" {
		t.Errorf("got %q", got)
	}
}

// Counts of model tokens and variables handed on are not secrets, and a secret is not
// let through by looking like either.
func TestTokenCountsAndHandedOnVariablesAreNotSecrets(t *testing.T) {
	for _, in := range []string{
		`"usage":{"input_tokens":100,"output_tokens":40}`, "tokens: 9999", "TokensBefore: 900000", "prompt_tokens=11",
		"p := payload{Event: e, Token: token}", "reporter{endpoint: endpoint, token: token, session: s}", "earlySecret:  earlySecret,",
	} {
		untouched(t, in)
	}
	for _, in := range []string{"token: token", "TOKEN=token", "password: password", "token: 123456", "api_token: 90210"} {
		if got, _ := NewScrubber().Scrub(in); got == in {
			t.Errorf("Scrub(%q) let a value through", in)
		}
	}
}
