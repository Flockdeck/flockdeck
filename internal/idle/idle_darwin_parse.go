package idle

import (
	"regexp"
	"strconv"
	"time"
)

// hidIdleTimeRe picks the nanosecond figure out of ioreg's output, e.g.:
//
//	+-o IOHIDSystem  <class IOHIDSystem, id 0x100000275, registered, matched, active, busy 0 (0 retain), last busy 0>
//	    {
//	      "HIDIdleTime" = 68130403958
//	    }
//
// ioreg has printed it both quoted with a space around "=" and, on some
// releases, run together as `"HIDIdleTime"=68130403958,`, so both are
// matched.
var hidIdleTimeRe = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)

// parseHIDIdleTime is Since's parser, kept apart from the command it reads so
// it can be tested on any platform against sample output, not only on a Mac.
func parseHIDIdleTime(output string) (time.Duration, bool) {
	m := hidIdleTimeRe.FindStringSubmatch(output)
	if m == nil {
		return 0, false
	}
	ns, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(ns) * time.Nanosecond, true
}
