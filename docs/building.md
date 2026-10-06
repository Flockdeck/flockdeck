# Building and working on Flockdeck

## Build from source

With Go 1.27 or later, from a clone:

```sh
make build      # a binary for this machine
make dist       # binaries for all six supported platforms
```

Where there is no `make`, as on many Windows machines, `go build .` builds the same
program. On Windows add `-ldflags -H=windowsgui`, as the Makefile does, so that it
opens without a console window behind it. `make build` on Windows also builds
`flockdeck-chat.exe`, the same program linked for the console, which is what an API
agent's pane runs there. The Windows release carries both.

The Windows build needs no C toolchain and cross-compiles from any machine. The
Linux and macOS window is drawn through cgo (GTK 4 and WebKitGTK, Cocoa), so a
release build of either needs a matching machine with a C toolchain. The Makefile
builds with `CGO_ENABLED=0`, which compiles everywhere but leaves a Linux or macOS
binary without a native window: the page opens in your default browser instead.

`make package` runs `cmd/release`, which cross-builds every platform and writes the
archives and `checksums.txt` that a release is made of. `make installer` builds the
Windows installer and needs NSIS's `makensis` on `PATH`.

## Tests

```sh
make check     # go vet + tests
make race      # tests under the race detector (needs a C toolchain)
```

The suite covers the layout tree, the hook transport, the output ring and bell
detection, layout persistence, and the server end to end, including a full terminal
round trip where a keystroke sent over a WebSocket reaches the process and its
output comes back. It also holds the documentation to the code: every action in the
key table has to be implemented in the front end and listed in the help, no binding
may be written into the front end by hand, and the table in
[docs/keys.md](keys.md) has to match the key table. [docs/testing.md](testing.md)
explains how tests are kept away from your own config directory and keys.

One table in `internal/pricing` holds every price the app states, each rate dated
with the day it was read from the provider's own pricing page. The table is never
fetched at run time. A release build's tests fail when any rate is more than 120
days old, so the pages are read again for each release.

## Adding an agent

Add a `Spec` to the built-in catalog under `internal/agent`. Verify its flags
against the tool itself first. An entry that claims a resume flag the CLI does not
have is worse than one that claims nothing, and an agent with no declared
capabilities still runs as a terminal with a program in it.

## Adding a help page, or changing a shortcut

To add a help page, write `internal/help/pages/<slug>.md`, starting with an `#`
heading and a paragraph of summary (the contents list takes it), and add its slug to
`order` in `internal/help/help.go`.

To change a shortcut, edit `internal/help/keys.go` and regenerate the table in
[docs/keys.md](keys.md):

```sh
go test ./internal/help -run TestKeysDocShortcuts -update
```

## Programs under cmd/

These make what is published, not the application:

- `cmd/release` builds every platform and writes the archives and `checksums.txt`
  a release is made of. `make package` runs it.
- `cmd/docgen` writes docs.flockdeck.ai, rendering the using-Flockdeck pages from
  `internal/help`.
- `cmd/portproxy` is the sidecar the Helm chart runs beside the server, in the same
  image.
- `cmd/deploydrift` is a CI check, run daily by `deploy-drift.yml`. It reports
  repositories whose main branch has changes that no version tag has picked up yet,
  and keeps one tracking issue in step with that. Merging to main deploys nothing
  for the relay, billing, site or docs. A `v*` tag does.
- `cmd/sitegen` writes the landing page at flockdeck.ai, its trust, privacy, terms,
  refunds and licences pages, and the install scripts it serves, from
  `cmd/sitegen/assets`:
  `go run ./cmd/sitegen -out ../flockdeck-site -release v1.2.3 -checksums checksums.txt`,
  where `checksums.txt` is that release's, from dl.flockdeck.ai, with its
  `checksums.txt.sig` beside it. sitegen checks that signature against the release
  key built into it, and writes nothing unless the key signed it. The install
  scripts install that release and check its archive against the SHA-256 written
  into them.

Four development aids also live under `cmd/` and are not part of the product:

- `cmd/hooktest` starts one real agent pane, sends it a prompt and prints every
  status transition, which checks the hook pipeline end to end. Build the binary
  first and point it there: `go run ./cmd/hooktest -hookbin ./flockdeck.exe`. The
  prompt is a real one, so it spends a short turn through your own Claude Code
  login.
- `cmd/ctl` drives a running instance over its control socket, `cmd/statedump`
  prints what the instance reports about its panes, and `cmd/treedump` prints the
  tab and split structure. Together they let you exercise and inspect the interface
  without clicking: send a rearrangement, then read the resulting tree back.

```sh
go build -o flockdeck.exe . && ./flockdeck.exe -solo -no-window
go run ./cmd/ctl "ws://127.0.0.1:PORT/ws/control?t=TOKEN" '{"cmd":"splitPane","dir":"h","kind":"agent","agent":"claude"}'
```

## The front end

The front end is vendored, not fetched at build time. To update it, replace the files
in `internal/webui/assets/vendor/` from the `@xterm/xterm`, `@xterm/addon-fit`,
`@xterm/addon-search` and `@xterm/addon-webgl` packages.

## Cutting a release

Pushing a tag such as `v1.2.3` cuts a release. The tag is the version. Before you
push it, add `docs/releases/v1.2.3.md`: the release workflow stops with an error when
that notes file is missing. The workflow vets and tests, builds all six platforms,
publishes them on GitHub, signs them and uploads them to `dl.flockdeck.ai`
(`scripts/publish-downloads.sh`). A tag with a suffix, such as `v1.2.3-rc.1`, is a
pre-release. It is published under its own version and never becomes the latest.
[Installing, updating and uninstalling](install.md) describes how the result is
signed and checked.
