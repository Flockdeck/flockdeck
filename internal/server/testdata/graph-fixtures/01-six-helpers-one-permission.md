# 01-six-helpers-one-permission

Six helpers on a checkout rewrite. One is asking to run a command, and two of the helpers change the same file.

The team is a manager and 6 helpers. The graph is judged at 2026-10-21T09:00:00Z.

## Needs the person (2)

- tax (366c9143): asks to run go test ./tax/... -run TestRounding -count=1
- cart (6fb40971) and tax (366c9143): cart and tax both change cart/total.go

## The other helpers

- docs (ec776492): idle, nothing owed
- migrate (dccc34a5): exited cleanly
- api (5dc771c1): idle, its task "Add the totals endpoint" is in review and waits on a reviewer
- search (1ce36fdc): idle, its task "Index the new fields" is blocked: needs the staging index, which only the person can create
