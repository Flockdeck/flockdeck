---
type: WebSocket Message
title: Control commands: chat, history, attachments
description: Commands on /ws/control for the chat view of a pane, session history, resume and image attach.
resource: internal/server/conversation.go
tags: [websocket, control, conversation]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/conversation.go]
---
Part of [/ws/control](/api/ws-control.md). Files: `conversation.go`, `conversation_search.go`, `history.go`, `imageattach.go`.

| `cmd` | Fields | Reply | Access |
|---|---|---|---|
| `conversationOpen` | `id` (pane), `after` (cursor) | `conversationPage`, then `conversationAppend` as the transcript grows | watcher |
| `conversationOlder` | `id`, `before` (entry id) | `conversationPage` | watcher |
| `conversationDetail` | `id`, `entry` | `conversationDetail` | watcher |
| `conversationClose` | `id` | stops the stream | watcher |
| `conversationSearch` | `id`, `q` | `conversationSearch` (case-insensitive match across the whole conversation) | watcher |
| `conversations` | `path` | `conversations`: cwd, items, error (saved sessions for a folder) | full |
| `resumeConversation` | `id`, `path`, `text` (title), `agent` | opens a pane resuming that session | full |
| `attachImage` | `id`, `name`, `mediaType`, `data` (base64) | `attachImageResult`: id, path, error | full |

"Watcher" means a viewer device is allowed only when the desk set `watchPanes` for it; see [viewer gate](/security/viewer-command-gate.md). Chat content is only sent to such devices through `watcherMessageTypes`.

`attachImage`: at most 8 MiB (`maxAttachedImageBytes`), rate limited per window, saved on this machine; old attached images are cleaned up at startup (`cleanupAttachedImages`).

The `conversationOpen` stream is kept per client in `conversationHub`; it is dropped when the window disconnects.
