# Cheaper CI (T167)

Draft, local only. Nothing here has been pushed, run, or applied to a repo setting.

## What costs the minutes

The `test` workflow is nearly all of the usage. The last 20 runs of `test.yml` (read with `gh run list` and `gh run view`, no reruns; job times rounded up to whole minutes, Windows counted twice, macOS ten times):

| Run | Runs counted | Billed minutes, average | Range |
| --- | --- | --- | --- |
| Push to a pull request (finished runs) | 10 | 223 | 197 to 251 |
| Push to main | 7 | 459 | 432 to 472 |

Raw job minutes for a pull request push are about 55 on Linux, 25 on Windows (50 billed) and 12 on macOS (120 billed). macOS is more than half of a pull request run. A push to main adds macOS race shards, so it costs twice as much: about 35 raw macOS minutes (350 billed).

The other workflows that run on pull requests are small. `PR review` and `PR review - post` take about 4.6 wall minutes per push together (wall time from the run list; I did not read their runner types). `linux-packages`, `helm-chart`, `design-tokens`, `flaky-soak` and `agent-compat` only run on their own paths. I did not look at the other repos: the manager says Actions is off on them.

## The change (`.github/workflows/test.yml`)

1. A pull request is run on Linux only until it carries the label `ready-to-merge`. With the label, Windows and macOS run too, on that push and on each later push while the label stays.
2. A push to main runs Linux only. A release (the file is called with `release: true`, or by a tag push), a manual run and a weekly run (Sunday 04:17 UTC) run all three, with the macOS race shards as before.
3. The `changes` job writes a new output, `oses`, and the static, shard and race matrices are built from it. The three required checks keep their names: `test (ubuntu-latest)`, `test (windows-latest)`, `test (macos-latest)`.
4. In a pull request without the label, the `test (windows-latest)` and `test (macos-latest)` checks fail at once with "Not run yet. Add the label ready-to-merge ...". On main they pass with a notice. A documentation-only change is unchanged: all three pass after the doc tests on Linux.
5. `labeled` is added to the pull request event types, so adding the label starts the full run.

Already in the file and unchanged: one run per pull request at a time (a newer push cancels the older run), the docs-only skip, and the `cancel-in-progress` rule that never cancels a main or release run. I did not add a path filter for the `site` directory: I did not check which Go tests read files there, and skipping the matrix for it could hide a break.

## Estimates

| | Before | After |
| --- | --- | --- |
| Push to a pull request, no label | 223 | about 50 |
| Push to a pull request, with the label | 223 | 223 |
| Merge (push to main) | 459 | about 50 |
| Weekly full run | none | 459 |

A typical pull request with three pushes and one labeled push, then a merge: before about 1,350 minutes (4 x 223 + 459); after about 420 (3 x 50 + 223 + 50). That is about 70% less. The weekly run adds about 460 a week.

The Linux figure after the change is an estimate, not a measurement: the Linux jobs are the same, and the two idle gate legs for Windows and macOS now finish in seconds. It is within 45 to 55.

## Branch protection

No setting has to change. The required checks keep their names, so the protection stays as it is:

- A pull request cannot merge on Linux results alone. The two other checks are red until the label is on and the full run has passed. That is the intended wait; it ends when the label is added, so it does not block for ever.
- The label has to exist: `gh label create ready-to-merge`. Create it by hand; I did not.
- Add the label as a person (or with a token that is not the workflow's own `GITHUB_TOKEN`). A label added by `GITHUB_TOKEN` does not start a workflow run, so the full run would never start.
- To go cheap again after a rebase or a fix, remove the label before pushing. While it stays on, each push is the full 223.
- If red checks on every unlabeled push are not wanted, the alternative is to post the two checks as pending commit statuses (needs `statuses: write` and a small job). I did not build that: a status and a check with one name behave differently in the merge box, and I could not test it without running CI.
- Any other label also starts a Linux run (about 50). Use only `ready-to-merge` on pull requests.

## Not tested

I could not run the workflow. What was checked locally: `actionlint` passes, the file parses, and the `diff` step's shell was run for eight cases (pull request without a label, with another label, with `ready-to-merge`, push to main, push of a tag, release input, schedule, manual run). It gave Linux only, Linux only, all three, Linux only, all three, all three, all three, all three; shard counts 4, 4, 10, 4, 10, 10, 10, 10. The gate steps and the failing leg are read, not run.
