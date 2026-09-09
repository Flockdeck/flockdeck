# Projects

The button at the left of the tab bar switches projects and opens new ones;
[[key:projects]] does the same from the keyboard.

Several projects stay open at once and **switching does not stop anything** —
the other project's agents keep working, and its marker turns amber if one of
them starts waiting on you while you are elsewhere.

Each project keeps its own tabs, its own layout and its own restored
conversations.

## Opening one

The picker offers the projects you have opened before, and a directory browser
that flags git repositories, so a project is two clicks away. You can also type
or paste a path.

The browser is served by the Go side rather than by the page, because a web
page cannot be handed a real directory path.

## From the command line

```sh
perch -C ~/code/api
```

This does not start a second set of agents. It finds the instance already
running, hands it the directory, and opens a window onto it — so the project
joins the session you already have.

Closing a project stops its agents. Switching away does not.
