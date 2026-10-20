# Old UI fix ledger

The current window (`internal/webui`) is frozen for features from 19 October 2026,
the date of the team graph rebuild plan. It still gets bug fixes while the new
interface is built. This page lists each of them and the behaviour it protects,
so that the new interface can be checked for the same behaviour before the old
one is retired.

**How to use it.** When you fix a bug in the old UI, or in code the old UI relies
on, add a line under Entries in the same pull request: the pull request number,
the date it merged, and the behaviour the fix protects, in a sentence a person
could test. When you build a part of the new interface, read the entries for what
it replaces and check each one there.

A change that adds something the old UI could not do is a feature and does not
belong in the old UI or in this ledger.

## Entries

Only fixes merged on or after 19 October 2026 are listed. Most of them are in
code the old window depends on and not in `internal/webui`; the one change under
`internal/webui` since that date, #378, is a guard and not a fix to a reported bug.

| Pull request | Merged | Behaviour it protects |
| --- | --- | --- |
| #322 | 19 Oct | Giving up on a slow folder scan while closing it no longer ends the program on Windows. Browsing a very large folder and leaving it must not crash the app. |
| #353 | 20 Oct | A helper being closed or restarted is not reported to its parent as having finished its turn. |
| #354 | 20 Oct | A pane keeps the name its current process reported. A late report from the process before a restart does not put the old name back in the pane title. |
| #373, #376 | 20 Oct | Reading Flockdeck mail is allowed without a prompt only when the command runs the Flockdeck app itself, from a directory with no `flockdeck*` files. Any other program called `flockdeck` still asks. |
| #378 | 20 Oct | The old window never sends keystrokes or opens a terminal socket for a structured pane, and shows "Structured pane, open in the new interface" in it. |
