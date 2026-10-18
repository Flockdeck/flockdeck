package secretname

import (
	"strings"
	"testing"
)

// The old implementation of the home-folder lookup: the list, read from
// testdata/names.txt when the test runs, and compared as text.
type oldHome struct{ exact, lower, above, aboveLower map[string]bool }

func oldHomeTabs(t testing.TB) oldHome {
	set := func(s []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range s {
			m[x] = true
		}
		return m
	}
	h, hl, a, al := homeNames(lists(t).homePaths)
	_ = hl
	return oldHome{set(h), set(hl), set(a), set(al)}
}

func TestHomePathsAreFound(t *testing.T) {
	l := lists(t).homePaths
	if len(l) == 0 {
		t.Fatal("no home paths listed")
	}
	seen := map[string]bool{}
	for _, p := range l {
		if seen[strings.ToLower(p)] {
			t.Errorf("%q listed twice", p)
		}
		seen[strings.ToLower(p)] = true
		if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "//") || strings.Contains(p, "\\") {
			t.Errorf("%q is not a plain relative path", p)
		}
		for _, fold := range []bool{false, true} {
			if !HomeSecret(p, fold) {
				t.Errorf("%q (fold %v) is listed but not found", p, fold)
			}
			if HomeSecretAbove(p, fold) {
				t.Errorf("%q (fold %v) is listed, and also on the way to another", p, fold)
			}
		}
		if !HomeSecret(strings.ToUpper(p), true) || !HomeSecret(strings.ToLower(p), true) {
			t.Errorf("%q is not found whatever the case", p)
		}
		parts := strings.Split(p, "/")
		if len(parts) > MaxHomeDepth {
			t.Errorf("%q is deeper than MaxHomeDepth", p)
		}
		for i := 1; i < len(parts); i++ {
			up := strings.Join(parts[:i], "/")
			if !HomeSecretAbove(up, false) || !HomeSecretAbove(up, true) {
				t.Errorf("%q, on the way to %q, is not found", up, p)
			}
			if HomeSecret(up, false) {
				t.Errorf("%q lies inside %q, both listed", p, up)
			}
		}
	}
}

func homeSame(t *testing.T, s string) {
	t.Helper()
	o := oldHomeTabs(t)
	if got, want := HomeSecret(s, false), o.exact[s]; got != want {
		t.Errorf("HomeSecret(%q, false) = %v, the list says %v", s, got, want)
	}
	if got, want := HomeSecret(s, true), o.lower[strings.ToLower(s)]; got != want {
		t.Errorf("HomeSecret(%q, true) = %v, the list says %v", s, got, want)
	}
	if got, want := HomeSecretAbove(s, false), o.above[s]; got != want {
		t.Errorf("HomeSecretAbove(%q, false) = %v, the list says %v", s, got, want)
	}
	if got, want := HomeSecretAbove(s, true), o.aboveLower[strings.ToLower(s)]; got != want {
		t.Errorf("HomeSecretAbove(%q, true) = %v, the list says %v", s, got, want)
	}
}

func TestHomeLookupAnswersAsTheListDid(t *testing.T) {
	for _, p := range lists(t).homePaths {
		homeSame(t, p)
		homeSame(t, strings.ToUpper(p))
		homeSame(t, strings.ToLower(p))
		homeSame(t, p+"/x")
		homeSame(t, p+"x")
		homeSame(t, "x/"+p)
		homeSame(t, p[:len(p)-1])
		homeSame(t, " "+p)
	}
	for _, s := range []string{"", "/", ".config", "Library", "AppData/Local", "appdata/local/microsoft"} {
		homeSame(t, s)
	}
}

func FuzzHomeLookupAnswersAsTheListDid(f *testing.F) {
	f.Add(".ssh")
	f.Add(".SSH")
	f.Add("Library/Application Support/Google/Chrome")
	f.Add("appdata/local/microsoft")
	f.Add(".config")
	f.Add(".config/gh/hosts.yml")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 200 {
			return
		}
		homeSame(t, s)
	})
}
