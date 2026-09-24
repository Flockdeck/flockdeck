package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// event is one line of `go test -json` (test2json's TestEvent).
type event struct {
	Action  string
	Package string
	Test    string
	Output  string
	// ImportPath, not Package, names the package in a build-output event.
	ImportPath string
}

// Failure is one test that failed at least once in a night's runs on one OS.
type Failure struct {
	Package string `json:"package"`
	// Test is the leaf test that failed (a failing subtest, not the parent
	// that only failed because of it). For a package that failed without any
	// test failing (build error, timeout, crash) it is PackageFailed.
	Test    string `json:"test"`
	Failed  int    `json:"failed"`
	Runs    int    `json:"runs"`
	Seed    string `json:"seed,omitempty"`
	Excerpt string `json:"excerpt"`
}

// Result is what one OS's soak saw.
type Result struct {
	OS       string    `json:"os"`
	Count    int       `json:"count"`
	Tests    int       `json:"tests"`
	Failures []Failure `json:"failures"`
	// Unparsed counts lines that were not JSON events, which a healthy
	// stream has none of.
	Unparsed int `json:"unparsed,omitempty"`
}

// PackageFailed names a package-level failure in Failure.Test.
const PackageFailed = "(package failed before or outside any test)"

const (
	maxExcerptLines = 14
	maxExcerptBytes = 700
	pkgTail         = 30
)

type counts struct {
	runs, failed int
	buf          []string
	excerpt      string
}

var shuffleRe = regexp.MustCompile(`^-test\.shuffle (\d+)`)

// Parse reads a `go test -json` stream and works out exactly which tests
// failed and how many of their runs did. It never fails on odd input: a
// stream cut short (a timeout kills go test mid-write) still yields what was
// seen up to that point.
func Parse(r io.Reader, osName string, count int) (Result, error) {
	res := Result{OS: osName, Count: count, Failures: []Failure{}}
	tests := map[string]*counts{}   // package + "\x00" + test
	order := []string{}             // first-seen order, for stable output
	pkgOut := map[string][]string{} // package-level output tail
	pkgFail := map[string]bool{}    // package reported fail
	seeds := map[string]string{}    // package -> shuffle seed
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Action == "" {
			res.Unparsed++
			continue
		}
		if ev.Action == "build-output" {
			ev.Action, ev.Package = "output", strings.Fields(ev.ImportPath + " ")[0]
		}
		if ev.Action == "build-fail" || ev.Action == "build-start" {
			continue
		}
		if ev.Test == "" {
			switch ev.Action {
			case "output":
				out := clean(ev.Output)
				if m := shuffleRe.FindStringSubmatch(out); m != nil {
					seeds[ev.Package] = m[1]
				}
				pkgOut[ev.Package] = append(pkgOut[ev.Package], out)
				if n := len(pkgOut[ev.Package]); n > pkgTail*2 {
					pkgOut[ev.Package] = pkgOut[ev.Package][n-pkgTail:]
				}
			case "fail":
				pkgFail[ev.Package] = true
			}
			continue
		}
		key := ev.Package + "\x00" + ev.Test
		c := tests[key]
		if c == nil {
			c = &counts{}
			tests[key] = c
			order = append(order, key)
		}
		switch ev.Action {
		case "run":
			c.buf = c.buf[:0]
		case "output":
			c.buf = append(c.buf, clean(ev.Output))
			if len(c.buf) > 400 {
				c.buf = c.buf[len(c.buf)-200:]
			}
		case "pass":
			c.runs++
		case "fail":
			c.runs++
			c.failed++
			if c.excerpt == "" {
				c.excerpt = excerpt(c.buf)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}

	// A parent test fails because a subtest did; report the subtest.
	failedNames := map[string]bool{}
	for k, c := range tests {
		if c.failed > 0 {
			failedNames[k] = true
		}
	}
	hasFailingChild := func(k string) bool {
		for other := range failedNames {
			if strings.HasPrefix(other, k+"/") {
				return true
			}
		}
		return false
	}
	pkgHasTestFailure := map[string]bool{}
	for _, k := range order {
		c := tests[k]
		pkg, name, _ := strings.Cut(k, "\x00")
		if c.runs > 0 {
			res.Tests++
		}
		if c.failed == 0 {
			continue
		}
		pkgHasTestFailure[pkg] = true
		if hasFailingChild(k) {
			continue
		}
		res.Failures = append(res.Failures, Failure{
			Package: pkg, Test: name, Failed: c.failed, Runs: c.runs,
			Seed: seeds[pkg], Excerpt: c.excerpt,
		})
	}
	var pkgs []string
	for p := range pkgFail {
		if !pkgHasTestFailure[p] {
			pkgs = append(pkgs, p)
		}
	}
	sort.Strings(pkgs)
	for _, p := range pkgs {
		res.Failures = append(res.Failures, Failure{
			Package: p, Test: PackageFailed, Failed: 1, Runs: 1,
			Seed: seeds[p], Excerpt: excerpt(pkgOut[p]),
		})
	}
	return res, nil
}

func clean(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimRight(s, "\n")
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	// Lines that only frame the output and say nothing about why.
	noiseRe = regexp.MustCompile(`^(=== (RUN|PAUSE|CONT|NAME)\b|\s*--- (PASS|FAIL|SKIP)\b|PASS$|FAIL$|FAIL\s|ok\s)`)
)

// excerpt keeps the lines of a failing test's output that say why it failed,
// capped so that a chatty test cannot fill an issue.
func excerpt(lines []string) string {
	var keep []string
	for _, chunk := range lines {
		for _, l := range strings.Split(chunk, "\n") {
			l = strings.TrimRight(ansiRe.ReplaceAllString(l, ""), " \t")
			if strings.TrimSpace(l) == "" || noiseRe.MatchString(l) {
				continue
			}
			keep = append(keep, l)
		}
	}
	// The reason is usually at the end of a long output, but a data race
	// or panic starts with its own marker: prefer from there when present.
	for i, l := range keep {
		if strings.HasPrefix(l, "WARNING: DATA RACE") || strings.HasPrefix(l, "panic:") {
			keep = keep[i:]
			break
		}
	}
	if len(keep) > maxExcerptLines {
		keep = append(keep[:maxExcerptLines-1:maxExcerptLines-1], "...")
	}
	out := strings.Join(keep, "\n")
	if len(out) > maxExcerptBytes {
		out = strings.ToValidUTF8(out[:maxExcerptBytes], "") + "\n..."
	}
	return out
}

// addExitFailure records a go test that exited non-zero without any failure
// having been named in its stream (the toolchain could not be fetched, the
// stream was cut off, a flag was wrong): a night like that is not a clean one.
func addExitFailure(res *Result, exit int, stderr string) {
	if exit == 0 || len(res.Failures) > 0 {
		return
	}
	ex := excerpt([]string{stderr})
	if ex == "" {
		ex = fmt.Sprintf("go test exited with status %d and printed no failure", exit)
	}
	res.Failures = append(res.Failures, Failure{Package: "go test", Test: PackageFailed, Failed: 1, Runs: 1, Excerpt: ex})
}
