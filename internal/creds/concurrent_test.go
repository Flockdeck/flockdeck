package creds

import (
	"fmt"
	"sync"
	"testing"
)

// Keys set at the same moment from this process -- two agents' keys saved from
// the window -- are all kept. Each save reads the store, adds its key and
// writes the whole store back, so without one at a time a save wrote back a
// store without the other's key. A save from `flockdeck keys set` is another
// process, which storeMu does not reach and this test cannot start; what
// covers it is the save and the read waiting out a hold on the file, tested
// in hold_windows_test.go.
func TestKeysSetAtOnceAreAllKept(t *testing.T) {
	isolateConfig(t)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			if err := Set(fmt.Sprintf("agent%d", i), fmt.Sprintf("sk-test-%d", i)); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	names, err := Names()
	if err != nil || len(names) != n {
		t.Errorf("the store holds %d keys (%v), want all %d", len(names), err, n)
	}
}
