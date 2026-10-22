package record

import (
	"runtime"
	"strings"
	"testing"
)

// invisibles are characters that show nothing: format characters, a bidi
// control, a variation selector, a filler.
var invisibles = []string{
	"\u200b", "\u200c", "\u200d", "\u2060", "\u00ad", "\ufeff", "\u202e", "\u202a",
	"\u2066", "\u200f", "\u061c", "\ufe0f", "\u034f", "\u3164", "\U000e0100", "\U000e0001",
}

// secretSamples hold one secret of each shape, with the part that must not
// survive.
var secretSamples = []struct{ name, text, core string }{
	{"anthropic", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH", "abcdefghijklmnopqrstuvwxyz0123456789"},
	{"openai", "sk-abcdefghijklmnopqrstuvwxyz0123456789ABCD", "abcdefghijklmnopqrstuvwxyz"},
	{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "abcdefghijklmnopqrstuvwxyz"},
	{"github-pat", "github_pat_11ABCDEFG0abcdefghijklmnop_qrstuvwxyz", "abcdefghijklmnop"},
	{"gitlab", "glpat-abcdefghijklmnopqrstuvwx", "abcdefghijklmnop"},
	{"aws", "AKIAIOSFODNN7EXAMPLE", "IOSFODNN7EXAMPLE"},
	{"slack", "xoxb-1234567890-abcdefghijkl", "1234567890"},
	{"google", "AIzaSyA-abcdefghijklmnopqrstuvwxyz01234", "abcdefghijklmnopqrstuvwxyz"},
	{"npm", "npm_abcdefghijklmnopqrstuvwxyz0123456789", "abcdefghijklmnopqrstuvwxyz"},
	{"sendgrid", "SG.abcdefghijklmnop12.abcdefghijklmnop34", "abcdefghijklmnop"},
	{"stripe", "sk_live_abcdefghijklmnop1234", "abcdefghijklmnop"},
	{"hf", "hf_abcdefghijklmnopqrstuvwxyz01234567", "abcdefghijklmnopqrstuvwxyz"},
	{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3.SflKxwRJSMeKKF2QT4fw", "SflKxwRJSMeKKF2QT4fw"},
	{"bearer", "Authorization: Bearer abcdefghijklmnop1234567890", "abcdefghijklmnop"},
	{"url", "https://deploy:hunter2hunter2@git.example.com/org/repo.git", "hunter2hunter2"},
	{"assigned", "API_KEY=abcdefghijklmnop1234567890", "abcdefghijklmnop"},
}

// insertAt puts mark before the rune at index i of s.
func insertAt(s, mark string, i int) string {
	r := []rune(s)
	return string(r[:i]) + mark + string(r[i:])
}

func TestRedactFindsASecretWithAnInvisibleCharacterInside(t *testing.T) {
	for _, sc := range secretSamples {
		if Redact(sc.text) == sc.text {
			t.Fatalf("%s: the sample is not redacted at all: change the table", sc.name)
		}
		n := len([]rune(sc.text))
		for _, mark := range invisibles {
			for i := 1; i < n; i++ {
				in := "before " + insertAt(sc.text, mark, i) + " after"
				got := Redact(in)
				if strings.Contains(got, sc.core) || strings.Contains(got, sc.core[len(sc.core)/2:]) {
					t.Errorf("%s with %U at %d: %q", sc.name, []rune(mark)[0], i, got)
				}
				if !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
					t.Errorf("%s with %U at %d: the text around it changed: %q", sc.name, []rune(mark)[0], i, got)
				}
			}
		}
	}
}

// Marks in several places at once, and the same mark repeated.
func TestRedactFindsASecretSplitSeveralTimes(t *testing.T) {
	for _, sc := range secretSamples {
		r := []rune(sc.text)
		var b strings.Builder
		for i, c := range r {
			b.WriteRune(c)
			b.WriteString(invisibles[i%len(invisibles)])
		}
		if got := Redact(b.String()); strings.Contains(got, sc.core[len(sc.core)/2:]) {
			t.Errorf("%s: %q", sc.name, got)
		}
	}
}

// Text with no secret in it is returned as it was, invisible characters and all.
func TestRedactKeepsInvisibleCharactersInCleanText(t *testing.T) {
	for _, s := range []string{
		"👩\u200d👩\u200d👧 went home",
		"می\u200cخواهم",
		"abc\u202edef",
		"heart ❤\ufe0f",
		"soft\u00adhyphen",
		"\ufeffstarts with a mark",
	} {
		if got := Redact(s); got != s {
			t.Errorf("Redact(%q) = %q", s, got)
		}
	}
}

// Redact(ctx+text) keeps its meaning: a text with a secret and a joiner
// elsewhere loses only what it has to.
func TestRedactDropsMarksOnlyOfATextWithASecret(t *testing.T) {
	got := Redact("👩\u200d👩 AK\u200bIAIOSFODNN7EXAMPLE done")
	if want := "👩👩 " + Redacted + " done"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSpansCoverASecretWithAMarkInside(t *testing.T) {
	for name, find := range map[string]func(string) []Span{"Find": Find, "FindTokens": FindTokens, "RedactSpans": RedactSpans} {
		for _, mark := range invisibles {
			secret := "AK" + mark + "IAIOSFODNN7EXAMPLE"
			s := "see " + secret + " ok"
			spans := find(s)
			if len(spans) != 1 {
				t.Fatalf("%s %U: spans = %+v", name, []rune(mark)[0], spans)
			}
			if got := s[spans[0].Start:spans[0].End]; got != secret {
				t.Errorf("%s %U: span covers %q, want %q", name, []rune(mark)[0], got, secret)
			}
		}
	}
	// Bytes that are not text, a mark at the ends, and two secrets.
	s := "\xff\u200bAKIAIOSFODNN7EXAMPLE\u200b and ghp_abc\u200bdefghijklmnopqrstuvwxyz0123456789"
	spans := Find(s)
	if len(spans) != 2 {
		t.Fatalf("spans = %+v", spans)
	}
	for _, sp := range spans {
		if sp.Start < 0 || sp.End > len(s) || sp.Start >= sp.End {
			t.Errorf("bad span %+v", sp)
		}
	}
}

func TestRedactValueFindsASecretKeyWithAMarkInside(t *testing.T) {
	got := RedactValue(map[string]any{"pass\u200bword": "hunter2", "ok": "fine"}).(map[string]any)
	if got["pass\u200bword"] != Redacted || got["ok"] != "fine" {
		t.Errorf("got %v", got)
	}
}

func TestStripInvisibleCutsMapBackToTheText(t *testing.T) {
	s := "a\u200bé\xffb\U000e0100\u200bc"
	out, cuts := stripInvisibleCuts(s)
	if out != "aé\xffbc" || len(cuts) != 2 {
		t.Fatalf("out = %q, cuts = %v", out, cuts)
	}
	for i := range out {
		if got := orig(cuts, i); s[got] != out[i] {
			t.Errorf("byte %d of %q is at %d of %q, which is %q", i, out, got, s, s[got])
		}
	}
	if o, cuts := stripInvisibleCuts("plain é text"); cuts != nil || o != "plain é text" {
		t.Error("text with none was changed")
	}
	if o, had := stripInvisible(s); !had || o != out {
		t.Errorf("stripInvisible = %q, %v", o, had)
	}
}

// Characters Unicode lists as default-ignorable that are not in category Cf.
func TestRedactReadsPastEveryDefaultIgnorableCharacter(t *testing.T) {
	for _, r := range []rune{0x17B4, 0x17B5, 0x180F, 0x2065, 0xFFF0, 0xFFF8, 0xE0080, 0x1D173, 0xFE00, 0xE01EF, 0x180B, 0x3164} {
		if !invisible(r) {
			t.Errorf("%U is not taken for invisible", r)
			continue
		}
		in := "key AK" + string(r) + "IAIOSFODNN7EXAMPLE"
		if got := Redact(in); got != "key "+Redacted {
			t.Errorf("%U: %q", r, got)
		}
	}
	for _, r := range []rune{0xA0, 0x3000, 0x2800, 0x7F, 0x01, 0x0301, 'a', 0xE9} {
		if invisible(r) {
			t.Errorf("%U is taken for invisible", r)
		}
	}
}

// A mark in the text written so far does not make the whole of it come back
// when the next piece holds a secret.
func TestRedactAfterSkipsMarksTheWholeDropped(t *testing.T) {
	ctx := "start \u200b" + strings.Repeat("filler words ", 1000)
	clean := redactAfter(strings.Replace(ctx, "\u200b", "", 1), "token AKIAIOSFODNN7EXAMPLE")
	got := redactAfter(ctx, "token AKIAIOSFODNN7EXAMPLE")
	if len(got) > len(clean)+16 || !strings.Contains(got, Redacted) || strings.Contains(got, "IOSFODNN7EXAMPLE") {
		t.Errorf("got %d bytes %q, a text with no mark gives %d bytes %q", len(got), got, len(clean), clean)
	}
	// A mark in the text and a secret that begins in the context and ends in the next piece.
	got = redactAfter("key AK\u200bIAIOSFODN", "N7EXAMPLE done")
	if strings.Contains(got, "N7EXAMPLE") {
		t.Errorf("got %q", got)
	}
}

// A big text with one mark is not copied bigger than the limit.
func TestRedactOfABigTextWithOneMarkAllocatesLittle(t *testing.T) {
	big := strings.Repeat("some words and numbers 12345 ", 300000) // 8.7 MB
	marked := big[:100] + "\u200b" + big[100:]
	measure := func(f func()) uint64 {
		var a, b runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&a)
		f()
		runtime.ReadMemStats(&b)
		return b.TotalAlloc - a.TotalAlloc
	}
	clean := measure(func() { Redact(big) })
	got := measure(func() { Redact(marked) })
	if got > clean+4<<20 {
		t.Errorf("Redact allocated %d bytes with a mark, %d without", got, clean)
	}
	cleanFind := measure(func() { Find(big) })
	gotFind := measure(func() { Find(marked) })
	if gotFind > cleanFind+2*uint64(len(big)) {
		t.Errorf("Find allocated %d bytes with a mark, %d without", gotFind, cleanFind)
	}
}

// A text over the limit that has a split secret in what is read is cut there and
// ends with the one mark for the rest, as one without marks does.
func TestRedactOfATextOverTheLimitWithASplitSecret(t *testing.T) {
	in := "AK​IAIOSFODNN7EXAMPLE " + strings.Repeat("some words 12345 ", 100000)
	if len(in) <= MaxRedactBytes {
		t.Fatal("the text is not over the limit")
	}
	got := Redact(in)
	if strings.Contains(got, "IOSFODNN7EXAMPLE") || !strings.HasPrefix(got, Redacted+" some words") ||
		!strings.HasSuffix(got, Redacted) || len(got) > MaxRedactBytes+len(Redacted) {
		t.Errorf("got %d bytes, starting %q ending %q", len(got), got[:30], got[len(got)-30:])
	}
}
