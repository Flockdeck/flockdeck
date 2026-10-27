# Deprecated fields: before one is removed

The fields marked deprecated in `catalog.json` (`deprecated`) and the schema stay in the graph push until the major version is raised. This file lists what has to change first. Check each item against the code on the day, not against this file.

## The desktop interface (`ui/`)

- The old Graph view reads `nodes[].depth` for its canvas layout, `teams[].spendUSD`, `spendPartial` and `spendIncludesPartial` for the manager box, and `nodes[].below` and `needYou` as fallbacks. It stays one more release. Remove those reads, or the view, first.
- `NeedsYou.js` is being retired. The strip code it shares with the old view reads the server's order of `teams[].needs` and `soloNeeds`.
- `scripts/realpage/check.mjs` asserts on strip rows and need order. Move its assertions to the order the new interface computes.
- The new interface computes `stuck`, `below`, `needYou`, `depth`, the team and node spend flags and the cap use itself, and does not read them. It uses `nodes[].lastOutput` for nothing deprecated.

## flockdeck-remote and the phone app

The phone app is the web app in flockdeck-remote (`web/app`); it is not a separate repository. Checked against flockdeck-remote `origin/main` (`b94f37b`), Go, JavaScript and the chat preview tool:

- It reads these control messages and no others: `agentStarted`, `agents`, `attachImageResult`, `conversationAppend`, `conversationDetail`, `conversationPage`, `conversationSearch`, `desktop`, `devices`, `group`, `home`, `notice`, `open`, `pane`, `state` and `waiting`. It does not read `graph`, `team`, `tasks` or `teamSnapshot`.
- No file in it reads `stuck`, `needYou`, `below`, `depth`, `spendUSD`, `spendPartial`, `spendIncludesPartial`, `lastOutput`, `caps`, `needs` or `soloNeeds` as a field of a message. The matches for `below` and `stuck` are in comments and CSS.
- A relay window is never sent the graph: `graph` is `dropForRelay` in `internal/server/relayscrub.go`, and the team and tasks messages are desk-only.

So nothing in flockdeck-remote has to change before these fields are removed. The other private repositories (`flockdeck-relay`, `flockdeck-site`, `flockdeck-billing`) were not searched. Check them before a removal.

## Removal

A removal is a change that raises `api.major` in `catalog.json`, deletes the field from the schema and `docs/team-graph.md`, regenerates the golden file, and removes the entry from `deprecatedFields` in `internal/server/apiversion.go`. `TestDeprecatedFieldsAreStillSent` fails until the entry is removed, which is the point.

## Removed in 1.14

The command `setAgentAddress` and the message `agentAddress` were removed in 1.14 together with the API agents (Anthropic, OpenAI, Google and OpenAI-compatible) and the address fields of the agent list. The per-token model prices in the agent list went too. This was a minor bump, not a major one, because nothing but the settings picker in the desktop interface sent or read them. A server answers `setAgentAddress` as `unsupported`, like any command it has no handler for. A client that still sends it gets that answer and should stop.
