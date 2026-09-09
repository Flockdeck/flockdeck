# Broadcast and the prompt bar

Some instructions are for every agent at once — "run the tests and fix what
breaks", across five worktrees. Broadcast is how you say a thing once.

## The broadcast set

[[key:toggleBroadcast]] turns broadcast on and off. While it is on, what you
type is mirrored into every pane in the **broadcast set**, which is by default
every agent in the current tab.

The `⇉` button in each pane header adds that pane to the set or takes it out.
Membership is shown in the pane header even while broadcast is off — otherwise
the button that toggles it would appear to do nothing.

The focused pane is always included, so typing never goes somewhere you cannot
see.

## The prompt bar

[[key:promptAll]] opens a single-line prompt at the bottom of the window.
Compose the instruction there, press <kbd>Enter</kbd>, and it is sent to every
pane in the set as one submitted message rather than typed key by key.

This is usually what you want in preference to live broadcast: you get to read
the sentence before six agents act on it, and a typo is yours to fix rather
than theirs to interpret.

The label tells you how many panes it is about to reach.
