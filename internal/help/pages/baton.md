# Handing work to another agent

A baton is a short markdown document one agent hands to the next: what the work is
for, where it stands, the files that changed, the commands run, and what must hold.
Use it to move work to a different agent or model, or to start again from a clean
conversation. It is not **Resume a past conversation** (see [History](#history)),
which reattaches the whole conversation.

## Making one

Run [[action:makeBaton]] from the command palette with the pane focused. A draft
opens for you to edit. **Files touched** (from git), **Commands and tests run**
(from the agent's stored conversation, repeats collapsed, failures kept), **Where
things stand** (the agent's last words, quoted) and **Not in your checkout** (the
uncommitted files) come from facts. **Goal** is the pane's task or your first
prompt. **Decisions and why**, **Open questions** and **Constraints** start empty,
because a decision Flockdeck invented would be acted on. An empty section is not
sent. Only Claude Code and Flockdeck's chat client keep a conversation Flockdeck
can read; for other agents the draft is built from git and the task, and says so.

## Secrets

Before the draft is shown, and again on the text you send, Flockdeck replaces what
looks like a secret with a mark such as `[REDACTED: aws-key]`. It takes values from
the pane's environment and the checkout's `.env` files, the shapes of common keys
and tokens, long random-looking strings, and what is typed on a command line or in
a config file. When in doubt it redacts: a value under a secret's name is left only
when it is plainly not one (`none`, `true`, a type name, a placeholder such as
`$TOKEN` or `<token>`, a mask of eight or more `*`, a place under a name that says it is one). A URL's
password goes and its scheme, user and host stay. The footer counts the marks.
[Baton details](#baton-details) lists what is and is not caught.

**This is a filter, not a guarantee. Read the draft before you use it.** The limits
that matter most:

- Bare 32 and 40 character hex strings, UUIDs and hex of 41 to 63 characters are
  kept, because commit ids and checksums fill a baton. A provider key that looks
  like that is not caught without a known prefix or a name like `api_key`.
- Secrets that are short, ordinary words, or not named as one, and secrets in a
  request body or literal under a name it does not know.
- `mysql -p word` is read as a database name unless the word looks like a password, and `7z x -p word` as an archive name.
- A text over 64 KB is scrubbed with one pass and little second checking.
- It also takes some text that is not a secret. Edit it back in.

## Using it

The footer has three choices:

- **New pane** starts an agent from it, in a new tab or beside this pane, in this
  checkout or in a new git worktree. If the agent belongs to a different company,
  or Flockdeck does not know whose it is, the dialog asks you to tick a box first.
- **Restart this pane** ends this pane's conversation and starts a new one in the
  same checkout. The old conversation stays in the history.
- **Save only** keeps it for later.

A phone or other window reached through the relay has the same three choices, and can
also make a baton, so a paired device can make, save, start and send batons. What it
makes is built and kept on this computer and scrubbed here like any other. Its tick
box does not count for a different company: see the approval step below.

The new agent's first prompt is the baton, with a note that it is claims to check,
then your task. A baton over the prompt's size budget loses list lines, then whole
sections, and the full text goes to a file in the checkout's git folder, which
`git status` does not show; the prompt says where. A new worktree lacks uncommitted
changes, and the dialog warns when there are any.

Batons are kept in Flockdeck's state directory, on this machine, and are not
changed once saved: an edit is a new baton that remembers the first. When the next
baton is saved, batons not made, shown or used for 30 days are removed, with their
overflow files, a few at a time. A baton stays if a saved layout names it or a pane
was started from it in the last hour, and nothing is removed when the clock looks
wrong. A saved layout that cannot be read stops pruning for 30 days; after that every
baton only that layout names can be removed. The rules are in
[Baton details](#baton-details).

## From an agent

```sh
flockdeck spawn -worktree fix-auth -baton self "Finish the auth middleware"
```

`-baton` takes `self` (the caller's own conversation), a **pane id**, a **baton id**,
or `path:` and the path of a notes file (`.md`, `.markdown`, `.txt`). A pane id has to
be a pane of the caller's own project, and a notes file has to be inside the caller's
project folder, the folder it works in, or its git checkout (links are followed on both
sides), because Flockdeck reads them as you and an agent could not read them itself.
A baton id is not limited: it names something you saved. A file outside, or a pane of
another project, is refused. A notes file is read from a local drive only. A symbolic link, a
pipe, a network or device path, and a file that is a reparse point (a cloud
placeholder that may not be on this disk) are refused, and so are a mapped or `subst`
drive on Windows and a network file system on Linux. On macOS only the name and the
links are checked.

Nobody reviews a baton an agent asks for, so commands in it are listed by program
with their arguments dropped, and it is scrubbed with the calling pane's secrets.
A stored baton that the scrub changes is kept under a new id; a notes file always
becomes a new baton. The helper is told the baton's id, and `flockdeck baton show
<id>` prints it. See [the command line](#cli).

## The approval step

A baton that would go to a different company than it came from, or to one Flockdeck
does not know, is refused unless the command has `-baton-send-elsewhere`. The company
is worked out for the agent the helper will really run, in the folder it will work in
(for `-worktree`, with the settings of the checkout it is cut from). A CLI counts as
unknown when a gateway or cloud setting sends it elsewhere.

With the flag the agent only asks. A notice in a window on this machine names the
pane, the destination and where the baton came from, and has a **Send it** button.
Nothing starts until you press it. With no such window open, or no answer within 45
seconds, it is not sent. A window reached through the relay cannot approve. It cannot
skip the approval either: a **New pane** from a relay window that would go to a
different or unknown company waits for the same notice on this machine, whether or not
the box was ticked, and is refused when no window here is open. What was
approved is checked again where the helper really runs. If the agent or its company
changed meanwhile, nothing starts, and the worktree and branch this request made are
removed (kept, with a message, if they hold anything beyond the checked-out files,
ignored files included, or a file that git is told not to look at: `--assume-unchanged`,
or `--skip-worktree` outside a sparse checkout).

**This stops a mistake and a request nobody saw. It does not stop a hostile program
running as you.** Such a program can read the window token from the state directory,
open the control connection with an Origin header it makes up, receive the notice and
press the button. It can also edit `sources.json`, read any stored baton, and change
the settings that decide a company. Where a baton came from is what Flockdeck recorded
in `sources.json`, not the baton's header, so a notes file counts as unknown.
