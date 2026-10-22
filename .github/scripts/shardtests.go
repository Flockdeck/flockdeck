//go:build ignore

// shardtests runs one share of the module's tests, so that the whole suite can
// be split across jobs without a change to any package.
//
//	go run .github/scripts/shardtests.go -shard 2/4 -profile windows [-retry] \
//	    [-log FILE] -- -count=1 -timeout 20m
//
// Everything after -- goes to go test, which is run once for the packages that
// are not split, and once for each package that is.
//
// How the split is made. Every package with tests is a unit with a weight, in
// seconds, read from .github/test-weights.txt for the profile (linux, windows,
// macos or race). A package that weighs SPLIT_FROM seconds or more is cut into
// as many units as there are shares: a test is in part fnv32a(name) mod N, so a
// new test is placed by its name and nothing is kept in a file. The units are
// then dealt out largest first, each to the share with the least so far. The
// weights only balance the shares. A package missing from the file, or a weight
// that is out of date, makes a share slower or faster and nothing else: which
// share runs a package or a test follows from the names alone, and every share
// works it out the same way from the same commit.
//
// What it checks. Before the run it fails unless every unit is in exactly one
// share. After it, it fails unless every package it ran reported exactly once,
// and every test of a split package reported exactly once (go test -v prints
// --- PASS, FAIL or SKIP for each). A failing package or test is run once more,
// on its own, when -retry is given, and one that passes the second time is
// named in a warning, as .github/scripts/go-test-once-more.sh does.
//
// -plan prints every share and the weight it holds, and runs nothing.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	modulePath    = "github.com/jmwri/flockdeck"
	splitFrom     = 30.0 // seconds; a package this slow is cut into parts
	defaultWeight = 10.0 // seconds, for a package the weights file does not list
	minSplitTests = 8    // a package with fewer tests than this is not cut
)

var profiles = map[string]int{"linux": 0, "windows": 1, "macos": 2, "race": 3}

func main() {
	shard := flag.String("shard", "1/1", "which share to run, as I/N counting from 1")
	profile := flag.String("profile", "linux", "which column of the weights to use: linux, windows, macos or race")
	weightsPath := flag.String("weights", ".github/test-weights.txt", "the file of package weights")
	retry := flag.Bool("retry", false, "run what failed once more, on its own")
	logPath := flag.String("log", "", "also write go test's output to this file")
	plan := flag.Bool("plan", false, "print all the shares and run nothing")
	jobsMax := flag.Int("jobs", 2, "how many go test commands of the share run at once")
	digestPath := flag.String("digest", "", "write the plan's digest to this file")
	goFlags := flag.String("gotestflags", "", "flags for go test, separated by spaces (in place of the ones after --)")
	flag.Parse()
	extra := flag.Args()
	if len(extra) > 0 && extra[0] == "--" {
		extra = extra[1:]
	}
	extra = append(strings.Fields(*goFlags), extra...)
	var i, n int
	if _, err := fmt.Sscanf(*shard, "%d/%d", &i, &n); err != nil || n < 1 || i < 1 || i > n {
		fatal("-shard wants I/N with 1 <= I <= N, got %q", *shard)
	}
	col, ok := profiles[*profile]
	if !ok {
		fatal("-profile is linux, windows, macos or race, got %q", *profile)
	}
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			fatal("%v", err)
		}
		defer f.Close()
		logw = f
	}

	weights := readWeights(*weightsPath, col)
	pkgs := packages()
	if len(pkgs) == 0 {
		fatal("go list finds no package with tests")
	}
	shares := deal(pkgs, weights, n)
	if err := check(pkgs, shares); err != nil {
		fatal("%v", err)
	}
	if *plan {
		printPlan(shares)
		return
	}

	mine := shares[i-1]
	fmt.Printf("shardtests: share %d of %d (%s): %d packages whole, %d cut, about %.0f s; plan %s\n",
		i, n, *profile, len(mine.whole), len(mine.cut), mine.weight, digest(shares))
	if *digestPath != "" {
		if err := os.WriteFile(*digestPath, []byte(digest(shares)+"\n"), 0o644); err != nil {
			fatal("%v", err)
		}
	}
	status := execute(mine, extra, *retry, *jobsMax)
	if c, ok := logw.(io.Closer); ok {
		c.Close()
	}
	os.Exit(status)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "shardtests: "+format+"\n", a...)
	os.Exit(2)
}

// short is a package's import path relative to the module.
func short(importPath string) string {
	if importPath == modulePath {
		return "."
	}
	return strings.TrimPrefix(importPath, modulePath+"/")
}

func readWeights(path string, col int) map[string]float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal("%v", err)
	}
	w := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) != 5 {
			fatal("%s: want package and four weights, got %q", path, line)
		}
		v, err := strconv.ParseFloat(f[1+col], 64)
		if err != nil {
			fatal("%s: %q: %v", path, line, err)
		}
		w[f[0]] = v
	}
	return w
}

// pkg is a package with tests. names are those of a package that is cut.
type pkg struct {
	path   string // relative to the module
	weight float64
	names  []string
}

// packages lists the module's packages that have tests, with the tests of the
// ones to be cut.
func packages() []*pkg {
	out := goOutput("go", "list", "-f", `{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}`, "./...")
	var list []*pkg
	for _, line := range strings.Fields(out) {
		list = append(list, &pkg{path: short(line)})
	}
	sort.Slice(list, func(a, b int) bool { return list[a].path < list[b].path })
	return list
}

func goOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		fatal("%s %s: %v\n%s", name, strings.Join(args, " "), err, errb.String())
	}
	return out.String()
}

// listTests is the names of a package's tests.
func listTests(path string) []string {
	arg := "./" + path
	if path == "." {
		arg = "."
	}
	var names []string
	for _, line := range strings.Split(goOutput("go", "test", "-list", "^Test", arg), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Test") && !strings.ContainsAny(line, " \t") {
			names = append(names, line)
		}
	}
	sort.Strings(names)
	return names
}

func fnvMod(name string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	return int(h.Sum32() % uint32(n))
}

// unit is a package, or one part of a cut package.
type unit struct {
	path   string
	part   int // -1 for a whole package
	weight float64
	names  []string
}

type share struct {
	weight float64
	whole  []string
	cut    map[string][]string // package -> test names
	units  []string            // for the plan and the check
}

func deal(pkgs []*pkg, weights map[string]float64, n int) []*share {
	// The packages to be cut are listed in parallel, a few at a time.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, p := range pkgs {
		w, ok := weights[p.path]
		if !ok {
			w = defaultWeight
		}
		p.weight = w
		if n > 1 && w >= splitFrom {
			wg.Add(1)
			go func(p *pkg) {
				defer wg.Done()
				sem <- struct{}{}
				p.names = listTests(p.path)
				<-sem
			}(p)
		}
	}
	wg.Wait()

	var units []unit
	for _, p := range pkgs {
		if len(p.names) < minSplitTests {
			units = append(units, unit{path: p.path, part: -1, weight: p.weight})
			continue
		}
		parts := make([][]string, n)
		for _, name := range p.names {
			k := fnvMod(name, n)
			parts[k] = append(parts[k], name)
		}
		for k, names := range parts {
			units = append(units, unit{path: p.path, part: k, weight: p.weight / float64(n), names: names})
		}
	}
	sort.Slice(units, func(a, b int) bool {
		if units[a].weight != units[b].weight {
			return units[a].weight > units[b].weight
		}
		if units[a].path != units[b].path {
			return units[a].path < units[b].path
		}
		return units[a].part < units[b].part
	})

	shares := make([]*share, n)
	for k := range shares {
		shares[k] = &share{cut: map[string][]string{}}
	}
	for _, u := range units {
		best := shares[0]
		for _, s := range shares[1:] {
			if s.weight < best.weight {
				best = s
			}
		}
		best.weight += u.weight
		if u.part < 0 {
			best.whole = append(best.whole, u.path)
			best.units = append(best.units, u.path)
		} else {
			best.cut[u.path] = append(best.cut[u.path], u.names...)
			best.units = append(best.units, fmt.Sprintf("%s#%d", u.path, u.part))
		}
	}
	for _, s := range shares {
		sort.Strings(s.whole)
		sort.Strings(s.units)
		for _, names := range s.cut {
			sort.Strings(names)
		}
	}
	return shares
}

// check fails unless every package is run, and every test of a cut package is
// run, by exactly one share.
func check(pkgs []*pkg, shares []*share) error {
	whole := map[string]int{}
	tests := map[string]int{}
	for _, s := range shares {
		for _, p := range s.whole {
			whole[p]++
		}
		for p, names := range s.cut {
			for _, name := range names {
				tests[p+"."+name]++
			}
		}
	}
	for _, p := range pkgs {
		if len(p.names) < minSplitTests {
			if whole[p.path] != 1 {
				return fmt.Errorf("package %s is in %d shares, not 1", p.path, whole[p.path])
			}
			continue
		}
		if whole[p.path] != 0 {
			return fmt.Errorf("package %s is cut and also whole", p.path)
		}
		for _, name := range p.names {
			if c := tests[p.path+"."+name]; c != 1 {
				return fmt.Errorf("%s %s is in %d shares, not 1", p.path, name, c)
			}
		}
	}
	return nil
}

// digest names the whole plan, so that two jobs can be seen to have agreed.
func digest(shares []*share) string {
	h := sha256.New()
	for _, s := range shares {
		fmt.Fprintln(h, strings.Join(s.units, ","))
		for p, names := range s.cut {
			fmt.Fprintln(h, p, strings.Join(names, ","))
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}

func printPlan(shares []*share) {
	for k, s := range shares {
		fmt.Printf("share %d: about %.0f s, %d whole, %d cut\n", k+1, s.weight, len(s.whole), len(s.cut))
		fmt.Printf("  %s\n", strings.Join(s.units, " "))
	}
}

// logw gets a copy of what is printed when -log is given.
var logw io.Writer = io.Discard

func out(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	fmt.Print(s)
	fmt.Fprint(logw, s)
}

// run is the result of one go test command.
type run struct {
	output  string
	status  int
	pkgs    map[string][]string // package -> its result lines: ok, FAIL or ?
	tests   map[string][]string // test name -> PASS, FAIL or SKIP
	pkgSec  map[string]float64  // package -> the seconds its line gives
	testSec map[string]float64  // test name -> the seconds its line gives
	wall    float64             // seconds the command took
}

var (
	pkgLine  = regexp.MustCompile(`^(ok|FAIL|\?)\s+` + regexp.QuoteMeta(modulePath) + `(\S*)(?:\s+([0-9.]+)s)?`)
	testLine = regexp.MustCompile(`^--- (PASS|FAIL|SKIP): (Test\S*) \(([0-9.]+)s\)`)
)

func goTest(args []string) *run {
	r := &run{pkgs: map[string][]string{}, tests: map[string][]string{}, pkgSec: map[string]float64{}, testSec: map[string]float64{}}
	began := time.Now()
	cmd := exec.Command("go", append([]string{"test"}, args...)...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	r.wall = time.Since(began).Seconds()
	if err != nil {
		r.status = 1
		if ee, ok := err.(*exec.ExitError); ok {
			r.status = ee.ExitCode()
		}
	}
	r.output = buf.String()
	sc := bufio.NewScanner(strings.NewReader(r.output))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := pkgLine.FindStringSubmatch(line); m != nil {
			p := strings.TrimPrefix(m[2], "/")
			if p == "" {
				p = "."
			}
			r.pkgs[p] = append(r.pkgs[p], m[1])
			if v, err := strconv.ParseFloat(m[3], 64); err == nil {
				r.pkgSec[p] += v
			}
		}
		if m := testLine.FindStringSubmatch(line); m != nil {
			r.tests[m[2]] = append(r.tests[m[2]], m[1])
			if v, err := strconv.ParseFloat(m[3], 64); err == nil {
				r.testSec[m[2]] += v
			}
		}
	}
	return r
}

func pkgArgs(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p == "." {
			out = append(out, ".")
		} else {
			out = append(out, "./"+p)
		}
	}
	return out
}

func anchored(names []string) string {
	return "^(" + strings.Join(names, "|") + ")$"
}

// job is one go test command of a share: the packages that are not cut, or one
// cut package.
type job struct {
	title string
	whole []string // the packages, when none is cut
	path  string   // the cut package
	names []string // its tests in this share
	args  []string
	r     *run
}

// failedItems is what did not pass: the packages that reported FAIL, or the
// tests of a cut package that did not report PASS or SKIP.
func (j *job) failedItems() []string {
	var out []string
	if j.path == "" {
		for _, p := range j.whole {
			for _, v := range j.r.pkgs[p] {
				if v == "FAIL" {
					out = append(out, p)
					break
				}
			}
		}
		return out
	}
	for _, name := range j.names {
		ok := false
		for _, v := range j.r.tests[name] {
			if v == "PASS" || v == "SKIP" {
				ok = true
			}
		}
		if !ok {
			out = append(out, name)
		}
	}
	return out
}

// again runs the failed items once more, on their own.
func (j *job) again(items []string, extra []string) *run {
	if j.path == "" {
		return goTest(append(append([]string{}, extra...), pkgArgs(items)...))
	}
	args := append([]string{"-v", "-run", anchored(items)}, extra...)
	return goTest(append(args, pkgArgs([]string{j.path})...))
}

// replace puts the second run's results where the first run's were.
func (j *job) replace(items []string, second *run) {
	for _, item := range items {
		if j.path == "" {
			j.r.pkgs[item] = second.pkgs[item]
		} else {
			j.r.tests[item] = second.tests[item]
		}
	}
}

// verify lists what did not report exactly once.
func (j *job) verify() []string {
	var out []string
	if j.path == "" {
		want := map[string]bool{}
		for _, p := range j.whole {
			want[p] = true
			if c := len(j.r.pkgs[p]); c != 1 {
				out = append(out, fmt.Sprintf("package %s reported %d times", p, c))
			}
		}
		for p := range j.r.pkgs {
			if !want[p] {
				out = append(out, fmt.Sprintf("package %s ran but is not in this share", p))
			}
		}
		return out
	}
	want := map[string]bool{}
	for _, name := range j.names {
		want[name] = true
		if c := len(j.r.tests[name]); c != 1 {
			out = append(out, fmt.Sprintf("%s %s reported %d times", j.path, name, c))
		}
	}
	for name := range j.r.tests {
		if !want[name] {
			out = append(out, fmt.Sprintf("%s %s ran but is not in this share", j.path, name))
		}
	}
	return out
}

// execute runs a share and returns the exit status.
func execute(s *share, extra []string, retry bool, jobsMax int) int {
	var jobs []*job
	if len(s.whole) > 0 {
		args := append(append([]string{}, extra...), pkgArgs(s.whole)...)
		jobs = append(jobs, &job{title: fmt.Sprintf("%d packages", len(s.whole)), whole: s.whole, args: args})
	}
	var cutPaths []string
	for p := range s.cut {
		cutPaths = append(cutPaths, p)
	}
	sort.Strings(cutPaths)
	for _, p := range cutPaths {
		names := s.cut[p]
		args := append([]string{"-v", "-run", anchored(names)}, extra...)
		args = append(args, pkgArgs([]string{p})...)
		jobs = append(jobs, &job{title: fmt.Sprintf("%s (%d tests)", p, len(names)), path: p, names: names, args: args})
	}

	// A share's commands run a few at a time, not all at once: on a 4 vCPU
	// runner a package that takes 3 minutes alone took 10 when everything ran
	// together. The cut packages with the most tests start first.
	sort.SliceStable(jobs, func(a, b int) bool { return len(jobs[a].names) > len(jobs[b].names) })
	began := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(jobsMax, 1))
	for _, j := range jobs {
		wg.Add(1)
		go func(j *job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			j.r = goTest(j.args)
		}(j)
	}
	wg.Wait()
	defer func() { summarize(jobs, time.Since(began).Seconds()) }()

	status := 0
	for _, j := range jobs {
		out("::group::go test: %s\n%s::endgroup::\n", j.title, j.r.output)
		if j.r.status != 0 {
			status = 1
		}
	}

	// Whatever failed is run once more, on its own.
	if status != 0 && retry {
		status = 0
		for _, j := range jobs {
			if j.r.status == 0 {
				continue
			}
			items := j.failedItems()
			if len(items) == 0 || len(items) > 50 {
				status = 1
				continue
			}
			second := j.again(items, extra)
			out("::group::Running once more: %s\n%s::endgroup::\n", strings.Join(items, " "), second.output)
			if second.status != 0 {
				status = 1
				continue
			}
			out("::warning title=Tests that passed only when run again::Failed, then passed when run again:%%0A%s\n", strings.Join(items, "%0A"))
			j.replace(items, second)
		}
	}

	// Every package, and every test of a cut package, reported exactly once.
	if status == 0 {
		var problems []string
		for _, j := range jobs {
			problems = append(problems, j.verify()...)
		}
		if len(problems) > 0 {
			sort.Strings(problems)
			out("::error title=Tests did not run exactly once::%s\n", strings.Join(problems, "%0A"))
			out("%s\n", strings.Join(problems, "\n"))
			status = 1
		}
	}
	return status
}

// summarize adds the share's timings to the job summary, when there is one:
// each command, then the slowest packages and tests, in seconds. These are the
// numbers to regenerate .github/test-weights.txt from.
func summarize(jobs []*job, wall float64) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	type row struct {
		name string
		sec  float64
	}
	var cmds, items []row
	for _, j := range jobs {
		cmds = append(cmds, row{j.title, j.r.wall})
		if j.path == "" {
			for _, p := range j.whole {
				items = append(items, row{p, j.r.pkgSec[p]})
			}
			continue
		}
		for _, name := range j.names {
			items = append(items, row{j.path + " " + name, j.r.testSec[name]})
		}
	}
	sort.Slice(items, func(a, b int) bool { return items[a].sec > items[b].sec })
	var b strings.Builder
	fmt.Fprintln(&b, "### Share timings (seconds)")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "The share took %.0f s.\n", wall)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "```")
	for _, r := range cmds {
		fmt.Fprintf(&b, "%7.1f  command: %s\n", r.sec, r.name)
	}
	fmt.Fprintln(&b)
	for k, r := range items {
		if k == 25 {
			break
		}
		fmt.Fprintf(&b, "%7.1f  %s\n", r.sec, r.name)
	}
	fmt.Fprintln(&b, "```")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(b.String())
}
