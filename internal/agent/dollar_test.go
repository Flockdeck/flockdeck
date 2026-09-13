package agent

import "testing"

// A "$" that begins no variable -- in a password, a price, a prompt string --
// is kept as written, and so is a variable that is not set. A variable's
// value is not read again for variables of its own.
func TestADollarThatIsNoVariableIsKeptAsWritten(t *testing.T) {
	t.Setenv("FLOCKDECK_TEST_DIR", "/opt/x")
	t.Setenv("FLOCKDECK_TEST_ODD", "a$FLOCKDECK_TEST_DIR")
	for in, want := range map[string]string{
		"pa$5word":                         "pa$5word",
		"a$$b":                             "a$$b",
		"costs $@ and ${":                  "costs $@ and ${",
		"$FLOCKDECK_TEST_DIR/bin":          "/opt/x/bin",
		"${FLOCKDECK_TEST_DIR}/bin":        "/opt/x/bin",
		"%FLOCKDECK_TEST_DIR%/bin":         "/opt/x/bin",
		"$FLOCKDECK_TEST_UNSET/bin":        "$FLOCKDECK_TEST_UNSET/bin",
		"%FLOCKDECK_TEST_ODD%":             "a$FLOCKDECK_TEST_DIR",
		"${FLOCKDECK_TEST_DIR}$5${":        "/opt/x$5${",
		"100%":                             "100%",
		"$FLOCKDECK_TEST_DIR:$FLOCKDECK_X": "/opt/x:$FLOCKDECK_X",
	} {
		if got := expandVars(in); got != want {
			t.Errorf("expandVars(%q) = %q, want %q", in, got, want)
		}
	}
}
