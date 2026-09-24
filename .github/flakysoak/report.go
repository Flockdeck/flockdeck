package main

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
)

// IssueTitle is the one tracking issue's title. The workflow finds the issue
// by this and by its label, never by anything a test printed.
const IssueTitle = "Flaky tests"

// Label is the label the tracking issue carries.
const Label = "flaky-tests"

// State is everything the tracking issue remembers between nights. It lives
// in the issue's own body, in a hidden comment, so there is no second store
// to keep in step with it.
type State struct {
	Entries     []Entry    `json:"entries"`
	Resolved    []Resolved `json:"resolved,omitempty"`
	CleanNights int        `json:"cleanNights"`
}

// Entry is one flaky test, deduplicated by package and name across nights.
type Entry struct {
	Package string            `json:"package"`
	Test    string            `json:"test"`
	Seed    string            `json:"seed,omitempty"`
	Excerpt string            `json:"excerpt"`
	OS      map[string]OSStat `json:"os"`
}

// OSStat is what one test has done on one OS since the issue last had no
// open flakes.
type OSStat struct {
	// Failed and Runs are from the latest night it failed.
	Failed      int    `json:"failed"`
	Runs        int    `json:"runs"`
	Nights      int    `json:"nights"`
	FirstSeen   string `json:"firstSeen"`
	LastSeen    string `json:"lastSeen"`
	FirstRunURL string `json:"firstRunURL"`
}

// Resolved is a test that was flaky and then stayed clean long enough for the
// issue to close. It is kept so that a return is recognised as one.
type Resolved struct {
	Package   string `json:"package"`
	Test      string `json:"test"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
}

// Night identifies the run being reported.
type Night struct {
	Date   string // YYYY-MM-DD, UTC
	RunURL string
}

// Input is everything Decide needs; it does no I/O.
type Input struct {
	Prev       *State // nil when there is no issue, or its state is unreadable
	Exists     bool
	Open       bool
	Results    []Result
	Expect     []string // every OS that ought to have reported
	Night      Night
	CloseAfter int
}

// Outcome says what the workflow should do to the tracking issue.
type Outcome struct {
	// Action is one of none, create, update, reopen, close.
	Action  string `json:"action"`
	Title   string `json:"title"`
	Body    string `json:"-"`
	Comment string `json:"-"`
}

const (
	stateOpen  = "<!-- flakysoak-state"
	stateClose = "-->"
	maxBody    = 60000
)

// ParseState reads the state back out of an issue body.
func ParseState(body string) (State, bool) {
	i := strings.Index(body, stateOpen)
	if i < 0 {
		return State{}, false
	}
	rest := body[i+len(stateOpen):]
	j := strings.Index(rest, stateClose)
	if j < 0 {
		return State{}, false
	}
	var st State
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:j])), &st); err != nil {
		return State{}, false
	}
	return st, true
}

type newFlake struct {
	pkg, test, os string
	failed, runs  int
	returned      bool
}

// Decide works out what the night means for the tracking issue.
func Decide(in Input) Outcome {
	var st State
	if in.Prev != nil {
		st = *in.Prev
	}
	reported := map[string]bool{}
	anyFailure := false
	for _, r := range in.Results {
		// A job that ran no tests says nothing about flakiness.
		if r.Tests > 0 || len(r.Failures) > 0 {
			reported[r.OS] = true
		}
		if len(r.Failures) > 0 {
			anyFailure = true
		}
	}
	var missing []string
	for _, o := range in.Expect {
		if !reported[o] {
			missing = append(missing, o)
		}
	}

	if anyFailure {
		news := merge(&st, in.Results, in.Night)
		st.CleanNights = 0
		out := Outcome{Title: IssueTitle, Body: render(st, in, missing), Comment: newComment(news, in.Night)}
		switch {
		case !in.Exists:
			out.Action, out.Comment = "create", ""
		case !in.Open:
			out.Action = "reopen"
		default:
			out.Action = "update"
		}
		return out
	}

	// A night is clean only if every OS reported and none failed. A job that
	// never got as far as running tests says nothing about flakiness.
	if len(missing) > 0 || len(in.Results) == 0 || !in.Exists || !in.Open {
		return Outcome{Action: "none", Title: IssueTitle}
	}
	st.CleanNights++
	if st.CleanNights >= in.CloseAfter {
		for _, e := range st.Entries {
			st.Resolved = append(st.Resolved, resolvedOf(e))
		}
		st.Entries = nil
		st.CleanNights = 0
		return Outcome{
			Action: "close", Title: IssueTitle,
			Body: render(st, in, nil),
			Comment: fmt.Sprintf("Clean on every OS for %d nights in a row (latest: %s), so closing. It reopens by itself if a flake comes back.",
				in.CloseAfter, in.Night.RunURL),
		}
	}
	return Outcome{Action: "update", Title: IssueTitle, Body: render(st, in, nil)}
}

func resolvedOf(e Entry) Resolved {
	r := Resolved{Package: e.Package, Test: e.Test}
	for _, s := range e.OS {
		if r.FirstSeen == "" || s.FirstSeen < r.FirstSeen {
			r.FirstSeen = s.FirstSeen
		}
		if s.LastSeen > r.LastSeen {
			r.LastSeen = s.LastSeen
		}
	}
	return r
}

func merge(st *State, results []Result, n Night) []newFlake {
	var news []newFlake
	for _, r := range results {
		for _, f := range r.Failures {
			var e *Entry
			for i := range st.Entries {
				if st.Entries[i].Package == f.Package && st.Entries[i].Test == f.Test {
					e = &st.Entries[i]
				}
			}
			if e == nil {
				st.Entries = append(st.Entries, Entry{Package: f.Package, Test: f.Test, Seed: f.Seed, Excerpt: f.Excerpt, OS: map[string]OSStat{}})
				e = &st.Entries[len(st.Entries)-1]
			}
			s, seen := e.OS[r.OS]
			if !seen {
				returned := false
				for _, old := range st.Resolved {
					if old.Package == f.Package && old.Test == f.Test {
						returned = true
					}
				}
				news = append(news, newFlake{f.Package, f.Test, r.OS, f.Failed, f.Runs, returned})
				s = OSStat{FirstSeen: n.Date, FirstRunURL: n.RunURL}
			}
			if s.LastSeen != n.Date {
				s.Nights++
			}
			s.Failed, s.Runs, s.LastSeen = f.Failed, f.Runs, n.Date
			e.OS[r.OS] = s
		}
	}
	sort.Slice(st.Entries, func(i, j int) bool {
		a, b := st.Entries[i], st.Entries[j]
		return a.Package+"\x00"+a.Test < b.Package+"\x00"+b.Test
	})
	return news
}

func newComment(news []newFlake, n Night) string {
	if len(news) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "New on the night of %s ([run](%s)):\n\n", n.Date, n.RunURL)
	for _, f := range news {
		suffix := ""
		if f.returned {
			suffix = " (it had been cleared before; it is back)"
		}
		fmt.Fprintf(&b, "- `%s` on %s: %d of %d runs failed%s\n", short(f.pkg, f.test), f.os, f.failed, f.runs, suffix)
	}
	return b.String()
}

// short names a test the way a person would look for it: the package's last
// two path elements and the test.
func short(pkg, test string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	if test == PackageFailed {
		return strings.Join(parts, "/") + " " + test
	}
	return strings.Join(parts, "/") + "." + test
}

// cell makes log text safe for a markdown table cell: nothing in it can start
// markup, mention anyone, or end the cell.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 110 {
		s = strings.ToValidUTF8(s[:110], "") + "..."
	}
	s = html.EscapeString(s)
	return strings.NewReplacer("|", "&#124;", "@", "&#64;", "`", "&#96;", "[", "&#91;", "]", "&#93;", "\\", "&#92;", "://", "&#58;//").Replace(s)
}

// fence wraps text in a code fence longer than any run of backticks in it.
func fence(text string) string {
	n, run := 3, 0
	for _, c := range text {
		if c == '`' {
			run++
			if run >= n {
				n = run + 1
			}
		} else {
			run = 0
		}
	}
	f := strings.Repeat("`", n)
	return f + "text\n" + text + "\n" + f
}

// oneLine picks the line of an excerpt that says what went wrong.
func oneLine(ex string) string {
	lines := strings.Split(ex, "\n")
	for _, l := range lines {
		if strings.Contains(l, "_test.go:") || strings.HasPrefix(l, "panic:") || strings.HasPrefix(l, "WARNING: DATA RACE") {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(lines[0])
}

func render(st State, in Input, missing []string) string {
	var b strings.Builder
	b.WriteString("Found by the nightly flaky-test soak (`.github/workflows/flaky-soak.yml`), which runs the concurrency- and timer-heavy packages under `go test -race -shuffle=on -count=N` on every OS. This issue is kept up to date by that workflow: it is edited each night something fails, commented on only when a test or OS appears that was not here before, and closed after a few clean nights.\n\n")
	fmt.Fprintf(&b, "Last updated %s: [run](%s).\n\n", in.Night.Date, in.Night.RunURL)
	if len(missing) > 0 {
		fmt.Fprintf(&b, "> This night's results are incomplete: no result from %s.\n\n", strings.Join(missing, ", "))
	}

	if len(st.Entries) == 0 {
		b.WriteString("No open flakes.\n\n")
	} else {
		b.WriteString("| Test | OS | Failed of runs (latest night) | Nights failed | First seen | What it printed |\n|---|---|---|---|---|---|\n")
		for _, e := range st.Entries {
			oses := make([]string, 0, len(e.OS))
			for o := range e.OS {
				oses = append(oses, o)
			}
			sort.Strings(oses)
			for _, o := range oses {
				s := e.OS[o]
				always := ""
				if s.Failed == s.Runs && s.Runs > 1 {
					always = " (every run: broken rather than flaky?)"
				}
				fmt.Fprintf(&b, "| `%s` | %s | %d of %d%s | %d | [%s](%s) | %s |\n",
					short(e.Package, e.Test), o, s.Failed, s.Runs, always, s.Nights, s.FirstSeen, s.FirstRunURL, cell(oneLine(e.Excerpt)))
			}
		}
		b.WriteString("\nA failure that depends on test order can be reproduced with the shuffle seed shown below: `go test -race -shuffle=<seed> -count=N <package>`.\n\n")
		b.WriteString("<details><summary>Log excerpts (from the first failure of each test)</summary>\n\n")
		for _, e := range st.Entries {
			seed := ""
			if e.Seed != "" {
				seed = ", shuffle seed " + e.Seed
			}
			fmt.Fprintf(&b, "**`%s`** (`%s`%s)\n\n%s\n\n", short(e.Package, e.Test), e.Package, seed, fence(e.Excerpt))
		}
		b.WriteString("</details>\n\n")
		fmt.Fprintf(&b, "Clean nights in a row: %d of %d needed to close.\n\n", st.CleanNights, in.CloseAfter)
	}
	if len(st.Resolved) > 0 {
		b.WriteString("<details><summary>Cleared earlier</summary>\n\n")
		for _, r := range st.Resolved {
			fmt.Fprintf(&b, "- `%s` (%s to %s)\n", short(r.Package, r.Test), r.FirstSeen, r.LastSeen)
		}
		b.WriteString("\n</details>\n\n")
	}

	sj, _ := json.Marshal(st)
	state := stateOpen + "\n" + string(sj) + "\n" + stateClose + "\n"
	body := b.String()
	if len(body)+len(state) > maxBody {
		// Too many flakes to show at length: keep the table, drop the
		// excerpts, which the first failing run still has.
		if i := strings.Index(body, "<details><summary>Log excerpts"); i >= 0 {
			j := strings.Index(body[i:], "</details>")
			body = body[:i] + "(Log excerpts left out: too many failing tests for one issue. See the run.)\n\n" + body[i+j+len("</details>\n\n"):]
		}
	}
	return body + state
}
