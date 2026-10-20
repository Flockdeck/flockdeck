# 03-nested-helper-question-and-returned-task

Seven helpers on a search feature, one of them with a helper of its own. One asks a question and one has a task back from review.

The team is a manager and 7 helpers, of which shards is a helper of indexer. The graph is judged at 2026-10-21T09:00:00Z.

## Needs the person (2)

- ranking (839bba23): asks: Boost exact title matches over body matches?
- ui (bf058d75): task returned from review: Focus is lost after a page change. Please fix before it goes in.

## The other helpers

- indexer (025b0446): working (Edit search/index.go)
- shards (21c76d4a): working (Bash go test ./search/shard)
- analytics (49e9335b): idle, nothing owed
- synonyms (ef584c9e): exited cleanly
- perf (ce2ebf6f): idle, its task "Cut query latency" is in review and waits on a reviewer
