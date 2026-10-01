package artifacts

import "testing"

// Threat: a path whose text alone is a way round the rules -- a Windows
// alternate data stream, a device name, a trailing dot that Windows drops, an
// 8.3 alias of a secret, a UNC or extended-length path, drive-relative syntax,
// control characters. The rules are pure so the Windows ones run on every CI
// platform, not only on Windows.
func TestCheckLexical(t *testing.T) {
	type c struct {
		p       string
		windows bool
		ok      bool
	}
	cases := []c{
		{"notes.md", false, true},
		{"a/b/c.txt", false, true},
		{`a\b\c.txt`, true, true},
		{"report v2.html", false, true},
		{"C:/proj/x.md", true, true},
		{`C:\proj\x.md`, true, true},
		{"x.md:stream", true, false},         // ADS
		{"x.md::$DATA", true, false},         // ADS, default stream spelled out
		{"x.md:stream", false, true},         // a colon is an ordinary character on Unix
		{"C:x.md", true, false},              // drive-relative: not where it looks
		{`\\?\C:\x.md`, true, false},         // extended-length
		{`\\.\COM1`, true, false},            // device namespace
		{`\\server\share\x.md`, true, false}, // UNC
		{"//server/share/x.md", true, false},
		{"con", false, true}, // an ordinary name on Unix, a device on Windows
		{"con", true, false},
		{"CON.txt", true, false},
		{"CON.txt", false, true},
		{"a/Nul", true, false},
		{"aux.tar.gz", true, false},
		{"com1", true, false},
		{"LPT9.md", true, false},
		{"com¹", true, false}, // superscript digit: a device on Windows
		{"console.md", false, true},
		{"console.md", true, true},
		{"x.md.", false, false}, // Windows drops the dot: it is x.md
		{"x.md ", false, false},
		{"a./b.md", false, false},
		{"SECRET~1.TXT", false, false}, // 8.3 alias
		{"dir~2/x.md", false, false},
		{"a\x00b.md", false, false},
		{"a\nb.md", false, false},
		{"a\x1bb.md", false, false},
		{"a\u2028b.md", false, false},
		{"a\u0085b.md", false, false},
		{"a<b.md", true, false},
		{"a*.md", true, false},
		{"a<b.md", false, true},
		{"", false, false},
	}
	for _, tc := range cases {
		if got := checkLexical(tc.p, tc.windows); got != tc.ok {
			t.Errorf("checkLexical(%q, windows=%v) = %v, want %v", tc.p, tc.windows, got, tc.ok)
		}
	}
}

// Threat: a secret reached by its name or by a folder that holds secrets.
// denied is the one place that decides; it uses review.SecretPath, the check
// recordings and auto-review already apply, plus the folders in
// deniedComponents.
func TestDenied(t *testing.T) {
	for p, want := range map[string]bool{
		".env":                 true,
		"sub/.env.local":       true,
		"sub/.ENV":             true, // case-insensitive file systems
		"id_rsa":               true,
		"id_rsa.pub":           false,
		"keys/server.pem":      true,
		".npmrc":               true,
		"aws-credentials.json": true,
		".git/config":          true,
		".git":                 true, // a worktree's .git is a file
		"a/.GIT/config":        true,
		".ssh/known_hosts":     true,
		".aws/config":          true,
		".gnupg/pubring.kbx":   true,
		".kube/config":         true,
		".docker/config.json":  true,
		"src/main.go":          false,
		"docs/git.md":          false,
		"notes/.github/ci.yml": false,
		"a/secrets/x.md":       true, // every component is judged
		"a/private/x.md":       true,
		"-prod.pem":            true,
		"key.pem=":             true,
		"secretary.md":         false,
		"environment.md":       false,
	} {
		if got := denied(p); got != want {
			t.Errorf("denied(%q) = %v, want %v", p, got, want)
		}
	}
}

// Fuzzing the pure rules: they must never panic, and nothing a Windows-mode
// check accepts may hold a NUL or be drive-relative.
func FuzzCheckLexical(f *testing.F) {
	for _, s := range []string{"a.md", "x:y", `\\?\C:\a`, "CON", "a./b", "SEC~1.TXT", "a\x00", "../a", "C:/a"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		_ = checkLexical(p, false)
		if checkLexical(p, true) {
			for _, r := range p {
				if r == 0 {
					t.Fatalf("accepted a NUL in %q", p)
				}
			}
			if len(p) >= 2 && p[1] == ':' && (len(p) < 3 || (p[2] != '/' && p[2] != '\\')) {
				t.Fatalf("accepted drive-relative %q", p)
			}
		}
	})
}
