# Structured-agent adapters

Some agents can be driven over the Agent Client Protocol (ACP) instead of
through a terminal. Flockdeck talks to them through an adapter, and each release
lists the exact adapters and versions it was tested with in
[`internal/acp/manifest/adapters.json`](../internal/acp/manifest/adapters.json).
There is no adapter for Claude Code.

## Tested with

This table is made from the manifest, and a test fails if the two differ.

<!-- adapters:begin -->
| Adapter | Starts with | Needs Node | Accepts | Tested with | Tested on |
| --- | --- | --- | --- | --- | --- |
| opencode | `opencode acp` | no | 1.19.3 or newer | 1.19.3 | 2026-10-19 |
| Gemini CLI | `gemini --acp` | yes, 20.0.0 or newer | 0.64.0 or newer | 0.64.0 | 2026-10-19 |
| Codex (ACP adapter) | `codex-acp` | yes, 20.0.0 or newer | 2.2.2 only | 2.2.2 | 2026-10-19 |
<!-- adapters:end -->

"Accepts" is the rule a release applies to the copy on the machine. An adapter
with a floor (opencode, Gemini) accepts that version and anything newer, because
the user installs it. An adapter that is pinned (Codex) is installed by Flockdeck
at exactly that version, because its adapter ships previews almost daily and has
changed owner before.

## Node

Node.js is not bundled. An adapter marked "yes" under Needs Node cannot start
without it. Before starting one, Flockdeck looks for `node` on `PATH` and runs
`node --version`. If it is missing, too old or does not run, the pane shows a
"Node required" state with the fix, which is to install a current Node.js from
<https://nodejs.org> and restart Flockdeck. The code returns this as a
`*manifest.NodeRequiredError` with the adapter, the problem (`missing`, `too
old` or `unusable`), the version found, the version needed and the fix text.

## The weekly canary

[`.github/workflows/acp-canary.yml`](../.github/workflows/acp-canary.yml) runs
every Sunday and on request. It does not run on pull requests and cannot block a
merge. It does two things:

1. Runs the fake-agent contract suite (`go test ./internal/acp/...`).
2. For each adapter and each of two channels, `pinned` (the version in the
   manifest) and `latest` (the newest the registry has, which for some adapters
   is a preview), installs the adapter into a temporary folder and runs one
   `initialize` handshake. A failure on `pinned` means the adapter changed under
   the pin or the machine changed; a failure on `latest` means the next bump
   will need work.

The handshake never logs in, sends a prompt or opens a session. Every run is
isolated: the adapter gets a home, config, cache and temporary folders that are
all inside one new temporary folder, a working directory inside it too, an
environment that holds only `PATH` and those folders (no keys or tokens), and
npm's cache inside it. The log prints that environment, and a closing step
lists whether anything in the runner's real home changed.

The adapter is installed with `--ignore-scripts` and from an `env -i` shell, so no install
script runs and the runner's variables do not reach npm. The version to install is
checked to be a plain version first, the job has no Go build cache, and every action
is pinned to a commit. If an adapter needs an install script to start, the handshake
fails and the manifest is what to change.

The result is shown in the run's summary. To change what a release accepts, edit
`adapters.json` and the table above in the same pull request, and set
`testedWith` and `testedOn` to what the canary last passed with.

## Structured entries in agents.json

A structured agent is an entry with `"runner": "acp"`. It names an adapter of the
manifest instead of a program, and says what the agent may see:

```json
{
  "id": "opencode-acp",
  "name": "opencode (structured)",
  "runner": "acp",
  "acp": {
    "adapter": "opencode",
    "auth": "none",
    "passEnv": ["HOME", "USERPROFILE", "APPDATA"],
    "mcp": true
  }
}
```

- `adapter` is an id from the manifest. The command, its arguments, the pinned
  version and the Node requirement come from there. An id the build does not
  know is refused with a notice, and the entry is not added; on a built-in
  entry, the built-in stays as it was.
- `auth` is `none` for an agent that answers without a login, or `external`
  (also the default) for one you sign in to with its own tool. Flockdeck never
  logs in. When the agent asks, the pane shows the methods it offered and the
  entry's `login` text.
- `passEnv` is the whole list of variables the agent gets from Flockdeck's
  environment, on top of the few that let it find programs and a temp folder
  (`acp.MinimalEnv`). Keys and tokens are not passed unless an entry names them,
  and a name starting `FLOCKDECK_` is dropped with a notice whatever the entry
  says.
- `mcp` says whether the agent takes per-session `mcpServers`.

The built-in entries are `opencode-acp`, and `gemini-acp` and `codex-acp`, which
are hidden until the tasks that check them end to end have landed. They are
separate ids from the terminal entries for the same tools, so a saved layout
keeps meaning what it did. There is no entry for Claude Code.

Structured entries are not offered in the picker until `"structuredPanes": true`
is set at the top level of `agents.json`. They can still be found by id.
