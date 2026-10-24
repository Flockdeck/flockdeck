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
2. A push to main runs Linux only. A release (the file is called with `release: true`, or by a tag push), a manual run (`workflow_dispatch`, added to `test.yml`) and the weekly run run all three, with the macOS race shards as before. A run that is not caused by a pull request or a push to main cannot cancel anything: its concurrency group is named by the run.
3. The weekly run is a new file, `weekly-full.yml` (Sunday 04:17 UTC, or by hand), which calls `test.yml` and then opens an issue "weekly full CI failed", or comments on the open one, if the call failed. The issue is written from that file on purpose: `release.yml` and `docker-image.yml` call `test.yml` and grant only `contents`, and a called workflow whose job asks for `issues: write` does not start when its caller grants less, so the permission cannot live in `test.yml`.
4. The `changes` job writes a new output, `oses`, and the static, shard and race matrices are built from it. The three required checks keep their names: `test (ubuntu-latest)`, `test (windows-latest)`, `test (macos-latest)`.
5. In a pull request without the label, the `test (windows-latest)` and `test (macos-latest)` checks fail at once with "Not run yet. Add the label ready-to-merge ...". On main they pass with a notice. A documentation-only change is unchanged: all three pass after the doc tests on Linux.
6. `labeled` is added to the pull request event types, so adding the label starts the full run.

Already in the file and unchanged: one run per pull request at a time (a newer push cancels the older run), the docs-only skip, and the `cancel-in-progress` rule that never cancels a main or release run. I did not add a path filter for the `site` directory: I did not check which Go tests read files there, and skipping the matrix for it could hide a break.

## Estimates

Billed minutes (Windows x2, macOS x10):

| | Before | After |
| --- | --- | --- |
| Push to a pull request, no label | 223 | about 50 |
| Push to a pull request, label on | 223 | 223 |
| Adding the label (a `labeled` event) | none | 223 |
| Adding any other label | none | about 50 (a Linux run) |
| Merge (push to main) | 459 | about 60 |
| Weekly full run | none | 459 |

A typical pull request, three pushes with no label, then the label and one full run, then the merge: before about 1,350 minutes (4 x 223 + 459); after about 430 (3 x 50 + 223 + 60). That is about 68% less. The weekly run adds about 460 a week. Each further push while the label is on costs 223 again.

The Linux figures after the change are estimates, not measurements: the Linux jobs are the same, and the two idle gate legs for Windows and macOS now finish in seconds. The range is 45 to 60.

## Branch protection

No setting has to change. The required checks keep their names, so the protection stays as it is:

- A pull request cannot merge on Linux results alone. The two other checks are red until the label is on and the full run has passed. That wait ends when the label is added, so it does not block for ever.
- The label has to exist: `gh label create ready-to-merge`. Create it by hand; I did not.
- Add the label as a person, or with a token that is not the workflow's own `GITHUB_TOKEN`. A label added by `GITHUB_TOKEN` does not start a workflow run, so the full run would never start.
- If red checks on every unlabeled push are not wanted, the alternative is to post the two checks as pending commit statuses (needs `statuses: write` and a small job). I did not build that: a status and a check with one name behave differently in the merge box, and I could not test it without running CI.

## Operating rules

1. Add `ready-to-merge` only after the Linux run of that head has finished green. The label starts a whole new run, Linux included, and cancels the one still going.
2. Add no other label to a pull request. Each one starts a Linux run (about 50 minutes) for nothing.
3. Remove the label before pushing a fix or a rebase, and add it again at the end. While it is on, every push is a full 223-minute run.
4. On a commit on main, `test (windows-latest)` and `test (macos-latest)` show green with a notice that says they were not run. That is not a test. Windows and macOS are tried on the pull request, by the weekly run and by a release, nowhere else.
5. A failed weekly run opens or updates the issue "weekly full CI failed". Someone has to read it; nothing else shows it.

## Proof before merging this

Not merged until a throwaway run has shown that the new conditions work. When the hold is lifted and Actions is on again:

1. Push this branch and open a test pull request that changes one Go file's comment (so it is not documentation-only). Do not add a label.
2. Expect: `changes` outputs `oses=["ubuntu-latest"]`; static, shard (4) and race (4) run on Linux only; `test (ubuntu-latest)` is green; `test (windows-latest)` and `test (macos-latest)` fail at "Say that this platform was not run" with the line about the label; no Windows or macOS runner is started.
3. Add `ready-to-merge`. Expect a new run with `oses` of all three, 10 shards, and all three `test` checks green.
4. Add a different label. Expect a Linux-only run and the two red checks again.
5. Remove the label and push. Expect Linux only again.
6. Run `test` by hand (`workflow_dispatch`) on the branch. Expect all three platforms and no cancellation of anything else.
7. Run `weekly full CI` by hand once. Expect the same as step 6. To see the issue step, run it on a branch with one test broken on purpose, then close the issue.
8. Open a documentation-only pull request. Expect the three checks green after the doc tests, as before.
9. After merging, the first push to main should run Linux only and show the notice on the other two checks.

## Not tested

I could not run the workflow. What was checked locally: `actionlint` passes for `test.yml` and `weekly-full.yml`, both parse, and the `diff` step's shell was run for eight cases (pull request without a label, with another label, with `ready-to-merge`, push to main, push of a tag, release input, schedule, manual run). It gave Linux only, Linux only, all three, Linux only, all three, all three, all three, all three; shard counts 4, 4, 10, 4, 10, 10, 10, 10. The gate steps and the failing leg are read, not run.
