package record

import (
	"strings"
	"testing"
)

// A value of no recognisable shape, registered, is redacted wherever it is
// said, including with an invisible character inside it; an unregistered one
// of the same shape is not.
func TestARegisteredSecretIsRedactedWhateverItsShape(t *testing.T) {
	const v, other = "plainword-7Qz93kLmWx", "plainword-0000000000"
	text := "env says GW=" + v + " and again " + v + "."
	if got := Redact(text); !strings.Contains(got, v) {
		t.Skip("the shape is recognised without registration; pick another value")
	}
	RegisterSecret(v)
	got := Redact(text)
	if strings.Contains(got, v) || strings.Count(got, Redacted) < 2 {
		t.Errorf("Redact = %q", got)
	}
	if got := Redact("x " + v[:6] + "​" + v[6:]); strings.Contains(got, v[6:]) {
		t.Errorf("a value with an invisible character inside survived: %q", got)
	}
	if got := Redact("keep " + other); got != "keep "+other {
		t.Errorf("an unregistered value changed: %q", got)
	}
}

func TestShortOrRepeatedSecretsAreNotRegistered(t *testing.T) {
	RegisterSecret("abc")
	RegisterSecret("   ")
	if got := Redact("abc def"); got != "abc def" {
		t.Errorf("a short value was redacted: %q", got)
	}
	for i := 0; i < 3*maxKnownSecrets; i++ {
		RegisterSecret("filler-value-" + strings.Repeat("x", i%7) + string(rune('A'+i%26)) + string(rune('a'+i/26)))
	}
	knownMu.Lock()
	n := len(knownList)
	knownMu.Unlock()
	if n > maxKnownSecrets {
		t.Errorf("registry holds %d", n)
	}
}
