package record

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// patternFindOthers is findOthers as it was written with patterns alone. The
// code scans by hand now (see findOthers); on text that uses none of the forms
// added since, the two must report the same spans.
func patternFindOthers(s string) []Span {
	if s == "" {
		return nil
	}
	var out []Span
	for _, m := range pemRe.FindAllStringIndex(s, -1) {
		out = append(out, Span{m[0], m[1], "private-key"})
	}
	for _, m := range bearerRe.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, Span{m[5], m[1], "bearer-token"})
	}
	for _, m := range urlCredRe.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, Span{m[3] + 1, m[1] - 1, "url-credential"})
	}
	for _, m := range assignRe.FindAllStringSubmatchIndex(s, -1) {
		name, value := strings.ToLower(strings.Trim(s[m[2]:m[3]], " \t'\":=")), s[m[4]:m[5]]
		if strings.Contains(value, Redacted) {
			continue
		}
		if (name == "authorization" || name == "proxy-authorization") && (strings.EqualFold(value, "bearer") || strings.EqualFold(value, "basic")) {
			continue
		}
		out = append(out, Span{m[4], m[5], "secret-value"})
	}
	return merge(out)
}

func TestHandScanFindsWhatThePatternsFound(t *testing.T) {
	names := []string{"password", "API_KEY", "token", "secret", "client_secret", "Authorization", "Proxy-Authorization",
		"hostname", "X-Api-Key", "pwd", "db.password", "monkey", "keyboard", "auth_token", "PRIVATE-KEY", "credential", "authority"}
	seps := []string{"=", ": ", " = ", ":", `":`, `": `, `'=`, " :\n  ", "=\t"}
	values := []string{"abc", "hunter2", `"a b c"`, `'x y'`, `""`, "Bearer", "basic", `"say \"hi\" now"`, `"a\\"`,
		"abcdefghijklmnop", "v,w", "v;w", "v&w", "(v)", "{v}", "[v]", "é", "日本語"}
	fillers := []string{"hello", "go test ./...", "line one", "see: this", "x=1", "a.b.c", "Bearer abcdefghijkl1234", "Basic dXNlcjpwYXNzd29yZA==",
		"postgres://user:pw@host/db", "https://example.com/a?b=c", `"`, `'`, "---", "é", "KEY", "token"}
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"
	pemOpen := "-----BEGIN PRIVATE KEY-----\nMIIabc"
	r := rand.New(rand.NewSource(11))
	for n := 0; n < 20000; n++ {
		var b strings.Builder
		for i, parts := 0, 1+r.Intn(6); i < parts; i++ {
			switch r.Intn(10) {
			case 0, 1, 2, 3:
				v, sep := values[r.Intn(len(values))], seps[r.Intn(len(seps))]
				scheme := strings.EqualFold(v, "bearer") || strings.EqualFold(v, "basic")
				if scheme && strings.Contains(sep, "\n") {
					// The old pattern did not take "name :\n Bearer" for a header.
					sep = "="
				}
				b.WriteString(names[r.Intn(len(names))] + sep + v)
				if scheme {
					// A credential after a scheme word is redacted now however short.
					b.WriteString("\n")
				}
			case 4:
				b.WriteString(pem)
			case 5:
				if r.Intn(4) == 0 {
					b.WriteString(pemOpen)
				}
			default:
				b.WriteString(fillers[r.Intn(len(fillers))])
			}
			b.WriteString([]string{" ", "\n", "  ", " \n"}[r.Intn(4)])
		}
		s := b.String()
		got, want := findOthers(s, false), patternFindOthers(s)
		if len(got) != len(want) {
			t.Fatalf("%q\n got %+v\nwant %+v", s, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%q\n got %+v\nwant %+v", s, got, want)
			}
		}
	}
}

func TestMoreFormsOfASecretAreRedacted(t *testing.T) {
	for in, want := range map[string]string{
		// A quoted value with no closing quote goes to the end of its line.
		"password=\"hunter2 and more\nnext line": "password=[redacted]\nnext line",
		"token = 'abc def":                       "token = [redacted]",
		`{"api_key": "abc`:                       `{"api_key": [redacted]`,
		// A quoted value may run over lines.
		"secret=\"line one\nline two\" after": "secret=[redacted] after",
		"password='a\n\nb' tail":              "password=[redacted] tail",
		// More names.
		"passphrase=correct horse": "passphrase=[redacted] horse",
		"DB_PASS=hunter2":          "DB_PASS=[redacted]",
		"pass: hunter2":            "pass: hunter2",
		`{"pass": "hunter2"}`:      `{"pass": "hunter2"}`,
		"export MY.PASS=hunter2":   "export MY.PASS=[redacted]",
		// Command lines.
		"app --password hunter2 --verbose":                 "app --password [redacted] --verbose",
		"app --api-key 'two words' run":                    "app --api-key [redacted] run",
		"app --token\tabc123":                              "app --token\t[redacted]",
		"curl -u alice:hunter2 https://example.com":        "curl -u alice:[redacted] https://example.com",
		"curl -s --user=alice:hunter2 https://example.com": "curl -s --user=alice:[redacted] https://example.com",
		`curl -u "admin:pw 1" https://example.com`:         `curl -u "admin:[redacted]" https://example.com`,
		"mysql -u root -phunter2 shop":                     "mysql -u root -p[redacted] shop",
		"mysqldump -p'two words' db":                       "mysqldump -p[redacted] db",
		"sshpass -p hunter2 ssh host":                      "sshpass -p [redacted] ssh host",
		"sshpass -phunter2 ssh host":                       "sshpass -p[redacted] ssh host",
		// Authorization schemes.
		"Authorization: Token abc123":                  "Authorization: Token [redacted]",
		"Authorization: ApiKey k":                      "Authorization: ApiKey [redacted]",
		"authorization = Basic dXNlcjpwYXNz":           "authorization = Basic [redacted]",
		"Proxy-Authorization: Bearer abcdefghijkl1234": "Proxy-Authorization: Bearer [redacted]",
		"Authorization: SSWS 00abc":                    "Authorization: SSWS [redacted]",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestOrdinaryCommandsAndWordsAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		"mkdir -p build/out",
		"cp -p a b",
		"ssh -p 2222 host",
		"mysql -p",
		"mysql -u root -p shop",
		"docker run --user 1000:1000 img",
		"curl -u alice https://example.com",
		"curl -s https://example.com -o out",
		"bypass=true compass=north passed=yes",
		"the pass was steep",
		"password",
		"--password",
		"app --password",
		"app --password --verbose",
		"keyboard: qwerty",
		"the token verification worked",
	} {
		if got := Redact(in); got != in {
			t.Errorf("Redact(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestKeyIsSecretTakesPassAsAWholeWordOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"pass": false, "DB_PASS": true, "db.pass": true, "my-pass-x": false, "passphrase": true, "PassPhrase": true,
		"bypass": false, "compass": false, "passed": false, "passenger": false, "hostname": false,
	} {
		if KeyIsSecret(name) != want {
			t.Errorf("KeyIsSecret(%q) = %v, want %v", name, !want, want)
		}
	}
}

func TestRedactIsFixedByItself(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	frags := []string{"-----END PRIVATE KEY-----", "-----BEGIN PRIVATE KEY-----", "--password", "--token ", "msg: it's a password: don't tell", "pass: ", "[redacted]", "password=", "token: ", `"secret":"`, "--password ", "Authorization: Token ", `"`, `'`, "abc", " ", "\n", "x y", "=", "mysql -p", "curl -u a:b "}
	for n := 0; n < 40000; n++ {
		var b strings.Builder
		for i, k := 0, 1+r.Intn(8); i < k; i++ {
			b.WriteString(frags[r.Intn(len(frags))])
		}
		s := b.String()
		once := Redact(s)
		if twice := Redact(once); twice != once {
			t.Fatalf("Redact(%q) = %q, and again %q", s, once, twice)
		}
	}
}

// The scan is linear: text built to make each step do the most work takes about as
// long per byte as text that does not.
func TestRedactDoesNotSlowDownOnHostileText(t *testing.T) {
	const size = 256 << 10
	for name, unit := range map[string]string{
		"openers":       `password="`,
		"escaped":       `password="\"`,
		"stems":         "key",
		"names":         "token_",
		"flags":         "--password ",
		"bearer":        "Bearer ",
		"pem":           "-----BEGIN PRIVATE KEY-----",
		"pem-end":       "-----BEGIN PRIVATE KEY----- -----END PRIVATE KEY-----",
		"mysql":         "mysql -x ",
		"curl":          "curl -u a:",
		"urls":          "a://b:",
		"assign-equals": "a=",
	} {
		s := strings.Repeat(unit, size/len(unit))
		start := time.Now()
		Redact(s)
		d := time.Since(start)
		t.Logf("%s: %d bytes in %v", name, len(s), d)
		if d > 5*time.Second {
			t.Errorf("%s: %d bytes took %v", name, len(s), d)
		}
	}
}

// Find is for callers that scrub text by rules of their own and use its spans
// among them; it keeps to what it found before Redact took the forms above.
func TestFindKeepsToTheOlderForms(t *testing.T) {
	for _, in := range []string{
		"password=\"unclosed\nnext",
		"DB_PASS=hunter2",
		"passphrase=hunter2",
		"app --password hunter2",
		"curl -u alice:hunter2 https://example.com",
		"mysql -phunter2 shop",
		"sshpass -p hunter2 ssh host",
	} {
		if got := Find(in); len(got) != 0 {
			t.Errorf("Find(%q) = %+v", in, got)
		}
		if Redact(in) == in {
			t.Errorf("Redact(%q) left it as it was", in)
		}
	}
}

// A quoted value is looked for its closing quote over maxQuotedValue bytes. Past
// that it is taken to the end of its line, so the line's text after the quote goes
// with it and the next line stays.
func TestQuotedValueBeyondTheWindowIsCutAtTheEndOfItsLine(t *testing.T) {
	in := `password="` + strings.Repeat("x", maxQuotedValue+10) + `" more` + "\nnext"
	if got := Redact(in); got != "password=[redacted]\nnext" {
		t.Errorf("Redact = %q", got)
	}
}

// Once the looks for closing quotes have read as much as they may, a quoted value
// is cut at the end of its line without another look.
func TestQuotedValueLooksHaveABudget(t *testing.T) {
	s := "a=\"xx\" tail\nnext"
	sc := &scan{s: s, wide: true, budget: 0, lineEnd: -1}
	if got := sc.quotedEnd(2); got != 11 {
		t.Errorf("quotedEnd with no budget = %d, want the line end 11", got)
	}
	sc = &scan{s: s, wide: true, budget: 100, lineEnd: -1}
	if got := sc.quotedEnd(2); got != 6 {
		t.Errorf("quotedEnd with a budget = %d, want the closing quote 6", got)
	}
	if sc.budget >= 100 {
		t.Error("the look did not use the budget")
	}
}

// adversarial are texts built to make a scan do the most work, by the unit that is
// repeated.
var adversarial = map[string]string{
	"token=": "token=", "pass:": "pass:", "password=a": "password=a", "a_secret=x": "a_secret=x",
	"secret=:": "secret=:", "token=a.": "token=a.", "password=tab": "password=\t", "DB_PASS=": "DB_PASS=",
	"openers": `password="`, "escaped": `password="\"`, "apostrophes": `token='`, "stems": "key",
	"names": "token_", "flags": "--password ", "flags-dash": "--token --", "bearer": "Bearer ", "authorization": "Authorization: Token ",
	"pem": "-----BEGIN PRIVATE KEY-----", "pem-end": "-----BEGIN PRIVATE KEY----- -----END PRIVATE KEY-----", "pem-orphan": "-----END PRIVATE KEY-----",
	"mysql": "mysql -x ", "mysql-p": "mysql -p'", "curl": "curl -u a:", "curl-q": `curl -u "a:`, "sshpass": "sshpass -p ", "urls": "a://b:",
	"assign-equals": "a=", "colons": "token:", "quote-pairs": `token="a"`,
}

// Doubling a text doubles the time to read it, within the noise of a clock.
func TestRedactTimeGrowsLinearlyOnEveryAdversarialText(t *testing.T) {
	best := func(s string) time.Duration {
		d := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			Redact(s)
			d = min(d, time.Since(start))
		}
		return d
	}
	for name, unit := range adversarial {
		var prev time.Duration
		for _, size := range []int{64 << 10, 128 << 10, 256 << 10} {
			s := strings.Repeat(unit, size/len(unit))
			d := best(s)
			t.Logf("%-14s %6d bytes %v", name, len(s), d)
			if prev > 5*time.Millisecond && d > 8*prev {
				t.Errorf("%s: %d bytes took %v, the half took %v", name, len(s), d, prev)
			}
			if d > slow*750*time.Millisecond {
				t.Errorf("%s: %d bytes took %v", name, len(s), d)
			}
			prev = d
		}
	}
}

func TestRedactStopsReadingAtTheLimit(t *testing.T) {
	s := strings.Repeat("a", MaxRedactBytes) + "tail text"
	got := Redact(s)
	if len(got) > MaxRedactBytes+len(Redacted) || !strings.HasSuffix(got, Redacted) || strings.Contains(got, "tail") {
		t.Errorf("Redact of %d bytes gave %d bytes ending %q", len(s), len(got), got[len(got)-12:])
	}
	if short := strings.Repeat("a", MaxRedactBytes); Redact(short) != short {
		t.Error("a text of exactly the limit was changed")
	}
}

// Lines of ordinary output, code and prose that name pass, a flag with a secret
// word in it, or a word the patterns look at, and have no secret in them. None
// may change.
func TestOrdinaryLinesAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		"--- PASS: TestFoo (0.00s)",
		"=== RUN   TestFoo/pass_case",
		"    --- PASS: TestFoo/sub (0.00s)",
		"ok  \texample.com/pkg\t0.512s",
		"ok  pkg 0.5s pass: 12 fail: 0",
		"PASS",
		"pass: true",
		"tests pass: all",
		"Pass: 5 tests",
		"pass=3 fail=0",
		"the build did not pass: see logs",
		"pass := check(x)",
		"if pass { return }",
		"func pass(a, b int) bool {",
		"bypass=true compass=north passed=yes",
		"docker login --password-stdin < file",
		"docker login -u user --password-stdin",
		"x --no-password foo",
		"x --pass foo",
		"mycmd --password < file",
		"mycmd --token | tee out",
		"mycmd --secret > out",
		`"vault_pass": true`,
		"db_pass=0",
		`printf 'Authorization: Bearer %s\n' "$TOKEN"`,
		"Authorization: Bearer ${TOKEN}",
		"runs; --token is still taken, for the settings",
		"x --passthrough foo",
		"tool --token-file /etc/tool/token",
		"tool --secret-name foo",
		"flockdeck run --max-tokens 1024",
		"go test -run TestPassword ./...",
		"make: Entering directory '/src/pass'",
		"git commit -m 'fix the password check'",
		"See the password section of the manual.",
		"## Passwords and tokens",
		"The api key is shown once.",
		"mysql --version",
		"mysql -u root -p",
		"curl -s https://example.com/api -o out.json",
		"ssh -p 2222 host",
		"Authorization header missing",
	} {
		if got := Redact(in); got != in {
			t.Errorf("Redact(%q) = %q", in, got)
		}
	}
}
