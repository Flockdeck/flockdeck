package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// A preference is changed in what is on disk, not in the copy read at
// start-up. Written back whole, that copy undid anything else that had changed
// the file since - another instance's settings, with two running - and a
// start-up read that failed was written over every setting there was.
func TestSavingOnePreferenceKeepsTheOthersOnDisk(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	// Another instance, started after this one, changes the font size.
	if err := store.SavePrefs(store.Prefs{FontSize: 20}); err != nil {
		t.Fatalf("save: %v", err)
	}
	sendCmd(t, conn, command{Cmd: "dismissTip", ID: "palette"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.Dismissed("palette") })

	var saved store.Prefs
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if saved = store.LoadPrefs(); saved.Dismissed("palette") {
			break
		}
	}
	if !saved.Dismissed("palette") {
		t.Fatalf("the dismissed hint was not saved: %+v", saved)
	}
	if saved.FontSize != 20 {
		t.Errorf("saving a dismissed hint put the font size back to %d; want the 20 another instance saved", saved.FontSize)
	}
}
