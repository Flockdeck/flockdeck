---
type: File Format
title: Preferences (prefs.json)
description: User preferences, including device access roles and remote artifact opt-ins, preserving unknown keys.
resource: internal/store/prefs.go
tags: [store, persistence, prefs]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/prefs.go, internal/store/store.go, internal/store/deviceaccess.go]
---
`prefs.json` in the config dir; `store.ReadPrefs` / `LoadPrefs` / `SavePrefs`. A missing file gives zero-value `Prefs`.

## Prefs fields (JSON keys, all omitempty)

`helpSeen`, `dismissedTips`, `fontSize`, `notificationsOff`, `scrollback`, `updatesOff`, `cursorSteady`, `screenReader`, `cursorStyle`, `fontFamily`, `railExpanded`, `railWidth`, `spend` ({`statusLine`}), `push` ({`off`, `anonymous`, `delaySeconds`}), `theme`, `accentColor`, `autoReviewDefault`, `jevStatus`, `conflictRadar`, `recordingAcknowledged`, `remoteArtifacts`, `devices`.

- `remoteArtifacts`: `kinds` (map of "recordings", "links", "files" to bool), `ack` (kind to {`v`, `at`}), `devices` (device ids).
- `devices`: map of device id to `DeviceAccess` ({`role`, `watchPanes`, `approve`, `approveV`, `approveAt`, `approveKey`}); see [device access roles](/security/device-access-roles.md).

## Behaviour

- Keys this build does not know are captured in `extra` on read (`unknownPrefs`, computed from the struct's json tags by reflection) and written back by `SavePrefs`, so a downgrade does not erase newer settings.
- A file that cannot be read or parsed returns `Prefs` with `accessLost` set; every device then resolves to viewer.
- Saves go through `keepUnread` then `writeAtomic`.

See [store](/packages/store.md).
