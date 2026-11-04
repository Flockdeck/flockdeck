package server

import (
	"testing"
	"time"
)

// TestDriveEntriesDoNotWaitForADriveThatHangs covers a drive mapped to a share
// that is offline: its Stat can wait out the network timeout, and the picker
// asked on every click. The drives that answer are listed, in order, and the
// one that does not is left out.
func TestDriveEntriesDoNotWaitForADriveThatHangs(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	exists := func(drive string) bool {
		switch drive[0] {
		case 'C', 'E':
			return true
		case 'Z':
			<-release
			return true
		}
		return false
	}

	start := time.Now()
	got := driveEntries(exists, 100*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the listing took %v with one drive hanging", elapsed)
	}
	if len(got) != 2 || got[0].Path != `C:\` || got[1].Path != `E:\` || got[1].Name != "E:" {
		t.Errorf("drives = %+v, want C: and E: only", got)
	}
}
