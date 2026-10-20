# 03-nested-helper-question-and-returned-task

Seven helpers on a search feature, one of them with a helper of its own. One asks a question and one has a task back from review.

The team is a manager and 7 helpers, of which shards is a helper of indexer. The graph is judged at 2026-10-21T09:00:00Z.

## Waiting on the person (2)

- ranking: asks: Boost exact title matches over body matches?
- ui: task returned from review: Focus is lost after a page change. Please fix before it goes in.

## The other helpers

- indexer: working (Edit search/index.go)
- shards: working (Bash go test ./search/shard)
- analytics: idle, nothing owed
- synonyms: exited cleanly
- perf: idle, its task "Cut query latency" is in review and waits on a reviewer
