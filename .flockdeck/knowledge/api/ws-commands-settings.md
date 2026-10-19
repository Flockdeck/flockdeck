---
type: WebSocket Message
title: Control commands: preferences, helpers, versions
description: Commands on /ws/control that change saved preferences, manage helper apps and install other versions.
resource: internal/server/control.go
tags: [websocket, control, prefs]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/control.go]
---
Part of [/ws/control](/api/ws-control.md). Each preference command goes through `updatePrefs`, which applies the change to the prefs file as it is on disk (not the start-up copy), saves, and broadcasts a `prefs` message to every window. Nothing is saved when the file cannot be read. See [prefs file](/data/prefs-file.md).

## Preference setters

| `cmd` | Field | Setting |
|---|---|---|
| `helpSeen`, `resetTips` | | first-run help flag, hints |
| `dismissTip` | `id` | one hint |
| `fontSize`, `scrollback`, `railWidth` | `size` | terminal font size, scrollback lines, rail width px |
| `fontFamily`, `cursorStyle`, `statusLine`, `theme`, `accentColor` | `text` | appearance |
| `notifications`, `updates`, `cursorBlink`, `pushNotify` | `kind` `off` | on/off pairs |
| `screenReader`, `railExpanded`, `pushAnonymous`, `autoReviewDefault`, `conflictRadar`, `jevStatus` | `kind` `on` | on/off |
| `pushDelay` | `size` | seconds before a wait is pushed to phones |
| `presence` | `kind` `used` | the desk reports real input (suppresses phone pushes) |

From a relay window `updates` and `jevStatus` (turning on) are refused with a notice and the window is resent `prefs`. `checkForUpdate` is desk-only too.

## Helpers

`helpers` (`force` refreshes), `helperPlan` (`id`, `text`), `helperInstall`, `helperStart`, `helperStop`, `helperOpen`, `helperUninstall` (`purge`) reply with `helpers` and `helperPlan` messages. Relay windows are not shown these (the `helpers` key action is removed from their key table). See also [helper endpoints](/api/helpers-endpoints.md).

## Versions

`listVersions` replies `versions` (items, error); `installVersion` (`text` = version) downloads and stages another release. Refused from relay windows.
