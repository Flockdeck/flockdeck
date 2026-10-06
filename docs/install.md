# Installing, updating and uninstalling Flockdeck

## Requirements

Flockdeck needs nothing but the binary on Windows and macOS. On Linux the window
is drawn by GTK 4 and WebKitGTK. The `.deb` and `.rpm` packages depend on them
(`libgtk-4-1` and `libwebkitgtk-6.0-4` on Debian and Ubuntu, `gtk4` and
`webkitgtk6.0` on Fedora). The archive and the install script do not check, so
install those libraries yourself first.

What a pane runs is a separate matter. A CLI agent has to be on your `PATH`
([Claude Code](https://claude.com/claude-code) for the default one), and an API
agent needs a key. The agent picker lists every agent Flockdeck knows about and
greys out the ones this machine does not have, with where to get them.

## Install scripts

```sh
curl -fsSL https://flockdeck.ai/install.sh | sh   # macOS and Linux
irm https://flockdeck.ai/install.ps1 | iex        # Windows, in PowerShell
```

Each script installs the release it was published with. The site is regenerated
for every release, and that writes the release's version and the SHA-256 of each
of its archives into both scripts. The script downloads the archive for your
machine from `dl.flockdeck.ai`, or from GitHub when that cannot be reached, and
checks it against the SHA-256 it carries itself, not against a `checksums.txt`
fetched from the place the archive came from.

It then puts Flockdeck in a directory you own, so neither installing nor
updating asks for admin rights:

- Linux: `~/.local/bin`, with an app-menu entry and icon under `~/.local/share`.
- macOS: `~/Applications/Flockdeck.app`.
- Windows: `%LOCALAPPDATA%\Programs\flockdeck`, with a Start menu shortcut.

The scripts are in `cmd/sitegen/assets`, beside the page that serves them. A
release also comes as a `.deb` and an `.rpm` for Linux and as a Windows
installer. The scripts do not install those. The Windows installer installs for
all users and asks for admin rights.

The site can be older than the latest release, for example when a CDN has cached
the page. So once the pinned release is installed, the script asks the new
binary to run `flockdeck update`, which moves it to the latest release. That
update is checked against the release signature, the same way every later update
is. The script itself checks only the pinned archive.

### Settings the scripts read

- `FLOCKDECK_VERSION`: another release, such as `v0.2.8`. It is left exactly as
  installed and not moved to the latest. The script carries checksums for its own
  release only, so any other release is checked against the `checksums.txt`
  downloaded beside it. To also stop Flockdeck updating itself once it runs, set
  `FLOCKDECK_UPDATE=off` where it runs.
- `FLOCKDECK_INSTALL_DIR`: another directory to install into.
- `FLOCKDECK_DOWNLOAD`: a mirror to fetch the release files from instead, laid
  out as `<mirror>/<version>/<file>`. It is the only place asked, with no falling
  back to GitHub, and it also skips moving to the latest, which a mirror may not
  carry. The mirror is asked for the script's own release unless
  `FLOCKDECK_VERSION` names another, and for that release's `checksums.txt` too
  when it does.
- `FLOCKDECK_NO_MODIFY_PATH=1`: Windows only. Leaves `PATH` and the Start menu
  alone. The script then says how to start Flockdeck by its path.

```sh
curl -fsSL https://flockdeck.ai/install.sh | FLOCKDECK_INSTALL_DIR=~/bin sh
```

```powershell
$env:FLOCKDECK_VERSION = 'v0.2.8'; irm https://flockdeck.ai/install.ps1 | iex
```

## With Go

Go 1.27 or later:

```sh
go install github.com/jmwri/flockdeck@latest
```

On Windows this leaves a console window behind the app, so use `make install`
from a clone there. [Building from source](building.md) has the rest.

## Staying up to date

Every release is published at `https://dl.flockdeck.ai` under its version, as
one archive per platform (a `.zip` for Windows, a `.tar.gz` elsewhere) with a
`checksums.txt` and a `manifest.json` that describe them. Both files are signed
with the release's Ed25519 key, whose public half is built into Flockdeck.
Nothing under a version changes once it is published. A second, standby key has
been trusted alongside the first since v0.3.5. Its private half is held offline
and never touches a build or a workflow, so it can sign one release if the first
key is lost or compromised. GitHub carries every release as well, as a mirror.

`latest.json` names the latest release, `{"version":"v1.2.3"}`, and nothing
more. It is not signed, and it is not purged from the CDN when it moves, because
it needs neither. All it can do is point at a release whose own manifest is
signed. An old or forged one can hold an update back for as long as it stays
cached. It can never get anything unsigned or older installed.

A running Flockdeck checks for releases and downloads anything newer in the
background. It asks `dl.flockdeck.ai` first and trusts only what carries the
release key's signature. When the site cannot be reached, answers with something
else or fails a signature, it goes to GitHub instead and logs why. Either way
the download is checked against its published SHA-256, and the request carries
nothing that tells one installation from another.

Nothing is replaced while you are working. When a release is ready, an **Update**
button naming the version appears at the right of the top bar. Installing it
from there is a restart you ask for: the layout is saved and reopened, but the
agents running in panes are stopped, which is why it is never done for you.
Otherwise the update goes in when Flockdeck next quits, and the next start is the
new version. Set `FLOCKDECK_UPDATE=off`, or turn off **Check for updates** in
Settings › Account & plan, to stop the background check and to stop a
downloaded update being put in place on exit. The subcommand still works.

From a terminal:

```sh
flockdeck update          # fetch the latest release and put it in place
flockdeck update -check   # say whether there is one, and stop
flockdeck update -version=v0.3.44   # install that release, forward or back; asks first (-yes skips)
```

Replacing the binary leaves a running instance alone, since it is already
loaded. A build you made yourself (stamped `dev` by `go build`, or by
`git describe` when built with make) is never replaced by a release.

## Uninstalling

If Flockdeck Remote is on, remove this machine from its relay first with
`flockdeck remote remove`, so the relay forgets it. That leaves your account on
the relay; `flockdeck remote delete-account` erases it instead. Then quit
Flockdeck and delete it:

- Linux: `~/.local/bin/flockdeck`, with `flockdeck.desktop` and `flockdeck.png`
  under `~/.local/share/applications` and `~/.local/share/icons`.
- macOS: `~/Applications/Flockdeck.app`.
- Windows: the `%LOCALAPPDATA%\Programs\flockdeck` folder, its Start menu
  shortcut and its entry in your `PATH`.

A `.deb` or `.rpm` is removed with your package manager, and the Windows
installer under Apps.

Settings, layouts and keys are kept apart from the program, in `%AppData%\flockdeck`
on Windows, `~/Library/Application Support/flockdeck` on macOS or
`~/.config/flockdeck` on Linux. Delete that folder too. On Windows also delete
`%AppData%\flockdeck.exe`, which holds the window's WebView2 data. Worktrees
Flockdeck made are ordinary git worktrees beside your repositories, and they stay
until you remove them.
