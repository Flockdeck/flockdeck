package baton

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// The placeholder list is short and exact: a real secret that happens to be wrapped
// in a bracket or begin with a dollar sign is still one.
func TestPlaceholdersAreExactAndASecretLookingOneIsNot(t *testing.T) {
	for _, in := range []string{
		"password=<your-token>", "token: <token>", "api_key=<api_key>", "Authorization: Bearer <token>",
		"Authorization: Bearer $TOKEN", "password=${DB_PASSWORD}", "password: {{ .Values.password }}",
		"password=%DB_PASSWORD%", "token=$API_TOKEN",
	} {
		untouched(t, in)
	}
	for in, leak := range map[string]string{
		"password=<Hunter2Hunter2>":            "Hunter2Hunter2",
		"Authorization: Bearer <Abc123def456>": "Abc123def456",
		"password=$UP3RS3CR3T":                 "UP3RS3CR3T",
		"password={{hunter2}}extra":            "hunter2",
		"password=%Sup3r%Secret":               "Sup3r",
		"password=$ECRET123abc":                "ECRET123abc",
		"password=${TOKEN}extra99":             "extra99",
	} {
		taken(t, in, []string{leak}, nil)
	}
}

func TestExportAndSetxDoNotTakeProse(t *testing.T) {
	untouched(t, "Then export password hashes to the CSV file")
	untouched(t, "we export secret santa lists every year")
	untouched(t, "You can setx token values later")
	taken(t, "cd app && export GITHUB_TOKEN ghp-words here", []string{"ghp-words here"}, []string{"export GITHUB_TOKEN"})
	taken(t, "export API_SECRET hunter2hunter2", []string{"hunter2hunter2"}, nil)
}

func TestMysqlSpacedWordThatLooksLikeASecretIsTaken(t *testing.T) {
	taken(t, "mysql -u root -p Hunter2Hunter2 mydb", []string{"Hunter2Hunter2"}, []string{"mydb"})
	taken(t, "mysqldump -u root -p SuperSecret99 app", []string{"SuperSecret99"}, []string{"app"})
	taken(t, "mysql -u root -p mydb", nil, []string{"mydb"})
	taken(t, "mysql -p production_db", nil, []string{"production_db"})
}

func TestCurlUserKeepsItsClosingQuote(t *testing.T) {
	got, _ := NewScrubber().Scrub(`curl --user "me:p@ss w0rd" https://x`)
	if got != `curl --user "me:[REDACTED: db-password]" https://x` {
		t.Errorf("got %q", got)
	}
	got, _ = NewScrubber().Scrub(`curl -u 'me:p@ss w0rd' https://x`)
	if got != `curl -u 'me:[REDACTED: db-password]' https://x` {
		t.Errorf("got %q", got)
	}
}

func TestOddSpacesDoNotMakeScrubbingTwiceDifferent(t *testing.T) {
	sc := NewScrubber()
	for _, in := range []string{
		"Authorization: Bearer abc123def456 keepme here",
		"run --token abc123def456　keepme",
		"password=hunter2hunter2 word",
		"Authorization: Bearer abc123def456　keepme",
		"x [REDACTED: secret-value and more",
		"x [REDACTED: secret-value more",
		"cat ~/.ssh/id_rsagh" + "p_0123456789abcdefghijklmnopqrstuvwxyz",
		"id_rsa[REDACTED: aws-key]",
	} {
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if once != twice {
			t.Errorf("Scrub(%q): once %q, twice %q", in, once, twice)
		}
	}
	for _, in := range []string{
		"Authorization: Bearer abc123def456 keepme here",
		"run --token abc123def456　keepme",
	} {
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if !strings.Contains(once, "keepme") || !strings.Contains(twice, "keepme") {
			t.Errorf("Scrub(%q): once %q, twice %q: the word next to the odd space was taken", in, once, twice)
		}
	}
}

// The baton's own flow scrubs the same text twice, once when it is built and once
// on what is sent: the second must change nothing.
func TestTheBatonFlowScrubsTwiceWithTheSameResult(t *testing.T) {
	sc := NewScrubber()
	b := Baton{Title: "t", Sections: map[Section]string{
		Goal:     "token=abc123def456 keepme and Authorization: Bearer zz9yy8xx7ww6　word",
		Standing: "see id_rsagh" + "p_0123456789abcdefghijklmnopqrstuvwxyz and [REDACTED: x",
	}}
	once := sc.ScrubBaton(b)
	twice := sc.ScrubBaton(once)
	if Render(once) != Render(twice) {
		t.Errorf("the second scrub changed the baton:\n%s\n---\n%s", Render(once), Render(twice))
	}
}

// Fragments that are hostile to the marks, the odd spaces and the key paths.
var hostileFragments = []string{
	"password", "=", ":", " ", "\n", "token", "AK" + "IAIOSFODNN7EXAMPLE", "abc123", "word", "-", "_", "+",
	"[REDACTED: secret-value]", "[REDACTED: ", "[REDACTED: high-entropy]", "]", "Bearer ", "Authorization: ",
	"secret", "x9Zq7Lm2Vb8Nc4Rt6Yh1Jk3", "/", ".", "mysql -p ", "\"", "'", "{", "}", "export ", "A", "b", "1",
	" ", "　", "​", " ", "id_rsa", ".pub", "gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz",
	"Cookie: ", "sid=", ";", "$", "${", "<", ">", "%", "curl -u ", "me:pw", "htpasswd -b f u ", "setx ", "- ",
}

func TestScrubIsIdempotentOnHostileFragments(t *testing.T) {
	sc := NewScrubber()
	rng := rand.New(rand.NewSource(11))
	end := time.Now().Add(4 * time.Second)
	for i := 0; i < 200000 && time.Now().Before(end); i++ {
		var b strings.Builder
		for n := rng.Intn(14) + 1; n > 0; n-- {
			b.WriteString(hostileFragments[rng.Intn(len(hostileFragments))])
		}
		in := b.String()
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if once != twice {
			t.Fatalf("Scrub(%q): once %q, twice %q", in, once, twice)
		}
	}
}

func FuzzScrubIsIdempotentHostile(f *testing.F) {
	for _, s := range hostileFragments {
		f.Add(s + "abc123def456" + s)
	}
	f.Add("token=abc123def456 keepme")
	// The shapes of the differential gate, and the wrapped keys, are the seeds too: what
	// is mutated is what is really written.
	for i, c := range gateCases() {
		if i%40 == 0 {
			f.Add(c.line)
		}
	}
	for i, c := range wrapCases(60) {
		if i%4 == 0 {
			f.Add(c.text)
		}
	}
	sc := NewScrubber()
	f.Fuzz(func(t *testing.T, in string) {
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if once != twice {
			t.Fatalf("Scrub(%q): once %q, twice %q", in, once, twice)
		}
	})
}

func TestAssignmentsOfQuotedLiteralsUnderSecretNames(t *testing.T) {
	taken(t, `password := "hunter2hunter2"`, []string{"hunter2hunter2"}, []string{"password :="})
	taken(t, `const apiToken = "Abcd1234efgh5678"`, []string{"Abcd1234efgh5678"}, nil)
	taken(t, `let secret_key = 'Zx9Qw8Er7Ty6';`, []string{"Zx9Qw8Er7Ty6"}, nil)
	taken(t, `_auth = "dXNlcjpwYXNzd29yZDEyMw=="`, []string{"dXNlcjpwYXNzd29yZDEyMw"}, nil)
	taken(t, `//registry.example/:_authToken="Abcd1234efgh5678"`, []string{"Abcd1234efgh5678"}, nil)
	untouched(t, `password := "changeme"`)
	untouched(t, `password := ""`)
	untouched(t, `passwordField := "password-field-label"`)
}

func TestBareProviderShapes(t *testing.T) {
	taken(t, "key is sk"+"_live_4eC39HqLyjWDarjtT1zdp7dc ok", []string{"4eC39HqLyjWDarjtT1zdp7dc"}, nil)
	taken(t, "token sh"+"pat_0123456789abcdef0123456789abcdef here", []string{"0123456789abcdef0123456789abcdef"}, nil)
	taken(t, "dop"+"_v1_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", []string{"0123456789abcdef0123"}, nil)
}

func TestDockerLoginQuotedAttachedPassword(t *testing.T) {
	got, _ := NewScrubber().Scrub(`docker login -u me -p"a b c" registry.example.com`)
	if got != `docker login -u me -p[REDACTED: db-password] registry.example.com` {
		t.Errorf("got %q", got)
	}
}

func TestAProseStringUnderASecretNamedVariableIsNotASecret(t *testing.T) {
	untouched(t, `secret := "fix the payroll export for ACME"`)
	taken(t, `secret := "correct horse 42 battery"`, []string{"correct horse 42"}, nil)
}
