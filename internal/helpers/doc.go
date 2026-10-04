// Package helpers installs, supervises and launches helper apps: small local
// programs, shipped as release archives, that Flockdeck downloads, verifies,
// installs under its state directory, starts, stops and opens in a browser.
//
// A helper is not a plugin. It runs as its own process with an environment
// built from an allowlist, and gets no access to the control server, the hooks
// server or the instance token. The design, and what each check is for, is in
// docs/plans/helper-apps.md.
//
// Nothing from a downloaded archive is executed until the user starts the
// helper, and nothing is installed that has not passed, in order: the
// signature on checksums.txt, the version rules, the SHA-256 of the archive,
// and the archive safety checks in extract.go.
package helpers
