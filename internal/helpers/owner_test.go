package helpers

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// A helper whose owner is a Flockdeck that is still running is left alone by
// the reaper, whatever the rest of the record says.
func TestReaperLeavesAHelperWhoseOwnerIsAlive(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	pid, started := spawnStale(t, "sleep")
	owner, ownerStarted := spawnStale(t, "sleep")
	if started.IsZero() || ownerStarted.IsZero() {
		t.Skip("this platform cannot say when a process started")
	}
	rec := RunInfo{PID: pid, Started: started, Port: 1, OwnerPID: owner, OwnerStarted: ownerStarted}
	if err := f.store.writeRun("lens", rec); err != nil {
		t.Fatal(err)
	}
	if n := f.sup.ReapStale(); n != 0 {
		t.Fatalf("reaped %d helpers whose owner is running", n)
	}
	if processGone(pid) {
		t.Fatal("a running Flockdeck's helper was killed")
	}
	if _, ok := f.store.ReadRun("lens"); !ok {
		t.Fatal("a running Flockdeck's record was removed")
	}
}

// An owner that names a live process which started at another time is a pid
// the system has handed on, or a forgery: it is not an owner.
func TestReaperIgnoresAForgedOrGoneOwner(t *testing.T) {
	for name, mutate := range map[string]func(RunInfo) RunInfo{
		"wrong start time": func(r RunInfo) RunInfo { r.OwnerStarted = r.OwnerStarted.Add(-time.Hour); return r },
		"gone":             func(r RunInfo) RunInfo { r.OwnerPID, r.OwnerStarted = 2147483000, time.Now().Add(-time.Hour); return r },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSupFixture(t, []Entry{fakeEntry("lens")})
			pid, started := spawnStale(t, "sleep")
			owner, ownerStarted := spawnStale(t, "sleep")
			if started.IsZero() || ownerStarted.IsZero() {
				t.Skip("this platform cannot say when a process started")
			}
			rec := mutate(RunInfo{PID: pid, Started: started, Port: 1, OwnerPID: owner, OwnerStarted: ownerStarted})
			if err := f.store.writeRun("lens", rec); err != nil {
				t.Fatal(err)
			}
			if n := f.sup.ReapStale(); n != 1 {
				t.Fatalf("reaped %d, want 1", n)
			}
			eventually(t, "the helper to end", 15*time.Second, func() bool { return processGone(pid) })
		})
	}
}

func TestRunJSONNamesItsOwner(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	st := f.startAndWait("lens")
	if st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	r, ok := f.store.ReadRun("lens")
	if !ok || r.OwnerPID != os.Getpid() {
		t.Fatalf("run.json = %+v, %v", r, ok)
	}
	if started, ok := store.ProcessStartedAt(os.Getpid()); ok && r.OwnerStarted.IsZero() {
		t.Fatalf("the owner's start time (%v) was not recorded", started)
	}
}

// From the moment a start is asked for there is a record, so nothing can be
// installed over or removed while the program is still being checked.
func TestAStartUnderWayBlocksAnUninstall(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	// Start has returned; the process may not exist yet, and run.json does.
	r, ok := f.store.ReadRun("lens")
	if !ok || r.OwnerPID != os.Getpid() {
		t.Fatalf("no record of the start: %+v, %v", r, ok)
	}
	if err := f.store.Uninstall("lens", false); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("an uninstall during a start: %v", err)
	}
	if err := f.store.PurgeData("lens"); err == nil {
		t.Fatal("a purge during a start")
	}
	if _, ok := f.store.Current("lens"); !ok {
		t.Fatal("the helper was removed during its start")
	}
}

// Between a crash and the restart there is no process, and still an owner.
func TestCrashBackoffBlocksAnUninstallAndAnInstall(t *testing.T) {
	state := t.TempDir() + "/runs"
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "crash", "--after", "50ms", "--state", state)}, func(c *Config) {
		tm := *fastTimings()
		tm.Backoff = []time.Duration{3 * time.Second, 3 * time.Second, 3 * time.Second}
		c.Timings = &tm
	})
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the crash and its restart delay", 60*time.Second, func() bool {
		st := f.sup.Status("lens")
		return st.State == StateStarting && strings.Contains(st.Err, "restarting")
	})
	r, ok := f.store.ReadRun("lens")
	if !ok || !r.Starting || r.PID != 0 {
		t.Fatalf("during the delay run.json = %+v, %v", r, ok)
	}
	if err := f.store.Uninstall("lens", false); err == nil {
		t.Fatal("an uninstall during a restart delay")
	}
	in := NewInstaller(Options{Store: f.store, Lookup: lookupTest(fakeEntry("lens")), Busy: f.sup.Active})
	if _, err := in.InstallPlan(t.Context(), in.sealPlan(&Plan{Entry: fakeEntry("lens"), Version: "0.9.0", SHA256: strings.Repeat("a", 64)})); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("an install during a restart delay: %v", err)
	}
	if !f.sup.Active("lens") {
		t.Fatal("the supervisor does not count a helper in its restart delay as active")
	}
}

func TestAStartRecordWhoseOwnerIsGoneBlocksNothing(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	if err := f.store.writeRun("lens", RunInfo{Starting: true, OwnerPID: 2147483000, OwnerStarted: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, running := f.store.RunningPID("lens"); running {
		t.Fatal("a start by a Flockdeck that is gone counts as running")
	}
	if n := f.sup.ReapStale(); n != 0 {
		t.Fatalf("reaped %d", n)
	}
	if _, ok := f.store.ReadRun("lens"); ok {
		t.Fatal("the stale record was kept")
	}
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatalf("a stale start record blocked a start: %v", err)
	}
}

func TestAStartIsRefusedWhileAnInstallIsUnderWay(t *testing.T) {
	busy := true
	f := newSupFixture(t, []Entry{fakeEntry("lens")}, func(c *Config) { c.InstallBusy = func(string) bool { return busy } })
	if _, err := f.sup.Start("lens"); err == nil || !strings.Contains(err.Error(), "being installed") {
		t.Fatalf("err = %v", err)
	}
	busy = false
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
}

// The hidden ctrl-break subcommand sends to a process group only when run.json
// names that pid, with the start time it recorded.
func TestCtrlBreakOnlyReachesARecordedHelper(t *testing.T) {
	var sent []int
	prev := sendCtrlBreak
	sendCtrlBreak = func(pid int) error { sent = append(sent, pid); return nil }
	defer func() { sendCtrlBreak = prev }()

	st := &Store{Root: t.TempDir()}
	pid, started := spawnStale(t, "sleep")
	if started.IsZero() {
		t.Skip("this platform cannot say when a process started")
	}
	if err := CtrlBreakHelper(st, pid); err == nil {
		t.Fatal("a pid with no record was signalled")
	}
	// A record with the right pid and a wrong start time: the pid was handed on.
	if err := st.writeRun("lens", RunInfo{PID: pid, Started: started.Add(-time.Hour), Port: 1}); err != nil {
		t.Fatal(err)
	}
	if err := CtrlBreakHelper(st, pid); err == nil {
		t.Fatal("a record with the wrong start time was believed")
	}
	// A record with no start time at all is not enough either.
	if err := st.writeRun("lens", RunInfo{PID: pid, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if err := CtrlBreakHelper(st, pid); err == nil {
		t.Fatal("a record with no start time was believed")
	}
	// Another process, with the record naming this one.
	other, _ := spawnStale(t, "sleep")
	if err := st.writeRun("lens", RunInfo{PID: pid, Started: started, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if err := CtrlBreakHelper(st, other); err == nil {
		t.Fatal("a pid the record does not name was signalled")
	}
	for _, bad := range []int{0, -1, os.Getpid()} {
		if err := CtrlBreakHelper(st, bad); err == nil {
			t.Fatalf("pid %d was signalled", bad)
		}
	}
	if len(sent) != 0 {
		t.Fatalf("signals were sent: %v", sent)
	}
	// The recorded helper is.
	if err := CtrlBreakHelper(st, pid); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != pid {
		t.Fatalf("sent = %v", sent)
	}
}
