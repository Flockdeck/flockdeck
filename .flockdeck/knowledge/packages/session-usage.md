---
type: Package
title: session resource usage
description: How a pane's CPU share and memory are measured across its process tree, per platform.
resource: internal/session/usage.go
tags: [session, usage, cpu, memory, process-tree]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/usage.go, internal/session/usage_linux.go, internal/session/usage_windows.go, internal/session/usage_other.go]
---
# Session usage

`Session.Usage()` returns `Usage{CPUPercent, RSSBytes, Procs, Known}` for the pane's process and every descendant, since the agent CLI does its work in child processes. `CPUPercent` is a share of one core, so two busy cores read 200. `Known` is false when the platform cannot answer, the pane has no process, or nothing has been read yet. An exited session returns the zero value.

## Shared process table

One `procTable` (parent to children map) serves all panes. `currentProcTable` rebuilds it when older than `usageInterval` (5 seconds). If the platform read fails, the pane keeps its last figure rather than blinking out. A pane reads once per table: a call between two table builds returns the cached `Usage`. Callers should use it from the periodic refresh, not per keystroke.

## Walking the tree

- Starts at the pane's pid and visits children breadth first, at most `maxTreeProcs` (512).
- A process that vanished between the table and the read is skipped but its children are still walked.
- A process whose start time is earlier than the root's is dropped. A reused pid can otherwise make an unrelated program look like a child.
- Memory is the sum of resident sets.

## CPU smoothing

CPU time is accumulated per process, not as a tree total, so an exited child does not drive the delta negative. A process seen for the first time counts its whole CPU time. The first measurable interval is reported as it stands (`cpuSeeded`). After that the figure is an exponentially weighted average with `alpha = 1 - exp(-dt / 15s)` (`cpuWindow`), which weights a reading by the time it covers. The value never goes below zero. This is a smoothing constant of 15 seconds, not a sliding window of raw samples.

## Platform readers

| Platform | Source |
|---|---|
| Linux (`usage_linux.go`) | `/proc/<pid>/stat`. Fields are split after the last `)` because the command name may contain spaces. CPU is user plus system ticks at an assumed 100 per second (`clockTick`); resident set is pages times the page size; start time is field 22 |
| Windows (`usage_windows.go`) | Toolhelp snapshot for parents, retried three times; `OpenProcess` with limited query rights, `GetProcessTimes` and `K32GetProcessMemoryInfo` (working set) per process. Uses only the `syscall` package |
| Other (`usage_other.go`, build tag `!windows && !linux`) | Returns nothing, so macOS shows no reading |

`sessionMembers(sid)` (Linux: processes whose `/proc` session field equals the pane's session; elsewhere nil) is also here and is used by pane shutdown ([pane process tree shutdown](/gotchas/pane-process-tree-shutdown.md)).

`readParents` and `readMetrics` are package variables so tests can supply a fake table.

Related: [session](/packages/session.md), [sysproc](/packages/sysproc.md).
