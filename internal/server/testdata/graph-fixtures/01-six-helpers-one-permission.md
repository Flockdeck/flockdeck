# 01-six-helpers-one-permission

Six helpers on a checkout rewrite. One is asking to run a command.

The team is a manager and 6 helpers. The graph is judged at 2026-10-21T09:00:00Z.

## Waiting on the person (1)

- tax: asks to run go test ./tax/... -run TestRounding -count=1

## The other helpers

- cart: working (Edit cart/total.go)
- docs: idle, nothing owed
- migrate: exited cleanly
- api: idle, its task "Add the totals endpoint" is in review and waits on a reviewer
- search: idle, its task "Index the new fields" is blocked: needs the staging index, which only the person can create
