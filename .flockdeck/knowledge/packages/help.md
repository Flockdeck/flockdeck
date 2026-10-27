---
type: Package
title: help
description: Embedded help pages and the single table of UI actions and shortcuts that the palette and help pages render from.
resource: ui/help/help.go
tags: [help, keybindings, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [ui/help/help.go, ui/help/keys.go, ui/help/render.go]
---
## Pages

`pages/*.md` are embedded with `go:embed` and rendered once on first use (Markdown to HTML via goldmark, external links post-processed). `Pages()` returns `Page` {slug, title (first heading), summary (first paragraph), html, text (words only, for search)}; `Slugs()` lists them. Order is a hard-coded list (getting-started, panes, agents, status, auto-approve, spend, rearranging, broadcast, worktrees, changes, github, history, baton, baton-details, recording, projects, persistence, remote, settings, shortcuts, cli, troubleshooting); a test keeps it level with the files present.

Pages use placeholders expanded at render time: `[[key:ID]]` inline shortcut, `{{keys}}` or `{{keys:Section}}` shortcut table, and `[[action:ID]]` (the binding, or the bold action name if it has none).

## Keys table

`keys.go` defines `Key` {`id`, `keys`, `label`, `short`, `section`, `page`, `noPalette`, `confirm`} and `Keys`, the one list of actions. Sections: Panes, Tabs, Agents, Git, Finding your way, The window. `Lookup(id)`, `InSection(section)`, `Key.Name()`, `ShortcutsMarkdown()`. An empty `keys` means palette-only; `keys` containing "…" is a range that cannot be remapped.

User overrides are applied by [keybindings](/packages/keybindings.md).
