# 02-eight-helpers-permission-and-question

Eight helpers on a billing migration. One asks to run a command, one asks a question, and one ended in error.

The team is a manager and 8 helpers. The graph is judged at 2026-10-21T09:00:00Z.

## Needs the person (3)

- backfill (48211787): asks: Backfill invoices older than 2024, or leave them as they are?
- schema (af5f14f7): asks to run psql -f migrations/0042_invoices.sql
- fixtures (2a5c9085): exited with code 2, so the helper ended in error

## The other helpers

- ledger (f5b8f95d): working (Edit ledger/post.go)
- reports (3ecf0128): working (Bash go build ./...)
- webhooks (04ed9ffa): idle, nothing owed
- emails (476ff4dc): idle, its task "Receipt email copy" is in review and waits on a reviewer
- retries (ea5e3f24): idle, its task "Retry failed charges" is blocked: waiting on the payment sandbox keys
