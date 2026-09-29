# Site screenshots

This directory retakes the screenshots in `../assets/shots/` from a staged
Flockdeck built from this checkout. It is outside `assets/`, so none of it is
embedded in sitegen.

| File | What it shows |
| --- | --- |
| `deck.png` | the staged window: four agents on four checkouts of shopfront |
| `fanout.png` | the Fan out dialog over it, from the Gemini pane's plan |
| `worktrees.png` | the Worktrees panel |
| `agent-picker.png` | the New agent tab picker |
| `settings.png` | Settings, on Account & plan, with the desktop enrolled on a relay |
| `remote-choices.png` | the web client on a phone, on the waiting pane, through a local relay |
| `og.png` | `../assets/og.html` rendered at 1200x630 over the new `deck.png` |

Each 3168x1970 shot is a 1584x985 page at 2x, and has a half-size copy
(`-1584.png`; `remote-choices-585.png` for the phone's 1170x2532) that is an
exact 2x2 box average of it. The templates' `width`, `height` and `srcset`
depend on those sizes, and `halve.py` refuses a capture of any other size.

## What is staged

Everything in the pictures is fictional.

- `C:\code\shopfront`, a small Go repository, with worktrees on
  `feat/pricing-package` (a new file), `feat/stripe-refunds` (a changed file)
  and `feat/search-api`, and `fix/flaky-cart-test`, whose folder is deleted so
  the panel shows it as gone. `billing-service`, `design-system` and `infra`
  sit beside it for the rail. Commits are by Sam, `sam@example.com`.
- `mockagent/` stands in for Claude Code, Codex and Gemini CLI (it is
  registered under their ids in the staged `agents.json`). It prints a
  scripted session chosen by the folder it runs in, and nothing talks to a
  model. The Claude mocks hand Claude-shaped status line input to the real
  `flockdeck statusline` bridge, so the header figures are the app's own: the
  pricing pane is on an API key and shows an estimated cost, the waiting pane
  is on a subscription and shows tokens and its five-hour limit.
- shopfront defaults to Claude Code's Sonnet with routing on, so the fan-out
  routes one task down to Haiku and one, by the payments rule, up to Opus.
- For the phone and the Settings shot, a `flockdeck-relay` built from a
  checkout of the relay repository runs on loopback with `-enforce-plans`, as
  the shared relay does, and the staged desktop is enrolled on it as
  "workstation". The phone is a 390x844, 3x, touch browser paired through it.

## Isolation: read this before running it

The machine this runs on is usually running its owner's own Flockdeck, and the
shell running this may be one of its panes. `shots.mjs` and `guard.mjs` keep
these rules, and refuse to go on when one does not hold:

1. **The staged state is in a temp work directory.** On Windows the state
   directory is `os.UserConfigDir()`, which is `%APPDATA%`, plus `flockdeck`
   (`internal/store`); the home directory (Claude's settings, git's config) is
   `%USERPROFILE%`. Both are pointed into the work directory. `HOME` or
   `FLOCKDECK_HOME` alone do **not** move it: an earlier attempt set those and
   still wrote into the real `%APPDATA%\flockdeck`. The harness refuses a
   staged state directory that is, contains or sits inside the live one, and
   after start-up checks that the staged instance's `instance.json` is in the
   work directory, that its address and pid are not the live instance's, and
   that the live `instance.json` has not changed.
2. **Nothing started inherits the caller's environment.** The staged
   instance, and through it the mock agents, get an environment built from an
   allow list (`stagedEnv`): the variables Windows needs, a `PATH` of Windows
   and git only (so no real agent is found), and the redirected directories.
   A pane's shell carries `FLOCKDECK_API`, `FLOCKDECK_TOKEN`,
   `FLOCKDECK_PANE` and more, and their `PERCH_*` twins, all pointing at the
   live instance; a mock that saw them would post to it or start panes in it.
   The browsers get the same, with the real `USERPROFILE` (Chrome will not
   start without it); `go build` gets the caller's environment with every
   `FLOCKDECK_*` and `PERCH_*` removed.
3. **The live state directory is compared before and after.** Every file in
   `%APPDATA%\flockdeck` (and the old `perch`) is listed with its size, time
   and SHA-256 before anything starts and after everything stops. A file
   added or removed, or a changed `instance.json`, fails the run. The live
   instance keeps saving its own layouts as its owner works, so a changed
   layout file is printed for a person to look at rather than failed on.
4. **It runs headless and cleans up.** `-solo` starts a separate instance
   rather than joining a running one, and `-no-window` serves without opening
   a window; the server listens on a random loopback port. Browsers run
   headless with profiles in the work directory. When it finishes or fails,
   everything whose command line holds the work directory is stopped (never
   the live instance's pid), the work directory and `C:\code` are removed.
   `C:\code` is refused if it exists and is not the harness's own.

## Running it

Windows, with Go, git, Node 22+, Python with Pillow, and Chrome.

```sh
# once: playwright-core, outside the repository
npm install --prefix "$TEMP/flockdeck-shots-node" playwright-core

# the relay, for settings.png and remote-choices.png: any checkout of
# Flockdeck/flockdeck-relay (it embeds the web client, a private module)
mkdir -p "$TEMP/relay-src" && git -C ../flockdeck-relay archive origin/main | tar -x -C "$TEMP/relay-src"

node cmd/sitegen/shots/shots.mjs -relay-src "$TEMP/relay-src"
```

- `-only deck,fanout,...` retakes some of them (`og` re-renders the card).
- `-keep` leaves the work directory, with the live-state listings
  `live-before.json` and `live-after.json`, for inspection.
- Without `-relay-src`, `settings.png` is taken with no relay enrolled and
  `remote-choices.png` is left alone.
- `PLAYWRIGHT_CORE_DIR` and `CHROME` override where playwright-core and
  Chrome are looked for.

It writes the PNGs straight into `../assets/shots/`. Look at every one at full
size before committing: the same composition as before, nothing cut off, and
nothing but the staged data in it. If what a picture shows has changed, update
its alt text in `../assets/index.html.tmpl`.
