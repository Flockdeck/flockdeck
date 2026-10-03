# GitHub

The **GitHub** panel in the rail opens and reads pull requests and issues,
and shows CI status, for the project on screen. It all goes through the `gh`
command-line tool, without leaving Flockdeck.

## Getting connected

Setting it up happens in **Settings › GitHub**; until `gh` is installed and
signed in, the panel says which is missing and offers **Open GitHub
settings**. If `gh` is not installed, that section offers to install it with
whatever package manager this machine already has (`winget` on Windows,
`brew` on a Mac, `apt-get`, `dnf`, `pacman` or `apk` on Linux), and always a
link to install it by hand from [cli.github.com](https://cli.github.com).

Once it is installed, **Sign in with GitHub** walks through `gh auth
login`: a one-time code appears here, to enter at github.com/login/device.
Approve it there, in your browser, and this comes back signed in on its own, and there is nothing more to do in Flockdeck. **Sign out**, in the same section,
runs `gh auth logout` after asking, so it signs `gh` out everywhere on this
machine, not only in Flockdeck.

## Pull requests and issues

The **Pull requests** and **Issues** tabs list what is open on the
repository, filterable to closed, merged (pull requests) or all. **New
pull request** and **New issue** open a small form: a title, a
description, and for a pull request an optional base branch and a draft
toggle. The item opens here once gh has created it, ready to read or
comment on.

Opening an item shows its description and its comments, and a box to add
one of your own. **Open on GitHub** leaves for the real thing when that is
what is needed.

## Checks

The **Checks** tab shows the pull request open from whatever branch is
checked out, if there is one, with its status checks summarised as passing,
failing or pending. It also shows the branch's own recent Actions runs,
whether or not there is a pull request yet, each linking to its own
page on GitHub.
