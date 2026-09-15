package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The list is a plain file somebody edits by hand, so every way of writing a
// line is read the one way, and a line that would put something odd on the
// site is refused with the line it is on.
func TestSponsorsAreReadFromAPlainList(t *testing.T) {
	got, err := parseSponsors("# a comment\n\nAda Lovelace\n  Example Ltd   https://example.com/  \r\nA & B https://a-and-b.example/x?y=1\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []sponsor{
		{Name: "Ada Lovelace"},
		{Name: "Example Ltd", Link: "https://example.com/"},
		{Name: "A & B", Link: "https://a-and-b.example/x?y=1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got, err := parseSponsors("# nobody yet\n"); err != nil || len(got) != 0 {
		t.Errorf("a list of comments gave %+v, %v", got, err)
	}
	for _, bad := range []string{
		"Example http://example.com",
		"Example javascript://alert(1)",
		"Example https://",
		"https://example.com",
		"https://example.com Example",
	} {
		_, err := parseSponsors("Ada\n" + bad + "\n")
		if err == nil {
			t.Errorf("%q was accepted", bad)
		} else if !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%q: the error does not say which line: %v", bad, err)
		}
	}
}

// The list that ships has to read, or the site cannot be generated.
func TestTheSponsorListReads(t *testing.T) {
	if _, err := loadSponsors(); err != nil {
		t.Fatal(err)
	}
}

// sponsorSection is the home page's #sponsor section.
func sponsorSection(t *testing.T, page string) string {
	t.Helper()
	from := strings.Index(page, `<section class="band" id="sponsor">`)
	if from < 0 {
		t.Fatal("the landing page has no #sponsor section")
	}
	end := strings.Index(page[from:], "</section>")
	if end < 0 {
		t.Fatal("the #sponsor section is not closed")
	}
	return strings.Join(strings.Fields(page[from:from+end]), " ")
}

// renderHome draws the landing page for a list of sponsors.
func renderHome(t *testing.T, sponsors []sponsor) string {
	t.Helper()
	files, err := render(site{Repo: defaultRepo, Module: defaultModule, URL: defaultURL, Downloads: downloadsURL, Sponsor: sponsorURL, Sponsors: sponsors})
	if err != nil {
		t.Fatal(err)
	}
	return string(files["index.html"])
}

// The section says plainly that sponsoring buys nothing, and links to GitHub
// Sponsors. With nobody listed it says so, and the FAQ's answer to "Is
// Flockdeck free?" leads to it.
func TestTheSponsorSectionSaysItBuysNothing(t *testing.T) {
	_, page := home(t)
	section := sponsorSection(t, page)
	for _, want := range []string{
		`href="` + sponsorURL + `"`,
		"Sponsoring buys no features, support or priority",
		"the app is the same for everyone",
		"named here only if they ask to be",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the #sponsor section does not have %q", want)
		}
	}
	if empty := sponsorSection(t, renderHome(t, nil)); !strings.Contains(empty, "No one is listed yet.") || strings.Contains(empty, `class="sponsors"`) {
		t.Errorf("with nobody listed, the section does not say so: %s", empty)
	}
	faq := page[strings.Index(page, "<summary>Is Flockdeck free?</summary>"):]
	faq = faq[:strings.Index(faq, "</details>")]
	if !strings.Contains(faq, `<a href="#sponsor">`) {
		t.Error(`the FAQ's "Is Flockdeck free?" does not lead to the #sponsor section`)
	}
}

// A sponsor's line is a name and, where they gave one, a link marked as a
// sponsor's: no logo, and nothing but the name as its text.
func TestSponsorsAreNamesAndLinksOnly(t *testing.T) {
	section := sponsorSection(t, renderHome(t, []sponsor{
		{Name: "Ada Lovelace"},
		{Name: "A & B <Ltd>", Link: "https://a-and-b.example/?x=1&y=2"},
	}))
	for _, want := range []string{
		`<ul class="sponsors">`,
		`<li>Ada Lovelace</li>`,
		`<li><a href="https://a-and-b.example/?x=1&amp;y=2" rel="sponsored">A &amp; B &lt;Ltd&gt;</a></li>`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the #sponsor section does not have %s:\n%s", want, section)
		}
	}
	if strings.Contains(section, "No one is listed yet.") {
		t.Error("the section says nobody is listed beside the names it lists")
	}
	if regexp.MustCompile(`(?i)<(img|svg|picture|video)\b`).MatchString(section) {
		t.Errorf("the #sponsor section shows a picture: %s", section)
	}
}

// GitHub's Sponsor button, the app's line, the site, the README and the help
// all send somebody to the one GitHub Sponsors account: the one
// .github/FUNDING.yml names.
func TestSponsorshipGoesToOneAccount(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(parts ...string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
	}
	m := regexp.MustCompile(`(?m)^github:\s*(\S+)\s*$`).FindStringSubmatch(read(".github", "FUNDING.yml"))
	if m == nil {
		t.Fatal(".github/FUNDING.yml names no GitHub Sponsors account")
	}
	if want := "https://github.com/sponsors/" + m[1]; sponsorURL != want {
		t.Errorf("the site links %s, but FUNDING.yml names %s", sponsorURL, want)
	}
	for _, f := range []struct{ what, text, want string }{
		{"the app's Settings", read("internal", "webui", "assets", "app.js"), `const SPONSOR_URL = "` + sponsorURL + `";`},
		{"the README", read("README.md"), "(" + sponsorURL + ")"},
		{"the help's Settings page", read("internal", "help", "pages", "settings.md"), "(" + sponsorURL + ")"},
	} {
		if !strings.Contains(f.text, f.want) {
			t.Errorf("%s does not link %s", f.what, sponsorURL)
		}
	}
}
