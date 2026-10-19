---
type: Package
title: workspace TOML reader
description: The small TOML parser the workspace uses to read a Codex config file, and what it accepts and rejects.
resource: internal/workspace/toml.go
tags: [workspace, toml, parser, provider]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/toml.go, internal/workspace/provider.go]
---
# TOML reader

`parseTOML(text)` in `internal/workspace/toml.go` reads the subset of TOML found in a CLI agent's config. Its one caller is `codexOverrideFrom` in `provider.go`, which reads Codex's `config.toml` to find where the agent sends data ([provider and gateway detection](/security/provider-gateway-detection.md)). Any error means the company is "not known" and the handoff is flagged.

## Result shape

Nested `map[string]any`. A table that is an array of tables is a `[]any` of maps. Strings are `string`, booleans are `bool`, arrays are `[]any`, inline tables are maps. Numbers and dates are not converted: they stay as `tomlRaw` text.

## Accepted

- `[table]` and `[[array.of.tables]]` headers; dotted and quoted keys (`a."b.c".d`); bare keys of letters, digits, `_` and `-`.
- Basic strings with escapes (`\b \t \n \f \r \" \\ \uXXXX \UXXXXXXXX`), literal strings, and multi-line forms of both. After the opening triple quote, one line end is dropped; up to two quotes may precede the closing three, and a run of more than five quotes fails. In multi-line basic strings a trailing backslash consumes the white space that follows.
- Arrays that span lines, inline tables, comments, CRLF line ends, a leading byte order mark.
- Dates with a space between date and time (`1979-05-27 07:32:00`); `inf` and `nan`.

## Rejected (returns an error wrapping `errTOML`)

- Invalid UTF-8.
- A key set twice, or a name used as both a value and a table, or a table and an array of tables.
- A line end inside a single-line string, a bad or cut-off escape, an unclosed string, array or inline table.
- Nesting deeper than `tomlMaxDepth` (64).
- A bare word that is not a boolean, number or date shape.
- Anything after a value or header on the same line other than a comment.

Errors carry the line number. The parser does not validate number or date syntax beyond the first character, so `1__2` is accepted as raw text; callers only read strings and tables.

## What the caller reads

Top-level keys, with the table named by `profile` (under `profiles`) laid over them; `oss_provider`, `model_provider`, `openai_base_url`, and `base_url` of the active entry in `model_providers`. A `base_url` in any other table, such as an MCP server's, is not read.

Related: [CODEX_HOME](/config/codex-home.md), [workspace](/packages/workspace.md).
