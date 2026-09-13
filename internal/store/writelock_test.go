package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Saves of one file from one process take turns at the rename. Racing there,
// they refuse each other on Windows, and on a slow machine eight of them
// could wait out the whole retry budget and lose a save.
func TestSavesOfOneFileFromOneProcessTakeTurns(t *testing.T) {
	isolateConfig(t)

	var inside, most atomic.Int32
	beforeRename = func(string) {
		n := inside.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inside.Add(-1)
	}
	t.Cleanup(func() { beforeRename = nil })

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each writer saves a different list, so none of them is spared
			// the write as unchanged.
			open := []string{fmt.Sprintf("/repo/%d", i)}
			for j := 0; j < i; j++ {
				open = append(open, fmt.Sprintf("/repo/%d/%d", i, j))
			}
			if err := SaveSession(&Session{Open: open, Active: open[0]}); err != nil {
				t.Errorf("save session: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if m := most.Load(); m > 1 {
		t.Errorf("%d saves of the one file were at the rename at once; saves from one process should take turns", m)
	}
}
