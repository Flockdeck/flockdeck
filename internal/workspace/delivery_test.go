package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

// Handing out the hook notice marks the messages notified, reading marks them
// read, and the event log records both without the text.
func TestHookNoticeMarksNotifiedAndTheEventLogHoldsNoText(t *testing.T) {
	c := newCrew(t)
	if _, err := c.ws.SendMessage(c.lead, c.reviewer, "launch codes: 0000"); err != nil {
		t.Fatal(err)
	}
	box, _ := c.ws.Inbox(c.reviewer, true)
	if len(box) != 1 || box[0].Delivery() != team.DeliveryQueued {
		t.Fatalf("after the send: %+v", box)
	}
	if c.ws.HookNotice(c.reviewer) == "" {
		t.Fatal("no notice was given")
	}
	box, _ = c.ws.Inbox(c.reviewer, true)
	if box[0].Delivery() != team.DeliveryNotified {
		t.Errorf("after the notice: %q", box[0].Delivery())
	}
	if _, err := c.ws.ReadMessage(c.reviewer, box[0].ID); err != nil {
		t.Fatal(err)
	}
	box, _ = c.ws.Inbox(c.reviewer, true)
	if box[0].Delivery() != team.DeliveryRead {
		t.Errorf("after the read: %q", box[0].Delivery())
	}

	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, eventsFile))
	if err != nil {
		t.Fatal(err)
	}
	var log team.EventLog
	log.Load(data)
	var states []string
	for _, e := range log.Events() {
		states = append(states, e.Kind+":"+e.State)
	}
	if got, want := strings.Join(states, " "), "message: delivery:notified delivery:read"; got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
	if strings.Contains(string(data), "launch codes") {
		t.Errorf("the event log holds message text: %s", data)
	}
}
