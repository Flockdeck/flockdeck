package ghcli

import "testing"

func TestParseVisibility(t *testing.T) {
	for in, want := range map[string]string{
		`{"visibility":"PRIVATE"}`:  "private",
		`{"visibility":"PUBLIC"}`:   "public",
		`{"visibility":"INTERNAL"}`: "internal",
		`{}`:                        "",
	} {
		got, err := parseVisibility(in)
		if err != nil || got != want {
			t.Errorf("parseVisibility(%s) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := parseVisibility("not json"); err == nil {
		t.Error("damaged output was accepted")
	}
}

// With gh missing the answer is an error, which callers read as not known to
// be private.
func TestVisibilityWithoutGhIsAnError(t *testing.T) {
	old := binName
	binName = "flockdeck-test-no-such-gh"
	defer func() { binName = old }()
	if v, err := Visibility(t.TempDir()); err == nil {
		t.Errorf("Visibility = %q with no gh, want an error", v)
	}
}
