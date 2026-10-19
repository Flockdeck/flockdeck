---
type: Security Boundary
title: Baton secret shapes
description: The second scrubbing layer's own patterns for config files, headers, URLs, cookies and code, and the exemptions that keep placeholders and amounts.
resource: internal/baton/shapes.go
tags: [security, secrets, baton, scrubbing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/shapes.go, internal/baton/shapes2.go, internal/baton/shapes3.go, internal/baton/shapes4.go, internal/baton/scrub.go]
---
# Baton secret shapes

The second layer of [baton scrubbing](/security/baton-scrubbing.md) is `record.Find` from [record](/packages/record.md) plus `extraSpans`, which holds forms that matter in a command line or config pasted into a conversation. Each finding is a `span` (start, end, kind). `Scrubber` drops any finding that `notSecret` rejects, and drops `record` findings that overlap a kept Authorization scheme word or take in a URL user together with its password (`mergedWithURLUser`). Command-line tools are in [baton command-line secrets](/security/baton-command-line-secrets.md).

## Cheap gate

`extraSpans` lower-cases the text (marks removed) and looks for `pass`, `pwd`, `secret`, `token`, `key`, `auth`, `cred` or `pw`. Without any, only auth headers, URL credentials, command lines, redis and npm forms, cookies (when `cookie:` appears), provider keys and bare `user:pass@host` run. With one, all rules run. `skipRules` lets a test switch a rule off; it is empty in use.

## Rules and kinds

| Rule | Finds | Kind |
|---|---|---|
| auth | Value of `Authorization` / `Proxy-Authorization`: the scheme word stays. Comma-separated further credentials are taken when the next scheme repeats or is one of bearer, basic, token, digest, apikey, negotiate, ntlm, bot. Digest, hawk, AWS4-HMAC-SHA256, oauth and mac take the rest of the line | `auth-credential` |
| urlcred | Password of `scheme://user:password@host`; scheme, user and host stay | `url-credential` |
| redis | `://:password@` with an empty user | `url-credential` |
| bare credential | `user:password@host.tld` with no scheme; `mailto`, `sip`, `from`, `to` and similar are skipped; the password must mix character classes | `url-credential` |
| npm | `_authToken`, `_auth`, `_password` as `:_x=value` or at line start of an npmrc | `registry-token` |
| json | Strings inside an array or object that is the value of a secret-named key (limit 8192 bytes), and the value of a `["name","value"]` pair | `secret-value` |
| yaml block | Indented body of a block scalar (`>` or `\|`) under a secret-named key | `block-secret` |
| env line | `NAME=value` lines (unquoted values with spaces) where `record.SecretName` accepts the name | `env-secret` |
| kv | Secret-named key then `:` or `=` and a value of several words, to end of line or ` #`. Key must end in a secret word (`password`, `token`, `api_key`, ...). Values that look like code are skipped | `env-secret` |
| passname | Names ending in `pass`, `passwd`, `pwd`, `pw` or `key` whose value looks chosen: 8 characters or more, 12 for `key`; public, cache, primary, sort, routing and similar key names are excluded | `secret-value` |
| call | A secret-named value that starts as an environment read (`os.Getenv`, `ENV`, `process.env`, ERB) but continues with more | `secret-value` |
| form | `pass=` or `pw=` fields of a query or form (4+ characters) | `secret-value` |
| export | `export NAME value` and `setx NAME value` | `env-secret` |
| k8s | `- name: X_TOKEN` followed by `value:` | `env-secret` |
| golit | `password: []byte("x")`, `keys := []string{...}` | `secret-value` |
| cookie | `Cookie:` / `Set-Cookie:` pairs; attributes such as path and expires are skipped; a secret, session, auth or jwt name needs 8+ characters, others 24+ | `secret-value` |
| literal | Quoted literal (8-200 chars) assigned to a secret-named name, mixed character classes; also npmrc lines | `secret-value`, `registry-token` |
| provider | Prefix shapes `sk_live_`/`sk_test_` (16+ alphanumerics), `shpat_`/`shpca_`/`shppa_`/`shpss_` (32 hex), `dop_v1_` (64 hex) | `secret-value` |

## What is not a secret

`notSecret` rejects placeholders for every finding, and the value checks below for `secret-value`, `env-secret` and `secret-flag`:

- Placeholders: `<name>`, `$NAME`, `${NAME}`, `%NAME%`, `$(cmd)`, `{{ x }}` templates (a `${X:-default}` is not one), `your-..-here`, masks of 8+ `*`, bullets or `#` (not `x`).
- Nothing words as a single value: none, null, nil, true, false, required, undefined, string, int, number, bool, object, array and similar. `password` or `changeme` are not on the list.
- A variable named like its key (`Token: token,`), an environment read call or ERB tag alone (`callRe`, `erbRe`, `exactPlaceholder`), or a safe call or slice (`safeCode`); anything after the call other than a closing bracket, comma, semicolon or a comment without chosen-looking words makes it a value.
- A YAML key on the next line, a number under a quantity name (`max`, `count`, `ttl`, `tokens`, `timeout`, ...), and lines of `name=number` pairs.
- Times: an ISO date or 9 to 13 digit epoch under a name ending `_at`, `_date`, `_time` or containing `expire`. With a secret word in the name, only `token` and `key_id` names that also say expires, expiry, issued, created or updated keep a number that starts with 1 and has 10 or 13 digits.
- Paths under names ending `_file`, `_path`, `_dir`, `_url`, `_uri` unless a segment looks random, and mounted secret paths such as `/run/secrets/...`.

Anything else under a secret name is redacted: the code comments say "when in doubt, redact".
