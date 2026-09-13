package creds

import (
	"fmt"
	"sync"
	"testing"
)

// Keys set at the same moment -- two agents' keys saved from the window, or
// one from the window and one from `flockdeck keys set` -- are all kept. Each
// save reads the store, adds its key and writes the whole store back, so
// without one at a time a save wrote back a store without the other's key.
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
