# 04-eight-helpers-three-waiting

Eight helpers on a release. One asks to run a command, one asks a question and one has a task back from review.

The team is a manager and 8 helpers. The graph is judged at 2026-10-21T09:00:00Z.

## Waiting on the person (3)

- build: asks to run git push origin release/2.4
- notes: asks: Name the breaking change in the headline?
- qa: task returned from review: The installer fails on a path with a space. Reproduce and fix.

## The other helpers

- deps: working (Edit go.mod)
- site: working (Edit docs/download.md)
- signing: idle, its task "Sign the Windows build" is blocked: the signing certificate has not arrived
- changelog: idle, nothing owed
- old-branch: exited cleanly
