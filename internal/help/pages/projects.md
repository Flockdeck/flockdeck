# Projects

Each open project has a tile in the rail down the left of the window: two
letters, with the full name and folder in its tooltip, and a click switches to
it. The project's name at the left of the top bar opens the projects dialog,
which switches between them, opens new ones and closes them; [[key:projects]]
does the same from the keyboard, and **Open a project**, under the tiles,
opens it at the folder browser.

The rail starts as icons alone. [[key:toggleRail]] widens it into a panel of
icons and names, or folds it back; widened, drag its right edge — or use the
left and right arrow keys once it has the keyboard — to whatever width suits
you. Both are kept, and are what a new window opens onto.

Several projects stay open at once and **switching does not stop anything** —
the other project's agents keep working, and its tile carries an amber badge if
one of them starts waiting on you while you are elsewhere.

Each project keeps its own tabs, its own layout, its own restored
conversations, and its own default agent and model — so the repository you
want Codex on gets Codex from the plain one-keystroke split, while everything
else goes on getting Claude Code.

## Two at once, side by side

Switching shows one project at a time. To watch two of them together, split a
pane into another project: the ⊞ beside a project in the projects dialog, or
**Split into project** in the command palette. The new agent works in that
project while sitting on this tab, and its header carries the project's name so
the tab still says which is which.

The tab goes on belonging to the project it was made in, and is saved with that
project's layout. Reopening it brings the other project back too, since the
agent on it belongs there — and closing that project stops the agent wherever
it is being shown.

## Opening one

The picker offers the projects you have opened before, and a directory browser
that flags git repositories, so a project is two clicks away. You can also type
or paste a path: <kbd>Enter</kbd> browses to it, and **Open this folder** opens
whichever folder the browser is showing.

The eight most recent projects are shown, with the rest a click away, and the
`×` beside one takes it off the list. Once more than one project is open, each open one has a `×` of its
own that closes it.

The browser is served by the Go side rather than by the page, because a web
page cannot be handed a real directory path.

## From the command line

```sh
flockdeck -C ~/code/api
```

This does not start a second set of agents. It finds the instance already
running, hands it the directory, and opens a window onto it — so the project
joins the session you already have.

Closing a project closes its panes, which stops its agents and ends what was
started in them, as closing each pane would. Switching away does not.
