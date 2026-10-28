package e2e_test

import (
	"bytes"
	"testing"

	"github.com/jmwri/flockdeck/internal/e2e"
)

// The host's session says which device key it was derived with: the one the
// handshake used, whatever a roster says.
func TestTheHostSessionNamesTheDeviceKeyItWasDerivedWith(t *testing.T) {
	devicePriv, _ := e2e.GenerateStaticKey()
	hostPriv, _ := e2e.GenerateStaticKey()
	dh, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	host, resp, err := e2e.RespondHostHandshake(hostPriv, devicePriv.PublicKey(), hello)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dh.Finish(resp); err != nil {
		t.Fatal(err)
	}
	if got := host.DeviceStaticKey(); !bytes.Equal(got, devicePriv.PublicKey().Bytes()) {
		t.Errorf("the session names %x, want the device's key", got)
	}
	got := host.DeviceStaticKey()
	got[0] ^= 0xff
	if bytes.Equal(host.DeviceStaticKey(), got) {
		t.Error("the key returned can be used to change the session's")
	}
}
