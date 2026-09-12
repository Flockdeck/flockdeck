package selfupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Releases are signed with Ed25519. Each signed file has a .sig beside it
// holding the signature of the file's exact bytes, in standard base64 on one
// line, so it survives being pasted, uploaded as text and read by a person. The
// signing key's own file, which cmd/release writes and FLOCKDECK_SIGNING_KEY
// holds, is the key's 32-byte seed in the same encoding.
//
// Both formats live here, beside the one thing that reads them, so the command
// that writes them (cmd/release) and the updater that checks them cannot drift.

// trustedKey is the key signatures are checked against: releaseKey decoded, or
// nil while releaseKey is still the placeholder or not a key at all. Tests put
// a key of their own here.
var trustedKey = parsePublicKey(releaseKey)

// ReleaseKey is the public key compiled into this build, and false while it is
// still the placeholder.
func ReleaseKey() (ed25519.PublicKey, bool) {
	k := parsePublicKey(releaseKey)
	return k, k != nil
}

// parsePublicKey decodes a public key as `cmd/release -keygen` prints it, or
// returns nil for anything that is not one.
func parsePublicKey(s string) ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// EncodePublicKey is a public key as it is printed, and as releaseKey holds it.
func EncodePublicKey(k ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(k)
}

// EncodeSigningKey is the content of a signing key's file.
func EncodeSigningKey(k ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k.Seed()) + "\n"
}

// ParseSigningKey reads a signing key's file, as FLOCKDECK_SIGNING_KEY holds
// it. The error never quotes the text, which is a secret.
func ParseSigningKey(s string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("not a signing key written by `go run ./cmd/release -keygen`: that is one line of base64 holding 32 bytes")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// Sign is the content of the .sig file for data.
func Sign(k ed25519.PrivateKey, data []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(k, data)) + "\n")
}

// errNoKey is the reason nothing is trusted from dl.flockdeck.ai by a build
// whose releaseKey is still the placeholder.
var errNoKey = errors.New("this build has no release key to check signatures with")

// Verify reports whether sig, the content of a .sig file, is key's signature
// of data.
func Verify(key ed25519.PublicKey, data, sig []byte) error {
	if key == nil {
		return errNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("the signature is not %d bytes of base64", ed25519.SignatureSize)
	}
	if !ed25519.Verify(key, data, raw) {
		return errors.New("the signature does not match the release key")
	}
	return nil
}
