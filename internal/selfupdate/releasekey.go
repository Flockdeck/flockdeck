package selfupdate

// releaseKey is the public half of the Ed25519 key every release is signed
// with. Terraform makes the key (svc/flockdeck-site/release.tf in terrawost)
// and writes its private half into the release workflow's
// FLOCKDECK_SIGNING_KEY secret itself. The public half goes here exactly as
//
//	terraform output -raw flockdeck_release_public_key
//
// prints it, a PEM block, in a raw string:
//
//	const releaseKey = `-----BEGIN PUBLIC KEY-----
//	MCowBQYDK2VwAyEA...
//	-----END PUBLIC KEY-----
//	`
//
// The one line of base64 `go run ./cmd/release -keygen <file>` prints is taken
// too, for a key made that way instead. The updater trusts a release's
// manifest, or its checksums.txt, from dl.flockdeck.ai only when it carries a
// signature this key checks.
//
// PLACEHOLDER, to be replaced before the next release: set releaseKey to that
// public key, in place of releaseKeyPlaceholder. Until then a release build
// fails its tests (TestReleaseKeyIsInPlace), `cmd/release -sign` refuses to
// sign, and a build that somehow shipped with it would never trust
// dl.flockdeck.ai and would go on updating from GitHub.
//
// The key is compiled in rather than fetched because a key fetched from where
// the release is would vouch for nothing: whoever could replace the release
// could replace the key beside it.
const releaseKey = releaseKeyPlaceholder

// releaseKeyPlaceholder is what releaseKey holds until the production key has
// been made. It is not a key, so nothing can ever be signed for it.
const releaseKeyPlaceholder = "PLACEHOLDER: the public key `terraform output -raw flockdeck_release_public_key` prints"
