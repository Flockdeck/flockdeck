package team

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDeliveryStatesInOrder(t *testing.T) {
	s, now := newTestStore()
	m, _ := s.Send(Sender{Pane: "p1", Name: "w"}, "p2", "hello")
	if got := m.Delivery(); got != DeliveryQueued {
		t.Fatalf("a new message is %q, want queued", got)
	}
	step := func(want string, do func()) {
		t.Helper()
		*now = now.Add(time.Second)
		do()
		if got := s.List("p2", true)[0].Delivery(); got != want {
			t.Errorf("delivery = %q, want %q", got, want)
		}
	}
	step(DeliveryNotified, func() { s.MarkNotified("p2", m.ID) })
	step(DeliveryWoken, func() { s.MarkWoken("p2", m.ID) })
	step(DeliveryRead, func() { _, _ = s.Read("p2", m.ID) })
	got := s.List("p2", true)[0]
	if !got.NotifiedAt.Before(got.WokenAt) || !got.WokenAt.Before(got.ReadAt) {
		t.Errorf("times out of order: %+v", got)
	}
}

// The newest state is the furthest along, whatever order they were marked in.
func TestDeliveryStatesOutOfOrder(t *testing.T) {
	s, _ := newTestStore()
	m, _ := s.Send(Sender{Pane: "p1", Name: "w"}, "p2", "hello")
	s.MarkWoken("p2", m.ID)
	s.MarkNotified("p2", m.ID)
	if got := s.List("p2", true)[0].Delivery(); got != DeliveryWoken {
		t.Errorf("woken then notified = %q, want woken", got)
	}
	_, _ = s.Read("p2", m.ID)
	s.MarkNotified("p2", m.ID)
	s.MarkWoken("p2", m.ID)
	if got := s.List("p2", true)[0].Delivery(); got != DeliveryRead {
		t.Errorf("marks after a read = %q, want read", got)
	}
}

func TestAMarkKeepsTheFirstTimeAndSkipsOtherInboxes(t *testing.T) {
	s, now := newTestStore()
	m, _ := s.Send(Sender{Pane: "p1", Name: "w"}, "p2", "hello")
	s.MarkNotified("p2", m.ID)
	first := s.List("p2", true)[0].NotifiedAt
	*now = now.Add(time.Hour)
	s.MarkNotified("p2", m.ID)
	s.MarkNotified("p3", m.ID) // not that pane's message
	s.MarkNotified("p2", 999)
	if got := s.List("p2", true)[0].NotifiedAt; !got.Equal(first) {
		t.Errorf("NotifiedAt moved from %v to %v", first, got)
	}
	if got := s.List("p2", true)[0]; got.WokenAt != (time.Time{}) {
		t.Errorf("a mark for another pane or id changed the message: %+v", got)
	}
}

// A snapshot written before the delivery times existed loads unchanged.
func TestLoadAnOldFormatSnapshot(t *testing.T) {
	old := `{"version":1,"next":3,"boxes":{"p2":[
	  {"id":1,"from":{"pane":"p1","name":"w"},"to":"p2","time":"2026-10-11T09:00:00Z","text":"read one","read":true},
	  {"id":2,"from":{"pane":"p1","name":"w"},"to":"p2","time":"2026-10-11T09:01:00Z","text":"unread one"}]}}`
	s, _ := newTestStore()
	if err := s.Load([]byte(old)); err != nil {
		t.Fatal(err)
	}
	got := s.List("p2", true)
	if len(got) != 2 || got[0].Delivery() != DeliveryRead || got[1].Delivery() != DeliveryQueued {
		t.Fatalf("loaded %+v", got)
	}
	if !got[0].ReadAt.IsZero() || !got[1].NotifiedAt.IsZero() {
		t.Errorf("times appeared from nowhere: %+v", got)
	}
	// And a snapshot of them does not write the new fields.
	data, _ := s.Snapshot()
	for _, f := range []string{"notifiedAt", "wokenAt", "readAt"} {
		if strings.Contains(string(data), f) {
			t.Errorf("snapshot has %s for a message without it", f)
		}
	}
}

func TestDeliveryTimesSurviveASnapshot(t *testing.T) {
	s, _ := newTestStore()
	m, _ := s.Send(Sender{Pane: "p1", Name: "w"}, "p2", "hello")
	s.MarkNotified("p2", m.ID)
	data, _ := s.Snapshot()
	r, _ := newTestStore()
	if err := r.Load(data); err != nil {
		t.Fatal(err)
	}
	if got := r.List("p2", true)[0].Delivery(); got != DeliveryNotified {
		t.Errorf("restored delivery = %q", got)
	}
}

func TestStoreReportsEventsWithoutText(t *testing.T) {
	s, _ := newTestStore()
	var log EventLog
	s.OnEvent(log.Append)
	m, _ := s.Send(Sender{Pane: "p1", Name: "w"}, "p2", "secret words")
	s.MarkNotified("p2", m.ID)
	s.MarkWoken("p2", m.ID)
	_, _ = s.Read("p2", m.ID)
	_, _ = s.Read("p2", m.ID) // again: no second event

	ev := log.Events()
	want := []string{"message", "delivery:notified", "delivery:woken", "delivery:read"}
	if len(ev) != len(want) {
		t.Fatalf("events %+v", ev)
	}
	for i, e := range ev {
		got := e.Kind
		if e.State != "" {
			got += ":" + e.State
		}
		if got != want[i] || e.ID != m.ID {
			t.Errorf("event %d = %+v, want %s", i, e, want[i])
		}
	}
	if ev[0].From != "p1" || ev[0].To != "p2" {
		t.Errorf("message event %+v", ev[0])
	}
	if strings.Contains(string(log.Encode()), "secret") {
		t.Errorf("the log holds message text: %s", log.Encode())
	}
}

func TestEventLogDropsTheOldestOverTheCount(t *testing.T) {
	var log EventLog
	for i := 1; i <= MaxEvents+10; i++ {
		log.Append(Event{Kind: EventDelivery, ID: i, State: DeliveryRead, Time: time.Unix(int64(i), 0).UTC()})
	}
	ev := log.Events()
	if len(ev) != MaxEvents || ev[0].ID != 11 || ev[len(ev)-1].ID != MaxEvents+10 {
		t.Errorf("kept %d events from %d to %d", len(ev), ev[0].ID, ev[len(ev)-1].ID)
	}
}

func TestEventLogDropsTheOldestOverTheSize(t *testing.T) {
	var log EventLog
	long := strings.Repeat("p", 4000)
	for i := 1; i <= 100; i++ {
		log.Append(Event{Kind: EventMessage, ID: i, From: long, To: "q"})
	}
	if n := len(log.Encode()); n > MaxEventBytes {
		t.Errorf("the log is %d bytes, the cap is %d", n, MaxEventBytes)
	}
	ev := log.Events()
	if len(ev) == 0 || ev[len(ev)-1].ID != 100 || ev[0].ID == 1 {
		t.Errorf("kept %d events", len(ev))
	}
}

func TestEventLogSkipsDamagedLinesAndKeepsNoText(t *testing.T) {
	data := strings.Join([]string{
		`{"kind":"message","id":1,"time":"2026-10-19T10:00:00Z","from":"a","to":"b","text":"smuggled"}`,
		`{"kind":"delivery","id":1,"stat`,
		`not json`,
		`{"kind":"nonsense","id":2,"time":"2026-10-19T10:00:00Z"}`,
		`{"kind":"delivery","id":0,"time":"2026-10-19T10:00:00Z"}`,
		``,
		`{"kind":"delivery","id":1,"state":"read","time":"2026-10-19T10:01:00Z"}`,
	}, "\n")
	var log EventLog
	log.Load([]byte(data))
	ev := log.Events()
	if len(ev) != 2 || ev[0].Kind != EventMessage || ev[1].State != DeliveryRead {
		t.Fatalf("loaded %+v", ev)
	}
	if strings.Contains(string(log.Encode()), "smuggled") {
		t.Error("a text field was kept")
	}
	// What is saved loads back the same.
	var again EventLog
	again.Load(log.Encode())
	a, _ := json.Marshal(again.Events())
	b, _ := json.Marshal(log.Events())
	if string(a) != string(b) {
		t.Errorf("round trip changed the log:\n%s\n%s", a, b)
	}
	if got := (&EventLog{}).Encode(); len(got) != 0 {
		t.Errorf("an empty log encodes to %q", got)
	}
}
