# 02-eight-helpers-permission-and-question

Eight helpers on a billing migration. One asks to run a command, one asks a question.

The team is a manager and 8 helpers. The graph is judged at 2026-10-21T09:00:00Z.

## Waiting on the person (2)

- backfill: asks: Backfill invoices older than 2024, or leave them as they are?
- schema: asks to run psql -f migrations/0042_invoices.sql

## The other helpers

- ledger: working (Edit ledger/post.go)
- reports: working (Bash go build ./...)
- webhooks: idle, nothing owed
- fixtures: exited cleanly
- emails: idle, its task "Receipt email copy" is in review and waits on a reviewer
- retries: idle, its task "Retry failed charges" is blocked: waiting on the payment sandbox keys
