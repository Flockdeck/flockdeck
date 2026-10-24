# Deprecated fields: before one is removed

The fields marked deprecated in `catalog.json` (`deprecated`) and the schema stay in the graph push until the major version is raised. This file lists what has to change first. Check each item against the code on the day, not against this file.

## The desktop interface (`ui/`)

- The old Graph view reads `nodes[].depth` for its canvas layout, `teams[].spendUSD`, `spendPartial` and `spendIncludesPartial` for the manager box, and `nodes[].below` and `needYou` as fallbacks. It stays one more release. Remove those reads, or the view, first.
- `NeedsYou.js` is being retired. The strip code it shares with the old view reads the server's order of `teams[].needs` and `soloNeeds`.
- `scripts/realpage/check.mjs` asserts on strip rows and need order. Move its assertions to the order the new interface computes.
- The new interface computes `stuck`, `below`, `needYou`, `depth`, the team and node spend flags and the cap use itself, and does not read them. It uses `nodes[].lastOutput` for nothing deprecated.

## flockdeck-remote and the phone app

Checked against flockdeck-remote `origin/main` (`b94f37b`): nothing in it reads the graph push or any of these fields (no match for `graph`, `stuck`, `needYou`, `below`, `depth`, `spendPartial`, `lastOutput`, `caps` or `soloNeeds`). A relay window is never sent the graph: `graph` is `dropForRelay` in `internal/server/relayscrub.go`, and the team and tasks messages are desk-only. So nothing there has to change for these fields.

Two things to confirm before removal, because they were not checked here:

- The phone app is not in the flockdeck-remote repository. If it is built from another repository, search that for the paths in the table.
- A relay window cannot read the graph today. If a future change sends a scrubbed graph to the relay (`relayTeams` in `docs/team-graph.md` is the relay form of the team message), it starts from the fields that remain, not these.

## Removal

A removal is a change that raises `api.major` in `catalog.json`, deletes the field from the schema and `docs/team-graph.md`, regenerates the golden file, and removes the entry from `deprecatedFields` in `internal/server/apiversion.go`. `TestDeprecatedFieldsAreStillSent` fails until the entry is removed, which is the point.
