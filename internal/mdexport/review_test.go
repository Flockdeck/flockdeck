package mdexport

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func TestRenderTheTitleIsACodeSpan(t *testing.T) {
	for _, title := range []string{"[x](http://evil.example)", "![](http://evil.example/p.png)", "<img src=x onerror=1>", "a`b`c", "# also"} {
		r := render(t, nil, Options{Title: title})
		first := strings.SplitN(r.Markdown, "\n", 2)[0]
		if !strings.HasPrefix(first, "# `") || !strings.HasSuffix(first, "`") || strings.Count(first, "`") != 2 {
			t.Errorf("title %q gave %q", title, first)
		}
	}
}

func TestRenderNeutralisesImagesAndHTMLInQuotedBodiesButNotLinksOrFences(t *testing.T) {
	body := "see ![pic](http://evil.example/p.png) and ![ref][r] and <img src=x onerror=1> and <http://auto.example>\n[link](http://ok.example)\n```go\nx := 1\n```"
	r := render(t, []transcript.Entry{
		{Kind: "reply", Markdown: body}, {Kind: "prompt", Text: body}, {Kind: "message", Dir: "in", Peer: "p", Text: body},
		{Kind: "subagent", Description: "d", Report: body}, {Kind: "tool", Label: "l", Summary: body}, {Kind: "notice", Text: body},
	}, Options{})
	for _, l := range strings.Split(r.Markdown, "\n") {
		if !strings.HasPrefix(l, ">") {
			continue
		}
		if strings.Contains(l, "<") {
			t.Errorf("raw HTML in %q", l)
		}
		for from := 0; ; {
			i := strings.Index(l[from:], "![")
			if i < 0 {
				break
			}
			i += from
			if i == 0 || l[i-1] != '\\' {
				t.Errorf("live image syntax in %q", l)
			}
			from = i + 2
		}
	}
	for _, keep := range []string{"> [link](http://ok.example)", "> ```go", "&lt;img src=x onerror=1>", `\![pic]`} {
		if !strings.Contains(r.Markdown, keep) {
			t.Errorf("missing %q in\n%s", keep, r.Markdown)
		}
	}
}

func TestWriteNewKeepsTheTimeInTheNameOfALongTitle(t *testing.T) {
	dir := t.TempDir()
	name := strings.Repeat("n", 60)
	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		p, err := WriteNew(dir, name, "20260101-120000", []byte("x"))
		if err != nil {
			t.Fatalf("export %d: %v", i, err)
		}
		if seen[p] || !strings.Contains(filepath.Base(p), "20260101-120000") {
			t.Fatalf("export %d: %q", i, p)
		}
		seen[p] = true
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 60 {
		t.Errorf("%d files", len(entries))
	}
	p, err := WriteNew(dir, name, "20260101-120001", []byte("x"))
	if want := "flockdeck-" + strings.Repeat("n", 20) + "-20260101-120001.md"; err != nil || filepath.Base(p) != want {
		t.Errorf("name = %q, %v, want %q", p, err, want)
	}
}

func TestWriteNewRefusesNetworkAndDevicePaths(t *testing.T) {
	for _, bad := range []string{`\\host\share`, `\\host\share\dir`, `//host/share`, `\\.\C:\`, `\\.\pipe\x`, `\\?\UNC\host\share`, `\\?\GLOBALROOT\x`} {
		if !networkOrDevicePath(bad) {
			t.Errorf("%q was allowed", bad)
		}
	}
	for _, ok := range []string{`C:\out`, `D:/out`, `\\?\C:\out`, `/home/me/out`, `relative`} {
		if networkOrDevicePath(ok) {
			t.Errorf("%q was refused", ok)
		}
	}
	if p, err := WriteNew(`\\host\share`, "x", "t", []byte("a")); err == nil {
		t.Errorf("wrote %q", p)
	}
	if runtime.GOOS == "windows" {
		// A folder that exists, reached through the machine's own admin share.
		dir := t.TempDir()
		if vol := filepath.VolumeName(dir); len(vol) == 2 {
			unc := `\\localhost\` + vol[:1] + `$` + dir[2:]
			if p, err := WriteNew(unc, "x", "t", []byte("a")); err == nil {
				t.Errorf("wrote %q through %q", p, unc)
			}
		}
	}
}
