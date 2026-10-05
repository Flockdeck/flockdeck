package helpers

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseProcNetTCP(t *testing.T) {
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	line := func(addr string, port int, state string, inode int) string {
		return "   0: " + addr + ":" + fmt.Sprintf("%04X", port) + " 00000000:0000 " + state + " 00000000:00000000 00:00000000 00000000  1000        0 " + strconv.Itoa(inode) + " 1 0\n"
	}
	text := header +
		line("0100007F", 8080, "0A", 11) + // 127.0.0.1, listening
		line("0100007F", 8080, "01", 22) + // an established connection
		line("0100007F", 8081, "0A", 33) + // another port
		line("00000000", 8080, "0A", 44) + // 0.0.0.0
		line("0501A8C0", 8080, "0A", 55) + // 192.168.1.5
		line("00000000000000000000000000000000", 8080, "0A", 66) + // ::
		line("00000000000000000000000001000000", 8080, "0A", 77) + // ::1
		line("0000000000000000FFFF00000100007F", 8080, "0A", 88) + // ::ffff:127.0.0.1
		line("0100007F", 8080, "0A", 0) + // no inode
		"   9: broken line\n"
	got := parseProcNetTCP(text, 8080)
	want := []listenerRow{
		{bindLoopback, 11}, {bindWildcard, 44}, {bindOther, 55}, {bindWildcard, 66}, {bindOther, 77}, {bindLoopback, 88},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if got := parseProcNetTCP(text, 9); len(got) != 0 {
		t.Fatalf("rows for another port = %v", got)
	}
}

func TestParseStat(t *testing.T) {
	cases := map[string][2]int{
		"4242 (lens) S 1 4242 4242 0 -1 4194560":   {1, 4242},
		"77 (a b) S 5 99 99 0 -1":                  {5, 99},
		"88 (name (with) parens) R 1 123 123 0 -1": {1, 123},
		"99 (evil) S 1 0) S 7 321 321 0 -1 ":       {7, 321},
	}
	for line, want := range cases {
		pp, pg, ok := parseStat(line)
		if !ok || pp != want[0] || pg != want[1] {
			t.Errorf("%q: got %d %d %v, want %v", line, pp, pg, ok, want)
		}
	}
	for _, bad := range []string{"", "no parens", "1 (x) S", "1 (x) S 1 notnumber 3", "1 (x) S notnumber 2 3"} {
		if _, _, ok := parseStat(bad); ok {
			t.Errorf("%q was parsed", bad)
		}
	}
	if pg, ok := parseStatPgrp("1 (x) S 5 6 7"); !ok || pg != 6 {
		t.Errorf("parseStatPgrp = %d, %v", pg, ok)
	}
}

func TestParseWindowsTCPTables(t *testing.T) {
	le := binary.LittleEndian
	port := func(p int) uint32 { return uint32(p>>8) | uint32(p&0xff)<<8 }
	t4 := make([]byte, 4+4*24)
	le.PutUint32(t4, 4)
	for i, row := range []struct {
		addr      uint32
		port, pid int
	}{{0x0100007F, 8080, 111}, {0x0100007F, 9000, 222}, {0, 8080, 333}, {0x0501A8C0, 8080, 444}} {
		r := t4[4+i*24:]
		le.PutUint32(r[4:], row.addr)
		le.PutUint32(r[8:], port(row.port))
		le.PutUint32(r[20:], uint32(row.pid))
	}
	want4 := []listenerRow{{bindLoopback, 111}, {bindWildcard, 333}, {bindOther, 444}}
	if got := parseTCPTable4(t4, 8080); fmt.Sprint(got) != fmt.Sprint(want4) {
		t.Fatalf("v4 rows = %v, want %v", got, want4)
	}

	t6 := make([]byte, 4+4*56)
	le.PutUint32(t6, 4)
	mapped := [16]byte{10: 0xff, 11: 0xff, 12: 127, 15: 1}
	loop6 := [16]byte{15: 1}
	for i, row := range []struct {
		addr      [16]byte
		port, pid int
	}{{[16]byte{}, 8080, 444}, {loop6, 8080, 555}, {mapped, 8080, 666}, {[16]byte{}, 1, 777}} {
		r := t6[4+i*56:]
		copy(r, row.addr[:])
		le.PutUint32(r[20:], port(row.port))
		le.PutUint32(r[52:], uint32(row.pid))
	}
	want6 := []listenerRow{{bindWildcard, 444}, {bindOther, 555}, {bindLoopback, 666}}
	if got := parseTCPTable6(t6, 8080); fmt.Sprint(got) != fmt.Sprint(want6) {
		t.Fatalf("v6 rows = %v, want %v", got, want6)
	}
	// A count that claims more rows than there are bytes is not read past.
	le.PutUint32(t4, 1000)
	if got := parseTCPTable4(t4, 8080); len(got) != 3 {
		t.Fatalf("rows = %v", got)
	}
	if parseTCPTable4(nil, 1) != nil || parseTCPTable6([]byte{1}, 1) != nil {
		t.Fatal("a short table gave rows")
	}
}

func TestDecide(t *testing.T) {
	ours := func(h uint64) holder {
		switch {
		case h < 100:
			return holderOurs
		case h < 200:
			return holderForeign
		}
		return holderUnknown
	}
	row := func(c bindClass, h uint64) listenerRow { return listenerRow{c, h} }
	cases := []struct {
		name string
		rows []listenerRow
		want ownerResult
	}{
		{"the helper alone, on loopback", []listenerRow{row(bindLoopback, 1)}, ownerVerified},
		{"the helper alone, on the wildcard", []listenerRow{row(bindWildcard, 1)}, ownerVerified},
		{"a squatter on loopback", []listenerRow{row(bindLoopback, 150)}, ownerMismatch},
		{"a squatter on loopback beside the helper", []listenerRow{row(bindLoopback, 1), row(bindLoopback, 150)}, ownerMismatch},
		{"a squatter on the wildcard alone", []listenerRow{row(bindWildcard, 150)}, ownerMismatch},
		// What would answer 127.0.0.1 is the most specific socket.
		{"a wildcard squatter beside the helper on loopback", []listenerRow{row(bindLoopback, 1), row(bindWildcard, 150)}, ownerVerified},
		{"a squatter on [::1] or another address beside the helper", []listenerRow{row(bindLoopback, 1), row(bindOther, 150)}, ownerVerified},
		{"only a squatter on [::1]", []listenerRow{row(bindOther, 150)}, ownerUnknown},
		{"no listener", nil, ownerUnknown},
		{"a holder that cannot be found", []listenerRow{row(bindLoopback, 250)}, ownerUnknown},
		{"the helper's and an unknown", []listenerRow{row(bindLoopback, 1), row(bindLoopback, 250)}, ownerUnknown},
		{"a foreign one beats an unknown", []listenerRow{row(bindLoopback, 150), row(bindLoopback, 250)}, ownerMismatch},
	}
	for _, c := range cases {
		if got := decide(c.rows, ours); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if pidHolder([]int{5})(5) != holderOurs || pidHolder([]int{5})(6) != holderForeign {
		t.Error("pidHolder")
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
// helper prints the right banner and never binds, is stopped, and every try on
// that port is a failure.
func TestASquatterOnTheHelpersPortIsCaught(t *testing.T) {
	if !OwnerCheckSupported() {
		t.Skip("this platform cannot verify who owns a port")
	}
	port := squatter(t)
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "no-bind")}, func(c *Config) {
		c.PickPort = func(int) (int, error) { return port, nil }
	})
	st := f.startAndWait("lens")
	if st.State != StateFailed || !strings.Contains(st.Err, "held by something other than lens") || !strings.Contains(st.Err, "3 different ports") {
		t.Fatalf("status = %+v", st)
	}
	if st.URL != "" || st.Owner != "" {
		t.Fatalf("a squatter's page was offered: %+v", st)
	}
}

// Losing the race for a port is retried on another one, as an early exit is.
func TestAContestedPortIsRetriedOnAnotherPort(t *testing.T) {
	if !OwnerCheckSupported() {
		t.Skip("this platform cannot verify who owns a port")
	}
	taken := squatter(t)
	first := true
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "no-bind-once", "--state", filepath.Join(t.TempDir(), "runs"))}, func(c *Config) {
		c.PickPort = func(int) (int, error) {
			if first {
				first = false
				return taken, nil
			}
			return ChoosePort(0)
		}
	})
	st := f.startAndWait("lens")
	if st.State != StateRunning || st.Port == taken || st.Owner != OwnerVerified {
		t.Fatalf("status = %+v (the contested port was %d)", st, taken)
	}
}

// A listener on [::1] with the same port number is not who answered a probe of
// 127.0.0.1, and the helper that holds 127.0.0.1 is not stopped for it.
func TestAListenerOnAnotherAddressDoesNotStopAHealthyHelper(t *testing.T) {
	if !OwnerCheckSupported() {
		t.Skip("this platform cannot verify who owns a port")
	}
	port, err := ChoosePort(0)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "[::1]:"+strconv.Itoa(port))
	if err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	f := newSupFixture(t, []Entry{fakeEntry("lens")}, func(c *Config) {
		c.PickPort = func(int) (int, error) { return port, nil }
	})
	st := f.startAndWait("lens")
	if st.State != StateRunning || st.Port != port || st.Owner != OwnerVerified {
		t.Fatalf("a healthy helper was stopped for a listener on [::1]: %+v", st)
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

// A mismatch has to hold on a second look before it is acted on: a verdict
// that goes away is not one.
func TestAMismatchThatGoesAwayIsNotActedOn(t *testing.T) {
	if !OwnerCheckSupported() {
		t.Skip("this platform has no owner check to fake; the unverified case is TestAnUnverifiableOwnerIsShownAsUnverified")
	}
	prev := checkListenerOwner
	calls := 0
	checkListenerOwner = func(int, []int) (ownerResult, string) {
		calls++
		if calls == 1 {
			return ownerMismatch, "seen mid-change"
		}
		return ownerVerified, ""
	}
	defer func() { checkListenerOwner = prev }()
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	st := f.startAndWait("lens")
	if st.State != StateRunning || st.Owner != OwnerVerified || calls != 2 {
		t.Fatalf("status = %+v after %d checks", st, calls)
	}
}
