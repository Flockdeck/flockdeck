package baton

import (
	"strings"
	"testing"
)

func TestAnyWordAfterAuthorizationIsTheScheme(t *testing.T) {
	sc := NewScrubber()
	for _, scheme := range []string{"Foo", "Custom-Scheme", "SSWS", "Zoho-oauthtoken", "GoogleLogin", "Client-ID", "AWS", "Splunk"} {
		in := "Authorization: " + scheme + " CRED9876zzxx and then some"
		got, _ := sc.Scrub(in)
		if got != "Authorization: "+scheme+" [REDACTED: auth-credential] and then some" {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
	}
	for in, leaks := range map[string][]string{
		"Authorization: Bearer a1b2c3d4e5f6g7, Bearer h8i9j0k1l2m3n4": {"a1b2c3d4", "h8i9j0k1"},
		`Authorization: Bearer abc"def123456`:                         {"abc", "def123456"},
		"Authorization: Bearer abcd;efgh1234 end":                     {"abcd", "efgh1234"},
		"Proxy-Authorization: Negotiate YIIabcdef123456":              {"YIIabcdef"},
	} {
		got, _ := sc.Scrub(in)
		for _, leak := range leaks {
			if strings.Contains(got, leak) {
				t.Errorf("Scrub(%q) = %q, still holds %q", in, got, leak)
			}
		}
		if !strings.Contains(got, "uthorization: ") {
			t.Errorf("Scrub(%q) = %q, lost the header name", in, got)
		}
	}
	// A credential with no scheme is a single word, and is record's to take.
	if got, _ := sc.Scrub("Authorization: abc123xyz789"); strings.Contains(got, "abc123xyz789") {
		t.Errorf("Scrub = %q", got)
	}
}

func TestJSONValuesUnderASecretKeyAreScrubbed(t *testing.T) {
	sc := NewScrubber()
	for in, leaks := range map[string][]string{
		`{"X-Api-Key":["zzzz9999yyyy"]}`:                                             {"zzzz9999yyyy"},
		`{"password":["hunter2hunter"]}`:                                             {"hunter2hunter"},
		`{"api_key": ["a1b2c3d4", "e5f6g7h8"]}`:                                      {"a1b2c3d4", "e5f6g7h8"},
		`{"token":{"value":"tok98765abcd","n":["inner99887766"]}}`:                   {"tok98765abcd", "inner99887766"},
		`[["Authorization","Bearer xyz12345"],["X-Api-Key","zzzz9999yyyy"]]`:         {"xyz12345", "zzzz9999yyyy"},
		"{\n  \"secrets\": [\n    \"line1secret99\",\n    \"line2secret88\"\n  ]\n}": {"line1secret99", "line2secret88"},
	} {
		got, _ := sc.Scrub(in)
		for _, leak := range leaks {
			if strings.Contains(got, leak) {
				t.Errorf("Scrub(%q) = %q, still holds %q", in, got, leak)
			}
		}
	}
	// Keys and unrelated fields stay.
	got, _ := sc.Scrub(`{"name":"flockdeck","password":["hunter2hunter"],"port":8080}`)
	if !strings.Contains(got, `"name":"flockdeck"`) || !strings.Contains(got, `"port":8080`) || !strings.Contains(got, `"password"`) {
		t.Errorf("Scrub = %q", got)
	}
}

func TestQuotedPasswordsInEveryShellForm(t *testing.T) {
	sc := NewScrubber()
	const cut = "-p[REDACTED: db-password]"
	for in, want := range map[string]string{
		`mysql -u root -p"a \"quoted\" pass" db`: "mysql -u root " + cut + " db",
		`mysql -u root -p'it''s a pass' db`:      "mysql -u root " + cut + " db",
		`mysql -u root -p'abc'def123 db`:         "mysql -u root " + cut + " db",
		`mysql -u root -p"my pass"suffix db`:     "mysql -u root " + cut + " db",
		`mysql -p'a b' -p'c d' db`:               "mysql " + cut + " " + cut + " db",
		`mysqldump -p"my pass" db`:               "mysqldump " + cut + " db",
		`sshpass -p 'my pass' ssh host`:          "sshpass -p [REDACTED: db-password] ssh host",
		`redis-cli -a 'my pass' ping`:            "redis-cli -a [REDACTED: db-password] ping",
		`docker login -p 'my pass' -u me`:        "docker login -p [REDACTED: db-password] -u me",
		`curl -u 'me:my pass' https://x`:         "curl -u 'me:[REDACTED: db-password] https://x",
	} {
		got, _ := sc.Scrub(in)
		if want != "" && got != want && !strings.HasPrefix(in, "curl") {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
		for _, residue := range []string{"my pass", "quoted", "it'", "def123", "suffix", "a b", "c d", "pass\"", "pass'"} {
			if strings.Contains(got, residue) {
				t.Errorf("Scrub(%q) = %q, residue %q", in, got, residue)
			}
		}
	}
	if got, _ := sc.Scrub("mysql -h db -P 3306 -u root"); got != "mysql -h db -P 3306 -u root" {
		t.Errorf("a port was taken: %q", got)
	}
}

func TestOddSpacesAreReadBothWays(t *testing.T) {
	sc := NewScrubber()
	for name, in := range map[string]string{
		"ideographic space in a token": "see gh" + "p_0123456789abcdefghij\u3000ABCDEFGHIJ012345 end",
		"no-break space in a url":      "https:" + "//user:pass\u00a0word@host/x",
		"no-break space in a bearer":   "Authorization: Bearer abc\u00a0def123456",
		"figure space in a token":      "see gh" + "p_0123456789abcdefghij\u2007ABCDEFGHIJ012345 end",
		"narrow no-break in a token":   "see gh" + "p_0123456789abcdefghij\u202fABCDEFGHIJ012345 end",
	} {
		got, _ := sc.Scrub(in)
		for _, leak := range []string{"gh" + "p_0123", "ABCDEFGHIJ012345", "pass\u00a0word", "def123456", "abc\u00a0"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: Scrub(%q) = %q, still holds %q", name, in, got, leak)
			}
		}
	}
	// Read as a space it ends the token; read as nothing it joins the word after
	// it to the token, and both readings are taken. So the word may go with the
	// token, and a word after that is kept.
	got, _ := sc.Scrub("key gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz\u3000and a word")
	if strings.Contains(got, "ghp_") || !strings.HasSuffix(got, "a word") {
		t.Errorf("Scrub = %q", got)
	}
}

func TestARandomPathPartWithDotsIsStillTaken(t *testing.T) {
	sc := NewScrubber()
	for _, in := range []string{
		"/home/u/x/aB3dE5gH7jK9mN1pQ3sT.5vX7zA9cE1gI3kM5oQ7s9xY2wZ4",
		`C:\x\Zq8Xv2LmN9pR4tYw6KdH.3sJf7BcA5eGu7HnQ2mW4xR8vT1\y`,
	} {
		got, _ := sc.Scrub("see " + in + " end")
		if strings.Contains(got, "aB3dE5") || strings.Contains(got, "Zq8Xv2") || strings.Contains(got, "5vX7zA") || strings.Contains(got, "3sJf7B") {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
		if !strings.HasPrefix(got, "see ") || !strings.HasSuffix(got, " end") {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
	}
	// Ordinary names with dots and digits stay.
	for _, in := range []string{
		"/home/u/projects/Microsoft.WindowsTerminal_8wekyb3d8bbwe/settings.json",
		"/usr/lib/python3.11/site-packages/numpy-1.26.4.dist-info/METADATA",
	} {
		if got, _ := sc.Scrub(in); got != in {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
	}
}

func TestToldValuesWithOddCharactersMatch(t *testing.T) {
	for name, c := range map[string]struct{ value, text string }{
		"full width, ascii text":  {"\uff30\uff21\uff33\uff33\uff17\uff18\uff19\uff10xyz", "the pass is PASS7890xyz here"},
		"full width, same text":   {"\uff30\uff21\uff33\uff33\uff17\uff18\uff19\uff10xyz", "the pass is \uff30\uff21\uff33\uff33\uff17\uff18\uff19\uff10xyz here"},
		"joiner in the value":     {"pass\u200dword1234", "the pass is password1234 here"},
		"joiner in both":          {"pass\u200dword1234", "the pass is pass\u200dword1234 here"},
		"no-break space in value": {"my\u00a0secret value", "the pass is my secret value here"},
		"no-break space in both":  {"my\u00a0secret value", "the pass is my\u00a0secret value here"},
		"plain":                   {"plainsecret99", "the pass is plainsecret99 here"},
	} {
		got, reds := NewScrubber(c.value).Scrub(c.text)
		if RedactionCount(reds) == 0 || strings.Contains(got, "7890xyz") || strings.Contains(got, "word1234") || strings.Contains(got, "secret value") || strings.Contains(got, "secret99") {
			t.Errorf("%s: Scrub(%q) = %q (%v)", name, c.text, got, reds)
		}
		if !strings.HasPrefix(got, "the pass is ") || !strings.HasSuffix(got, " here") {
			t.Errorf("%s: Scrub(%q) = %q, lost the words around it", name, c.text, got)
		}
	}
}

func TestSecretNamedKeysWithValuesOfSeveralWords(t *testing.T) {
	sc := NewScrubber()
	for in, want := range map[string]string{
		"password: my secret value here":            "password: [REDACTED: env-secret]",
		"secret_key = correct horse battery staple": "secret_key = [REDACTED: env-secret]",
		"token = a b c":                                  "token = [REDACTED: env-secret]",
		"  api_key: two words # a comment":               "  api_key: [REDACTED: env-secret] # a comment",
		"export DB_PASSWORD=pa ss word":                  "export DB_PASSWORD=[REDACTED: env-secret]",
		"db:\n  password: my secret value\n  user: root": "db:\n  password: [REDACTED: env-secret]\n  user: root",
	} {
		if got, _ := sc.Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
	// Code, and keys with one word, are not this layer's.
	for _, in := range []string{
		`if token == "" {`, `token := x`, `const maxTokens = 4096`, `--max-tokens 1024`,
		`token = get_token(a, b)`, `token = "a b c"`, `password: >`, `if password != other {`,
		`func (s *S) token() string {`, `token == a b c`,
	} {
		got, _ := sc.Scrub(in)
		if strings.Contains(got, "env-secret") {
			t.Errorf("Scrub(%q) = %q, took code", in, got)
		}
	}
}

func TestANonASCIINeighbourIsNotTakenWithTheSecret(t *testing.T) {
	sc := NewScrubber()
	for in, want := range map[string]string{
		"\u00e9\u00e9gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz\u00e9\u00e9": "\u00e9\u00e9[REDACTED: github-token]\u00e9\u00e9",
		"\u65e5\u672cgh" + "p_0123456789abcdefghijklmnopqrstuvwxyz\u672c":       "\u65e5\u672c[REDACTED: github-token]\u672c",
		"\u00e9\u00e9Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu7Hn\u00e9\u00e9":           "\u00e9\u00e9[REDACTED: high-entropy]\u00e9\u00e9",
	} {
		if got, _ := sc.Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoIdentifiersWithDigitsInThemStay(t *testing.T) {
	sc := NewScrubber()
	for _, in := range []string{
		"calls skipUint8LengthPrefixed twice", "see crypto/x509.ParsePKCS8PrivateKey for it",
		"uses parseBase64EncodedPrivateKey2 here", "x509.MarshalPKCS8PrivateKey(key)",
	} {
		if got, _ := sc.Scrub(in); got != in {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
	}
	// A random token is still not an identifier.
	if got, _ := sc.Scrub("tok Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu end"); strings.Contains(got, "Zq8X") {
		t.Errorf("Scrub = %q", got)
	}
}

// Keys that only contain a secret word, and prose and code with a name in it, are
// not values to take.
func TestKeysThatOnlyContainASecretWordAreKept(t *testing.T) {
	sc := NewScrubber()
	for _, in := range []string{
		"privateKeySize = seedSize + publicKeySize",
		"masterSecretLength   = 48 // Length of a master secret in TLS 1.1.",
		"token          = 1*<any CHAR except CTLs or separators>",
		"tokenCount = a number of things",
		"passwordPolicy: at least eight characters",
	} {
		// record's own pattern may still take the first word after the = (that is
		// documented); the line is not taken to its end.
		if got, _ := sc.Scrub(in); strings.Contains(got, "env-secret") {
			t.Errorf("Scrub(%q) = %q, took the line", in, got)
		}
	}
	for in, want := range map[string]string{
		"dbPassword = my secret value": "dbPassword = [REDACTED: env-secret]",
		"csrf_token: two words here":   "csrf_token: [REDACTED: env-secret]",
	} {
		if got, _ := sc.Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}
