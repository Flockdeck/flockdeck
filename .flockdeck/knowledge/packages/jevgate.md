---
type: Package
title: jevgate
description: The single gated path for every question Flockdeck sends to Jev: scrub, bound, cache, rate-limit, pause on failure.
resource: internal/jevgate
tags: [jev, rate-limit, cache, gate]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/jevgate]
---
# internal/jevgate

`Gate[T]` is a generic wrapper that runs text through one fixed flow before it reaches [jev](/packages/jev.md). Today the only user is `session.StatusAssist` (internal/session/assist.go), for quiet panes.

## Flow of `Do(ctx, scope, text)`

1. `jev.Scrub` the text, then apply the caller's `Bound`. The cache key is `scope` plus SHA-256 of this text.
2. Nothing left: `ErrNoText`, no call.
3. `NewClient()`; no key is an error and no call. A changed key or API base lifts any pause.
4. Cached answer for the same scope and text is returned, even when paused.
5. Paused after a failure: `ErrPaused`.
6. Scope limits (`ScopeInterval`, `ScopeHourly`), then the global `PerMinute` cap: `ErrRateCap`. The slot is taken before the call so concurrent asks cannot all slip under the cap.
7. `Ask` under `Timeout` and the caller's context.
8. Failure pauses every scope for `Cooldown`, or `CooldownRefused` for errors in `Refused`; no pause when the caller's own context was cancelled. An answer `Read` rejects counts as a failure.
9. A good answer is cached (`CacheTTL`, `CacheCap`, or `LatestPerScope`).

`Forget(scope)` drops one scope; `Reset()` drops everything.

Why it matters for privacy: see [Jev data handling](/security/jev-data-handling.md).
