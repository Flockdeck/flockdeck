package selfupdate

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
		why                string
	}{
		{"v1.4.0", "v1.3.9", true, "a later patch line"},
		{"v1.4.1", "v1.4.0", true, "a later patch"},
		{"v2.0.0", "v1.99.99", true, "a later major"},
		{"v1.4.0", "v1.4.0", false, "the version already running"},
		{"v1.3.0", "v1.4.0", false, "an older release"},
		{"1.4.0", "1.3.0", true, "tags without the v prefix"},

		// A pre-release sorts before the release it leads to, both ways round.
		{"v1.4.0", "v1.4.0-rc.1", true, "the release beats its candidate"},
		{"v1.4.0-rc.1", "v1.4.0", false, "a candidate does not replace the release"},
		{"v1.4.0-rc.2", "v1.4.0-rc.1", true, "a later candidate"},
		{"v1.4.0-rc.10", "v1.4.0-rc.9", true, "candidates are counted, not sorted as text"},
		{"v1.4.0-rc.2", "v1.4.0-rc.10", false, "an earlier candidate with fewer digits"},
		{"v1.4.0-rc.1.1", "v1.4.0-rc.1", true, "one more part after equal ones"},
		{"v1.4.0-beta", "v1.4.0-alpha", true, "words sort as text"},
		{"v1.4.0-alpha", "v1.4.0-1", true, "a word sorts after a number"},

		// Build metadata does not order a version.
		{"v1.4.0+build.9", "v1.4.0", false, "build metadata alone is not newer"},

		// An unreadable version on either side means there is nothing to
		// compare, which is what keeps a local build in place.
		{"v1.4.0", "dev", false, "an untagged local build is never behind"},
		{"dev", "v1.4.0", false, "an unreadable candidate is never ahead"},
		{"", "v1.4.0", false, "an empty candidate"},
		{"v1.4.0", "", false, "an empty current version"},
		{"vNope", "v1.0.0", false, "a candidate that is not a version"},

		// What `git describe` stamps on a local build is not a pre-release of
		// the tag it came after, so the tag is not newer than it.
		{"v1.4.0", "v1.4.0-3-gdeadbee", false, "a build three commits past its tag"},
		{"v1.4.0", "v1.4.0-dirty", false, "a build of the tag with changes in the tree"},
		{"v1.4.0", "v1.4.0-3-gdeadbee-dirty", false, "both at once"},
		{"v1.4.0", "v1.4.0-rc.1-2-g1234abc", false, "a build past a candidate"},
	}

	for _, c := range cases {
		if got := Newer(c.candidate, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v (%s)", c.candidate, c.current, got, c.want, c.why)
		}
	}
}

func TestParseVersionShortForms(t *testing.T) {
	// Tags are written v1.4.0, but a two- or one-part tag should still order
	// rather than be thrown away as unreadable.
	for _, s := range []string{"v1.4", "v1"} {
		if _, ok := parseVersion(s); !ok {
			t.Errorf("parseVersion(%q) refused a short version", s)
		}
	}
	if !Newer("v1.5", "v1.4.9") {
		t.Error("a short version did not order against a full one")
	}
}
