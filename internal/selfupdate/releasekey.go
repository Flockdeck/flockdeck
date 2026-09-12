package selfupdate

// releaseKey is the public half of the Ed25519 key every release is signed
// with, in base64, exactly as `go run ./cmd/release -keygen <file>` prints it.
// The updater trusts a latest.json or a checksums.txt from dl.flockdeck.ai only
// when it carries a signature this key checks.
//
// PLACEHOLDER, to be replaced before the next release: set releaseKey to the
// public key `go run ./cmd/release -keygen <file>` prints, in place of
// releaseKeyPlaceholder. Until then a release build fails its tests
// (TestReleaseKeyIsInPlace), `cmd/release -sign` refuses to sign, and a build
// that somehow shipped with it would never trust dl.flockdeck.ai and would go
// on updating from GitHub.
//
// The key is compiled in rather than fetched because a key fetched from where
// the release is would vouch for nothing: whoever could replace the release
// could replace the key beside it.
const releaseKey = releaseKeyPlaceholder

// releaseKeyPlaceholder is what releaseKey holds until the production key has
// been made. It is not a key, so nothing can ever be signed for it.
const releaseKeyPlaceholder = "PLACEHOLDER: the public key `go run ./cmd/release -keygen <file>` prints"
