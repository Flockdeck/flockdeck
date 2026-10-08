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
		"passphrase=correct horse":    "passphrase=[redacted] horse",
		"DB_PASS=hunter2":             "DB_PASS=[redacted]",
		"pass: hunter2":               "pass: [redacted]",
		`{"pass": "hunter2"}`:         `{"pass": [redacted]}`,
		"export MY.PASS.WORD=hunter2": "export MY.PASS.WORD=[redacted]",
		// Command lines.
		"app --password hunter2 --verbose":                 "app --password [redacted] --verbose",
		"app --api-key 'two words' run":                    "app --api-key [redacted] run",
		"app --token\tabc123":                              "app --token\t[redacted]",
		"curl -u alice:hunter2 https://example.com":        "curl -u alice:[redacted] https://example.com",
		"curl -s --user=alice:hunter2 https://example.com": "curl -s --user=alice:[redacted] https://example.com",
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
		"pass": true, "DB_PASS": true, "db.pass": true, "my-pass-x": true, "passphrase": true, "PassPhrase": true,
		"bypass": false, "compass": false, "passed": false, "passenger": false, "hostname": false,
	} {
		if KeyIsSecret(name) != want {
			t.Errorf("KeyIsSecret(%q) = %v, want %v", name, !want, want)
		}
	}
}

func TestRedactIsFixedByItself(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	frags := []string{"[redacted]", "password=", "token: ", `"secret":"`, "--password ", "Authorization: Token ", `"`, `'`, "abc", " ", "\n", "x y", "=", "mysql -p", "curl -u a:b "}
	for n := 0; n < 5000; n++ {
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
