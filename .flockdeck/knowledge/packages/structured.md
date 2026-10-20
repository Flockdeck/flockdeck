---
type: Package
title: structured
description: The runtime of one structured pane: an ACP agent, its turns, permission questions and transcript, with no terminal.
resource: internal/structured/structured.go
tags: [acp, agents, package]
timestamp: 2026-10-20T00:00:00Z
code_paths: [internal/structured/structured.go]
---
- One `Runtime` per pane. `Start(ctx, Options)` returns at once and starts the agent in the background: it resolves the command (the adapter manifest, or `Options.Command` in tests), checks Node where the adapter needs it, starts the stream recording if `Options.Records` or `Options.Recorder` is set, then runs `acp.Start`, `Initialize` and `NewSession`.
- API: `Send(text)`, `Cancel()`, `Answer(requestID, optionID)`, `AnswerCard(seq, requestID, optionID)`, `Close()`, `Snapshot()`, `Subscribe()`. `Options.Entry` carries the part of the catalog entry the runtime uses (adapter id, install and login advice, `PassEnv`, `MCP`), so the package does not import the catalog; the workspace fills it in.
- Phases: `starting`, `ready`, `needs setup` (a login the agent asked for, or Node missing; `Snapshot.Setup` says which), `exited` (`Err` holds the agent's last stderr line, cleaned), `failed`, `closed`. While ready, `Status` is the bridge's working, waiting or idle.
- `Send` queues behind a running turn (16 prompts, 1 MiB each) and may be called while starting. `Cancel` ends the running turn and drops the queue. A permission request waits for `Answer`; nothing answers by itself. An answer must name an option on the card, and `AnswerCard` also the card's `Seq`, or it is `ErrStale`. `Close` releases every waiting request as cancelled, closes the client, then the recording.
- The environment is `acp.MinimalEnv()` plus the variables `Entry.PassEnv` lists from `Options.Environ`. Names starting `FLOCKDECK_` or `PERCH_` are never passed, nor names MinimalEnv already gives; at most 64 names are read. A test starts a child with a poisoned parent environment and reads it back.
- Everything shown that came from the agent (errors, stderr, authentication methods, its name) is cleaned: control and format characters removed, cut to a length.
- The recording's tap is `Config.Tap`. A goroutine polls `Client.TapDropped()` every 500 ms and, if the recorder has a `Gap(n int64)` method, tells it how many lines were lost; `Close` does a last poll. `Snapshot.TapDropped` shows the count.
