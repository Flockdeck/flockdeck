# When something is wrong

The things that go wrong most often, and what they mean.

## An agent pane opened as a shell

The `claude` CLI was not found on your `PATH`. Install
[Claude Code](https://claude.com/claude-code), then restart the application —
the path is looked up once at startup.

## A pane is dead, with a Restart button

The process exited. **Restart** starts it again in the same directory and
resumes the same conversation. If it exits immediately every time, run
`claude` yourself in that directory to see what it says.

## An agent stopped to ask about trusting a folder

A worktree is a directory Claude Code has never seen. Answer it once and it
will not ask again for that directory; to avoid it entirely when fanning out,
use the trust checkbox in the fan-out dialog, which carries over the answer
already given for the project.

## The window looks like a browser tab

The interface is a local web page shown in a chromeless application window,
provided by whichever Chromium-based browser is found first — Chrome, Edge,
Brave or Chromium. If none is installed it opens as an ordinary tab instead,
which works but looks less like an application. `AGENT_WRAPPER_BROWSER` forces
a particular one.

## Nothing is exposed to the network

The server binds to `127.0.0.1` on a random port, and every request — the page,
the assets and both WebSockets — must carry a token generated fresh for each
run. A second launch of the binary reaches the running instance through that
same loopback address.

## Desktop notifications never appear

The browser asks for permission on your first interaction with the window. If
it was refused, grant it in the browser's site settings for the address in the
title bar. Notifications are only raised while the window is *not* in front.

## An agent seems to think it is a child of another session

It is not: each pane is a separate top-level Claude session with its own
session id, its own generated settings file, and an environment scrubbed of
the markers a parent Claude session would otherwise pass down. If a pane is
behaving as though it inherited something, restart it — and the state that
survives a restart is the conversation, deliberately.

## Everything stopped when I closed the window

Closing the window quits the application. Use **Detach** in the command
palette to close the window and leave the agents running, and
`agent-wrapper` to come back to them.
