---
type: Gotcha
title: Mail notices ride hook replies; only a fixed wake line is typed
description: Messages reach an agent in the reply to its next hook. The one thing typed for mail is a fixed wake line, only where an agent has a tested rule, and none has.
resource: internal/team/notify.go
tags: [team, messaging, windows, hooks, wake]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/team/notify.go, internal/workspace/wake.go]
---
Direction (27 Oct 2026): hook notices and the typed wake line are planned for removal. Status: planned removal. See docs/acp-direction.md.

- A notice is returned in the reply to `UserPromptSubmit` or `PostToolUse` (`hooks.NoticeEvents`), via `SetNoticeHandler`. Text typed into an agent's message box is a bug whether or not it is submitted, and on Windows a line and its Enter arriving in one read are taken for a paste and left unsubmitted.
- No message text is ever typed. For an idle agent Flockdeck can type one fixed line, `Flockdeck: new mail, message N. Run: flockdeck inbox read N` (`wakeService`, `internal/workspace/wake.go`), and only where the agent's built-in `agent.WakeRule` is marked tested. None is, so nothing is typed yet. The Enter is a separate write after `pasteSettle`, for the Windows paste rule above. If the user types in that pause the Enter is skipped and the line stays in the input box with their text after it; the same happens if the Enter write fails. The message is then not marked woken and nothing more is typed into the pane until the user types in it. Every typed line is a `wake` event in the team event log.
- The notice handler runs on the hook goroutine and is asked on every tool call, so it must be quick; `Notifier.Pending` is the cheap check.
- A hook reply can be lost (interrupted or timed out), so an unread message is announced again after 5 minutes, at most twice (`Renotify`, `MaxReminders`). `Forget(pane)` makes a pane be told again when its session was replaced.
- An agent that fires no hooks finds mail only with `flockdeck inbox`; `Sent.Notifies` is false for it and the window shows an unread count.
- `flockdeck inbox -follow` prints one line per message (or one digest line for a burst), with the id and the quoted sender but not the text.
