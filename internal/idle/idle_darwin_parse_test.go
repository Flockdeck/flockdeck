package idle

import (
	"testing"
	"time"
)

// Real sample output from `ioreg -c IOHIDSystem -d 4 -r -k HIDIdleTime`, in
// the two forms it has been seen printed in.
const sampleIOReg = `+-o IOHIDSystem  <class IOHIDSystem, id 0x100000275, registered, matched, active, busy 0 (0 retain), last busy 0>
    {
      "HIDIdleTime" = 68130403958
    }

`

const sampleIORegTight = `+-o IOHIDSystem  <class IOHIDSystem, id 0x100000275, registered, matched, active, busy 0 (0 retain), last busy 0>
    {
      "HIDIdleTime"=1234567890
    }

`

func TestParseHIDIdleTime(t *testing.T) {
	d, ok := parseHIDIdleTime(sampleIOReg)
	if !ok {
		t.Fatal("did not parse HIDIdleTime")
	}
	if want := 68130403958 * time.Nanosecond; d != want {
		t.Errorf("got %v, want %v", d, want)
	}

	d, ok = parseHIDIdleTime(sampleIORegTight)
	if !ok || d != 1234567890*time.Nanosecond {
		t.Errorf("the run-together form: got %v, %v", d, ok)
	}

	if _, ok := parseHIDIdleTime("nothing of the kind here"); ok {
		t.Error("parsed a HIDIdleTime out of output with none")
	}
}
