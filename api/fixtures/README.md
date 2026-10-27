# Fixtures

Graph pushes (`type: "graph"`) written by a real server, for a client in another module to draw or check against. Each file is the message a window is sent in answer to `graphSnapshot`, with pane ids, times and the project path made the same on every run.

| File | Scenario |
| --- | --- |
| `01-six-helpers-one-permission.json` | Six helpers; one asks to run a command. |
| `02-eight-helpers-permission-and-question.json` | Eight helpers; one asks to run a command and one asks a question. |
| `03-nested-helper-question-and-returned-task.json` | Seven helpers, one with a helper of its own; one asks a question and one has a task back from review. |
| `04-eight-helpers-three-waiting.json` | Eight helpers; a command, a question and a returned task. |
| `05-six-helpers-undelivered-message.json` | Six helpers; one asks a question and the manager is idle with an unread message. |
| `06-rate-limiting-tree-home` | Built, not recorded. See below. |

## Prompt delivery example

`built-graph-01-prompt-delivery.json` is a graph push built by hand from fixture 05, not recorded. The manager has sent a message to four structured helpers and one terminal helper, and the message edges show each delivery state: `queued` (after a cancelled turn, with `stopReason` and `attempts` kept), `delivered`, `handled` and `undeliverable` for the structured ones, and `woken` for the terminal one. The manager's `undelivered` is 2 (the queued and the undeliverable message). A test validates it against `api/schema/team-graph.schema.json`.

## Changes examples

`built-changes-*.json` are the messages of the team-wide changes contract (`docs/changes-api.md`, `api/schema/changes.schema.json`): a `changesTeam` push, a page of files, diffs as hunks, unchanged lines, and the answers to commands. They are built by hand, not recorded: no server writes these messages yet. A test validates each against the schema and checks what a schema cannot (hunk line counts, word ranges, a gate blocked exactly when it has blockers). `docs/changes-api.md` section 11 says what each one shows.

## How they are made

`internal/server/api_fixtures_test.go` starts a server, puts panes in the states of the scenario (statuses, a board with tasks moved through their states, a message), and reads the push with the code a window's `graphSnapshot` runs. `TestAPIFixturesAreCurrent` fails when a file differs from what the server writes now. To write them again: `API_FIXTURES_UPDATE=1 go test -run TestAPIFixturesAreCurrent ./internal/server`.

Made repeatable by the test, not by the server:

- Pane ids are the ids of `internal/server/testdata/graph-fixtures` for the same scenario. The shell the server starts with is listed after the helpers.
- Times are written as their age before 2026-10-21T09:00:00Z, rounded to five minutes, so a slow machine records the same file. A real server reads them from the clock the panes were set up on, so every status in a recording is five minutes old, where the built fixtures give each helper its own age.
- The project is `project` at `/work/project`.

## Built fixtures

`internal/server/testdata/graph-fixtures` holds the same scenarios built from the derivation without a server, with the answer key (`.md`) each was written against. Where a recording and the built fixture disagree, the built one has what the scenario says and the recording has what a server does with the states a test can set up. Differences:

- A helper that ends in error is recorded as exited. An exit status needs a real process, so the `down` item of fixture 02 is not in the recording.
- No pair of helpers is listed by the conflict radar (fixture 01). That needs two git checkouts with the same file changed.
- The idle manager of fixture 05 has had its unread message for five minutes, not forty-five, so the `stale-unread` item is not in the recording.
- Spend, last output and the per-node rollups are zero or absent.

Fixture 06 is built, not recorded. It needs a helper that ended in error, a conflict pair, per-helper spend, and a helper that has written nothing for twelve minutes, none of which a test can make a server do. A client may use the built file, which the schema test validates, but it is not what a server was seen to write.
