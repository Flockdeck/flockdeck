package baton

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// The second gate. The first (gate_test.go, generator 1) is kept as it is. This one has
// the shapes that came after it: names that end in pass, pwd, pw and key, a lower case
// name for setx and export, a password in an ERB tag or as the default of a call, a
// mask that is not one, and more command-line tools. Its golden file is the output of the
// scrubber when the file was written, with each line that is not in it looked at by hand:
// each of those lines should lose its secret, and the file keeps it that way.
//
// What is in it. The generator makes 14400 lines: 12000 of the shapes above, then 2400 of
// secrets that are numbers, masks that are not masks (x of any length, four to seven
// stars), and numbers under names that end as a time (gateCases2Extra). The golden bits
// name the lines that lose their secret, and the header says how many that is and what the
// others are. Those that do not are left out on purpose, so that the gate does not insist
// on them: values shorter than 12 characters under a name that ends in key, values shorter
// than 8 under one that ends in pass, pwd or pw, and values that are plain words (letters
// only, as in a name written in camel case) under a name that ends in pass (they read as
// identifiers). A line may leave the list only on purpose, with the reason in the commit.
//
// Regenerate it with
//
//	GATE_WRITE2=testdata/gate2_golden.bits go test -run TestGate2WriteGolden ./internal/baton

const gate2GeneratorVersion = 2

var gateKeys2 = []string{
	"db_pass", "DB_PASS", "userpass", "user_pass", "signing_key", "SIGNING_KEY", "ssh_key", "encryption_key", "master_key", "deploy_key",
	"service_key", "webhook_key", "license_key", "app_pw", "DBPW", "redis_pwd", "mysql_pwd", "ldap_pass", "smtp_pass", "admin_pass",
	"root_pass", "backup_passwd", "jwt_key", "hmac_key", "cookie_key", "session_key", "vault_key", "registry_pass", "ftp_pass", "imap_pw",
	"pg_pass", "mongo_pwd", "kafka_key", "stripe_key", "sendgrid_key", "datadog_key", "sentry_key", "rails_master_key", "aes_key", "crypt_key",
}

func gateCases2() []gateCase {
	r := rand.New(rand.NewSource(9))
	out := make([]gateCase, 0, 12000)
	for len(out) < 12000 {
		k := gateKeys2[r.Intn(len(gateKeys2))]
		k1 := gateKeys[r.Intn(len(gateKeys))]
		u := gateUsers[r.Intn(len(gateUsers))]
		var line, v string
		switch r.Intn(26) {
		case 0:
			v = gateValue(r, true)
			line = k + "=" + v
		case 1:
			v = gateValue(r, true)
			line = k + ": " + v
		case 2:
			v = gateValue(r, true)
			line = `{"` + k + `": "` + v + `"}`
		case 3:
			v = gateValue(r, true)
			line = "export " + strings.ToUpper(k) + "=" + v
		case 4:
			v = gateValue(r, true)
			line = "setx " + strings.ToLower(k1) + " " + v
		case 5:
			v = gateValue(r, true)
			line = "setx " + strings.ToUpper(k1) + " " + v
		case 6:
			v = gateValue(r, true)
			line = k1 + ` = <%= "` + v + `" %>`
		case 7:
			v = gateValue(r, true)
			line = k1 + ` = ENV.fetch("APP_SECRET", "` + v + `")`
		case 8:
			v = "xxxxxxxx"
			line = k1 + "=" + v
		case 9:
			v = gateValue(r, true)
			line = "vault login " + v
		case 10:
			v = gateValue(r, true)
			line = "az login -u " + u + " -p " + v
		case 11:
			v = gateValue(r, true)
			line = "doctl auth init -t " + v
		case 12:
			v = gateValue(r, true)
			line = "unzip -P " + v + " backup.zip"
		case 13:
			v = gateValue(r, true)
			line = "gpg --batch --passphrase " + v + " -d data.gpg"
		case 14:
			v = gateValue(r, true)
			line = "7z a -p" + v + " out.7z src"
		case 15:
			v = gateValue(r, true)
			line = "aws configure set aws_secret_access_key " + v
		case 16:
			v = gateValue(r, true)
			line = "http -a " + u + ":" + v + " GET api.example.com/v1"
		case 17:
			v = gateValue(r, true)
			line = "echo " + v + " | docker login -u " + u + " --password-stdin registry.example.com"
		case 18:
			v = gateValue(r, true)
			line = "docker login -u " + u + " --password-stdin registry.example.com <<< " + v
		case 19:
			v = gateValue(r, true)
			line = "https://api.example.com/v1/items?" + k + "=" + v + "&page=2"
		case 20:
			v = gateValue(r, true)
			line = "  " + k + ": '" + v + "'"
		case 21:
			v = gateValue(r, true)
			line = "az login --service-principal --password=" + v
		case 22:
			v = gateValue(r, true)
			line = "gpg --passphrase='" + v + "' -c file.txt"
		case 23:
			v = gateValue(r, true)
			line = "zip -P " + v + " -r a.zip dir"
		case 24:
			v = gateValue(r, true)
			line = "printf '%s' " + v + " | podman login --password-stdin -u " + u
		default:
			v = gateValue(r, true)
			line = k1 + "=" + v + "&mode=test"
		}
		out = append(out, gateCase{line, v})
	}
	return append(out, gateCases2Extra()...)
}

// readGolden reads a golden bits file and checks the hash of the generator's lines.
func readGolden(t *testing.T, path string, cases []gateCase) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "#") || !strings.HasPrefix(lines[1], "sha256 ") {
		t.Fatalf("%s is not what the gate writes: %d lines", path, len(lines))
	}
	bits, err := base64.StdEncoding.DecodeString(lines[2])
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimPrefix(lines[1], "sha256 "); gateHash(cases) != want {
		t.Fatalf("the generator no longer makes the lines %s was made for (hash %s, want %s): a changed generator has to make the bits again", path, gateHash(cases), want)
	}
	if len(bits) != (len(cases)+7)/8 {
		t.Fatalf("%s is for %d lines, the generator makes %d", path, len(bits)*8, len(cases))
	}
	return bits
}

func TestGate2WriteGolden(t *testing.T) {
	path := os.Getenv("GATE_WRITE2")
	if path == "" {
		t.Skip("GATE_WRITE2 is not set")
	}
	sc := NewScrubber()
	cases := gateCases2()
	bits := make([]byte, (len(cases)+7)/8)
	n := 0
	var kept []string
	shortKey, shortPass, other := 0, 0, 0
	for i, c := range cases {
		if got, _ := sc.Scrub(c.line); !strings.Contains(got, c.secret) {
			bits[i/8] |= 1 << (i % 8)
			n++
			continue
		}
		low := strings.ToLower(c.line)
		switch {
		case strings.Contains(low, "key") && len(c.secret) < 12:
			shortKey++
		case (strings.Contains(low, "pass") || strings.Contains(low, "pw") || strings.Contains(low, "pwd")) && len(c.secret) < 8:
			shortPass++
		default:
			other++
			kept = append(kept, c.line)
		}
	}
	out := fmt.Sprintf("# seed=9+11 count=%d redacted=%d generator=%d redacted-by-the-scrubber-when-written; not redacted by design: %d short values under key names, %d short values under pass names, %d others (words under pass names, and the like)\nsha256 %s\n%s\n",
		len(cases), n, gate2GeneratorVersion, shortKey, shortPass, other, gateHash(cases), base64.StdEncoding.EncodeToString(bits))
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d of %d lines lose their secret; %d short under key names, %d short under pass names, %d others, some of which: %q", n, len(cases), shortKey, shortPass, other, kept[:min(len(kept), 12)])
}

func TestDifferentialGate2(t *testing.T) {
	start := time.Now()
	cases := gateCases2()
	bits := readGolden(t, "testdata/gate2_golden.bits", cases)
	sc := NewScrubber()
	leaks, total := 0, 0
	for i, c := range cases {
		if bits[i/8]&(1<<(i%8)) == 0 {
			continue
		}
		total++
		if got, _ := sc.Scrub(c.line); strings.Contains(got, c.secret) {
			leaks++
			if leaks <= 15 {
				t.Errorf("leak: %q -> %q", c.line, got)
			}
		}
	}
	// The file has to be of lines that matter: nearly all of what the generator makes.
	if total < len(cases)*85/100 {
		t.Fatalf("the golden bits name only %d of %d lines", total, len(cases))
	}
	if leaks > 0 {
		t.Errorf("%d of %d lines that were scrubbed before now leak", leaks, total)
	}
	// The time limit is for a build without the race detector, which slows this several times.
	if d := time.Since(start); !slowRun() && d > 5*time.Second {
		t.Errorf("the gate took %s", d)
	}
}

// gateSample is every step-th golden line of both gates, for the mutation test.
func gateSample(t *testing.T, step int) []gateCase {
	t.Helper()
	var out []gateCase
	for _, g := range []struct {
		path  string
		cases []gateCase
	}{{"testdata/gate_golden.bits", gateCases()}, {"testdata/gate2_golden.bits", gateCases2()}} {
		bits := readGolden(t, g.path, g.cases)
		n := 0
		for i, c := range g.cases {
			if bits[i/8]&(1<<(i%8)) == 0 {
				continue
			}
			if n++; n%step == 0 {
				out = append(out, c)
			}
		}
	}
	return out
}

// mutationRules are the rules that the gate has to notice when one is switched off: each
// takes some of the gate's lines with nothing else taking them. Not here, because the
// gate does not show them (another rule takes the same lines, as record and the shapes
// overlap on purpose, or the gate has no line of that shape): kv, form, envline, json,
// provider, barecred, golit, k8s, wrapped, wrappedruns and keypath. Their own tests are
// what covers them.
var mutationRules = []string{
	"urlcred", "dbcmd", "passname", "call", "export", "cookie", "auth", "record", "flag", "literal", "entropy",
}

// valueMutations change a value that decides what is a secret and not, in the way a careless
// edit would: a mask rule that takes more than eight stars, a time rule applied to every name,
// a secret word that lets a time through, a time shape that is widened. Each applies the
// change and returns what puts it back. The widening of epochRe is not here: under a secret
// name epochShape is the stricter test, and under any other the numbers are not secrets, so
// no line of the gate can tell it from the original.
var valueMutations = []struct {
	name  string
	apply func() (restore func())
}{
	{"a mask of four stars or six x", func() func() {
		old := maskRe
		maskRe = regexp.MustCompile(`^(?:\*{4,}|[xX]{6,}|•{4,}|#{6,})$`)
		return func() { maskRe = old }
	}},
	{"the time rule for every name", func() func() {
		old := secretNameWords
		secretNameWords = map[string]bool{}
		return func() { secretNameWords = old }
	}},
	{"password lets a time through", func() func() {
		epochSecretWords["password"] = epochShape
		return func() { delete(epochSecretWords, "password") }
	}},
	{"a token's time of any ten digits", func() func() {
		old := epochSecretWords["token"]
		epochSecretWords["token"] = regexp.MustCompile(`^[0-9]{9,13}$`)
		return func() { epochSecretWords["token"] = old }
	}},
}

// Switching a rule off has to make the gate fail: the gate is only worth having if it
// notices that a rule has gone. The rules are switched off through skipRules, one at a
// time, on a sample of the golden lines.
func TestTheGateNoticesARuleThatIsSwitchedOff(t *testing.T) {
	sample := gateSample(t, 15)
	sc := NewScrubber()
	leaksWith := func() int {
		n := 0
		for _, c := range sample {
			if got, _ := sc.Scrub(c.line); strings.Contains(got, c.secret) {
				n++
			}
		}
		return n
	}
	if n := leaksWith(); n != 0 {
		t.Fatalf("%d lines of the sample leak with every rule on", n)
	}
	var report []string
	for _, name := range mutationRules {
		skipRules[name] = true
		n := leaksWith()
		delete(skipRules, name)
		report = append(report, fmt.Sprintf("%s=%d", name, n))
		if n == 0 {
			t.Errorf("switching %q off did not make the gate fail: nothing in it needs that rule on its own", name)
		}
	}
	for _, m := range valueMutations {
		restore := m.apply()
		n := leaksWith()
		restore()
		report = append(report, fmt.Sprintf("%q=%d", m.name, n))
		if n == 0 {
			t.Errorf("the change %q did not make the gate fail", m.name)
		}
	}
	sort.Strings(report)
	t.Logf("leaks of %d sampled lines with one rule off or one value rule changed: %s", len(sample), strings.Join(report, " "))
}
