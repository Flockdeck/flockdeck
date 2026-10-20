# 05-six-helpers-undelivered-message

Six helpers on an onboarding flow. One asks a question. The manager is idle with an unread message from a helper that has waited seven minutes.

The team is a manager and 6 helpers. The graph is judged at 2026-10-21T09:00:00Z.

## Needs the person (2)

- forms (c2e39f87): asks: Make the phone number optional?
- manager (c2beaec2): 1 unread: the message from tests has waited seven minutes and the manager is idle

## The other helpers

- copy (bcaec151): working (Edit onboarding/welcome.md)
- auth (e8918e34): idle, its task "Magic link sign-in" is in review and waits on a reviewer
- tests (2023df34): idle, nothing owed
- icons (77843dc7): exited cleanly
- i18n (a2153c58): idle, its task "Extract the new strings" is blocked: waiting on the translation vendor's key format

## The message

tests sent the manager a message 7 minutes before the graph's time, and it is still queued: the manager has not been shown it, woken for it or read it. The manager is idle and has not read it, so after five minutes it is a stale-unread item for the person. The message edge says `queued`; stage 0a had no delivery field, and the schema now requires one on a message edge.
