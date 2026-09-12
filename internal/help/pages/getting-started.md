# Getting started

Flockdeck runs several coding agents at once, each in a real terminal, and
answers one question at a glance: which agent needs you right now.

Every agent pane is a real program in a genuine pseudo-terminal, so it behaves
exactly as it does in a normal terminal — permission prompts, slash commands,
plan mode, colours, mouse. Claude Code is what a pane runs unless you say
otherwise; any other coding agent, and any model API spoken to directly, is a
pick away. What the app adds is everything you need once there is more than one
of them.

## The three things to know first

**The dot in each pane header says what that agent is doing.** Green is
working, amber is *waiting on you*, grey is idle, red means the process
exited. A tab holding a waiting agent is marked `▲`, and so is the window
title, so an agent that blocks while you are looking elsewhere still reaches
you.

**Panes are moved, not recreated.** Drag a pane by its header to another edge,
another tab, or a tab of its own; nothing restarts, and the conversation,
working directory and scrollback come with it.

**[[key:palette]] opens the command palette**, and so does **Commands** at the
right of the top bar. It holds every action in the application but switching
tabs, searchable. If you remember one shortcut, remember that one.

## A first session

1. Press [[key:newAgentTab]] for an agent tab, or [[key:splitRight]] to put a
   second agent beside the one you have. Both take the default agent;
   [[action:newAgentTabChoose]] is the same thing with the picker in front of
   it, for when you want a different agent or a different model.
2. Type into a pane as you would in any terminal. Click a pane to give it the
   keyboard.
3. When an agent blocks on a permission prompt its dot turns amber and its tab
   is marked. Answer it, and it goes back to work.
4. When one has produced something, press [[key:changes]] to see the diff,
   commit and push it without leaving the window.

## When one agent is not enough

Two agents editing the same files will fight. Give each its own checkout
instead: [[key:worktrees]] opens the worktrees, where you can create them, and
[[key:fanout]] fans out — it turns one agent's plan into a set of agents that
each take a task in a worktree of their own. [Git worktrees](#worktrees) and
[Fan out](#fanout) cover both.

## Which agent, which model

A pane's header names what is in it — `claude · sonnet`, `codex · gpt-5.6-sol` —
beside its branch. [Agents and models](#agents) explains the picker, what changes when
an agent cannot report its own status, and how to add one of your own. For the
API agents, and for Claude Code where its status line is read, the header also
estimates what the conversation has spent and shows how near a usage limit it
is; [Spend and limits](#spend) says how, and when Claude Code's are read.

## Finding the rest of it

The rail down the left of the window has a tile for each open project — one
with an amber badge has an agent waiting on you — and under them the tools:
broadcast, changes, history, worktrees, remote access, help and settings. Rest the
pointer on one, or reach it with the keyboard, and it says what it is and the
key that does the same. On a phone or a narrow window the rail folds into the
menu at the left of the top bar.

[[key:help]] brings this page back from anywhere.
