---
type: Gotcha
title: Mail notices are never typed into a pane
description: Messages reach an agent in the reply to its next hook, because typing into the input box leaves text the user did not write.
resource: internal/team/notify.go
tags: [team, messaging, windows, hooks]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/team/notify.go]
---
- A notice is returned in the reply to `UserPromptSubmit` or `PostToolUse` (`hooks.NoticeEvents`), via `SetNoticeHandler`. Text typed into an agent's message box is a bug whether or not it is submitted, and on Windows a line and its Enter arriving in one read are taken for a paste and left unsubmitted.
- The notice handler runs on the hook goroutine and is asked on every tool call, so it must be quick; `Notifier.Pending` is the cheap check.
- A hook reply can be lost (interrupted or timed out), so an unread message is announced again after 5 minutes, at most twice (`Renotify`, `MaxReminders`). `Forget(pane)` makes a pane be told again when its session was replaced.
- An agent that fires no hooks finds mail only with `flockdeck inbox`; `Sent.Notifies` is false for it and the window shows an unread count.
- `flockdeck inbox -follow` prints one line per message (or one digest line for a burst), with the id and the quoted sender but not the text.
