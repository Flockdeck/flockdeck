package recordview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/record"
)

// samples builds secrets of each shape at run time, so that no literal in
// this file looks like one to a scanner. secret is the part that must not
// survive in any run of eight bytes; text is what is put in the transcript.
type sample struct {
	kind, text, secret string
}

func samples(r *rand.Rand) []sample {
	const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	pick := func(set string, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = set[r.Intn(len(set))]
		}
		return string(b)
	}
	gh := "gh" + "p_" + pick(alnum, 36)
	ant := "sk-" + "ant-" + "api03-" + pick(alnum, 40)
	aws := "AK" + "IA" + pick(upper, 16)
	bearer := pick(alnum, 30)
	body := pick(alnum, 64) + "\n" + pick(alnum, 64) + "\n" + pick(alnum, 20)
	pem := "-----BEGIN " + "RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----"
	return []sample{
		{"github", "token " + gh + " end", gh},
		{"anthropic", "key=" + ant + ".", ant},
		{"aws", "id: " + aws + " region", aws},
		{"bearer", "Authorization: Bearer " + bearer + "\n", bearer},
		{"pem", "before\n" + pem + "\nafter", body},
	}
}

// inserts is every kind of character and sequence that can be put into a secret
// to break it up without changing how it looks.
var inserts = []string{
	"\u200b", "\u200c", "\u200d", "\u200e", "\u200f",
	"\u202a", "\u202b", "\u202c", "\u202d", "\u202e",
	"\u2060", "\u2061", "\u2062", "\u2063", "\u2064",
	"\u2066", "\u2067", "\u2068", "\u2069", "\u206a", "\u206f",
	"\ufeff", "\u00ad", "\u180e", "\ufff9", "\ufffa", "\ufffb",
	"\u034f", "\u3164", "\ufe0f", "\U000e0041",
	"\t", "\u00a0", "\u2003", "\u3000",
	"\x00", "\x07", "\x7f", "\u0085", "\r",
	"\x1b", "\x1b[0m", "\x1b[38;2;255;0;0m", "\x1b]0;title\x07", "\x1b]8;;http://example.com\x1b\\",
	"\x1bP1$r0m\x1b\\", "\x1b(B", "\x1b7", "\u009b0m", "\u009d0;t\u009c",
}

// longestKept is the length of the longest run of bytes of secret found in out.
func longestKept(out, secret string) int {
	best := 0
	for i := 0; i < len(secret); i++ {
		for n := best + 1; i+n <= len(secret) && strings.Contains(out, secret[i:i+n]); n++ {
			best = n
		}
	}
	return best
}

// Whatever is put into a secret, at whatever offset, no eight bytes in a row of
// it come out. The same text goes through the same path as a line of a file.
func TestInsertedCharactersDoNotHideASecret(t *testing.T) {
	r := rand.New(rand.NewSource(20261008))
	for _, sm := range samples(r) {
		if sm.secret == "" {
			continue
		}
		for _, ins := range inserts {
			for off := 0; off <= len(sm.text); off++ {
				if off < len(sm.text) && !utf8.RuneStart(sm.text[off]) {
					continue
				}
				text := sm.text[:off] + ins + sm.text[off:]
				st := &state{clipped: map[string]int{}}
				out := st.redact(text)
				if n := longestKept(out, sm.secret); n >= 8 {
					t.Fatalf("%s: %q put at %d leaves %d bytes of the secret in %q", sm.kind, ins, off, n, out)
				}
				if strings.ContainsRune(out, 0x1b) {
					t.Fatalf("%s: %q at %d leaves an escape in %q", sm.kind, ins, off, out)
				}
			}
		}
	}
}

// The same with two and three insertions of any kind, anywhere, in a text with
// words around the secret.
func TestSeveralInsertedCharactersDoNotHideASecret(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	words := []string{"the", "build", "failed", "with", "error", "=", ":", "\n", "  ", "ok"}
	for n := 0; n < 3000; n++ {
		all := samples(r)
		sm := all[r.Intn(len(all))]
		var b strings.Builder
		for i, k := 0, r.Intn(4); i < k; i++ {
			b.WriteString(words[r.Intn(len(words))] + " ")
		}
		b.WriteString(sm.text)
		b.WriteString(" " + words[r.Intn(len(words))])
		text := b.String()
		// Offsets are chosen in the original text and applied from the last, so that
		// no insertion lands inside another and breaks it into pieces.
		offs := map[int]bool{}
		for k := 1 + r.Intn(3); k > 0; k-- {
			offs[r.Intn(len(text)+1)] = true
		}
		for off := len(text); off >= 0; off-- {
			if offs[off] && (off == len(text) || utf8.RuneStart(text[off])) {
				text = text[:off] + inserts[r.Intn(len(inserts))] + text[off:]
			}
		}
		st := &state{clipped: map[string]int{}}
		out := st.redact(text)
		if n := longestKept(out, sm.secret); n >= 8 {
			t.Fatalf("%s: %q leaves %d bytes of the secret in %q", sm.kind, text, n, out)
		}
	}
}

func TestInsertedCharactersInAKeyAndAValueOfToolInput(t *testing.T) {
	secret := "gh" + "p_" + strings.Repeat("a1B2c3", 6)
	for _, ins := range []string{"\u200b", "\x1b[0m", "\t", "\u2060", "\u00ad"} {
		for off := 1; off < len(secret); off++ {
			s := secret[:off] + ins + secret[off:]
			e, ok := parseLine([]byte(line(2, "tool_call", map[string]any{"tool": "Bash", "input": map[string]any{
				"command": "echo " + s, s: "v", "list": []any{s},
			}})))
			if !ok {
				t.Fatal("line not parsed")
			}
			if n := longestKept(fmt.Sprint(e.Input), secret); n >= 8 {
				t.Fatalf("%q at %d leaves %d bytes in %v", ins, off, n, e.Input)
			}
		}
	}
}

func TestStripUnsafeRemovesWholeSequences(t *testing.T) {
	for in, want := range map[string]string{
		"a\x1b[0mb":                        "ab",
		"a\x1b[38;5;196mb\x1b[Kc":          "abc",
		"a\x1b]0;window title\x07b":        "ab",
		"a\x1b]8;;http://x.example\x1b\\b": "ab",
		"a\x1bPdata\x1b\\b":                "ab",
		"a\x1b_apc\x1b\\b":                 "ab",
		"a\x1b^pm\x1b\\b":                  "ab",
		"a\x1bXsos\x1b\\b":                 "ab",
		"a\x1b(Bb":                         "ab",
		"a\x1b7b":                          "ab",
		"a\x1bsb":                          "ab",
		"KEY\x1b=v":                        "KEYv",
		"a\x1b":                            "a",
		"a\x1b[":                           "a",
		"a\x1b[31":                         "a",
		"a\x1b]unterminated\nb":            "aunterminated\nb",
		"a\u009b31mb":                      "ab",
		"a\u009dtitle\u009cb":              "ab",
		"a\u200bb\u200dc\u2060d\u00ade":    "abcde",
		"a\ufeffb\u202ec\u2066d":           "abcd",
		"a\u2028b\u2029c":                  "a\nb\nc",
		"tab\there":                        "tab\there",
		"a\xffb":                           "a\uFFFDb",
		"é日本語":                             "é日本語",
		"a\x00\x07\x7fb\u0085c":            "abc",
	} {
		if got := stripUnsafe(in); got != want {
			t.Errorf("stripUnsafe(%q) = %q, want %q", in, got, want)
		}
	}
	r := rand.New(rand.NewSource(1))
	pieces := append([]string{"a", "b", "[", "]", "\\", "\n", "é", "\xff", "0", ";", "m", "\x07"}, inserts...)
	for n := 0; n < 5000; n++ {
		var b strings.Builder
		for i, k := 0, r.Intn(12); i < k; i++ {
			b.WriteString(pieces[r.Intn(len(pieces))])
		}
		out := stripUnsafe(b.String())
		if !utf8.ValidString(out) || stripUnsafe(out) != out {
			t.Fatalf("stripUnsafe(%q) = %q is not valid and settled", b.String(), out)
		}
		for _, c := range out {
			if unsafeRune(c) {
				t.Fatalf("stripUnsafe(%q) = %q has %U", b.String(), out, c)
			}
		}
	}
}

func TestTabsAndOddSpacesAreKeptButDoNotEndASecret(t *testing.T) {
	gh := "gh" + "p_" + strings.Repeat("a1B2c3", 6)
	st := &state{clipped: map[string]int{}}
	out := st.redact("a\tb " + gh[:10] + "\t" + gh[10:] + "\t z")
	if strings.Contains(out, gh[:12]) || strings.Contains(out, gh[10:]) {
		t.Errorf("secret survived: %q", out)
	}
	if !strings.HasPrefix(out, "a\tb ") || !strings.HasSuffix(out, "\t z") {
		t.Errorf("tabs around it were not kept: %q", out)
	}
	if !st.redacted {
		t.Error("redacted not set")
	}
}

func TestKeysAreCheckedBeforeTheyAreCut(t *testing.T) {
	long := strings.Repeat("x", 300) + "password"
	e, _ := parseLine([]byte(line(2, "tool_call", map[string]any{"tool": "Bash", "input": map[string]any{long: "plainvalue"}})))
	in, _ := e.Input.(map[string]any)
	if len(in) != 1 {
		t.Fatalf("input = %v", e.Input)
	}
	for k, v := range in {
		if v != record.Redacted || len(k) > maxIDBytes {
			t.Errorf("key of %d bytes has value %v", len(k), v)
		}
	}
	if !e.Redacted {
		t.Error("redacted not set")
	}
}

func TestKeysThatCollapseKeepTheirValues(t *testing.T) {
	long := strings.Repeat("k", 300)
	e, _ := parseLine([]byte(line(2, "tool_call", map[string]any{"tool": "Bash", "input": map[string]any{
		long + "1": "one", long + "2": "two", long + "3": "three",
		"a\u200bb": "x", "ab": "y", "a\x1b[0mb": "z",
	}})))
	in, _ := e.Input.(map[string]any)
	if len(in) != 6 {
		t.Fatalf("%d keys survive of 6: %v", len(in), e.Input)
	}
	got := map[any]bool{}
	for _, v := range in {
		got[v] = true
	}
	for _, v := range []string{"one", "two", "three", "x", "y", "z"} {
		if !got[v] {
			t.Errorf("value %q was dropped", v)
		}
	}
	// And the same file gives the same keys every time, whatever order the
	// original keys came in.
	for i := 0; i < 30; i++ {
		again, _ := parseLine([]byte(line(2, "tool_call", map[string]any{"tool": "Bash", "input": map[string]any{
			long + "1": "one", long + "2": "two", long + "3": "three",
			"a​b": "x", "ab": "y", "a\x1b[0mb": "z",
		}})))
		if !reflect.DeepEqual(again.Input, e.Input) {
			t.Fatalf("two reads differ:\n%v\n%v", e.Input, again.Input)
		}
	}
}

// countdown is a context that is done after n looks at it.
type countdown struct {
	context.Context
	n atomic.Int64
}

func (c *countdown) Err() error {
	if c.n.Add(-1) < 0 {
		return context.DeadlineExceeded
	}
	return nil
}

func (c *countdown) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *countdown) Done() <-chan struct{}       { return nil }

func manyLines(n int) string {
	lines := []string{started()}
	for i := 0; i < n; i++ {
		lines = append(lines, line(i+2, "user_prompt", map[string]any{"text": fmt.Sprint("message ", i)}))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestAPageThatRunsOutOfTimeEndsOnALineAndTheNextOneContinues(t *testing.T) {
	data := []byte(manyLines(100))
	// The first look is the one Page makes before it starts, one is made by the
	// timeout wrapper's own context, and one follows each line.
	for _, looks := range []int64{3, 5, 20} {
		ctx := &countdown{Context: bg}
		ctx.n.Store(looks)
		pg, err := pageOf(ctx, bytes.NewReader(data), int64(len(data)), 0, MaxPageEntries)
		if err != nil {
			t.Fatal(err)
		}
		if pg.Done || len(pg.Entries) == 0 || len(pg.Entries) >= 100 {
			t.Fatalf("looks %d: %d entries, done %v", looks, len(pg.Entries), pg.Done)
		}
		if pg.Next == 0 || data[pg.Next-1] != '\n' {
			t.Fatalf("page ends at %d, not after a line", pg.Next)
		}
		rest, err := pageOf(bg, bytes.NewReader(data), int64(len(data)), pg.Next, MaxPageEntries)
		if err != nil || len(pg.Entries)+len(rest.Entries) != 101 {
			t.Fatalf("looks %d: %d + %d entries, err %v", looks, len(pg.Entries), len(rest.Entries), err)
		}
	}
}

func TestACallThatIsOutOfTimeBeforeItBeginsReturnsErrBudget(t *testing.T) {
	r, err := openAt(writeFile(t, started(), line(2, "user_prompt", map[string]any{"text": "x"})))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := r.Page(ctx, 0, 10); !errors.Is(err, ErrBudget) {
		t.Errorf("Page: %v", err)
	}
	if _, err := Open(ctx, filepath.Dir(filepath.Join(t.TempDir(), "x")), "x"); !errors.Is(err, ErrBudget) {
		t.Errorf("Open: %v", err)
	}
	past, cancel2 := context.WithDeadline(bg, time.Now().Add(-time.Second))
	defer cancel2()
	if _, err := r.Page(past, 0, 10); !errors.Is(err, ErrBudget) {
		t.Errorf("Page past its deadline: %v", err)
	}
}

// Lines built to be the slowest to redact do not take a page past its budget by
// more than a line.
func TestSlowLinesStayInsideTheBudget(t *testing.T) {
	texts := map[string]string{
		"prefix":   strings.Repeat("sk-", 10<<10),
		"pem":      strings.Repeat("-----BEGIN PRIVATE KEY-----", 1100),
		"openers":  strings.Repeat(`password="`, 3000),
		"names":    strings.Repeat("token_", 5000),
		"flags":    strings.Repeat("--password ", 2700),
		"bearer":   strings.Repeat("Bearer ", 4000),
		"gaps":     strings.Repeat("ghp_\t", 6000),
		"escapes":  strings.Repeat("\x1b[", 15000),
		"invisble": strings.Repeat("a\u200b", 15000),
	}
	for name, text := range texts {
		var lines []string
		lines = append(lines, started())
		for i := 0; i < 130; i++ {
			lines = append(lines, line(i+2, "user_prompt", map[string]any{"text": text}))
		}
		p := writeFile(t, lines...)
		r, err := openAt(p)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		pg, err := r.Page(bg, 0, MaxPageEntries)
		took := time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// One line is at most about 30 KiB here; the budget is checked between lines.
		if took > DefaultBudget+1500*time.Millisecond {
			t.Errorf("%s: a call took %v for %d entries", name, took, len(pg.Entries))
		}
	}
}

func TestLongLinesAreSkippedAtTheLineCap(t *testing.T) {
	at := line(2, "user_prompt", map[string]any{"text": ""})
	pad := strings.Repeat("a", MaxLineBytes-len(at)-3)
	ok := line(2, "user_prompt", map[string]any{"text": pad})
	if len(ok) >= MaxLineBytes {
		t.Fatalf("test line is %d bytes", len(ok))
	}
	over := line(3, "user_prompt", map[string]any{"text": pad + strings.Repeat("a", 4096)})
	if len(over) <= MaxLineBytes {
		t.Fatalf("over line is %d bytes", len(over))
	}
	r, err := openAt(writeFile(t, started(), ok, over, line(4, "user_prompt", map[string]any{"text": "after"})))
	if err != nil {
		t.Fatal(err)
	}
	pg, err := r.Page(bg, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pg.Entries) != 3 || pg.Skipped != 1 || pg.Entries[2].Text != "after" {
		t.Fatalf("%d entries, %d skipped", len(pg.Entries), pg.Skipped)
	}
}

// A line at the cap costs a bounded amount of memory, not a multiple of the line
// that grows without limit.
func TestALineAtTheCapAllocatesABoundedAmount(t *testing.T) {
	text := strings.Repeat("sk-", (MaxLineBytes-1000)/3)
	r, err := openAt(writeFile(t, started(), line(2, "user_prompt", map[string]any{"text": text})))
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := r.Page(bg, 0, 10); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if used := after.TotalAlloc - before.TotalAlloc; used > 24<<20 {
		t.Errorf("one line of %d bytes allocated %d MiB", len(text), used>>20)
	}
}

func TestFileSizeLimit(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, size int64) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(started() + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	mk("at.jsonl", MaxFileBytes)
	mk("over.jsonl", MaxFileBytes+1)
	if _, err := Open(bg, dir, "at.jsonl"); err != nil {
		t.Errorf("a file of exactly MaxFileBytes: %v", err)
	}
	if _, err := Open(bg, dir, "over.jsonl"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a file of MaxFileBytes+1: %v", err)
	}
}

func TestPageRefusesAFileThatWasReplacedOrShrunk(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, lines ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l2 := line(2, "user_prompt", map[string]any{"text": "two"})
	l3 := line(3, "user_prompt", map[string]any{"text": "three"})
	write("a.jsonl", started(), l2)
	r, err := Open(bg, dir, "a.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	// Growing is what a transcript does.
	f, _ := os.OpenFile(filepath.Join(dir, "a.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(l3 + "\n")
	f.Close()
	if pg, err := r.Page(bg, 0, 10); err != nil || len(pg.Entries) != 3 {
		t.Fatalf("after an append: %d entries, %v", len(pg.Entries), err)
	}
	// Shrinking it in place is not.
	if err := os.Truncate(filepath.Join(dir, "a.jsonl"), int64(len(started())+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Page(bg, 0, 10); !errors.Is(err, ErrChanged) {
		t.Errorf("after a truncation: %v", err)
	}
	// Nor is another file under the same name, even a longer one.
	write("b.jsonl", started(), l2, l3, l3, l3)
	r2, err := Open(bg, dir, "b.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	write("other.jsonl", started(), l3, l3, l3, l3, l3, l3, l3, l3, l3)
	if err := os.Remove(filepath.Join(dir, "b.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "other.jsonl"), filepath.Join(dir, "b.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Page(bg, 0, 10); !errors.Is(err, ErrChanged) {
		t.Errorf("after a replacement: %v", err)
	}
}

func TestNamesAndRootsThatAreNotPlain(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.jsonl"), []byte(started()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "in.jsonl"), []byte(started()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ok.jsonl", "sub/in.jsonl"} {
		if _, err := Open(bg, root, name); err != nil {
			t.Errorf("Open(%q): %v", name, err)
		}
	}
	for _, name := range []string{
		"", ".", "..", "../ok.jsonl", "sub/../ok.jsonl", "./ok.jsonl", "/ok.jsonl", "sub//in.jsonl", "sub/",
		`\ok.jsonl`, `sub\in.jsonl`, `\\server\share\ok.jsonl`, `\\?\C:\ok.jsonl`, `\\.\pipe\x`, "//server/share/ok.jsonl",
		"C:ok.jsonl", "C:/ok.jsonl", `C:\ok.jsonl`, "ok.jsonl:stream", "ok.jsonl::$DATA", "ok.jsonl:$DATA:stream", "sub:x/in.jsonl",
		"CON", "con.jsonl", "NUL", "nul.txt", "PRN", "AUX", "COM1", "com9.jsonl", "LPT1", "lpt3.x", "CONIN$", "CONOUT$", "COM\u00b9",
		"ok.jsonl.", "ok.jsonl ", "ok*.jsonl", "ok?.jsonl", `ok".jsonl`, "ok<.jsonl", "ok>.jsonl", "ok|.jsonl", "ok\x00.jsonl", "ok\n.jsonl", "ok\x7f",
		"a/b/c/d/e/f/g/h/i.jsonl", strings.Repeat("a", 600),
	} {
		if _, err := Open(bg, root, name); err == nil {
			t.Errorf("Open(%q) succeeded", name)
		} else if !errors.Is(err, ErrUnsupported) {
			t.Errorf("Open(%q): %v", name, err)
		}
	}
	for _, bad := range []string{"", "relative/dir", ".", `\\server\share`, "//server/share", `\\?\` + root, `\\.\` + root, root + "/../" + filepath.Base(root) + "x\x00"} {
		if _, err := Open(bg, bad, "ok.jsonl"); err == nil {
			t.Errorf("Open with root %q succeeded", bad)
		}
	}
	if runtime.GOOS == "windows" {
		if _, err := Open(bg, root+":stream", "ok.jsonl"); err == nil {
			t.Error("a root with a stream name succeeded")
		}
	}
	// The root must be a folder.
	if _, err := Open(bg, filepath.Join(root, "ok.jsonl"), "x"); err == nil {
		t.Error("a file as the root succeeded")
	}
}

// link makes a link to a folder: a junction on Windows, which needs no privilege,
// and a symbolic link elsewhere.
func link(t *testing.T, from, to string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", from, to).CombinedOutput(); err != nil {
			t.Skipf("no junctions here: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(to, from); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

func TestAParentThatIsALinkIsRefused(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	root := filepath.Join(base, "root")
	for _, d := range []string{outside, filepath.Join(root, "real")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(outside, "a.jsonl"), filepath.Join(root, "real", "a.jsonl")} {
		if err := os.WriteFile(p, []byte(started()+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link(t, filepath.Join(root, "linked"), outside)
	if _, err := Open(bg, root, "real/a.jsonl"); err != nil {
		t.Fatalf("a plain folder: %v", err)
	}
	if _, err := Open(bg, root, "linked/a.jsonl"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a file under a linked folder: %v", err)
	}
	// A root that is itself a link.
	link(t, filepath.Join(base, "rootlink"), root)
	if _, err := Open(bg, filepath.Join(base, "rootlink"), "real/a.jsonl"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a root that is a link: %v", err)
	}
	// A folder that becomes a link after Open is refused by the next Page.
	r, err := Open(bg, root, "real/a.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "real"), filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(root, "real"), filepath.Join(root, "moved"))
	if _, err := r.Page(bg, 0, 10); err == nil {
		t.Error("Page followed a folder that became a link")
	}
}

func TestAFileThatIsALinkIsRefused(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real.jsonl")
	if err := os.WriteFile(real, []byte(started()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "sym.jsonl")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if _, err := Open(bg, root, "sym.jsonl"); err == nil {
		t.Error("a symbolic link to a file was opened")
	}
}

// A file with more than one name is accepted, as the package comment says.
func TestAHardLinkIsAccepted(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real.jsonl")
	if err := os.WriteFile(real, []byte(started()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(real, filepath.Join(root, "hard.jsonl")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	if _, err := Open(bg, root, "hard.jsonl"); err != nil {
		t.Errorf("a hard link: %v", err)
	}
}

// A line of many string introducers with no terminator is read once, not once
// for each of them.
func TestManyUnterminatedStringsAreReadOnce(t *testing.T) {
	for _, unit := range []string{"\x1b_", "\x1b]", "\x1bP", "\x1bX", "\x1b^", "\u009d", "\u0090"} {
		s := strings.Repeat(unit, MaxLineBytes/len(unit))
		start := time.Now()
		cleanText(s)
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%q: %d bytes took %v", unit, len(s), d)
		}
	}
}

// A line is read into memory whole, so the cap on its size is what bounds the
// memory one call uses.
func TestTheLineCapStaysSmall(t *testing.T) {
	if MaxLineBytes > 256<<10 {
		t.Errorf("MaxLineBytes is %d", MaxLineBytes)
	}
}
