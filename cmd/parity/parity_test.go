package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/testiso"
)

func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }

// root is the root of the repository, from this package's directory.
const root = "../.."

// An action or command the old interface has and the new one does not do must
// be replaced, cut or owned by a task. This is the test the whole tool is for:
// it fails with the name of the thing nobody has accounted for.
func TestEveryOldActionAndCommandIsAccountedFor(t *testing.T) {
	f, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, problems := rows(f)
	for _, p := range problems {
		t.Error(p + "\n\tAdd it to cmd/parity/classify.go: cut with a reason, replaced by a command the new interface sends, or owned by a task, then run `go run ./cmd/parity -w`.")
	}
}

// What is classified has to exist, or the table keeps lines for things that were
// renamed or removed. And a thing the new interface already does must not still
// be called cut.
func TestClassificationsNameRealThings(t *testing.T) {
	f, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	for id := range keyVerdicts {
		if _, ok := help.Lookup(id); !ok {
			t.Errorf("classify.go classifies action %q, which is not in the key table", id)
		}
	}
	for name := range cmdVerdicts {
		if _, ok := f.OldCommands[name]; !ok {
			t.Errorf("classify.go classifies command %q, which the old interface does not send", name)
		}
	}
	for name, v := range cmdVerdicts {
		if f.NewCommands[name] {
			t.Errorf("command %q is classified (%v), but the new interface sends it: remove the line", name, v.Kind)
		}
	}
	for id, v := range keyVerdicts {
		if f.Implemented[id] && v.Kind != Planned {
			t.Errorf("action %q is classified as cut or replaced, but the new shell runs it", id)
		}
	}
}

// Every verdict says what it needs to: a cut has a reason, a planned thing a task
// of the board's form, a replaced one a command the new interface sends.
func TestVerdictsAreComplete(t *testing.T) {
	f, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	task := regexp.MustCompile(`^T\d+ \(`)
	check := func(kind, name string, v Verdict) {
		switch v.Kind {
		case Cut:
			if strings.TrimSpace(v.Why) == "" {
				t.Errorf("%s %q is cut with no reason", kind, name)
			}
		case Planned:
			if !task.MatchString(v.To) {
				t.Errorf("%s %q is owned by %q, which is not a task of the form %q", kind, name, v.To, "T123 (title)")
			}
		case Replaced:
			if !f.NewCommands[v.To] {
				t.Errorf("%s %q is replaced by %q, which the new interface does not send", kind, name, v.To)
			}
		default:
			t.Errorf("%s %q has no kind", kind, name)
		}
	}
	for k, v := range keyVerdicts {
		check("action", k, v)
	}
	for k, v := range cmdVerdicts {
		check("command", k, v)
	}
}

// The intended cuts are the tiling, rearranging and broadcast the plan names, and
// nothing else. A cut that is not of those needs a decision, not a line here.
func TestOnlyTheIntendedCutsAreCut(t *testing.T) {
	allowed := []string{whyTiling, whyRearrange, whyBroadcast}
	for _, set := range []map[string]Verdict{keyVerdicts, cmdVerdicts} {
		for name, v := range set {
			if v.Kind != Cut {
				continue
			}
			ok := false
			for _, a := range allowed {
				if strings.HasPrefix(v.Why, a) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%q is cut for a reason that is not an intended cut: %q", name, v.Why)
			}
		}
	}
}

// The places the old interface builds a command name at run time are the ones
// this tool cannot read the names of. A new one means a command may be missing.
func TestDynamicCommandSitesAreTheKnownOnes(t *testing.T) {
	f, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(f.DynamicSites)
	want := slices.Clone(dynamicSites)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the old interface's dynamic command sites are\n\t%q\nclassify.go knows\n\t%q\nLook at the new site, add the names it can take to cmdVerdicts, and list it in dynamicSites.", got, want)
	}
}

// A mistake in the scan shows as a command the server has never heard of, or as
// too few of them.
func TestTheScanFindsWhatIsThere(t *testing.T) {
	f, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.OldCommands) < 120 {
		t.Errorf("only %d old commands found", len(f.OldCommands))
	}
	if len(f.NewCommands) < 8 {
		t.Errorf("only %d new commands found: %v", len(f.NewCommands), f.NewCommands)
	}
	for _, want := range []string{"palette", "toggleRail", "projects", "nextRegion", "prevRegion", "nextNeedsYou", "managerBar"} {
		if !f.Implemented[want] {
			t.Errorf("the scan does not find %q among the actions the new shell runs: %v", want, f.Implemented)
		}
	}
	known, err := serverKnows(root)
	if err != nil {
		t.Fatal(err)
	}
	for name := range f.OldCommands {
		if !known[name] {
			t.Errorf("the old interface sends %q, which no `case %q` in internal/server handles: a scan mistake, or a command with no handler", name, name)
		}
	}
	for name := range f.NewCommands {
		if !known[name] {
			t.Errorf("the new interface sends %q, which no `case %q` in internal/server handles", name, name)
		}
	}
}

// The scan has to find what it is built to find. These are the shapes of the
// old interface's sends, written out.
func TestScanCommandsReadsTheShapesItMeets(t *testing.T) {
	src := `
  send({ cmd: "one", id });
  ws.send(JSON.stringify({cmd:"two"}));
  // send({ cmd: "inComment" })
   * { cmd: "inDoc" }
  send({ cmd: kind === "a" ? "three" : "four", x: 1 });
  const c = { cmd: msg.resend, path };
  send({ cmd: 'five' });
`
	lits, dyn := scanCommands(src)
	if lits["one"] != 1 || lits["two"] != 1 {
		t.Errorf("literals %v", lits)
	}
	if _, ok := lits["inComment"]; ok {
		t.Error("a command in a comment was read as sent")
	}
	if _, ok := lits["inDoc"]; ok {
		t.Error("a command in a doc comment was read as sent")
	}
	want := []string{"'five' }", `kind === "a" ? "three" : "four"`, "msg.resend"}
	// The single-quoted name is reported as a site; its trailing text is not
	// part of the contract, so match on the start.
	if len(dyn) != 3 {
		t.Fatalf("dynamic sites %q, want %d", dyn, 3)
	}
	for _, w := range want[1:] {
		if !slices.Contains(dyn, w) {
			t.Errorf("dynamic sites %q lack %q", dyn, w)
		}
	}
	if !strings.HasPrefix(dyn[0], "'five'") {
		t.Errorf("a single-quoted name was not reported as a dynamic site: %q", dyn)
	}
}

// docs/parity.md is generated, and a copy that is out of date would say a thing
// is planned that is done.
func TestParityDocIsCurrent(t *testing.T) {
	want, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, outFile))
	if err != nil {
		t.Fatalf("%v\nrun `go run ./cmd/parity -w` from the repository root", err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Errorf("%s is out of date: run `go run ./cmd/parity -w` from the repository root", outFile)
	}
}
