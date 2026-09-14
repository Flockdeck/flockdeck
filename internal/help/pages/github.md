# GitHub

The **GitHub** panel in the rail opens and reads pull requests and issues,
and shows CI status, for the project on screen — all through the `gh`
command-line tool, without leaving Flockdeck.

## Getting connected

If `gh` is not installed, the panel — and its own section in **Settings** —
offers to install it: with whatever package manager this machine already
has (`winget` on Windows, `brew` on a Mac, `apt`, `dnf` or `pacman` on
Linux), or a link to install it by hand from
[cli.github.com](https://cli.github.com).

Once it is installed, **Sign in with GitHub** walks through `gh auth
login`: a one-time code appears here, along with a link to
github.com/login/device to enter it. Approve it there, in your browser, and
this comes back signed in on its own — there is nothing more to do in
Flockdeck. **Sign out** at any point undoes it.

## Pull requests and issues

The **Pull requests** and **Issues** tabs list what is open on the
repository, filterable to closed, merged (pull requests) or all. **New
pull request** and **New issue** open a small form — a title, a
description, and for a pull request, an optional base branch and a draft
toggle — and the item opens here once gh has created it, ready to read or
comment on.

Opening an item shows its description and its comments, and a box to add
one of your own. **Open on GitHub** leaves for the real thing when that is
what is needed.

## Checks

The **Checks** tab shows the pull request open from whatever branch is
checked out, if there is one — its status checks, summarised as passing,
failing or still running — and, regardless of whether there is a pull
request yet, the branch's own recent Actions runs, each linking to its own
page on GitHub.
