package helpers

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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
	if _, err := in.InstallPlan(t.Context(), &Plan{Entry: fakeEntry("lens"), Version: "0.9.0", SHA256: strings.Repeat("a", 64), Signed: true}); err == nil || !strings.Contains(err.Error(), "running") {
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

// port owner -----------------------------------------------------------------

func TestParseProcNetTCP(t *testing.T) {
	text := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:1F90 0100007F:D2F0 01 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0000000000000000 100 0 0 10 0\n" +
		"   2: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 33333 1 0000000000000000 100 0 0 10 0\n" +
		"   3: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 44444 1 0000000000000000 100 0 0 10 0\n" +
		"   4: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 55555 1 0000000000000000 100 0 0 10 0\n" +
		"   5: broken line\n" +
		"   6: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 0 1 0000000000000000 100 0 0 10 0\n"
	got := parseProcNetTCP(text, 8080)
	want := []uint64{12345, 44444, 55555}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("inodes = %v, want %v (a connection, another port, a broken line and inode 0 are not listeners on 8080)", got, want)
	}
	if got := parseProcNetTCP(text, 9); len(got) != 0 {
		t.Fatalf("inodes for another port = %v", got)
	}
}

func TestParseStatPgrp(t *testing.T) {
	cases := map[string]int{
		"4242 (lens) S 1 4242 4242 0 -1 4194560":   4242,
		"77 (a b) S 5 99 99 0 -1":                  99,
		"88 (name (with) parens) R 1 123 123 0 -1": 123,
		"99 (evil) S 1 0) S 1 321 321 0 -1 ":       321,
	}
	for line, want := range cases {
		got, ok := parseStatPgrp(line)
		if !ok || got != want {
			t.Errorf("%q: got %d, %v; want %d", line, got, ok, want)
		}
	}
	for _, bad := range []string{"", "no parens", "1 (x) S", "1 (x) S 1 notnumber 3"} {
		if _, ok := parseStatPgrp(bad); ok {
			t.Errorf("%q was parsed", bad)
		}
	}
}

func TestParseWindowsTCPTables(t *testing.T) {
	le := binary.LittleEndian
	port := func(p int) uint32 { return uint32(p>>8) | uint32(p&0xff)<<8 }
	t4 := make([]byte, 4+3*24)
	le.PutUint32(t4, 3)
	for i, row := range []struct{ port, pid int }{{8080, 111}, {9000, 222}, {8080, 333}} {
		r := t4[4+i*24:]
		le.PutUint32(r[8:], port(row.port))
		le.PutUint32(r[20:], uint32(row.pid))
	}
	if got := parseTCPTable4(t4, 8080); fmt.Sprint(got) != "[111 333]" {
		t.Fatalf("v4 owners = %v", got)
	}
	t6 := make([]byte, 4+2*56)
	le.PutUint32(t6, 2)
	for i, row := range []struct{ port, pid int }{{8080, 444}, {1, 555}} {
		r := t6[4+i*56:]
		le.PutUint32(r[20:], port(row.port))
		le.PutUint32(r[52:], uint32(row.pid))
	}
	if got := parseTCPTable6(t6, 8080); fmt.Sprint(got) != "[444]" {
		t.Fatalf("v6 owners = %v", got)
	}
	// A count that claims more rows than there are bytes is not read past.
	le.PutUint32(t4, 1000)
	if got := parseTCPTable4(t4, 8080); len(got) != 2 {
		t.Fatalf("owners = %v", got)
	}
	if parseTCPTable4(nil, 1) != nil || parseTCPTable6([]byte{1}, 1) != nil {
		t.Fatal("a short table gave owners")
	}
}

func TestOwnersWithin(t *testing.T) {
	cases := []struct {
		owners []uint32
		group  []int
		want   ownerResult
	}{
		{[]uint32{5}, []int{5, 6}, ownerVerified},
		{[]uint32{5, 6}, []int{5, 6}, ownerVerified},
		{[]uint32{5, 9}, []int{5, 6}, ownerMismatch},
		{[]uint32{9}, []int{5}, ownerMismatch},
		{nil, []int{5}, ownerUnknown},
		{[]uint32{5}, nil, ownerMismatch},
	}
	for _, c := range cases {
		if got := ownersWithin(c.owners, c.group); got != c.want {
			t.Errorf("ownersWithin(%v, %v) = %v, want %v", c.owners, c.group, got, c.want)
		}
	}
}

// squatter listens on a loopback port and answers every request 200, as a
// program that took a helper's port would.
func squatter(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(srv.Close)
	port, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	return port
}

// A program that took the port and answers /readyz and /healthz, while the
// helper prints the right banner and never binds, is stopped, not run.
func TestASquatterOnTheHelpersPortIsCaught(t *testing.T) {
	if !OwnerCheckSupported() {
		t.Skip("this platform cannot verify who owns a port")
	}
	port := squatter(t)
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "no-bind")}, func(c *Config) {
		c.PickPort = func(int) (int, error) { return port, nil }
	})
	st := f.startAndWait("lens")
	if st.State != StateFailed || !strings.Contains(st.Err, "held by something other than lens") {
		t.Fatalf("status = %+v", st)
	}
	if st.URL != "" || st.Owner != "" {
		t.Fatalf("a squatter's page was offered: %+v", st)
	}
}

func TestAHelperThatHoldsItsOwnPortIsVerified(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	st := f.startAndWait("lens")
	if st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	want := OwnerUnverified
	if OwnerCheckSupported() {
		want = OwnerVerified
	}
	if st.Owner != want {
		t.Fatalf("owner = %q, want %q", st.Owner, want)
	}
}

// Where the platform cannot say, the helper still runs and is shown as not
// verified.
func TestAnUnverifiableOwnerIsShownAsUnverified(t *testing.T) {
	prev := checkListenerOwner
	checkListenerOwner = func(int, []int) (ownerResult, string) { return ownerUnknown, "no" }
	defer func() { checkListenerOwner = prev }()
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	st := f.startAndWait("lens")
	if st.State != StateRunning || st.Owner != OwnerUnverified {
		t.Fatalf("status = %+v", st)
	}
}
