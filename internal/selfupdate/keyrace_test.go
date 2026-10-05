package selfupdate

import (
	"crypto/ed25519"
	"sync"
	"sync/atomic"
	"testing"
)

// A signature check running in the background reads the keys while a test
// swaps them. Run with -race, this fails if the two are not serialised.
func TestTrustedKeysCanBeReadWhileTheyAreSwapped(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = TrustedKeys()
				reads.Add(1)
			}
		}
	}()
	// Swap until the reader has certainly been running alongside.
	for reads.Load() < 1000 {
		TrustKeysForTest(pub, nil)()
	}
	for i := 0; i < 1000; i++ {
		TrustKeysForTest(pub, nil)()
	}
	close(stop)
	wg.Wait()
}
