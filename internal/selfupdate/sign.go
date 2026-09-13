package selfupdate

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Releases are signed with Ed25519. Each signed file has a .sig beside it
// holding the signature of the file's exact bytes, in standard base64 on one
// line, so it survives being pasted, uploaded as text and read by a person.
//
// Each half of the key is read in either of two forms, so that a key made
// either way works without anything converted by hand. Terraform makes the
// release key (tls_private_key, in terrawost) and gives the private half as a
// PKCS#8 PEM (private_key_pem_pkcs8) and the public half as a PEM of its
// SubjectPublicKeyInfo (public_key_pem). `go run ./cmd/release -keygen` writes
// the private half as the key's 32-byte seed and prints the public half, each
// in standard base64 on one line.
//
// Both formats live here, beside the one thing that reads them, so the command
// that writes them (cmd/release) and the updater that checks them cannot drift.

// trustedKey is the primary key signatures are checked against: releaseKey
// decoded, or nil while releaseKey is still the placeholder or not a key at
// all. Tests put a key of their own here.
var trustedKey = parsePublicKey(releaseKey)

// trustedStandbyKey is the standby key signatures are checked against as
// well: releaseKeyStandby decoded, or nil while it is still the placeholder.
// Tests put a key of their own here.
var trustedStandbyKey = parsePublicKey(releaseKeyStandby)

// ReleaseKey is the primary public key compiled into this build, and false
// while it is still the placeholder.
func ReleaseKey() (ed25519.PublicKey, bool) {
	k := parsePublicKey(releaseKey)
	return k, k != nil
}

// StandbyKey is the standby public key compiled into this build, and false
// while it is still the placeholder: before the user has generated the
// standby keypair and its public half has been put in releaseKeyStandby.
func StandbyKey() (ed25519.PublicKey, bool) {
	k := parsePublicKey(releaseKeyStandby)
	return k, k != nil
}

// TrustedKeys is every key this build's updater accepts a signature from: the
// primary, and the standby once releaseKeyStandby holds a real key instead of
// its placeholder. Every signature this package checks against "the release
// key" is checked against all of these, so that a release signed with the
// standby is accepted exactly as one signed with the primary is.
func TrustedKeys() []ed25519.PublicKey {
	var keys []ed25519.PublicKey
	if trustedKey != nil {
		keys = append(keys, trustedKey)
	}
	if trustedStandbyKey != nil {
		keys = append(keys, trustedStandbyKey)
	}
	return keys
}

// TrustKeysForTest makes this build trust exactly primary and standby (either
// may be nil) in place of releaseKey and releaseKeyStandby, until the
// returned func puts back what it trusted before. It exists for tests of
// other packages, such as cmd/release's, that need TrustedKeys and runSign's
// check against it to see keys the test itself generated, since the real
// compiled keys' private halves are held by nobody the tests can reach.
//
// It panics outside a test binary: exported only so another package's tests
// can reach it, never so that anything shipped could swap out the keys a
// build trusts.
func TrustKeysForTest(primary, standby ed25519.PublicKey) func() {
	if !testing.Testing() {
		panic("selfupdate: TrustKeysForTest is for tests only")
	}
	oldPrimary, oldStandby := trustedKey, trustedStandbyKey
	trustedKey, trustedStandbyKey = primary, standby
	return func() { trustedKey, trustedStandbyKey = oldPrimary, oldStandby }
}

// parsePublicKey decodes a public key as Terraform's public_key_pem gives it,
// or as `cmd/release -keygen` prints it, and returns nil for anything that is
// neither. A PEM block is found wherever it starts, so what `terraform output`
// prints around it does no harm.
func parsePublicKey(s string) ed25519.PublicKey {
	if block, _ := pem.Decode([]byte(s)); block != nil {
		if block.Type != "PUBLIC KEY" {
			return nil
		}
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if pub, ok := k.(ed25519.PublicKey); err == nil && ok {
			return pub
		}
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// EncodePublicKey is a public key as `cmd/release -keygen` prints it.
func EncodePublicKey(k ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(k)
}

// EncodeSigningKey is the content of the signing key's file -keygen writes.
func EncodeSigningKey(k ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k.Seed()) + "\n"
}

// ParseSigningKey reads the signing key as FLOCKDECK_SIGNING_KEY holds it: the
// PKCS#8 PEM Terraform gives, or the base64 seed -keygen writes. The error
// never quotes the text, which is a secret.
func ParseSigningKey(s string) (ed25519.PrivateKey, error) {
	if block, _ := pem.Decode([]byte(s)); block != nil {
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if key, ok := k.(ed25519.PrivateKey); err == nil && ok && block.Type == "PRIVATE KEY" {
			return key, nil
		}
		return nil, errors.New("a PEM block, but not an Ed25519 private key in PKCS#8, which is what Terraform's tls_private_key gives as private_key_pem_pkcs8")
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("not a signing key: that is an Ed25519 private key in PKCS#8 PEM, as Terraform's tls_private_key gives it, or one line of base64 holding 32 bytes, as `go run ./cmd/release -keygen` writes it")
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

// VerifyAny reports whether sig, the content of a .sig file, is any of keys'
// signature of data: the primary release key's, or the standby's once it
// holds one. It refuses everything, as errNoKey, when keys is empty, which is
// every build's own trusted keys (TrustedKeys) while both are still
// placeholders.
func VerifyAny(keys []ed25519.PublicKey, data, sig []byte) error {
	if len(keys) == 0 {
		return errNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("the signature is not %d bytes of base64", ed25519.SignatureSize)
	}
	for _, k := range keys {
		if ed25519.Verify(k, data, raw) {
			return nil
		}
	}
	return errors.New("the signature does not match any trusted release key")
}
