package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
)

// A fingerprint only protects anyone once a person has compared it with what
// the device shows. The commands here record that they did (see
// internal/remote/verified.go for what the record is bound to). Recording it
// is the desk's alone: whether anyone compared a code is the one thing a
// window reached through the relay cannot be believed about, since a relay
// that wanted a swapped key trusted would simply have the window say so.

// deviceVerifier is what the server needs of remote access to record and read
// verifications. It is separate from RemoteAccess so that a remote access
// without it (a test fake) simply has nothing verified.
type deviceVerifier interface {
	DeviceVerifyState(deviceID string, origin remote.KeyOrigin, key []byte) remote.VerifyState
	VerifyDevice(ctx context.Context, deviceID string, origin remote.KeyOrigin, claimed string) error
	UnverifyDevice(deviceID string, origin remote.KeyOrigin) error
	ForgetDevice(deviceID string) error
	ForgetDevicesNotIn(ids []string) error
}

func (s *Server) deviceVerifier() deviceVerifier {
	if ra := s.remoteAccess(); ra != nil {
		if v, ok := ra.(deviceVerifier); ok {
			return v
		}
	}
	return nil
}

// deskOnlyVerify is what a window reached through the relay is told when it
// asks to mark a device verified, or to take that away.
const deskOnlyVerify = "A device is marked verified on the machine Flockdeck runs on, after comparing its code with the one the device shows, not from a window reached through the relay"

// Verification states a device view carries for one origin's key.
const (
	verifyStateVerified = "verified"
	verifyStateChanged  = "changed"
)

// verifyStateFor is the view's word for one device key's state: "" for
// not verified, "verified", or "changed" for a key that was verified and has
// since been replaced.
func (s *Server) verifyStateFor(deviceID string, origin remote.KeyOrigin, enc string) string {
	v := s.deviceVerifier()
	if v == nil || enc == "" {
		return ""
	}
	pub, err := e2e.DecodePublicKey(enc)
	if err != nil {
		return ""
	}
	switch v.DeviceVerifyState(deviceID, origin, pub.Bytes()) {
	case remote.Verified:
		return verifyStateVerified
	case remote.KeyChanged:
		return verifyStateChanged
	}
	return ""
}

// remoteVerify marks a device's key for one origin verified. cmd.ID is the
// device, cmd.Kind the origin ("usual" or "desk") and cmd.Text the code the
// window showed the person, which is checked against the key as it is now.
func (s *Server) remoteVerify(c *controlClient, cmd command) {
	if s.refusedVerifyThroughRelay(c, cmd) {
		return
	}
	origin, ok := remote.ParseKeyOrigin(cmd.Kind)
	if !ok || cmd.ID == "" {
		c.notify("No device was named", true)
		return
	}
	v := s.deviceVerifier()
	if v == nil {
		c.notify("Verifying a device is not available here", true)
		return
	}
	go func() {
		defer s.surviveFor(c, "verifying a device")
		ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
		defer cancel()
		switch err := v.VerifyDevice(ctx, cmd.ID, origin, cmd.Text); {
		case errors.Is(err, remote.ErrFingerprintMismatch):
			c.notify("The code on screen is not the code for that device's key now. Look at the code again and compare it", true)
		case err != nil:
			c.notify("Could not verify that device: "+err.Error(), true)
		default:
			c.notify("Device verified", false)
		}
		s.remoteDevices(c)
	}()
}

// remoteUnverify takes a device's verification for one origin away.
func (s *Server) remoteUnverify(c *controlClient, cmd command) {
	if s.refusedVerifyThroughRelay(c, cmd) {
		return
	}
	origin, ok := remote.ParseKeyOrigin(cmd.Kind)
	if !ok || cmd.ID == "" {
		c.notify("No device was named", true)
		return
	}
	v := s.deviceVerifier()
	if v == nil {
		c.notify("Verifying a device is not available here", true)
		return
	}
	if err := v.UnverifyDevice(cmd.ID, origin); err != nil {
		c.notify("Could not remove that verification: "+err.Error(), true)
	} else {
		c.notify("Verification removed", false)
	}
	s.remoteDevices(c)
}

// refusedVerifyThroughRelay refuses a verify or unverify that came from a
// window reached through the relay, and reports whether it did. Nothing in
// the app sends one from there, so one is either a modified page or a relay
// trying it: the attempt goes to error.log and to the windows at the desk,
// as well as being refused to the window that sent it.
func (s *Server) refusedVerifyThroughRelay(c *controlClient, cmd command) bool {
	if !c.remote {
		return false
	}
	c.notify(deskOnlyVerify, true)
	logf("refused %s for device %q from a window reached through the relay (paired device %q)", cmd.Cmd, cmd.ID, c.device)
	desk := fmt.Sprintf("A window reached through the relay tried to change which devices are marked verified (%s). It was refused", cmd.Cmd)
	for _, w := range s.clientList() {
		if !w.remote {
			w.notify(desk, true)
		}
	}
	return true
}

// forgetVerifiedExcept drops the records of devices no longer in a roster
// the relay returned.
func (s *Server) forgetVerifiedExcept(devices []remote.Device) {
	v := s.deviceVerifier()
	if v == nil {
		return
	}
	ids := make([]string, len(devices))
	for i, d := range devices {
		ids[i] = d.ID
	}
	_ = v.ForgetDevicesNotIn(ids)
}

// forgetVerified drops everything recorded for a device that was unpaired.
func (s *Server) forgetVerified(deviceID string) {
	if v := s.deviceVerifier(); v != nil {
		_ = v.ForgetDevice(deviceID)
	}
}
