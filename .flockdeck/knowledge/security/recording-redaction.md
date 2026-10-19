---
type: Security Boundary
title: Recording redaction and secret-file withholding
description: Best-effort pattern redaction applied before any transcript line is written and again before any line is served to a client.
resource: internal/record/redact.go
tags: [security, recording, redaction]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/record/redact.go]
---
Everything a recording writes goes through `Redact` and `Clip` first. The code says plainly this is best effort.

## What is matched

- Token shapes (`tokenShapes`): Anthropic and generic `sk-` keys, GitHub, GitLab, AWS, Slack, Google, npm, SendGrid, Stripe, Hugging Face, DigitalOcean and Twilio keys, and JWTs.
- PEM private keys, `Bearer`/`Basic` credentials, `user:password@` in URLs.
- Values of variables and fields whose name contains secret, token, password, passwd, pwd, api key, access key, private key, credential, authorization, auth token/key/header (`nameIsSecret`; `KeyIsSecret` also takes `passphrase` and a trailing `pass` word).
- Marker text: `[redacted]`; kinds named by `record.Kinds()`.
- Text over `MaxRedactBytes` (1 MiB) is cut and the rest replaced by one mark, never passed unread.

## Secret files

Tool calls touching a path that `review.SecretPath` calls secret have their output replaced by `[withheld: a secret file]`.

## Second pass on read

[recordview](/packages/recordview.md) redacts again when serving, since rules improve and old recordings predate them; it does not repeat the secret-file decision.

## Known gaps (listed in recordview's package comment)

Secrets with no recognisable shape and no naming context; secrets split across entries; quoted values over 4 KiB; command-line password forms other than a fixed list of flags, `curl -u`, `mysql -p`, `sshpass -p`; session, agent, project, tool and model fields get patterns only.

Also: recordings are 0600 files in 0700 folders, and exports are refused inside any git repository ([record](/packages/record.md)).
