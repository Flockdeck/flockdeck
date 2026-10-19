---
type: Security Boundary
title: Baton approval for another provider
description: A baton leaving its provider needs a user click in a local window; the mechanism and its stated limits.
resource: internal/server/approval.go
tags: [security, baton, approval]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/approval.go]
---
When an agent runs spawn with `-baton-send-elsewhere`, `approveElsewhere` shows each local window that opened with an `Origin` header a `notice` with a pinned "Send it" button. The button sends `{"cmd":"approveBaton","text":"<token>"}`.

- Notices go only to connections with `page` set (an `Origin` header was sent) and that are not remote. No such window means the baton is refused.
- Each window gets its own random token (16 bytes); a token works only from the connection it was sent to, once.
- The wait is `batonApprovalWait` (shorter than the spawn timeout so the agent is told the outcome). First window to claim wins; the others get a `noticeWithdraw` and "Another window allowed that baton."
- Timeout, shutdown, cancellation or no windows are all refusals.

The code states the limit: this stops an agent that errs and a request nobody saw. It does not stop a hostile process running as the same user, which can read the token from the instance file, open the control socket with an `Origin` of its own and press the button.
