# Getting started

agent-wrapper runs several Claude Code agents at once, each in a real terminal,
and answers one question at a glance: which agent needs you right now.

Every pane is the `claude` CLI running in a genuine pseudo-terminal, so it
behaves exactly as it does in a normal terminal — permission prompts, slash
commands, plan mode, colours, mouse. What the app adds is everything you need
once there is more than one of them.

## The three things to know first

**The dot in each pane header says what that agent is doing.** Green is
working, amber is *waiting on you*, grey is idle, red means the process
exited. A tab holding a waiting agent is marked `▲`, and so is the window
title, so an agent that blocks while you are looking elsewhere still reaches
you.

**Panes are moved, not recreated.** Drag a pane by its header to another edge,
another tab, or a tab of its own; nothing restarts, and the conversation,
working directory and scrollback come with it.

**[[key:palette]] opens the command palette**, which holds every action in the
application, searchable. If you remember one shortcut, remember that one.

## A first session

1. Press [[key:newAgentTab]] for an agent tab, or [[key:splitRight]] to put a
   second agent beside the one you have.
2. Type into a pane as you would in any terminal. Click a pane to give it the
   keyboard.
3. When an agent blocks on a permission prompt its dot turns amber and its tab
   is marked. Answer it, and it goes back to work.
4. When one has produced something, press [[key:changes]] to see the diff,
   commit and push it without leaving the window.

## When one agent is not enough

Two agents editing the same files will fight. Give each its own checkout
instead: worktrees creates them, and fan out turns one agent's plan into a set
of agents that each take a task in a worktree of their own.
