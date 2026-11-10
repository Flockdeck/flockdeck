package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// picStream is a Stream holding fixed entries. It counts the Detail calls, so a
// test can tell which path a key took.
type picStream struct {
	entries     []transcript.Entry
	detailCalls int
}

func (s *picStream) Refresh() []transcript.Entry  { return nil }
func (s *picStream) Snapshot() []transcript.Entry { return s.entries }
func (s *picStream) Detail(id string) (transcript.Detail, bool) {
	s.detailCalls++
	if id == "t1" {
		return transcript.Detail{Text: "tool output"}, true
	}
	return transcript.Detail{}, false
}
func (s *picStream) Reset() bool     { return false }
func (s *picStream) SetLight(bool)   {}
func (s *picStream) EntryCount() int { return len(s.entries) }

func detailReply(t *testing.T, c *controlClient) conversationDetailMsg {
	t.Helper()
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationDetail" {
		t.Fatalf("got %s, want conversationDetail", raw)
	}
	var d conversationDetailMsg
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestConversationDetailForAPromptPictureKey(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "claude")
	root, err := uploadsRoot()
	if err != nil {
		t.Fatal(err)
	}
	data := testPNG(t, 2, 2)
	writeUpload(t, root, paneID, "1-shot.png", data)
	big := make([]byte, maxPromptPictureDetail+1)
	copy(big, data)
	writeUpload(t, root, paneID, "2-big.png", big)
	st := &picStream{entries: []transcript.Entry{
		{ID: "e1", Kind: transcript.KindPrompt, Images: []transcript.PromptImage{
			{MediaType: "image/png", Bytes: len(data), Upload: "1-shot.png"},
			{MediaType: "image/png", Bytes: len(big), Upload: "2-big.png"},
			{MediaType: "image/png", Bytes: 5, Upload: "9-swept.png"},
		}},
		{ID: "t1", Kind: transcript.KindTool, HasDetail: true},
	}}
	pc := srv.convos.get(paneID)
	pc.mu.Lock()
	pc.supported, pc.stream = true, st
	pc.mu.Unlock()

	// A window on this machine: the key reads the upload and never the stream's Detail.
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "conversationDetail", ID: paneID, Entry: "e1#0"})
	if d := detailReply(t, c); d.Gone || d.Detail.Data != base64.StdEncoding.EncodeToString(data) {
		t.Errorf("picture 0 = %+v", d)
	}
	if st.detailCalls != 0 {
		t.Errorf("a picture key asked the stream for detail %d times", st.detailCalls)
	}
	for _, key := range []string{
		"e1#1",   // over what one reply carries
		"e1#2",   // swept
		"e1#3",   // past the list
		"t1#0",   // an entry that is not a prompt
		"nope#0", // no such entry
	} {
		srv.handleCommand(c, command{Cmd: "conversationDetail", ID: paneID, Entry: key})
		if d := detailReply(t, c); !d.Gone || d.Detail.Data != "" {
			t.Errorf("%s = %+v, want gone", key, d)
		}
	}
	// A plain key still goes to the stream.
	srv.handleCommand(c, command{Cmd: "conversationDetail", ID: paneID, Entry: "t1"})
	if d := detailReply(t, c); d.Gone || d.Detail.Text != "tool output" || st.detailCalls != 1 {
		t.Errorf("tool detail = %+v, calls = %d", d, st.detailCalls)
	}

	// A viewer that may not watch panes is refused; one that may is still a relay
	// window, and conversation text does not go to a relay window.
	for _, watch := range []bool{false, true} {
		setAccess(t, srv, "viewer-dev", "viewer", watch)
		v := &controlClient{out: make(chan []byte, 8), remote: true, device: "viewer-dev"}
		srv.handleCommand(v, command{Cmd: "conversationDetail", ID: paneID, Entry: "e1#0"})
		select {
		case raw := <-v.out:
			var n noticeMsg
			if err := json.Unmarshal(raw, &n); err != nil || n.Type != "notice" || !n.Error {
				t.Fatalf("watch=%v: got %s, want a refusal", watch, raw)
			}
			want := viewerRefused
			if watch {
				want = conversationsOnDesktop
			}
			if n.Text != want {
				t.Errorf("watch=%v: text = %q, want %q", watch, n.Text, want)
			}
			if bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(data))) {
				t.Errorf("watch=%v: the picture went to the window", watch)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("watch=%v: no answer", watch)
		}
		expectNoMessage(t, v, 50*time.Millisecond)
	}
}
