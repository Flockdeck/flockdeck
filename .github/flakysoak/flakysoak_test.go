package main

import (
	"os"
	"strings"
	"testing"
)

const pkg = "github.com/jmwri/flockdeck/internal/server"

func parseFixture(t *testing.T, name, osName string, count int) Result {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := Parse(f, osName, count)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func byTest(res Result) map[string]Failure {
	m := map[string]Failure{}
	for _, f := range res.Failures {
		m[f.Test] = f
	}
	return m
}

// failing.jsonl is a real `go test -shuffle=on -count=6 -json` stream: one
// test that fails on runs 2 and 5, one subtest that fails on runs 1 and 5, and one
// that always passes.
func TestARealFailingStreamNamesExactlyTheTestsAndHowManyRunsFailed(t *testing.T) {
	res := parseFixture(t, "failing.jsonl", "ubuntu-latest", 6)
	got := byTest(res)
	if len(got) != 2 {
		t.Fatalf("want 2 failing tests, got %d: %+v", len(got), res.Failures)
	}
	if f := got["TestSometimesFails"]; f.Failed != 2 || f.Runs != 6 || f.Package != pkg {
		t.Errorf("TestSometimesFails = %+v, want 2 of 6", f)
	}
	if !strings.Contains(got["TestSometimesFails"].Excerpt, `status write dropped: want "settled", got "running"`) {
		t.Errorf("excerpt lost the reason: %q", got["TestSometimesFails"].Excerpt)
	}
	if f := got["TestSubtestsOneFlaky/case-1"]; f.Failed != 2 || f.Runs != 6 {
		t.Errorf("subtest = %+v, want 2 of 6", f)
	}
	if _, parent := got["TestSubtestsOneFlaky"]; parent {
		t.Error("the parent of a failing subtest is reported as well as the subtest")
	}
	if f := got["TestSometimesFails"]; f.Seed == "" {
		t.Error("the shuffle seed was not kept")
	}
	// TestAlwaysFine, TestSometimesFails, TestSubtestsOneFlaky and its two subtests.
	if res.Tests != 5 {
		t.Errorf("tests = %d, want 5", res.Tests)
	}
	if res.Unparsed != 0 {
		t.Errorf("%d lines unparsed", res.Unparsed)
	}
}

func TestACleanStreamHasNoFailures(t *testing.T) {
	res := parseFixture(t, "clean.jsonl", "macos-latest", 3)
	if len(res.Failures) != 0 {
		t.Fatalf("failures = %+v", res.Failures)
	}
}

// A package that does not build has no test to blame; it is named itself,
// with the compiler's message.
func TestABuildFailureIsReportedAgainstThePackage(t *testing.T) {
	res := parseFixture(t, "build-failure.jsonl", "windows-latest", 1)
	if len(res.Failures) != 1 {
		t.Fatalf("failures = %+v", res.Failures)
	}
	f := res.Failures[0]
	if f.Package != pkg || f.Test != PackageFailed {
		t.Errorf("failure = %+v", f)
	}
	if !strings.Contains(f.Excerpt, "cannot use") {
		t.Errorf("excerpt lacks the compiler message: %q", f.Excerpt)
	}
}

func TestAStreamCutShortAndNoiseAreTolerated(t *testing.T) {
	in := `not json at all
{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"output","Package":"p","Test":"TestA","Output":"a_test.go:1: boom\r\n"}
{"Action":"fail","Package":"p","Test":"TestA"}
{"Action":"run","Package":"p","Test":"TestB"}
{"Action":"output","Package":"p","Test":"TestB","Output":"partial`
	res, err := Parse(strings.NewReader(in), "x", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failures) != 1 || res.Failures[0].Test != "TestA" || res.Failures[0].Excerpt != "a_test.go:1: boom" {
		t.Errorf("failures = %+v", res.Failures)
	}
	if res.Unparsed != 2 {
		t.Errorf("unparsed = %d, want 2", res.Unparsed)
	}
}

// A data race is printed by the race detector into the running test's
// output; the excerpt starts at it rather than at the noise before it.
func TestADataRaceExcerptStartsAtTheReport(t *testing.T) {
	in := `{"Action":"run","Package":"p","Test":"TestR"}
{"Action":"output","Package":"p","Test":"TestR","Output":"=== RUN   TestR\n"}
{"Action":"output","Package":"p","Test":"TestR","Output":"some setup log\n"}
{"Action":"output","Package":"p","Test":"TestR","Output":"WARNING: DATA RACE\n"}
{"Action":"output","Package":"p","Test":"TestR","Output":"Write at 0x00c0 by goroutine 8:\n"}
{"Action":"output","Package":"p","Test":"TestR","Output":"    testing.go:1490: race detected during execution of test\n"}
{"Action":"fail","Package":"p","Test":"TestR"}`
	res, _ := Parse(strings.NewReader(in), "ubuntu-latest", 1)
	if len(res.Failures) != 1 || !strings.HasPrefix(res.Failures[0].Excerpt, "WARNING: DATA RACE") {
		t.Errorf("failures = %+v", res.Failures)
	}
}

func failed(test, os string, failed, runs int) Result {
	return Result{OS: os, Count: runs, Failures: []Failure{{Package: pkg, Test: test, Failed: failed, Runs: runs, Excerpt: "x_test.go:9: got 1"}}}
}

var allOS = []string{"ubuntu-latest", "macos-latest", "windows-latest"}

func passed(os string) Result { return Result{OS: os, Count: 5, Tests: 40, Failures: []Failure{}} }

func night(d string) Night { return Night{Date: d, RunURL: "https://example.test/runs/" + d} }

func TestTheFirstFailureCreatesTheIssueWithoutAComment(t *testing.T) {
	o := Decide(Input{Results: []Result{failed("TestX", "macos-latest", 2, 5), passed("ubuntu-latest"), passed("windows-latest")}, Expect: allOS, Night: night("2026-09-25"), CloseAfter: 3})
	if o.Action != "create" || o.Comment != "" {
		t.Fatalf("action=%q comment=%q", o.Action, o.Comment)
	}
	for _, want := range []string{"| `internal/server.TestX` | macos-latest | 2 of 5 | 1 | [2026-09-25](https://example.test/runs/2026-09-25) |", "x_test.go:9: got 1", "flakysoak-state"} {
		if !strings.Contains(o.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, o.Body)
		}
	}
}

func TestTheSameTestOnTheNextNightIsEditedInSilently(t *testing.T) {
	first := Decide(Input{Results: []Result{failed("TestX", "macos-latest", 2, 5), passed("ubuntu-latest"), passed("windows-latest")}, Expect: allOS, Night: night("2026-09-25"), CloseAfter: 3})
	st, ok := ParseState(first.Body)
	if !ok {
		t.Fatal("state did not round-trip")
	}
	second := Decide(Input{Prev: &st, Exists: true, Open: true, Results: []Result{failed("TestX", "macos-latest", 1, 5), passed("ubuntu-latest"), passed("windows-latest")}, Expect: allOS, Night: night("2026-09-26"), CloseAfter: 3})
	if second.Action != "update" || second.Comment != "" {
		t.Fatalf("action=%q comment=%q", second.Action, second.Comment)
	}
	st2, _ := ParseState(second.Body)
	s := st2.Entries[0].OS["macos-latest"]
	if len(st2.Entries) != 1 || s.Nights != 2 || s.FirstSeen != "2026-09-25" || s.Failed != 1 || s.FirstRunURL != "https://example.test/runs/2026-09-25" {
		t.Errorf("state = %+v", st2)
	}
}

func TestANewTestOrANewOSIsCommentedOn(t *testing.T) {
	st := State{Entries: []Entry{{Package: pkg, Test: "TestX", OS: map[string]OSStat{"macos-latest": {Failed: 1, Runs: 5, Nights: 1, FirstSeen: "2026-09-25", LastSeen: "2026-09-25"}}}}}
	o := Decide(Input{Prev: &st, Exists: true, Open: true, Results: []Result{failed("TestX", "windows-latest", 3, 5), failed("TestY", "ubuntu-latest", 1, 5), passed("macos-latest")}, Expect: allOS, Night: night("2026-09-27"), CloseAfter: 3})
	if o.Action != "update" {
		t.Fatalf("action = %q", o.Action)
	}
	for _, want := range []string{"`internal/server.TestX` on windows-latest: 3 of 5", "`internal/server.TestY` on ubuntu-latest: 1 of 5"} {
		if !strings.Contains(o.Comment, want) {
			t.Errorf("comment lacks %q:\n%s", want, o.Comment)
		}
	}
}

func TestCleanNightsCloseTheIssueAndAReturnReopensIt(t *testing.T) {
	st := State{Entries: []Entry{{Package: pkg, Test: "TestX", OS: map[string]OSStat{"macos-latest": {Failed: 1, Runs: 5, Nights: 1, FirstSeen: "2026-09-25", LastSeen: "2026-09-25"}}}}}
	var o Outcome
	for i, d := range []string{"2026-09-26", "2026-09-27", "2026-09-28"} {
		o = Decide(Input{Prev: &st, Exists: true, Open: true, Results: []Result{passed("ubuntu-latest"), passed("macos-latest"), passed("windows-latest")}, Expect: allOS, Night: night(d), CloseAfter: 3})
		next, _ := ParseState(o.Body)
		if i < 2 {
			if o.Action != "update" || next.CleanNights != i+1 {
				t.Fatalf("night %d: action=%q clean=%d", i, o.Action, next.CleanNights)
			}
		}
		st = next
	}
	if o.Action != "close" || !strings.Contains(o.Comment, "3 nights in a row") {
		t.Fatalf("action=%q comment=%q", o.Action, o.Comment)
	}
	if len(st.Entries) != 0 || len(st.Resolved) != 1 {
		t.Fatalf("state after close = %+v", st)
	}
	back := Decide(Input{Prev: &st, Exists: true, Open: false, Results: []Result{failed("TestX", "macos-latest", 1, 5), passed("ubuntu-latest"), passed("windows-latest")}, Expect: allOS, Night: night("2026-10-02"), CloseAfter: 3})
	if back.Action != "reopen" || !strings.Contains(back.Comment, "it is back") {
		t.Fatalf("action=%q comment=%q", back.Action, back.Comment)
	}
}

func TestAnIncompleteNightIsNeitherCleanNorFailing(t *testing.T) {
	st := State{Entries: []Entry{{Package: pkg, Test: "TestX", OS: map[string]OSStat{"macos-latest": {Failed: 1, Runs: 5}}}}, CleanNights: 2}
	o := Decide(Input{Prev: &st, Exists: true, Open: true, Results: []Result{passed("ubuntu-latest"), passed("macos-latest")}, Expect: allOS, Night: night("2026-09-28"), CloseAfter: 3})
	if o.Action != "none" {
		t.Fatalf("action = %q: a night with a job missing closed or edited the issue", o.Action)
	}
	if none := Decide(Input{Expect: allOS, Night: night("2026-09-28"), CloseAfter: 3}); none.Action != "none" {
		t.Errorf("no results and no issue: action = %q", none.Action)
	}
}

// Whatever a test printed ends up in a table cell or a code fence; it must
// not be able to ping anyone, break the table, or close the state comment.
func TestLogTextCannotEscapeItsCell(t *testing.T) {
	bad := Failure{Package: pkg, Test: "TestX", Failed: 1, Runs: 2,
		Excerpt: "x_test.go:1: @octocat | <script>alert(1)</script> `` ``` --> [click](http://evil)\nsecond line"}
	o := Decide(Input{Results: []Result{{OS: "ubuntu-latest", Failures: []Failure{bad}}}, Expect: []string{"ubuntu-latest"}, Night: night("2026-09-25"), CloseAfter: 3})
	row := ""
	for _, l := range strings.Split(o.Body, "\n") {
		if strings.HasPrefix(l, "| `internal/server.TestX`") {
			row = l
		}
	}
	if row == "" {
		t.Fatal("no table row")
	}
	for _, bad := range []string{"@octocat", "<script>", "[click]", "http://evil", "-->"} {
		if strings.Contains(row, bad) {
			t.Errorf("row still contains %q: %s", bad, row)
		}
	}
	if n := strings.Count(row, "|"); n != 7 {
		t.Errorf("row has %d pipes, want 7: %s", n, row)
	}
	st, ok := ParseState(o.Body)
	if !ok || st.Entries[0].Excerpt != bad.Excerpt {
		t.Errorf("state did not survive an excerpt containing -->: ok=%v", ok)
	}
	if strings.Count(o.Body, "flakysoak-state") != 1 {
		t.Error("more than one state marker")
	}
}

func TestParseStateOfAnIssueTypedByHand(t *testing.T) {
	if _, ok := ParseState("someone wrote this\n<!-- flakysoak-state\nnot json\n-->"); ok {
		t.Error("unreadable state reported as readable")
	}
	if _, ok := ParseState("nothing here"); ok {
		t.Error("no marker reported as readable")
	}
}

// A job that ran no tests, or one where go test died without naming a
// failure, must not be read as a clean night.
func TestANightWithNoTestsRunIsNotClean(t *testing.T) {
	st := State{Entries: []Entry{{Package: pkg, Test: "TestX", OS: map[string]OSStat{"macos-latest": {Failed: 1, Runs: 5}}}}}
	empty := Result{OS: "windows-latest", Count: 5, Failures: []Failure{}}
	o := Decide(Input{Prev: &st, Exists: true, Open: true, Results: []Result{passed("ubuntu-latest"), passed("macos-latest"), empty}, Expect: allOS, Night: night("2026-09-28"), CloseAfter: 3})
	if o.Action != "none" {
		t.Fatalf("action = %q", o.Action)
	}
}

func TestAnExitWithoutAFailureIsReportedAsOne(t *testing.T) {
	res := Result{OS: "ubuntu-latest", Count: 5, Tests: 10, Failures: []Failure{}}
	addExitFailure(&res, 2, "go: downloading go1.99 (linux/amd64)\nsomething went wrong\n")
	if len(res.Failures) != 1 || res.Failures[0].Test != PackageFailed || !strings.Contains(res.Failures[0].Excerpt, "something went wrong") {
		t.Fatalf("failures = %+v", res.Failures)
	}
	named := parseFixture(t, "failing.jsonl", "ubuntu-latest", 6)
	n := len(named.Failures)
	addExitFailure(&named, 1, "")
	if len(named.Failures) != n {
		t.Error("an exit was added on top of failures that already explain it")
	}
	ok := Result{Failures: []Failure{}}
	addExitFailure(&ok, 0, "")
	if len(ok.Failures) != 0 {
		t.Error("a zero exit was reported as a failure")
	}
}
