# 05-six-helpers-undelivered-message

Six helpers on an onboarding flow. One asks a question. The manager has an unread message from a helper that has waited seven minutes.

The team is a manager and 6 helpers. The graph is judged at 2026-10-21T09:00:00Z.

## Waiting on the person (1)

- forms: asks: Make the phone number optional?

## The other helpers

- copy: working (Edit onboarding/welcome.md)
- auth: idle, its task "Magic link sign-in" is in review and waits on a reviewer
- tests: idle, nothing owed
- icons: exited cleanly
- i18n: idle, its task "Extract the new strings" is blocked: waiting on the translation vendor's key format

## The message

tests sent the manager a message 7 minutes before the graph's time, and it is still queued: the manager has not been shown it, woken for it or read it. The graph does not list it as waiting on the person, because the manager is working. The message edge says `queued`; stage 0a had no delivery field, and the schema now requires one on a message edge.
