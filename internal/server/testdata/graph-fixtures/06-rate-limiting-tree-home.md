# 06-rate-limiting-tree-home

The prototype's team "rate limiting": a manager and an api lead with tests and bench nested under it, eight helpers in all (the cap). The lead asks to run a command, ci asks a question, api and cfg change the same file, and lint ended in error. cdx has written nothing for twelve minutes while working, which is a quiet stuck item. The nodes carry the per-node figures: spend, last output, undelivered messages and the rollups below and needYou. The prototype's docs helper is left out to stay at the cap, and its cdx is stuck at seven minutes where the real rule waits for ten.

The team is a manager and 8 helpers, of which tests is a helper of api, of which bench is a helper of api. The graph is judged at 2026-10-21T09:00:00Z.

## Needs the person (5)

- api (dc608d0f): asks to run go test ./... -race -count=1
- ci (4a6766e4): asks: T2 came back with "races on map". Send it back a third time, or try a different approach?
- api (dc608d0f) and cfg (a45592cb): api and cfg both change cfg/limits.yaml
- lint (5caf410e): exited with code 2, so the helper ended in error
- cdx (02f43567): no output for 12m while working; a quiet item, it does not ask anything of the person

## The other helpers

- tests (b5377bc4): working (Write limiter_test.go)
- bench (d7fa6ec5): working (Bash go test -bench Limiter ./mw)
- cfg (a45592cb): idle, its task "Config flags" is in review and waits on a reviewer
- rev (9a1d60ea): working (Read diff, 2 files)

## The message

tests sent api a message 8 minutes before the graph's time, and it is still queued. The sender's node counts it in `undelivered`. api is not idle, so it is not a stale-unread item.
