# Flockdeck (version to be set)

## Batons: hand work from one agent to another
A baton is a short markdown document one agent hands to the next: what the work
is for, where it stands, the files that changed, the commands that were run and
what must hold. Any agent can be started from one, whichever agent wrote it.
Nothing makes a baton on its own. One exists only when you run Make baton or an
agent runs `flockdeck spawn -baton`.

To make one, run Make baton from the command palette with a pane focused. A
draft opens for you to edit. Files touched come from git (what the branch has
committed since it left its base, and what is uncommitted). Commands and tests
run come from the agent's stored conversation, with repeats collapsed and
failures kept. Where things stand quotes the agent's last message. Decisions,
open questions and constraints are left empty for you to fill in, and a section
left empty is not sent. Only Claude Code and Flockdeck's chat client keep a
conversation Flockdeck can read. For other agents the draft is built from git
and the task.

The footer of the draft has three choices. New pane starts an agent from the
baton in a tab of its own or beside this pane, in this checkout or in a new git
worktree on a branch you name. Restart this pane ends the pane's conversation and
starts a new one in the same checkout from the baton. Save only keeps it for
later. If the new agent belongs to a different company than this pane's, or
Flockdeck does not know whose it is, the dialog asks you to tick a box first,
because the baton is sent to it.

Agents can do the same from a terminal:

    flockdeck spawn -worktree fix-auth -baton self "Finish the auth middleware"

`-baton` takes `self`, a pane id, a baton id, or `path:` and the path of a notes
file (`.md`, `.markdown` or `.txt`). `flockdeck baton list` lists stored batons
and `flockdeck baton show [-path] <baton-id>` prints one or the file it is kept
in.

Two help pages cover this: Handing work to another agent, and Baton details,
which has the long lists (what the scrubber takes and misses, how an agent's
company is decided, and how batons are kept and removed).

### Secrets
Before a draft is shown, and again on the text you send, the scrubber replaces
what looks like a secret with a mark such as `[REDACTED: aws-key]`, and the
footer counts the marks. It is a filter and not a guarantee. It redacts when in
doubt, and leaves a value under a secret's name only when the value is plainly
not one, such as a placeholder, a mask of eight or more `*`, a type name, or a
call that reads an environment variable. That means it takes some text that is
not a secret, mostly in code. It also misses some secrets. See Known limits.

### Sending to another company
A baton for an agent of a different company than the one it came from, or of one
whose company Flockdeck cannot tell, is refused unless the command has
`-baton-send-elsewhere`. With the flag the agent only asks. A notice in a
Flockdeck window on this machine names the pane, the destination and where the
baton came from, and has a Send it button. Nothing starts until you press it. If
no such window is open, or nobody answers within 45 seconds, the baton is not
sent, and the spawn command waits that long on top of its usual minute. A window
reached through the relay is never asked.

The company is judged for the agent that will really run, in the folder it will
run in, and judged again where it runs once its worktree exists. If the agent or
its company changed in the meantime, nothing starts. A command-line agent counts
as unknown when a setting that applies to it points it at another host. For
`claude` that is a base URL, or the Bedrock, Vertex or Foundry switches, read from
the agent's own env, Flockdeck's environment and the `env` of Claude's
`settings.json` files (the user's, the managed one, and the project's and its
parents' up to the git root, with their `settings.local.json`). For `codex` it is `OPENAI_BASE_URL` and the like
and `config.toml`, which is read as TOML with the active provider resolved: the
profile that `profile` selects, then `model_provider`, `oss_provider`,
`openai_base_url` and the `base_url` of the active provider's table. A
`config.toml` that cannot be parsed makes the company unknown. For `gemini` it is
the Google base URL and Vertex settings.

This fails closed. A settings file that is there and cannot be opened, is over
1 MB or cannot be parsed makes the company unknown, and the notice names the
file. Claude's settings and managed settings are read with limits on size and on
file type: a link to a regular file is followed, but a pipe, a device or a link to
anything else is refused without being opened. Hosts are compared as plain ASCII,
so a lookalike such as `api.anthropİc.com` is never taken for the vendor's own.
For a new branch, the `.claude/settings.json` it will hold is read from the
commit the branch is cut from, and the checkout's working copy is read as well. If
either says another company, or cannot be read, the dialog asks. For a branch that
exists, what the branch itself commits is read. That includes a
`settings.local.json` the repository committed, which is the only way one reaches
a worktree, and a `.claude` committed as a link to a folder of the same commit,
which is followed through folders of that commit only. The link's target is read
exactly as written. A target with a `.` or `..` part, an empty part, a space at
either end, a control character, a backslash or a colon, an absolute one, one that
goes through another link, one that is not a folder of the commit, and a `.claude`
that is a submodule all make the company unknown. Names are matched in any case,
so `.Claude/Settings.json` is read.

The `env` key of a settings file is read by its exact spelling. A file that also
has `ENV` or `Env`, or has `env` twice, is unreadable, so the company is unknown,
because Claude Code may read either one. Variable names under `env` are matched
in any case.

The approval stops a mistake and a request nobody saw. It does not stop a
program running as you. Such a program can read the window's token from the
state directory, open the control connection with an Origin header it makes up,
receive the notice and press the button. The notice goes only to a connection
that opened with an Origin header, and each window gets its own one-use token.
The help page says the same.

When a spawn fails after it made a worktree and a branch, Flockdeck removes them.
It keeps the worktree, and says so, if it holds anything beyond the files git
checked out. Ignored files such as `.env`, `node_modules` or build output count,
and so do changed files, untracked files, empty folders and tracked files that git
is told not to look at, which `git status` does not report: any file marked
`--assume-unchanged`, and any file marked `--skip-worktree` unless the repository
uses a sparse checkout, where that mark is how files are left out. The branch is
deleted only if it was made by that request and is still at the commit it was made
at.

### Notes files and drives
A notes file is read only from a local drive, and only from inside the project or
checkout of the pane that asked. A pane id an agent names has to be a pane of its own
project. Symbolic links, pipes, UNC and
device paths, and reparse points (a cloud placeholder that may not be on the
disk) are refused. On Windows a mapped network drive and a `subst` drive are
refused too, after the drive's type is asked of the system, and on Linux so are
NFS, SMB, CIFS, AFS, Ceph, Coda and 9P. FUSE and virtiofs are not checked, and on
macOS only the name and the links are.

### Size and time limits
What a baton is built from is cut to its own maximum before it is scrubbed. A text
of up to 1 MB is scrubbed whole, so a key wrapped over several lines is seen in one
piece, and a text over 1 MB has its middle clipped. The whole build has a budget of
5 seconds, looked at before each text is begun. A text not begun when the budget is
spent is replaced by `[REDACTED: too-large-to-scrub]` and is never passed on. A
hostile or very large conversation therefore costs a bounded amount of time and
loses text, not secrets. A text already being scrubbed is not cut off, and one made
to be slow can take several seconds at 1 MB.

### Storage and pruning
Batons are kept in Flockdeck's state directory on your machine and are not
changed once saved. When a baton is saved, batons not made, shown or used for 30
days are removed, with the overflow files written for them: one at a time when
there are fewer than four, and otherwise at most half in one run, oldest first.
Nothing is removed when the clock looks wrong (earlier than at the last run, set
while Flockdeck ran, or a baton dated in the future), when the state folder
cannot be listed, or for 30 days after a saved layout was first found unreadable.
A baton stays if a saved layout names it or a pane was started from it in the
last hour, and a baton no layout names that looks more than five years old is
never removed. A record that is there and damaged (`sources.json`, the overflow
record, the record of what was used) is kept aside as `<name>.bad`, then
`<name>.bad.1` and so on up to `.bad.99`, and started again empty. Nothing is
written over an earlier copy, and with all 100 taken no more are kept, which is
logged once. Nothing is pruned in that run or for a day after, since batons used a
moment ago may look unused. A record that cannot be read at the moment (another
process holds it, or access is refused) is not treated as damaged: it is tried
again a moment later, and if it still cannot be read nothing is moved and nothing
is pruned. That lasts 30 days from the first time it was seen so, said once a day
in the log, and then pruning goes on with the record taken as empty. A baton used
in that time is not lost, because a use is also written on the baton's own file
time, which the age rule reads first. The rules are in the Baton details page.

## Conflict radar
When two panes in the same repository have changed the same files, the conflict
radar asks git whether their work would merge. If git says it would not, the
header of each pane shows a chip naming the other pane. Hover it for a summary.
Press it, or tab to it and press Enter, for a list of each other pane, its branch
and the files git stops on (up to five panes and twenty files each). Escape or a
press elsewhere puts it away.

The radar is off by default. Turn it on in Settings > General > Conflict radar. It
needs git 2.38 or newer, and on an older git the switch is dimmed and says so.

It copies each checkout (committed, uncommitted and untracked files) into a
throwaway object store in the system's temporary folder and has git merge the
copies in memory with `git merge-tree`. Nothing in a checkout is changed. A chip
appears after the same pair has conflicted on two refreshes at least five seconds
apart. The check runs with the git refresh, about every 15 seconds, for the panes
on screen.

The copy costs git processes: 11 for a pane with uncommitted or new files, 4 for a
pane with only commits, and 14 when the checkout's index cannot be copied and is
rebuilt. Choosing a base again and checking a shallow clone add a few, and each
pair of panes that share a path adds a merge. A pane with uncommitted or new files
is copied on every refresh, which is noticeable on a very large checkout. A
test counts them, so these numbers cannot drift from the code.

The radar skips a checkout that ran long. A refresh counts as a strike against a
checkout whose git commands ran past 20 seconds, or whose own copy had been
running for at least 15 seconds when the radar's 30 seconds ran out (usually a
very large untracked file such as a video). A checkout queued behind others, or
one that had only just begun, is not held to account. After three strikes in a
row, a quiet checkout that is always slow is skipped for 10, 20, 40, 80 and then
at most 120 minutes. One whose commit or counts of changed and new files keep
changing is tried again at every refresh and costs up to the radar's 30 seconds
each time: the skip ends with the change, but its strikes are kept and raise the
step, which matters only while it stays quiet. Strikes are forgotten after a
snapshot that works, when the radar is turned off and on, and after a day without
one. The log says which checkout it was and what was running. Nothing in the
window shows a skipped checkout.

The radar runs the header's index refresh as part of its own refresh. A checkout
never has more than two index refresh processes alive, however often one is asked
for.

Work is measured from the nearest of the candidate base branches that shares
history with the pane: the one with the fewest commits between where the two
meet and the pane's HEAD. The candidates are the branch the main worktree has
checked out, a bare repository's HEAD, origin's default branch as last recorded,
and main and master. A branch with no history in common with the pane is never
chosen, so a stale or unrelated default branch is passed over for a better one.

In a partial clone, git 2.44 and newer are told not to fetch missing objects.
Before git 2.44 the radar does not run in a partial clone, because it could
contact the remote. A submodule moved to another commit counts as a changed path,
so two panes that move it differently are compared.

Pane names, branches and file paths go to every open window, including one
reached through the relay. File contents are not sent.

## Recording redaction
The scrubber that recordings and exports use is now built from spans, which
batons also use. It has a new Kinds function that lists the kinds it can name.
The text it redacts changes in three ways:

- Where two patterns match overlapping text, the whole stretch is one
  `[redacted]` and not several.
- The word `bearer` or `basic` is skipped only under an `authorization` or
  `proxy-authorization` name. Under any other name (`password=basic`) it is a
  value like any other and is redacted. `Authorization: Bearer x` still keeps the
  scheme and redacts the credential.
- SendGrid keys (`SG.x.y`) are redacted by shape.

`docs/recording-format.md` lists them. Nothing else about the recording output
changes.

## Fixes
`git diff` and `git update-index --refresh` rewrite a checkout's index when a
file's recorded stat data is stale. On Windows a `git status` that opened the
index at that moment failed with "index file open failed: Permission denied",
which showed as a failed read of the changed files in the Changes window. It was
rare. Each checkout now has a gate. The commands that read the changed files
never wait behind a refresh that is only waiting its turn, wait at most half a
second for one that is running, and are asked again up to three times if they are
refused the index anyway. The index refresh waits at most two seconds for them
and does nothing if they do not let go, if an `index.lock` is there (the refresh is
skipped), if an earlier refresh of the checkout is still running, or while the
checkout is backing off. It backs off, for 10 minutes, then an hour, then six
hours, only after a refresh that ran and failed or ran for more than ten minutes.
A refresh that never started, that nobody was waiting for any more, or that exited
because a lock was there (a collision with somebody's git) counts for nothing.

The index refresh never kills git and never removes a lock. A git killed while it
writes the index is how a lock is left behind, and a lock found afterwards cannot
be known to be that git's and not a commit's that began since. Flockdeck stops
waiting after 60 seconds, and git finishes and removes its own lock. The checkout
counts as having a refresh in flight while any of its processes is alive, so no
second one starts beside it. One that runs past ten minutes is said in the log, and
again every hour it still runs. If one has been in flight for more than an hour and
no `index.lock` is there, one more may start, and never more than two are alive at
once per checkout. A lock that has been there more than an hour is said in the
log, once an hour, and left alone.
`GIT_INDEX_FILE` is also taken out of the environment of the git commands
Flockdeck runs, since it would point them at an index that is not the checkout's.
None of this covers a git command run by another program, including an agent's
own.

## What this first version does not do
- A baton has no drafter. The sections that need a decision from a person are
  written by the person.
- There is no picker for starting several agents from one baton at once, and no
  row action in the history list to make a baton from a past conversation.
- A baton does not carry uncommitted changes. A new worktree starts from the
  branch, and the dialog warns when the pane has uncommitted files.
- The radar has no Tell both action, no suggested merge order, no Worktrees badge
  and no comparison with main.

## Known limits
The scrubber on a baton is a filter and not a guarantee. It misses a secret that
is short, is a word, or is not named as one, a password in forms it has no pattern
for (a heredoc body, a request body that is not under a secret-looking key, a
`passwd value` in prose), and a hex session id in a cookie. It keeps bare
32-character and 40-character hex strings and UUIDs, and removes hex strings of 64
characters and more. It also takes some text that is not a secret: long
identifiers and base64 in paths, and code such as `const tokenKey = "auth-token"`.
On source code the rate is high and deliberate. Of the lines that contain the letters `key`,
`pass`, `pw` or `pwd` anywhere, in any case (so `monkey` and `passport` count), the
scrubber changes 2.6% (157 of 5958) in the non-test Go source of net and crypto in
the Go 1.27 standard library, and 14.2% (119 of 839) in this repository's
`internal/webui/assets/app.js`. Where one of the four is a word of its own, it is
5.6% (110 of 1954) and 34.9% (117 of 335). A password with
brackets or dots in it looks like code, and under those names nothing is left alone
for that. Above 64 KB a text gets one pass and little second checking. Read a draft
before you send it.

The record of where a baton came from is
`sources.json`, which any process of yours can change, so it stops mistakes and
not an agent that means to send a baton on. Settings that a macOS configuration
profile delivers are not read when the company of a Claude Code agent is decided,
and the paths of Claude Code's managed settings are from its documentation and
were not checked on a managed machine.

A radar chip is a prediction for one pair of panes from the last time both were
read, and no chip is not a promise. The radar skips panes that are clean, detached
or in the middle of a rebase, merge, cherry-pick, revert, am or bisect, panes with
unmerged files, panes before their first commit and panes with more than a
thousand changed files. Where a submodule is not checked out in the panes'
folders, git calls any two different moves a conflict even when one commit
descends from the other. Where it is checked out in both, such a pair gives no
chip. A shallow clone whose history does not reach the base is not checked. A pane
that shares history with none of the candidate bases is not checked either, and a
repository whose real base is none of them is measured from the wrong one. On
Windows a pane whose only change is a file with a path over 260 characters looks
clean unless `core.longpaths` is set in the repository. A clean filter such as Git
LFS, or a merge driver a `.gitattributes` names, runs on the copies and has not
been checked. A merge that is clean can still fail to build, and the radar does
not say so.
