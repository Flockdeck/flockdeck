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
`flockdeck-chat.exe`, the same program linked for the console, which the hook and
status line commands Claude Code runs use there. The Windows release carries both.

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

To add a help page, write `ui/help/pages/<slug>.md`, starting with an `#`
heading and a paragraph of summary (the contents list takes it), and add its slug to
`order` in `ui/help/help.go`.

To change a shortcut, edit `ui/help/keys.go` and regenerate the table in
[docs/keys.md](keys.md):

```sh
go test ./ui/help -run TestKeysDocShortcuts -update
```

## Programs under cmd/

These make what is published, not the application:

- `cmd/release` builds every platform and writes the archives and `checksums.txt`
  a release is made of. `make package` runs it.
- `cmd/docgen` writes docs.flockdeck.ai, rendering the using-Flockdeck pages from
  `ui/help`.
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
in `ui/src/vendor/` from the `@xterm/xterm`, `@xterm/addon-fit`,
`@xterm/addon-search` and `@xterm/addon-webgl` packages.

### The shell

The front end is the team-graph shell in `ui/src/`. The directory
keeps the name it had while the old front end existed; it is the only one now, and a
plain `go build` embeds it. There is no build tag and no runtime flag. The old front end
was deleted in T162; its history is in git, and `docs/old-ui-fix-ledger.md` is a record of
what was fixed in it while it was frozen.

```sh
go build -o flockdeck.exe .
go test ./ui/...
```

`e2e.js` (the end-to-end script the relay window uses), `vendor/` (the terminal library
and Preact), the fonts and the icons are all in `ui/src`.

`index.html` names its module by the relative path `assets/main.js`, so the page works only when it is served from `/`, where `assets/` is the server's `/assets/`. A module must be served as JavaScript: `ui/embed.go` registers the type for `.mjs`, because on Windows Go takes it from the registry, which has none, and the file server would send `text/plain`.

The shell is plain ES modules that the browser loads as they are: no bundler and
no build step. `index.html` loads `assets/main.js` with `<script type="module">`, every
import is a relative path, and nothing is inline or evaluated, so the page runs under the
relay's `script-src 'self'`. Preact, its hooks and htm are vendored in
`ui/src/vendor/` (versions, licences and hashes in `LICENSES.md` beside them and in
`next/checksums.txt`). A component is written with htm: html`<${Tree} model=${model} />`.

The page frame is in `ui/src/shell/`: `app.js` (the shell: a rail of the open projects, a top strip for the goal tabs and the view switch, the main area, a region for the drawer and a notice area), `rail.js`, `palette.js` and `keys.js`. Key handling is driven by the key table in the server's hello, which comes from `ui/help/keys.go`, so a binding is changed there and nowhere in the new code; a test fails if the shell names a key of its own (key names are allowed only in the marked vocabulary block of `keys.js`). The palette lists every action of the table, runs the ones listed in `IMPLEMENTED` in `shell/app.js`, and marks the rest "not in this window yet". `ui/src/app.css` begins with the typefaces and tokens copied unchanged from `assets/app.css` and a test holds the copy equal; the layout under it uses the tokens and writes no colour or typeface. The key table is read when the hello's `keys` change and not on each render. A binding in it that the reader cannot turn into a key (an unknown modifier in a remap, say) does nothing, and the shell says so in its notice area and names the action, so a key that has stopped working is not a silent failure. The palette is a modal dialog: the keyboard goes to its field when it opens and back to where it was when it closes, Tab stays inside it, Escape closes it from the field or a row, and the region and projects keys close it. The page passes each project the rail chooses, and each active project the state names, to `onWatch`. Below 641px wide the rail becomes a bar along the top and the main area a list; `NARROW_QUERY` in `app.js` and the media rule in the stylesheet are held together by a test.

Its tests are Node's own `node:test`, in `ui/test/`, run from
`next_test.go` under the tag. Components are drawn with Preact to a string, so no browser
or DOM is needed; `preact-render-to-string` is in `ui/test/vendor/` and is never
embedded. Node is a development and CI tool only and is not part of a release. Without
node on PATH the Go test skips, and fails when `CI` is set. Other Go tests there check
that no file under `ui/src` has an inline script or style, `eval`, `new Function`, an
absolute URL or a bare import, and that the vendored files match `checksums.txt`.

The shell reaches the running Flockdeck through `assets/lib/`. `socket.js` is the client of
`/ws/control`: it drops a frame that is not a JSON object with a string `type`, and reconnects after
1 s, 2 s, 4 s and so on up to 30 s, starting over at the next `hello`. `store.js` is a plain store
with `getState`, `subscribe`, `send`, `watchGraph` and `unwatchGraph`; its header comment lists
everything in a snapshot (`hello`, `state`, `graph`, `graphGone`, `graphRefused`, `graphError`,
`notice`). It never sends a graph command in a window reached through the relay, and sends
`graphWatch` again after a reconnect for the project last asked for. `use-store.js` is the Preact
hook. The tests drive them with a fake socket and a fake clock (`test/fakes.mjs`).

`assets/lib/terminal.js` and `pty.js` show one pane's terminal. `pty.js` is the client of `/ws/pty`: it
asks only for the output the terminal is missing, reconnects with a backoff, sends input in frames of at
most 1 MiB, and, when the hello says the window is reached through the relay and carries the desktop's key,
seals every frame with `e2e.js` (served as it is) and closes rather than
fall back to plain. `terminal.js` puts xterm on it and keeps the size rule the old window had: the terminal takes
the size of the pty, and nothing sends a resize when it opens, closes or its box changes. Only
`fitToBox()` does, for a drag or a maximise. A structured pane gets a one-line stub and no socket, and a
viewer that may not watch panes gets no socket. The page has to load `assets/vendor/xterm.js`,
`assets/vendor/addon-fit.js` and `assets/e2e.js` as classic scripts before the module, as the old page does. The three must not be `async` or `defer`, or the module runs before they have: the terminal then shows "The terminal library is not loaded." and opens nothing.

A release is never built with the tag. The test workflow builds, vets and tests the
tagged variant on all three platforms, and `ui/release_tag_test.go`
fails when the release workflow, the Dockerfile, the Makefile or `cmd/release`
passes `-tags` with `next`. The release workflow runs the tests first, so a release
with the tag does not get built.

## Cutting a release

Pushing a tag such as `v1.2.3` cuts a release. The tag is the version. Before you
push it, add `docs/releases/v1.2.3.md`: the release workflow stops with an error when
that notes file is missing. Its contents are the notes the update chip shows and the
GitHub release carries.
A `-rc.` pre-release without one gets GitHub's generated notes instead.
The workflow vets and tests, builds all six platforms,
publishes them on GitHub, signs them and uploads them to `dl.flockdeck.ai`
(`scripts/publish-downloads.sh`). A tag with a suffix, such as `v1.2.3-rc.1`, is a
pre-release. It is published under its own version and never becomes the latest.
[Installing, updating and uninstalling](install.md) describes how the result is
signed and checked.
