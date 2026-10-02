package secretname

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/jmwri/flockdeck/internal/review"
)

const (
	longS    = "\u017f" // LATIN SMALL LETTER LONG S: folds to s
	kelvin   = "\u212a" // KELVIN SIGN: folds to k
	dotI     = "\u0130" // LATIN CAPITAL LETTER I WITH DOT ABOVE: strings.ToLower makes it i
	sharpS   = "\u00df" // folds to ss
	capSharp = "\u1e9e" // folds to ss
	ligFi    = "\ufb01" // folds to fi
)

// Threat: the Unicode case-folding bypass. A file system that ignores case
// (APFS by default, ext4 with casefold, vfat, NTFS, SMB mounts) resolves
// ".ſsh" to ".ssh", the Kelvin sign to k, a dotted capital I to i, "ß" to "ss".
// The name check must read each as the secret it opens.
func TestUnicodeCaseFoldingCannotHideASecret(t *testing.T) {
	secret := []string{
		"." + longS + "sh", longS + "ecrets", strings.ToUpper(longS) + "ECRETS", longS + "ECRETS",
		"terraform.tf" + longS + "tate", ".z" + longS + "h_history", "pa" + longS + longS + "word.txt",
		"pa" + sharpS + "word.txt", "pa" + capSharp + "word.txt", dotI + "d_rsa", "tls." + kelvin + "ey",
		"TLS." + kelvin + "EY", "my." + longS + "ecret.txt", "ke" + longS + "tore.jks", "to" + kelvin + "en.json",
		"api_" + kelvin + "ey.txt", "." + longS + "3cfg", "." + kelvin + "ube", "." + longS + "ecrets",
		"private_" + kelvin + "ey.asc", "." + longS + "ecret", "." + "env" + "." + longS + "ecret",
		"pri" + "vate", ligFi + "le.pem", "crede" + "ntial" + longS,
		"--file=." + longS + "sh", "x=" + longS + "ecrets",
	}
	for _, n := range secret {
		if !Component(n) {
			t.Errorf("Component(%q) = false, want secret", n)
		}
	}
	for _, p := range []string{
		"." + longS + "sh/key", longS + "ecrets/x", "a/" + longS + "ecrets/b/c.txt", `a\` + "." + longS + `sh\id`,
		".CONFIG/GCLOUD/x", ".config/" + "gcloud/x",
	} {
		if !Path(p) {
			t.Errorf("Path(%q) = false, want secret", p)
		}
	}
	// Ordinary names that merely contain such runes are not secrets.
	for _, n := range []string{"stra" + sharpS + "e.md", "caf\u00e9.md", "\u65e5\u672c\u8a9e.md", "ma" + longS + "t.txt", "notes" + kelvin + ".md"} {
		if Component(n) {
			t.Errorf("Component(%q) = true, want an ordinary name", n)
		}
	}
}

// Threat: argv forms. A name with an "=" in it is also judged by what is after
// it, so that this package is a superset of review.SecretPath even for the
// words of a command line (which should still not be run through it).
func TestEqualsForms(t *testing.T) {
	for _, n := range []string{
		"x=.env.local", "--file=.env.local", "-o=.env.local.bak", "=.env.pub", "a=b=id_rsa", "--key=server.pem",
		"--cfg=a/.env", "k=-id_rsa", "--token-file=credentials.json",
	} {
		if !Path(n) {
			t.Errorf("Path(%q) = false, want secret", n)
		}
	}
	for _, n := range []string{"a=b", "--flag=value", "x=id_rsa.pub", "=", "a=", "--out=report.html"} {
		if Path(n) {
			t.Errorf("Path(%q) = true, want an ordinary name", n)
		}
	}
}

// Parity: nothing internal/review.SecretPath calls secret may be let through
// here, for any input, argv forms with "=" or a leading "-" included. That
// function is the check recordings and auto-review use; the two must not drift
// apart, and this one must only ever be stricter.
func TestAtLeastAsStrictAsReview(t *testing.T) {
	bases := []string{
		".env", ".env.x", ".ENV.Local", "id_rsa", "id_dsa.old", "id_ed25519", "id_rsa.pub", ".npmrc", ".netrc", "_netrc", ".pgpass",
		".git-credentials", "x-credential-y", "a.pem", "a.key", "a.p12", "a.pfx", "a.ppk", "a.keystore", "a.jks", "A.PEM",
		"plain.txt", "", "-id_rsa", dotI + "d_rsa", "tls." + kelvin + "ey", ".env" + kelvin,
	}
	prefixes := []string{"", "dir/", `dir\`, "a/b/", "--file=", "x=", "-o=", "=", "--k=a/", "a=b=", "-", "--", "../", "/", `C:\`}
	suffixes := []string{"", "/", ".bak", "=", "#"}
	n := 0
	for _, p := range prefixes {
		for _, b := range bases {
			for _, s := range suffixes {
				name := p + b + s
				n++
				if review.SecretPath(name) && !Path(name) {
					t.Errorf("review calls %q secret and this does not", name)
				}
			}
		}
	}
	if n < 1000 {
		t.Fatalf("only %d names were compared", n)
	}
}

func FuzzAtLeastAsStrictAsReview(f *testing.F) {
	for _, s := range []string{
		".env", "id_rsa", "a.pem", "x/y/.npmrc", "a.pub", "credential", ".ENV.local",
		"x=.env.local", "--file=.env.local", "-o=.env.local.bak", "=.env.pub", "-id_rsa", dotI + "d_rsa", "a=b=.env", "k" + kelvin + ".pem",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, n string) {
		if review.SecretPath(n) && !Path(n) {
			t.Fatalf("review calls %q secret and this does not", n)
		}
	})
}

// Threat: a fold that forgets an orbit. For every rune of Unicode: fold gives
// what strings.ToLower gives unless the rune case-folds to an ASCII letter (or
// expands), and every rune with an ASCII letter in its folding orbit is read as
// that letter.
func TestFoldCoversEveryRune(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xd800 && r < 0xe000 {
			continue
		}
		if ignorable(r) {
			if fold(string(r)) != "" {
				t.Fatalf("fold(%U) kept an ignorable code point", r)
			}
			continue
		}
		if _, ok := expansions[r]; ok {
			continue
		}
		got, want := fold(string(r)), strings.ToLower(string(r))
		if got != want {
			if len(got) != 1 || got[0] >= 0x80 {
				t.Fatalf("fold(%U) = %q, ToLower gives %q", r, got, want)
			}
		}
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < 0x80 && unicode.IsLetter(f) && got != strings.ToLower(string(f)) {
				t.Fatalf("fold(%U) = %q, but it case-folds to %q", r, got, string(f))
			}
		}
	}
}

// Threat: code points a file system ignores in a name (ext4 casefold drops
// default-ignorable ones) used to split a secret's name. Dropped before matching.
func TestIgnorableCodePointsCannotSplitAName(t *testing.T) {
	for _, n := range []string{".s‍sh", ".en​v", "id­_rsa", "pass⁠word.txt", ".e️nv"} {
		if !Component(n) {
			t.Errorf("Component(%q) = false, want secret", n)
		}
	}
}

// Threat: a hostile component made to cost CPU. A name over MaxName bytes is
// called secret outright, and a name with many "=" is read after only a few.
// The time for the worst names of each kind is bounded.
func TestHostileNamesAreCheap(t *testing.T) {
	worst := []string{
		strings.Repeat("a=", MaxName/2),
		strings.Repeat(longS+"=", MaxName/4),
		strings.Repeat("=", MaxName),
		strings.Repeat("-a.bak=", MaxName/7),
		strings.Repeat("a", MaxName),
	}
	start := time.Now()
	for i := 0; i < 200; i++ {
		for _, n := range worst {
			Component(n)
		}
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("1000 worst-case names took %v", d)
	}
	if !Component(strings.Repeat("a", MaxName+1)) || Component(strings.Repeat("a", MaxName)) {
		t.Error("the length limit is not at MaxName")
	}
	// An "=" deep in a name is still read: the first few and the last tail.
	if !Path(strings.Repeat("a=", 20) + ".env.local") {
		t.Error("the tail after the last = was not read")
	}
}

func BenchmarkComponentWorst(b *testing.B) {
	n := strings.Repeat(longS+"=", MaxName/4)
	for i := 0; i < b.N; i++ {
		Component(n)
	}
}

// Threat: the names the third review found missing.
func TestMoreNames(t *testing.T) {
	for _, n := range []string{
		"ssh_host_rsa_key", "ssh_host_ed25519_key", ".mylogin.cnf", ".rails_master_key", ".authinfo", ".authinfo.gpg", ".msmtprc",
		"rclone.conf", "serviceAccountKey.json", "firebase-adminsdk-abc123.json", ".vault_pass", "authorized_keys", "wallet.dat",
		"User Data", "keyrings", ".mozilla",
	} {
		if !Component(n) {
			t.Errorf("Component(%q) = false, want secret", n)
		}
	}
	if Component("ssh_host_rsa_key.pub") {
		t.Error("the public half of a host key is not secret")
	}
}
