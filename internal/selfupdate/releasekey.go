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
// This is Flockdeck's release key, made by Terraform on 12 September 2026.
//
// Since v0.3.3 a copy believes nothing it downloads without this key's
// signature, from GitHub as much as from dl.flockdeck.ai, so GitHub is no
// way round a key it does not hold. A copy that never got a new key cannot
// update at all once the old key stops signing, and has to be installed again
// by hand. So a new key means a release carrying its public half here, itself
// signed by the old key, shipped well before the old key stops signing: long
// enough for the copies in use to have moved to it. TrustedKeys trusts both
// releaseKey and releaseKeyStandby at once, but rotating the primary is still
// no quicker for it: every release signed by the new key alone is refused by
// a copy that only ever trusted the old one.
//
// The key is compiled in rather than fetched because a key fetched from where
// the release is would vouch for nothing: whoever could replace the release
// could replace the key beside it.
const releaseKey = `-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEApz2sDY7Kt1XaOwrrH/NdCkChEMoBty1B1bXB5J4ROc4=
-----END PUBLIC KEY-----
`

// releaseKeyStandby is a second, backup release key, trusted alongside
// releaseKey. Decided on 13 September 2026: if releaseKey is ever lost or
// compromised, a release signed with the standby's private half still has to
// be accepted by every copy already installed, so the standby has to be
// trusted from before that day comes, not from the release that first needs
// it.
//
// Unlike releaseKey, the standby's private half is never held by Terraform or
// any other system: `go run ./cmd/release -keygen -standby <file>` makes it,
// and the file goes straight offline, with the person who ran it, and nowhere
// a build or a workflow can reach. It is used only in the emergency this key
// exists for (see cmd/release's package comment): signing one release by
// hand, with its private half in FLOCKDECK_SIGNING_KEY for that run alone.
//
// The public half goes here exactly as -keygen prints it, one line of
// base64. While it is still the placeholder, nothing trusts it, and every
// release is checked against releaseKey alone, as before the standby existed.
//
// This is Flockdeck's standby release key, made with
// `go run ./cmd/release -keygen` on 13 September 2026, before -standby
// existed. Its private half is held offline by the user alone.
const releaseKeyStandby = "ty1U7aJkzB0IqhCQdtLCCeaSsWs608iLxIBXZcpvyyE="

// releaseKeyPlaceholder is what releaseKey holds until the production key has
// been made. It is not a key, so nothing can ever be signed for it.
const releaseKeyPlaceholder = "PLACEHOLDER: the public key `terraform output -raw flockdeck_release_public_key` prints"

// releaseKeyStandbyPlaceholder is what releaseKeyStandby holds until the user
// has generated the standby keypair and pasted its public half in. It is not
// a key, so nothing can ever be signed for it, and TrustedKeys leaves it out.
const releaseKeyStandbyPlaceholder = "PLACEHOLDER: the public key `go run ./cmd/release -keygen -standby <file>` prints"
