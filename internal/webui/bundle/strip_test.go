package bundle

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func strip(t *testing.T, src string) string {
	t.Helper()
	out, err := Strip([]byte(src))
	if err != nil {
		t.Fatalf("Strip(%q): %v", src, err)
	}
	return string(out)
}

func TestStripRemovesCommentsAndSpacesAndKeepsTheLines(t *testing.T) {
	got := strip(t, "// a\n/** doc\n * more */\nconst  a = 1;   // tail\n\n\n    const b = a /* x */ + 2;\nreturn  b\n")
	want := "const a = 1;\nconst b = a + 2;\nreturn b\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A comment over a line break is still a line break: it can end a statement.
	if got := strip(t, "a /* one\ntwo */ b"); got != "a\nb" {
		t.Errorf("a block comment over lines gave %q", got)
	}
}

func TestStripLeavesStringsTemplatesAndRegularExpressionsAlone(t *testing.T) {
	for _, src := range []string{
		`const a = "// not  a comment";`,
		`const a = '/* nor */  this';`,
		`const a = "say \"hi\"  // there";`,
		"const a = `line  one\n    indented // kept\n  ${x + 1} and ${ {b: 1}.b } `;",
		"const a = `a ${`inner  ${y}`} b`;",
		`const r = /a  b\/\/c[/ ]+"/g;`,
		`x = a / b / c;`,
		`return /  x/.test(s);`,
		`const n = f(a) / 2;`,
		`if (s.replace(/'/g, "''")) ok();`,
	} {
		if got := strip(t, src); got != src {
			t.Errorf("changed %q to %q", src, got)
		}
	}
	// What is around them is still stripped.
	if got := strip(t, "x  =   `a  b`   // c"); got != "x = `a  b`" {
		t.Errorf("got %q", got)
	}
}

func TestStripRefusesWhatItCannotTell(t *testing.T) {
	for name, src := range map[string]string{
		"a string left open":        "const a = 'abc\nconst b = 1;",
		"a comment left open":       "a /* never closed",
		"a template left open":      "const a = `abc",
		"a regular expression open": "const a = /abc\n",
		"a slash after a brace":     "if (a) {\n}\n/x/.test(s)",
	} {
		if _, err := Strip([]byte(src)); err == nil {
			t.Errorf("%s was stripped", name)
		}
	}
}

// Every file the front end is made of strips, and the result is still valid
// script. (Whether it does the same is the node test that runs the joined file.)
func TestEveryFileOfTheFrontEndStripsAndStillParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	root := "../ui/assets"
	dir := t.TempDir()
	n := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") && !strings.HasSuffix(p, ".mjs") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := Strip(b)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		if len(out) >= len(b) && len(b) > 200 && !strings.Contains(filepath.ToSlash(p), "/vendor/") {
			t.Errorf("%s did not get smaller", p)
		}
		n++
		file := filepath.Join(dir, strings.ReplaceAll(filepath.ToSlash(p), "/", "_")+".mjs")
		if err := os.WriteFile(file, out, 0o600); err != nil {
			return err
		}
		if o, err := exec.Command(node, "--check", file).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse once stripped: %s", p, o)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 80 {
		t.Errorf("only %d files were checked", n)
	}
}
