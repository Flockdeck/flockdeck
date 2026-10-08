package server

import (
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// stubExport replaces the slow part of an export, restoring it afterwards. The
// pane is not looked up: the conversation is named for the pane id.
func stubExport(t *testing.T, run func(job *workspace.ExportJob, cancel <-chan struct{}) (record.ExportResult, error)) {
	t.Helper()
	prep, runner := exportPrepare, exportRun
	exportPrepare = func(_ *workspace.Workspace, id string) (*workspace.ExportJob, error) {
		return &workspace.ExportJob{Conversation: "conv-" + id}, nil
	}
	exportRun = run
	t.Cleanup(func() { exportPrepare, exportRun = prep, runner })
}

// exportNotices reads what has been sent to a window.
func exportNotices(c *controlClient) []noticeMsg {
	var out []noticeMsg
	for {
		select {
		case b := <-c.out:
			var n noticeMsg
			if json.Unmarshal(b, &n) == nil && n.Type == "notice" {
				out = append(out, n)
			}
		default:
			return out
		}
	}
}

func untilExport(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A slow export does not hold up the workspace goroutine, and the window that
// asked has its answer once it is done.
func TestAnExportDoesNotBlockTheServerLoop(t *testing.T) {
	srv, _ := newTestServer(t)
	release := make(chan struct{})
	started := make(chan struct{})
	stubExport(t, func(*workspace.ExportJob, <-chan struct{}) (record.ExportResult, error) {
		close(started)
		// A stub that cannot be released (the export was run on the loop) gives up
		// so that a failure ends the test instead of hanging it.
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		return record.ExportResult{Path: "/x/y.jsonl", Lines: 7}, nil
	})
	c := &controlClient{out: make(chan []byte, 8)}
	returned := make(chan struct{})
	go func() {
		srv.exportTranscript(c, command{ID: "p1", Confirmed: true})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("asking for an export waited for the export")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the export never started")
	}
	// The loop answers while the export is stalled.
	got := make(chan bool, 1)
	go func() { _, ok := ask(srv, func() int { return 1 }); got <- ok }()
	select {
	case ok := <-got:
		if !ok {
			t.Fatal("the loop did not answer")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the workspace goroutine is blocked by the export")
	}
	if n := exportNotices(c); len(n) != 0 {
		t.Fatalf("answered before the export finished: %v", n)
	}
	close(release)
	untilExport(t, "the export's notice", func() bool { return len(c.out) > 0 })
	n := exportNotices(c)
	if len(n) != 1 || n[0].Error || !strings.Contains(n[0].Text, "Exported 7 lines to /x/y.jsonl") {
		t.Errorf("notice = %+v", n)
	}
}

// The answer goes to the window that asked, and only to it.
func TestAnExportsAnswerGoesToTheAskingWindowOnly(t *testing.T) {
	srv, _ := newTestServer(t)
	stubExport(t, func(*workspace.ExportJob, <-chan struct{}) (record.ExportResult, error) {
		return record.ExportResult{}, errors.New("disk is full")
	})
	asker := &controlClient{out: make(chan []byte, 8)}
	other := &controlClient{out: make(chan []byte, 8)}
	srv.exportTranscript(asker, command{ID: "p1", Confirmed: true})
	untilExport(t, "the notice", func() bool { return len(asker.out) > 0 })
	n := exportNotices(asker)
	if len(n) != 1 || !n[0].Error || !strings.Contains(n[0].Text, "disk is full") {
		t.Errorf("asker got %+v", n)
	}
	if len(other.out) != 0 {
		t.Error("another window was told")
	}
}

// Two windows asking for one conversation at once run one export, and both are
// told how it went.
func TestTwoWindowsExportingOneConversationRunOneExport(t *testing.T) {
	srv, _ := newTestServer(t)
	release := make(chan struct{})
	var runs atomic.Int32
	stubExport(t, func(*workspace.ExportJob, <-chan struct{}) (record.ExportResult, error) {
		runs.Add(1)
		<-release
		return record.ExportResult{Path: "/x/y.jsonl", Lines: 3, Replaced: true}, nil
	})
	a := &controlClient{out: make(chan []byte, 8)}
	b := &controlClient{out: make(chan []byte, 8)}
	srv.exportTranscript(a, command{ID: "p1", Confirmed: true})
	untilExport(t, "the export to start", func() bool { return runs.Load() == 1 })
	srv.exportTranscript(b, command{ID: "p1", Confirmed: true})
	close(release)
	untilExport(t, "both notices", func() bool { return len(a.out) > 0 && len(b.out) > 0 })
	if runs.Load() != 1 {
		t.Errorf("%d exports ran, want one", runs.Load())
	}
	for _, c := range []*controlClient{a, b} {
		if n := exportNotices(c); len(n) != 1 || !strings.Contains(n[0].Text, "replacing the earlier export") {
			t.Errorf("notice = %+v", n)
		}
	}
	// Once it is over, another export of the conversation runs.
	srv.exportTranscript(a, command{ID: "p1", Confirmed: true})
	untilExport(t, "a later export", func() bool { return runs.Load() == 2 })
}

// A server that closes while an export runs stops it, waits for it, and says
// nothing to a window that is going.
func TestClosingTheServerStopsAnExportInFlight(t *testing.T) {
	srv, _ := newTestServer(t)
	started := make(chan struct{})
	var stopped atomic.Bool
	stubExport(t, func(_ *workspace.ExportJob, cancel <-chan struct{}) (record.ExportResult, error) {
		close(started)
		<-cancel
		stopped.Store(true)
		return record.ExportResult{}, workspace.ErrExportCancelled
	})
	c := &controlClient{out: make(chan []byte, 8)}
	srv.exportTranscript(c, command{ID: "p1", Confirmed: true})
	<-started
	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("Close did not return while an export was running")
	}
	if !stopped.Load() {
		t.Error("Close returned before the export stopped")
	}
	if n := exportNotices(c); len(n) != 0 {
		t.Errorf("a window of a closed server was told: %v", n)
	}
	// Nothing starts once it is closed.
	srv.exportTranscript(c, command{ID: "p1", Confirmed: true})
	time.Sleep(50 * time.Millisecond)
	if len(c.out) != 0 {
		t.Error("an export was started on a closed server")
	}
}

// A window reached through the relay is refused, and nothing runs.
func TestAnExportFromARemoteWindowIsRefused(t *testing.T) {
	srv, _ := newTestServer(t)
	var runs atomic.Int32
	stubExport(t, func(*workspace.ExportJob, <-chan struct{}) (record.ExportResult, error) {
		runs.Add(1)
		return record.ExportResult{}, nil
	})
	c := &controlClient{out: make(chan []byte, 8), remote: true, device: "dev-1"}
	srv.exportTranscript(c, command{ID: "p1", Confirmed: true})
	n := exportNotices(c)
	if len(n) != 1 || !n[0].Error || !strings.Contains(n[0].Text, "relay") {
		t.Errorf("notice = %+v", n)
	}
	// And one that did not confirm is told to.
	d := &controlClient{out: make(chan []byte, 8)}
	srv.exportTranscript(d, command{ID: "p1"})
	if n := exportNotices(d); len(n) != 1 || !strings.Contains(n[0].Text, "Export from the window") {
		t.Errorf("unconfirmed notice = %+v", n)
	}
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 0 {
		t.Error("an export ran")
	}
}

// Once the server is closed an export is not joined or counted, so Close, which
// has already looked at the count, is never left waiting on one.
func TestNoExportIsJoinedAfterTheServerClosed(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Close()
	c := &controlClient{out: make(chan []byte, 8)}
	first, ok := srv.joinExport("conv-1", c)
	if first || ok {
		t.Errorf("joinExport = %v, %v after Close, want false, false", first, ok)
	}
	srv.exports.mu.Lock()
	waiting := len(srv.exports.waiting)
	srv.exports.mu.Unlock()
	if waiting != 0 {
		t.Errorf("%d conversations waiting after Close", waiting)
	}
	counted := make(chan struct{})
	go func() { srv.connWG.Wait(); close(counted) }()
	select {
	case <-counted:
	case <-time.After(2 * time.Second):
		t.Error("an export was counted after Close and nothing will finish it")
	}
}
