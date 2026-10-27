# Structured-agent adapters

Some agents can be driven over the Agent Client Protocol (ACP) instead of
through a terminal. Flockdeck talks to them through an adapter, and each release
lists the exact adapters and versions it was tested with in
[`internal/acp/manifest/adapters.json`](../internal/acp/manifest/adapters.json).
Claude Code and Codex have Flockdeck-written Go adapters, built into the `flockdeck` binary and
hidden for now (T204, T206); see [Claude Code adapter](acp-claude-adapter.md) and
[Codex adapter](acp-codex-adapter.md). The direction is no third-party adapter packages and no
Node; see [ACP direction](acp-direction.md). The Codex row below is the interim third-party
adapter. The Go adapters are not in the table because the manifest marks them hidden.

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

`runsWithoutAsking` is set on an adapter whose agent may run shell commands and edits without
sending a permission request. It is what was seen of the tested version. A structured pane of such an agent
shows a standing warning in the drawer header. opencode is marked although Flockdeck starts it to ask
(`launchEnv`, below): a repository's own agent permissions can override that. For an adapter with a
`permissionProbe` the flag holds only until the launch check has run, and the check's answer replaces it.

`permissionProbe` names a command that lists the agents and their resolved permissions (`args`, after the
adapter's command), the agent whose rules the value is built from (`agent`), the `launchEnv` variable to put the
value in (`setting`) and the tools that must ask (`tools`, up to four). Flockdeck runs it in the pane's directory
without the setting, builds a value that keeps the denials and turns allows into asks, runs it again with the
value, and flags the pane if a tool still allows in any agent it lists. The value falls
back to the plain `launchEnv` one when the rules cannot be read or kept. `internal/acp/permprobe` has the rules.

`launchEnv` is environment Flockdeck sets for the adapter when it starts it: up to four variables, with
capital-letter names that are not Flockdeck's own and fixed values written in the manifest. It is added after
`passEnv` and replaces a variable of the same name, so the user's environment cannot undo it. opencode has one:
`OPENCODE_CONFIG_CONTENT`, which makes `bash` and `edit` ask (see [Structured panes](structured-pane.md)). The weekly canary (`acp-canary.yml`) runs
`opencode debug agent build` with it set, on the pinned and the latest version, and fails if `bash` or `edit` no
longer resolve to ask. It looks at the build agent in an empty project, so it shows that the launch value still
takes effect, not that a repository cannot override it. It reads the resolved configuration, not a tool call,
which would need a model.

## Node

Direction (27 Oct 2026): Flockdeck is dropping the Node requirement. Agents that need
Node today (Gemini CLI, the Codex adapter) keep working until their replacements land. After
that the check is planned to become a generic "agent unavailable" state (T207) that does not
name the agent's own dependencies. See [ACP direction](acp-direction.md).

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
  entry's `login` text, which is stripped of control characters and cut at 300
  characters.
- `passEnv` is the whole list of variables the agent gets from Flockdeck's
  environment, on top of the few that let it find programs and a temp folder
  (`acp.MinimalEnv`), and it holds at most 64 names. Keys and tokens are not
  passed unless an entry names them. A name starting `FLOCKDECK_` or `PERCH_`
  is refused in any case, since Flockdeck sets its tokens and the pane's
  identity under both; the refusal is made again where the environment is
  built, so an entry that skipped the catalog's check is still held to it. A
  name `acp.MinimalEnv` already gives (`PATH` and the like) is not passed twice.
  Names are compared exactly on Linux and macOS and without regard to case on
  Windows.
- `mcp` says whether the agent takes per-session `mcpServers`.

The list sent in `session/new` is built per pane by the workspace
(`internal/workspace/mcpservers.go`), and only when `mcp` is true. It holds
Flockdeck's own entry, `flockdeck mcp-server` run by the running program's path
with the pane's token as `FLOCKDECK_TOKEN` in that entry's own `env`, and then
the servers in the `mcpServers` list of `prefs.json` (`name`, `command`, `args`,
`env`, `off`). Flockdeck's entry is left out until the server exists
(`Workspace.UseMCPToken`). A user entry with a bad or repeated name, the name
`flockdeck`, no command or a control character is skipped, at most 16 servers
are sent, and a variable starting `FLOCKDECK_` or `PERCH_` is dropped from a
user entry in any case. Nothing is taken from Flockdeck's own environment. The
list is never sent to a window, since an `env` may hold a key.

An adapter built into the `flockdeck` binary is started from the running
program's path and not by looking `flockdeck` up on PATH, and is also given
`HOME`, `USERPROFILE` and `APPDATA`. Other adapters get only the names their
entry lists. What an agent wrote to stderr is never shown: a pane whose agent
did not start or has ended says `the agent is unavailable`.

The built-in entries are `opencode-acp`, and `gemini-acp` and `codex-acp`, which
are hidden until the tasks that check them end to end have landed. They are
separate ids from the terminal entries for the same tools, so a saved layout
keeps meaning what it did. There is no entry for Claude Code yet (planned: T191).

Structured entries are not offered in the picker until the beta is turned on:
`"structuredPanes": true` in the preferences file (`prefs.json`), not in
`agents.json`, which holds entries only. They can still be found by id.
