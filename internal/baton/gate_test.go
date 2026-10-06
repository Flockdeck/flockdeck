package baton

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"
)

// The differential gate. A scrubber that is made to leave fewer false positives
// must not start to leak: for 20,000 generated lines of key, value and format
// combinations, the ones whose secret an earlier scrubber (db1c2f1f) removed are
// marked in testdata/gate_golden.bits (one bit for each generated line), and every one
// of them must still lose its secret. The lines are generated again from the seed each
// time, the secret of each is known, and a hash of what the generator makes is kept
// beside the bits.
//
// Regenerate it, from the scrubber that is to be the floor, with
//
//	GATE_WRITE=testdata/gate_golden.bits go test -run TestGateWriteGolden ./internal/baton
//
// It is only ever widened: a line may leave the list when a change makes the
// scrubber leak less on purpose, and that is a change to review, not to wave through.

type gateCase struct{ line, secret string }

var gateKeys = []string{
	"password", "PASSWORD", "DB_PASSWORD", "passwd", "secret", "client_secret", "SECRET_KEY", "token", "API_TOKEN",
	"GITLAB_TOKEN", "api_key", "apiKey", "access_token", "auth_token", "private_key", "github_token", "pwd", "SLACK_TOKEN",
}

var gateUsers = []string{"x-access-token", "gitlab-ci-token", "deploy-token", "x-token-auth", "oauth2", "bob", "ci", "api-token", "svc_secret"}

const gateAlpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func gatePick(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

// gateValue makes a value of one of several kinds. A value for a URL or a form
// field must not hold what ends it, so slash, colon and at sign are left out there.
func gateValue(r *rand.Rand, plain bool) string {
	var v string
	switch r.Intn(9) {
	case 0: // base64 with slashes
		v = gatePick(r, gateAlpha, 6+r.Intn(8)) + "/" + gatePick(r, gateAlpha+"-_", 4+r.Intn(8)) + "/" + gatePick(r, gateAlpha, 3+r.Intn(6))
	case 1: // hex
		v = gatePick(r, "0123456789abcdef", []int{32, 40, 64}[r.Intn(3)])
	case 2: // provider tokens
		switch r.Intn(6) {
		case 0:
			v = "ghp_" + gatePick(r, gateAlpha, 36)
		case 1:
			v = "ghs_" + gatePick(r, gateAlpha, 36)
		case 2:
			v = "glpat-" + gatePick(r, gateAlpha, 20)
		case 3:
			v = "sk_live_" + gatePick(r, gateAlpha, 24)
		case 4:
			v = "xoxb-" + gatePick(r, "0123456789", 11) + "-" + gatePick(r, "0123456789", 12) + "-" + gatePick(r, gateAlpha, 24)
		default:
			v = "AKIA" + gatePick(r, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16)
		}
	case 3: // dots
		v = gatePick(r, gateAlpha, 5+r.Intn(6)) + "." + gatePick(r, gateAlpha, 5+r.Intn(6)) + "." + gatePick(r, gateAlpha, 6+r.Intn(8))
	case 4: // password-like
		v = []string{"hunter2", "Sup3rS3cret", "correct-horse-9", "P@ssw0rd", "Tr0ub4dor", "letmein99"}[r.Intn(6)] + gatePick(r, gateAlpha, 2+r.Intn(5))
	case 5: // with a slash and a word
		v = []string{"hunter2", "ab3", "Zk39dLq", "prodpw"}[r.Intn(4)] + "/" + gatePick(r, gateAlpha, 4+r.Intn(8))
	case 6: // colon and at sign inside
		v = gatePick(r, gateAlpha, 6+r.Intn(8)) + ":" + gatePick(r, gateAlpha, 4+r.Intn(6)) + "@" + gatePick(r, gateAlpha, 3+r.Intn(4))
	case 7: // ends in a colon
		v = gatePick(r, gateAlpha, 16+r.Intn(8)) + ":"
	default: // plain random
		v = gatePick(r, gateAlpha, 16+r.Intn(16))
	}
	if plain {
		v = strings.NewReplacer("/", "x", ":", "y", "@", "z").Replace(v)
	}
	return v
}

func gateCases() []gateCase {
	r := rand.New(rand.NewSource(8))
	out := make([]gateCase, 0, 20000)
	for len(out) < 20000 {
		k := gateKeys[r.Intn(len(gateKeys))]
		u := gateUsers[r.Intn(len(gateUsers))]
		host := []string{"github.com/o/r", "gitlab.example.com/g/p.git", "bitbucket.org/w/r.git", "db.internal:5432/app", "registry.example.com"}[r.Intn(5)]
		var line, v string
		switch r.Intn(20) {
		case 0:
			v = gateValue(r, false)
			line = k + "=" + v
		case 1:
			v = gateValue(r, false)
			line = "export " + k + "=" + v
		case 2:
			v = gateValue(r, false)
			line = `{"` + k + `": "` + v + `"}`
		case 3:
			v = gateValue(r, false)
			line = k + ": " + v
		case 4:
			v = gateValue(r, false)
			line = "  " + k + ": " + v
		case 5:
			v = gateValue(r, false)
			line = k + ` = "` + v + `"`
		case 6:
			v = gateValue(r, false)
			line = k + ` := "` + v + `"`
		case 7, 8, 9: // URL credentials, more often: the user is the point
			v = gateValue(r, true)
			line = []string{"https://", "http://", "postgres://", "git+https://"}[r.Intn(4)] + u + ":" + v + "@" + host
		case 10:
			v = gateValue(r, false)
			line = "curl -u " + u + ":" + v + " https://api.example.com/v1"
		case 11:
			v = gateValue(r, true)
			line = "curl -H 'Authorization: Bearer " + v + "' https://api.example.com/v1"
		case 12:
			v = gateValue(r, true)
			line = `curl -H "X-Api-Key: ` + v + `" https://api.example.com/v1`
		case 13:
			v = gateValue(r, true)
			line = "tool --" + []string{"password", "token", "api-key", "secret"}[r.Intn(4)] + " " + v + " --verbose"
		case 14:
			v = gateValue(r, true)
			line = "docker login -u " + u + " -p " + v + " registry.example.com"
		case 15:
			v = gateValue(r, true)
			line = "mysql -u root -p" + v + " mydb"
		case 16:
			v = gateValue(r, true)
			line = "sshpass -p " + v + " ssh deploy@host"
		case 17:
			v = gateValue(r, true)
			line = "Cookie: sid=" + v + "; theme=dark"
		case 18:
			v = gateValue(r, true)
			line = "redis-cli -a " + v + " ping"
		default:
			v = gateValue(r, false)
			line = "Authorization: Bearer " + v
		}
		out = append(out, gateCase{line, v})
	}
	return out
}

// gateGeneratorVersion is bumped when the generator is changed on purpose, which
// also means making the golden bits again.
const gateGeneratorVersion = 1

// gateHash is the sha256 of every generated line and its secret, so that a change to
// the generator cannot quietly change which lines the bits stand for.
func gateHash(cases []gateCase) string {
	h := sha256.New()
	for _, c := range cases {
		h.Write([]byte(c.line + "\x00" + c.secret + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestGateWriteGolden writes testdata/gate_golden.bits from the scrubber it is run
// with: a header line, the hash of the generator's output, and one bit for each
// generated line, set when the secret was removed. It does nothing unless GATE_WRITE
// names the file.
func TestGateWriteGolden(t *testing.T) {
	path := os.Getenv("GATE_WRITE")
	if path == "" {
		t.Skip("GATE_WRITE is not set")
	}
	sc := NewScrubber()
	cases := gateCases()
	bits := make([]byte, (len(cases)+7)/8)
	n := 0
	for i, c := range cases {
		if got, _ := sc.Scrub(c.line); !strings.Contains(got, c.secret) {
			bits[i/8] |= 1 << (i % 8)
			n++
		}
	}
	out := fmt.Sprintf("# seed=8 count=%d redacted=%d generator=%d\nsha256 %s\n%s\n",
		len(cases), n, gateGeneratorVersion, gateHash(cases), base64.StdEncoding.EncodeToString(bits))
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d of %d lines lose their secret", n, len(cases))
}

func TestDifferentialGate(t *testing.T) {
	start := time.Now()
	data, err := os.ReadFile("testdata/gate_golden.bits")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "#") || !strings.HasPrefix(lines[1], "sha256 ") {
		t.Fatalf("testdata/gate_golden.bits is not what the gate writes: %d lines", len(lines))
	}
	bits, err := base64.StdEncoding.DecodeString(lines[2])
	if err != nil {
		t.Fatal(err)
	}
	cases := gateCases()
	if want := strings.TrimPrefix(lines[1], "sha256 "); gateHash(cases) != want {
		t.Fatalf("the generator no longer makes the lines the golden bits were made for (hash %s, want %s): a changed generator has to make the bits again, from the scrubber that is the floor", gateHash(cases), want)
	}
	if len(bits) != (len(cases)+7)/8 {
		t.Fatalf("the bits are for %d lines, the generator makes %d", len(bits)*8, len(cases))
	}
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
				t.Errorf("leak: %q -> %q (the secret %q was removed before)", c.line, got, c.secret)
			}
		}
	}
	if total < 5000 {
		t.Fatalf("the golden bits name only %d lines", total)
	}
	if leaks > 0 {
		t.Errorf("%d of %d lines that were scrubbed before now leak", leaks, total)
	}
	// The time limit is for a build without the race detector, which slows this several times.
	if d := time.Since(start); !slowRun() && d > 5*time.Second {
		t.Errorf("the gate took %s", d)
	}
}
