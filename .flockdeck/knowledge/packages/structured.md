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
- API: `Send(text)`, `Cancel()`, `Answer(requestID, optionID)`, `AnswerCard(seq, requestID, optionID)`, `Close()`, `Snapshot()`, `Subscribe()`. `Options.Spec` is the catalog entry (`agent.Spec`, runner `acp`); the adapter, install and login advice and `MCP` are read from it.
- Phases: `starting`, `ready`, `needs setup` (a login the agent asked for, or Node missing; `Snapshot.Setup` says which), `exited` (`Err` holds the agent's last stderr line, cleaned), `failed`, `closed`. While ready, `Status` is the bridge's working, waiting or idle.
- `Send` queues behind a running turn (16 prompts, 1 MiB each) and may be called while starting. `Cancel` ends the running turn and drops the queue. A permission request waits for `Answer`; nothing answers by itself. An answer must name an option on the card, and `AnswerCard` also the card's `Seq`, or it is `ErrStale`. `Close` releases every waiting request as cancelled, closes the client, then the recording.
- The environment is `acp.MinimalEnv()` plus `Spec.ACPPassEnv(Options.Environ)`, which is where the rules live (reserved prefixes, names MinimalEnv gives, the cap, case on Windows). `Command.env` is unexported, so only tests in the package can give another. A test starts a child with a poisoned parent environment and reads it back.
- Everything shown that came from the agent (errors, stderr, authentication methods, its name) is cleaned: control and format characters removed, cut to a length.
- The recording's tap is `Config.Tap`. A goroutine polls `Client.TapDropped()` every 500 ms and calls `Recorder.Gap(n)` with the new count (`record.RPCRecorder.Gap` writes an `rpc_gap` line); `Close` does a last poll. `Snapshot.TapDropped` shows the count.
- A prompt error that is not an exit, a login or a close ends only that turn (notice "Turn ended: failed: ...", the pane stays ready, the queue is kept). A setup state is final: the pane must be started again after the person signs in.
