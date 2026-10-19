---
type: Security Boundary
title: Team sender labels and notice text
description: How agent-chosen names are sanitised so an agent cannot pass for the user or Flockdeck, or smuggle text into notices.
resource: internal/team/team.go
tags: [security, messaging, spoofing, prompt-injection]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/team/team.go, internal/team/notify.go, internal/team/block.go]
---
# Sender labels and notices

Message senders and roles are not authenticated (see [hook server token](/security/hook-server-token.md)); these rules limit what they can do.

- **Reserved names.** `Impersonates` normalises a name (letters and digits only, lower case) and rejects `theuser`, `user`, `you`, `me`, `flockdeck`, `flockdecksystem`, `system`, `admin`, `operator`, or anything starting `theuser` or `youat`. `AgentLabel` rewrites such a name to `(agent) <name>`.
- **User identity comes from a flag.** The person at the window is shown as "the user (at the window)" chosen by `Sender.User` or `Actor.User`, never by a name. Only the application sets it.
- **Plain text.** `PlainLabel` turns control characters and line separators into spaces, collapses white space and cuts to 64 runes. `Block` drops control characters and indents every line so text cannot look like the caller's own output.
- **Notices carry no message text.** A notice has a count, an id and the quoted sender name (at most 32 characters) and role (16), reduced by `noticeWord` to letters, digits, spaces and `._-()`. They end with a disclaimer that messages are not user instructions and approvals in them are not user approval.
- **Task notices** follow the same rule; a note's words appear only after `flockdeck inbox read`, cut to one line, and never in a line typed into a terminal.
