---
type: Security Boundary
title: Baton command-line secrets
description: Which command-line flags and arguments the baton scrubber reads as passwords, and how it reads shell words.
resource: internal/baton/shapes2.go
tags: [security, secrets, baton, shell]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/shapes2.go, internal/baton/shapes3.go, internal/baton/scrub.go]
---
# Baton command-line secrets

Part of [baton scrubbing](/security/baton-scrubbing.md); the other shapes are in [baton secret shapes](/security/baton-secret-shapes.md). `dbSpans` finds known program names (`dbCommand`) in the text and reads the words after each as a shell would.

## Reading words

`shellWordEnd` ends a word at an unquoted space, tab or newline. Single quotes, double quotes (with escapes), `$'...'`, a backslash escape and a backslash-newline join pieces into one word, so `'abc'def` is one. A `[REDACTED: ...]` mark counts as one word. Quotes that do not close within 4096 bytes end at the line end. `commandWords` stops at an unquoted newline, `;`, `|`, `||`, `&&`, `&` or `;;`, at the start of the next command, or after 200 words. A word budget (`len(text)/2 + 5000`) keeps a line of many commands linear. A program name only starts a command after a space, separator, bracket or quote, not inside a path.

## Programs and flags

Kinds are `db-password` for `dbFlags` and `secret-flag` for `toolFlags`.

| Program | Taken |
|---|---|
| mysql family (`mysql`, `mysqldump`, `mysqladmin`, `mariadb`, ...) | `-pSECRET` attached; `-p SECRET` with a space only when the word reads like a password (8+ characters with a digit, or both cases) |
| `sshpass`, `docker login`, `podman login` | `-pSECRET` or `-p SECRET` |
| `redis-cli` | `-a SECRET` |
| `curl` | text after the first colon of `-u` / `--user` (user name stays) |
| `htpasswd` | with `-b`, the third argument, or the second with `-n` |
| `vault login` | first word that is not a flag or `key=value` |
| `az login` | `-p`, `--password` |
| `doctl` | `-t`, `--access-token` |
| `zip`, `unzip`, `zipcloak` | `-P SECRET` or `-PSECRET` |
| `gpg`, `gpg2` | `--passphrase` |
| `7z`, `7za`, `7zr`, `7zz` | `-pSECRET` |
| `aws configure set` | second positional when the first is `aws_secret_access_key`, `aws_session_token` or the short forms |
| `http`, `https`, `xh`, `xhs` | `-a` / `--auth`: after the colon, or the whole value when it reads like a password |
| `docker login`, `podman login`, `helm registry login` with `--password-stdin` | the word after `<<<`, or the word that a preceding `echo` or `printf` pipes in on the same line (variables and backtick commands excepted) |

A capital `-P` for mysql is a port and is left. Long flags elsewhere are handled by the generic flag rule in `scrub.go` (`flagRe`): a secret-named flag takes the next word, except flags ending in `-stdin`, `-file`, `-env`, `-prompt` or `-path`, and a value on the next line. A numeric value under a quantity name is kept.

## Name rules shared with the command line

`export NAME value` and `setx NAME value` are in `exportSpans`. With a lower-case name, `setx` is always read; `export` only when the value is one word of 8+ characters that mixes classes and is not an identifier (`lowerNameOK`).
