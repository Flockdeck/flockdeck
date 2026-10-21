# The real-page check

This is the dry run of the week-10 test on the page users get (T98). It loads the tagged page (`go build -tags next`), served by `cmd/graphfixture` with no workspace and no configuration directory, in headless Chromium, once for each of the five fixtures in `internal/server/testdata/graph-fixtures`. It reads the page and nothing else. It does not replace the people-based test.

Run it with `node scripts/realpage/check.mjs [--out result.json] [--slowdown 1,4] [--repeat 3] [--only N] [--allow-config-change] [--ceiling 20]`. It builds the server, starts one for each fixture with every home and configuration variable pointed at an empty temporary directory, and exits 1 when a check fails.

It also exits 1 when the listing of `%APPDATA%lockdeck` differs after the run or when the sandbox directory is not empty, because either means the page's server wrote outside its sandbox. If another Flockdeck is running on the machine and changes that directory on its own, pass `--allow-config-change`; the change is then still printed. The temporary directories (build, sandbox and Chrome profile, about 19 MB) are removed when the run ends, and so are the server and the browser, on success, on an error, on SIGINT or SIGTERM, and when `--ceiling` minutes pass (20 by default). A DevTools call that gets no answer in 30 s, a socket that closes mid-run, and a `go build` over 3 minutes each end the run with exit 1.

Results recorded here, each on the tree named in its section: Chrome 153.0.8010.12 (headless, a Playwright Chromium) on Windows 11, Node 24.20.0, CPU slowdown 1x and 4x, three runs of each timed step. The first run was on main at `dc2b3966`, the rerun on #412's head `1bfc48d5`, the card on a tree with #412 and #413, and the last on current main.

## What passes

For all five fixtures, with the drawer closed on load (these checks passed on every tree below):

- Every node is a box, and the drawer is closed.
- The strip has the rows the fixture's `.md` lists under "Needs the person" and no others. A conflict is compared as the sorted pair of its panes, since the `.md` names them in either order.
- Each row has the reason word of docs/team-graph.md section 5 and the pane's name.
- The strip order is the order of the graph's `needs` (the teams' lists, then the solo one), not the order of the `.md`. Fixture 4 differs: the `.md` lists the permission first and the graph the question first.
- A badge is on each pane the `.md` names and on no other, with the reason word of that pane's first item. A pane with two items (fixture 1's tax) shows the first and "+1".
- Fixture 3: the nested helper (shards) is drawn below its parent (indexer). No line is drawn from parent to helper; the tree shows by position.
- Fixture 5: the undelivered message edge is drawn from tests to the manager in amber, with "not told yet".
- No exceptions or console errors on load.
- A window reached the way the relay reaches it, with the hello made remote in the page (the same page, a changed hello): no `graph`, `structured`, `paneSend` or `permissionAnswer` command goes out, the line "The team graph is shown on the desktop." shows, the roster lists every agent, and there is no graph, manager bar or drawer.
- The configuration directory (`%APPDATA%\flockdeck`) was listed before and after the whole run: 84 files both times, no difference. The temporary directory the server ran in was left empty. A Flockdeck was running on the same machine; it changed nothing in the window of the run.

## First run: what failed

This is the record of the first run, on main at `dc2b3966` (before #412 and #413), kept as it was. The second week-10 step failed on the real page, for all five fixtures at both slowdowns.

From the strip, one click or key should open the drawer for the named agent and show the real command or question in under 2 s. A strip row is not a control on the page: `shell/Home.js` renders `GraphView` without `onSelect`, and `NeedsYou` draws a row as a button only when it has one. A click on a row opens nothing. The row cannot take focus, so Enter on it opens nothing either. Result for the first permission or question of each fixture: 0 of 3 runs opened the drawer, at 1x and at 4x, by click and by key.

Follow-up: T100, pass `onSelect` from Home to `GraphView`, make a row reachable by Tab, and rerun this check. The check is not changed to pass. The rerun is below.

For comparison, and not the strip path: a click on the agent's box in the graph opens the drawer and puts the text on the screen. Time from the pointer event to the text, three runs each:

| Fixture | Agent | 1x CPU slowdown (ms) | 4x CPU slowdown (ms) |
| --- | --- | --- | --- |
| 1 | tax (permission) | 3.2, 3.7, 4.2 | 19.5, 20.3, 23.4 |
| 2 | backfill (question) | 3.1, 3.7, 3.9 | 16.7, 17.5, 19 |
| 3 | ranking (question) | 3.3, 3.6, 4 | 17.2, 19.4, 20.3 |
| 4 | notes (question) | 4.1, 4.1, 4.2 | 17.4, 20.7, 21.6 |
| 5 | forms (question) | 4.1, 4.1, 5 | 18.5, 20.9, 21.9 |

The time is from the browser's `pointerdown` to the first moment the drawer holds the first 30 characters of the item's text. It does not include the person finding the box.

## Rerun against T100 (#412)

Same check, machine and Chrome, run on the tree of #412 (branch `strip-select`, head `1bfc48d5`), three runs of each timed step. All five fixtures pass every check, including both strip ways of the second step. The configuration directory was 84 files before and after.

Time from the pointer event or key to the first 30 characters of the item's text in the drawer. Each cell is the range over the five fixtures, three runs each:

| Way | 1x CPU slowdown (ms) | 4x CPU slowdown (ms) |
| --- | --- | --- |
| Click on the strip row | 2.7 to 4.4 | 15.2 to 22.2 |
| Enter on the strip row | 2.4 to 3.9 | 8.6 to 17.1 |
| Click on the box, for comparison | 3.3 to 5.1 | 17.6 to 23.4 |

The bar is under 2 s. The slowest strip run was 22.2 ms.

The check had a defect that showed up on the way: the first rerun sent Enter to Chrome without the character that makes a focused button act, so the key way read 0 of 3 although the row was a focusable control and the click way passed. The check now sends the key with its character. That is a fix to the check, not to the page. In the first run the key way failed for a real reason (the row was not a control).

Esc returning the keyboard to the strip row is covered by #412's node tests. This script does not press Esc in the real page.

## The card (T101, #413)

The check now also reads the card the strip click opened. For a permission it needs the whole command in the card and both a Yes and a No button; for a question it needs the whole question and not the "choices are in the terminal" fallback. Run on a tree with #412 and #413, at 1x with one run: fixture 1 (tax, permission) shows `go test ./tax/... -run TestRounding -count=1` with Yes and No, and fixtures 2 to 5 show their question in full. Run on a tree with #412 and without #413 the same check fails on fixture 1 (command empty, no buttons, the fallback line shown), so it does look at the card.

## Current main

The check was also run on current main twice (by a reviewer, not by the author), after #412 and #413 had merged. Both runs merged cleanly and passed every check, including the strip click, the strip key, the box click and the card.

The Yes and No buttons are drawn and not pressed: the fixture server has no `/ws/pty`, so a press has nowhere to go.

## What was not checked

- The card of every item. The check reads the card of the first permission or question of each fixture only, so fixture 2's permission and fixture 4's permission are not opened. (Before #413 the fixture server had no permission or question view, so the first run's drawer showed the graph's text cut to 200 characters; that is the tree of the first run, not a gap now.)
- The terminal body is empty: the fixture server has no `/ws/pty`.
- A real remote window: the hello is made remote in the page, so the sealed socket, the key and `e2e.js` are not exercised, and the relay is not involved.
- The old page. This check is of the new shell only.
- Narrow (phone) width, dark and light themes, and a screen reader.
- Whether a person finds who needs them and why in about five seconds. That is W3.

## How the check was tested

The check was run against changes that should make it fail, and failed each time: the strip rows reversed (fixture 2, the order check), the phrase of the `ask` reason changed (fixture 4, the strip word and the badge word) and the badges not drawn (fixture 1, the badge check).
