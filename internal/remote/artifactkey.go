package remote

import (
	"context"

	"github.com/jmwri/flockdeck/internal/e2e"
)

// E2EDeviceKey is the end-to-end public key the relay's roster gives deviceID
// for origin, as the raw bytes of the key (internal/e2e.DecodePublicKey, then
// Bytes), and false where there is none or the roster cannot be read. The
// artifacts socket asks for it before a handshake so that it can check the
// key is the one the user verified, and asks again afterwards to see that it
// did not change in between.
func (m *Manager) E2EDeviceKey(ctx context.Context, deviceID string, origin KeyOrigin) ([]byte, bool) {
	if deviceID == "" {
		return nil, false
	}
	devices, err := m.e2eRoster(ctx, deviceID, origin)
	if err != nil {
		return nil, false
	}
	enc := origin.key(devices[deviceID])
	if enc == "" {
		return nil, false
	}
	pub, err := e2e.DecodePublicKey(enc)
	if err != nil {
		return nil, false
	}
	return pub.Bytes(), true
}
