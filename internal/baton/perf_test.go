package baton

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// median is the middle of three timings of a scrub of in. One slow run on a busy
// machine does not fail a test; a slow one every time does.
func median(in string) time.Duration {
	var ds []time.Duration
	for i := 0; i < 3; i++ {
		start := time.Now()
		NewScrubber().Scrub(in)
		ds = append(ds, time.Since(start))
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[1]
}

// timed fails a scrub of a pathological input whose median time is over limit. The
// limits are about twice what these take on the machine they were set on, and the
// quadratic work they guard against takes minutes.
func timed(t *testing.T, name, in string, limit time.Duration) {
	t.Helper()
	d := median(in)
	t.Logf("%s: %d bytes in %s", name, len(in), d)
	if d > limit {
		t.Errorf("%s: %d bytes took %s, over %s", name, len(in), d, limit)
	}
}

func TestPathologicalInputsStayFast(t *testing.T) {
	if raceEnabled {
		t.Skip("wall-clock limits do not hold under the race detector")
	}
	timed(t, "one long line of mysql -p", strings.Repeat("mysql -pabc ", 80000), 3*time.Second)
	timed(t, "one long line of curl", strings.Repeat("curl -u a:b ", 80000), 3*time.Second)
	timed(t, "docker login -p on lines", strings.Repeat("docker login -p abc\n", 84000), 4*time.Second)
	timed(t, "many lines of mysql -p", strings.Repeat("mysql -pabc\n", 80000), 3*time.Second)
	var b strings.Builder
	for i := 0; i < 1500; i++ {
		b.WriteString(strings.Repeat(" ", i%300))
		b.WriteString("password: |\n")
	}
	timed(t, "yaml blocks with deepening indentation", b.String(), 3*time.Second)
	b.Reset()
	for i := 0; i < 20000; i++ {
		b.WriteString("key" + strings.Repeat(" ", i%40) + "password: |\n  line\n")
	}
	timed(t, "many yaml blocks", b.String(), 3*time.Second)
	timed(t, "unclosed quotes", strings.Repeat("sshpass -p '", 60000), 3*time.Second)
}

// Twice the input must cost about twice as much: a ratio past 3.5 is the quadratic
// work that a linear scan avoids.
func TestPathologicalInputsScaleLinearly(t *testing.T) {
	if raceEnabled {
		t.Skip("wall-clock limits do not hold under the race detector")
	}
	for name, f := range map[string]func(n int) string{
		"mysql on one line": func(n int) string { return strings.Repeat("mysql -pabc ", n) },
		"curl on one line":  func(n int) string { return strings.Repeat("curl -u a:b ", n) },
		"nested yaml": func(n int) string {
			var b strings.Builder
			for i := 0; i < n; i++ {
				b.WriteString(strings.Repeat(" ", i%300) + "password: |\n")
			}
			return b.String()
		},
	} {
		small, big := median(f(10000)), median(f(20000))
		if small > 20*time.Millisecond && big > 3500*small/1000 {
			t.Errorf("%s: %s for n=10000, %s for n=20000", name, small, big)
		}
	}
}

// A text of many URLs with credentials is read in time that grows with its length: the
// credentials are found once and looked up by position for each span.
func TestManyURLCredentialsAreLinear(t *testing.T) {
	if raceEnabled {
		t.Skip("wall-clock limits do not hold under the race detector")
	}
	url := func(n int) string { return strings.Repeat("https:"+"//u:p@h/ ", n) }
	timed(t, "2 MB of URLs with credentials", url(2*1048576/15), 3*time.Second)
	small, big := median(url(40000)), median(url(80000))
	if small > 20*time.Millisecond && big > 3500*small/1000 {
		t.Errorf("URL credentials: %s for 40000, %s for 80000", small, big)
	}
}

// A long line of many names and values costs time that grows with its length: what is read
// after a value to decide it is code is a bounded window, not the rest of the line.
func TestLongLinesOfNamesAndValuesScaleLinearly(t *testing.T) {
	if raceEnabled {
		t.Skip("wall-clock limits do not hold under the race detector")
	}
	for name, unit := range map[string]string{
		"minified json":     `{"key":"AbcdefGhijkl12","name":"x","id":12},`,
		"dotted names":      `a.key=b,c.pass=d;e={key:f,pw:g};`,
		"a key and a quote": `key="`,
		"brace and key":     `{key=`,
		"query and key":     `?key=`,
		"paren and pw":      `(pw:`,
		"odd spaces":        "key\u3000=\u00a0Zk3jQ9xLm2vB ",
	} {
		in := func(bytes int) string { return strings.Repeat(unit, bytes/len(unit)) }
		small, big := median(in(200000)), median(in(400000))
		t.Logf("%s: %s for 200 KB, %s for 400 KB", name, small, big)
		if big > 4*time.Second {
			t.Errorf("%s: 400 KB took %s", name, big)
		}
		if small > 20*time.Millisecond && big > 2500*small/1000 {
			t.Errorf("%s: %s for 200 KB, %s for 400 KB: more than twice as long for twice the text", name, small, big)
		}
	}
}
