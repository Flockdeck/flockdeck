---
type: Security Boundary
title: Paired device access roles
description: Per-device full or viewer role stored in prefs, failing closed to viewer.
resource: internal/store/deviceaccess.go
tags: [security, remote, devices, store]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/deviceaccess.go, internal/store/prefs.go]
---
Devices paired through Flockdeck Remote each have a `DeviceAccess` in [prefs](/data/prefs-file.md) (`devices` map).

| Role | Meaning |
|---|---|
| `full` | Everything a local window can do, except a few desk-only actions. Default for a device with no record (devices paired before roles existed). |
| `viewer` | Can look, cannot act. Artifacts only unless `watchPanes` is set, which also shows pane names, status and terminal output, but still no typing, resizing or running. |

`DeviceAccess` also has `approve` (with `approveV`, the wording version agreed to, and `approveAt`): whether the device may answer a structured pane's permission request from its phone. It is off for every device, full ones included, and the "no record means full" rule does not reach it. It is turned on at the desk for one device (`setDeviceApprove`) after the device's usual key is verified and the person agrees to the wording; `mayApprove` is true only when the running program's record and the preferences file both say so at the current wording. Changing a role keeps it; unpairing drops it. The plan is in `docs/phone-approval-plan.md`.

Fail-closed rules in code:

- `EffectiveRole` returns viewer for any value that is not exactly `full`.
- If prefs could not be read or parsed (`accessLost`), `AccessFor` returns viewer for every device.
- `AdoptAccess` makes the running program's copy of the roles win over a file re-read, so editing, damaging or deleting `prefs.json` while running cannot widen or narrow access.
- `WithoutDevicesNotIn` drops stale records but does nothing for an empty id list, since an empty list could be a failed or hostile answer.
- `WithAccess` stores nothing for a plain full device and refuses new entries past `MaxDeviceAccess` (256).
- `ForWindow` blanks `Devices`, so windows (including a viewer) do not see which other devices are paired.

See [store](/packages/store.md).
