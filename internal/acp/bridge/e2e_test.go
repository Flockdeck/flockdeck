package bridge

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// session starts the fake agent with the bridge as its handler and opens a
// session. answer decides permission requests; nil allows.
func session(t *testing.T, b *Bridge, answer func(Card) string) (*acp.Client, string) {
	t.Helper()
	if answer == nil {
		answer = func(c Card) string { return c.Options[0].OptionID }
	}
	path, args, env := acptest.Command(t)
	c, err := acp.Start(acp.Config{Path: path, Args: args, Env: env, Dir: acptest.SessionDir(t),
		Handler: b.Handler(func(_ context.Context, c Card) string { return answer(c) })})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	return c, sid
}

func turn(t *testing.T, b *Bridge, c *acp.Client, sid, text string) string {
	t.Helper()
	b.TurnStart(text)
	stop, err := c.Prompt(ctxT(t), sid, text)
	if err != nil {
		t.Fatalf("prompt %q: %v", text, err)
	}
	b.TurnEnd(stop)
	return stop
}

func TestEndToEndAPlainTurn(t *testing.T) {
	b := New(Config{})
	c, sid := session(t, b, nil)
	turn(t, b, c, sid, "plain")
	got := b.Snapshot()
	if kinds(got) != "prompt,reply" || got[1].Markdown != "Hello world" {
		t.Fatalf("entries = %+v", got)
	}
	if b.Status() != StatusIdle {
		t.Errorf("status = %s", b.Status())
	}
}

func TestEndToEndAToolCallIsUpdatedInPlace(t *testing.T) {
	b := New(Config{})
	c, sid := session(t, b, nil)
	turn(t, b, c, sid, "tool")
	got := b.Snapshot()
	if kinds(got) != "prompt,tool,reply" {
		t.Fatalf("kinds = %s", kinds(got))
	}
	tool := got[1]
	if tool.Status != transcript.StatusOK || tool.Label != "Run echo" || tool.Summary != "" || !strings.Contains(tool.Diff, "+hi") {
		t.Errorf("tool = %+v", tool)
	}
	if u := b.Usage(); u.Used != 1234 || u.Size != 200000 {
		t.Errorf("usage = %+v", u)
	}
}

func TestEndToEndAPermissionWhileChunksStream(t *testing.T) {
	b := New(Config{})
	asked := make(chan Card, 1)
	release := make(chan struct{})
	path, args, env := acptest.Command(t)
	c, err := acp.Start(acp.Config{Path: path, Args: args, Env: env, Dir: acptest.SessionDir(t),
		Handler: b.Handler(func(ctx context.Context, card Card) string {
			asked <- card
			select {
			case <-release:
			case <-ctx.Done():
			}
			return "allow-1"
		})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan string, 1)
	b.TurnStart("perm-chunks")
	go func() {
		stop, err := c.Prompt(ctxT(t), sid, "perm-chunks")
		if err != nil {
			t.Errorf("prompt: %v", err)
		}
		done <- stop
	}()
	var card Card
	select {
	case card = <-asked:
	case <-time.After(30 * time.Second):
		t.Fatal("no permission request")
	}
	// The 20 chunks sent before the request were handled before it, in wire
	// order, and are one reply.
	if p := b.Pending(); p == nil || p.RequestID != card.RequestID {
		t.Fatalf("pending = %+v", p)
	}
	if b.Status() != StatusWaiting {
		t.Errorf("status = %s", b.Status())
	}
	if got := b.Snapshot(); kinds(got) != "prompt,tool,reply" || got[2].Markdown != strings.Repeat("a", 20) {
		t.Errorf("before the answer: %+v", got)
	}
	close(release)
	select {
	case stop := <-done:
		b.TurnEnd(stop)
	case <-time.After(30 * time.Second):
		t.Fatal("turn did not end")
	}
	got := b.Snapshot()
	if kinds(got) != "prompt,tool,reply" || got[2].Markdown != strings.Repeat("a", 20)+strings.Repeat("b", 20) || got[1].Status != transcript.StatusOK {
		t.Errorf("after: %+v", got)
	}
	if b.Pending() != nil || b.Status() != StatusIdle {
		t.Errorf("pending %+v, status %s", b.Pending(), b.Status())
	}
}

func TestEndToEndCancelMidTurn(t *testing.T) {
	b := New(Config{})
	c, sid := session(t, b, nil)
	b.TurnStart("slow")
	stop := make(chan string, 1)
	go func() {
		s, err := c.Prompt(ctxT(t), sid, "slow")
		if err != nil {
			t.Errorf("prompt: %v", err)
		}
		stop <- s
	}()
	deadline := time.Now().Add(30 * time.Second)
	for len(b.Snapshot()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("no chunk arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Cancel(sid); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-stop:
		if s != acp.StopCancelled {
			t.Errorf("stop = %s", s)
		}
		b.TurnEnd(s)
	case <-time.After(30 * time.Second):
		t.Fatal("the turn did not end")
	}
	got := b.Snapshot()
	if len(got) != 3 || got[1].Markdown != "working..." || got[2].Kind != transcript.KindNotice || got[2].Text != "Turn cancelled." {
		t.Errorf("entries = %+v", got)
	}
	if b.Status() != StatusIdle {
		t.Errorf("status = %s", b.Status())
	}
}

// TestEndToEndAFloodOfUpdates: a thousand 64 KiB rewrites of one tool call and
// 8 MiB of thoughts. The caps hold, Flush is throttled, and the heap does not
// grow with the traffic.
func TestEndToEndAFloodOfUpdates(t *testing.T) {
	b := New(Config{MinInterval: 50 * time.Millisecond})
	c, sid := session(t, b, nil)

	var base runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&base)

	stopFlush := make(chan struct{})
	flushed := make(chan int, 1)
	go func() {
		n := 0
		for {
			select {
			case <-stopFlush:
				flushed <- n
				return
			default:
			}
			for _, e := range b.Flush() {
				n++
				if len(e.Summary) > 1024 || len(e.Markdown) > maxText {
					t.Errorf("an entry went out over its cap: summary %d, markdown %d", len(e.Summary), len(e.Markdown))
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	started := time.Now()
	turn(t, b, c, sid, "storm")
	took := time.Since(started)
	close(stopFlush)
	handed := <-flushed

	got := b.Snapshot()
	if kinds(got) != "prompt,tool,thinking" {
		t.Fatalf("kinds = %s", kinds(got))
	}
	if got[1].Status != transcript.StatusOK || got[1].Label != "Run storm" || !got[1].HasDetail || len(got[1].Summary) > 200 {
		t.Errorf("tool = %+v", got[1])
	}
	d, ok := b.Detail(got[2].ID)
	if !ok || len(d.Text) != maxText {
		t.Errorf("thinking detail holds %d bytes, want the cap %d", len(d.Text), maxText)
	}
	// 9000 updates in `took`: hand-outs are bounded by the interval for
	// content growth plus a few for new entries and status changes.
	limit := int(took/(50*time.Millisecond))*2 + 10
	if handed > limit {
		t.Errorf("%d hand-outs for 9193 updates in %v (limit %d)", handed, took, limit)
	}

	// Held: one tool output, one 4 MiB thinking text, nothing per update. Well
	// under the traffic of ~72 MiB.
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(base.HeapAlloc); grew > 64<<20 {
		t.Errorf("heap grew by %d MiB during the flood", grew>>20)
	}
}
