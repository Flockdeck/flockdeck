# Team-wide changes: protocol reference

This is the contract for the Changes view that shows every agent's checkout at
once: what changed, the diff, review comments, checks and the merge gate. It is
written before the server builds it, so that the interface and the server can be
built at the same time from one description. **Nothing here is served yet.**
`api/catalog.json` gives each command and message below the status `planned`
until the server builds it, and a command sent before then is answered with a
`changesResult` whose `code` is `unsupported`.

The shapes are defined in [`changes.schema.json`](../api/schema/changes.schema.json)
(JSON Schema, draft 2020-12). This page explains them and says what the
commands do. Hand-built examples of every message are in `api/fixtures/`
(`built-changes-*.json`); no server has written them, and a test validates each
against the schema. The conventions are those of the team graph reference
([`team-graph.md`](team-graph.md)): optional fields are absent and never null,
times are RFC 3339, and a pane is named by its id.

## 1. Using it

A client looks in the hello's `api.caps` for the names it needs and draws by
section when one is missing, so an older desktop gives the single-pane view of
today and not an error.

| Cap | Gives |
| --- | --- |
| `changesTeam` | `changesWatch`, `changesUnwatch`, `changesSnapshot` and `filesPage`: the team-wide list of branches and their files. |
| `changesDiff` | `diffHunks` and `diffContext`: a file's diff as hunks. |
| `changesComments` | `commentAdd`, `commentResolve`, `commentReopen` and the `comments` of each branch. |
| `changesChecks` | `runChecks`, `requestReview`, `submitReview` and the `checks` and `reviews` of each branch. Without it both are empty. |
| `changesGate` | `approveMerge`, `unapproveMerge`, `mergeBranch` and the `gate` of each branch. Without it `gate` is absent. |

A cap is added to `api.caps` in the change that builds what it names.
`changesTeam` came with API 1.5. A window
reached through the relay is sent none of the five (section 9).

## 2. Commands

All of these are `accessFull`: a window on the machine Flockdeck runs on, or a
full device. They are refused to a window through the relay, whatever its role,
with a `changesResult` whose `code` is `deskOnly` and a notice.

| Command | What it does |
| --- | --- |
| `changesWatch` | Answers with a `changesTeam` now and again whenever it changes. One project at a time, like `graphWatch`: watching another replaces the first. |
| `changesUnwatch` | Stops the pushes. |
| `changesSnapshot` | Answers once with a `changesTeam`. |
| `filesPage` | Answers with a `changesFiles`: a page of one branch's files, for a branch with more than a push carries. |
| `diffHunks` | Answers with a `diffHunks`: one file's diff as hunks (section 4). |
| `diffContext` | Answers with a `diffContext`: the unchanged lines between two hunks. |
| `commentAdd` | Adds a comment and sends it to the file's owner (section 5). Answers with a `changesResult` holding the `comment`. |
| `commentResolve`, `commentReopen` | Close and reopen a comment. Answer with a `changesResult` holding the `comment`. |
| `runChecks` | Runs the project's local checks for a branch. Answers at once with a `changesResult`; results arrive in the next `changesTeam`. |
| `requestReview` | Asks a pane to review a branch: records a `requested` review and sends the pane a message. |
| `submitReview` | Records the person's own sign-off (`approved`, `changes` or `dismissed`) on a branch. Taken from a window page only (section 6). |
| `approveMerge` | Approves a branch's current head (section 7). |
| `unapproveMerge` | Withdraws an approval. |
| `mergeBranch` | Merges an approved branch into its base (section 7). |

A refused command is a `changesResult` with `ok` false, a `code` and an `error`.
Which command answers with which code:

| Code | Commands |
| --- | --- |
| `deskOnly`, `unsupported` | All of them (section 9; a build without the cap). |
| `badRequest` | Any with a missing, too long or out of range field; `diffHunks`, `diffContext` and `commentAdd` for a `file` that is not one of the branch's files or leaves the checkout, and `commentAdd` for a line the diff does not have. |
| `noBranch` | Any with an `id` that is not a pane with a branch, or a `branch` that differs. |
| `notFound` | `commentResolve`, `commentReopen` (no such comment); `requestReview` (no such reviewer, or not an agent pane); `runChecks` (no check file). |
| `headMoved` | `commentAdd`, `submitReview`, `approveMerge`, `mergeBranch` (`expectSha` is not the head). |
| `checksNotPassing`, `conflicts`, `openComments`, `reviewChangesRequested`, `unpushed`, `empty` | `approveMerge`, in that order. |
| `notApproved` | `mergeBranch`, when the gate is not approved for that head. |
| `busy` | `changesSnapshot`, `filesPage`, `diffHunks` and `diffContext`, when the same window still has an earlier request of that kind being answered. One of each kind runs at a time for a window. `runChecks` is also `busy` while the branch's checks are running. |
| `notClean`, `notFastForward`, `failed` | `mergeBranch`. |

The fields these commands use, besides `cmd`:

| Field | Used by | Meaning |
| --- | --- | --- |
| `req` | all | A token of the window's choosing, copied into every answer, as for the graph commands. |
| `root` | `changesWatch`, `changesSnapshot` | The project's directory. Empty means the project on screen. |
| `id` | all but the three watch commands | The pane whose branch is meant. Required. |
| `branch` | `approveMerge`, `unapproveMerge`, `mergeBranch`, `runChecks` | The branch name the window believes the pane is on. When it is sent and differs, the answer is `noBranch`. |
| `file` | `diffHunks`, `diffContext`, `commentAdd` | A path relative to the repository root. It must be one of the branch's listed files (section 4). |
| `against` | `diffHunks`, `diffContext` | `worktree` (uncommitted changes, HEAD to the working tree), `head` (committed changes, `baseSha` to `headSha`) or `merge-base` (everything on the branch, `baseSha` to the working tree). Default `merge-base`. |
| `context` | `diffHunks` | Lines of context around a change: 0 to 50, default 3, or `-1` for the whole file. |
| `ignoreWhitespace` | `diffHunks` | True to ignore whitespace-only changes. |
| `side` | `diffContext`, `commentAdd` | `old` or `new`. |
| `from`, `to` | `diffContext` | The first and last line wanted on that side, from 1. At most 200 lines are returned. |
| `line` | `commentAdd` | The line on that side. 0 is a comment on the whole file. |
| `text` | `commentAdd`, `requestReview` | The comment (1 to 4096 characters), or an optional note to the reviewer. |
| `commentId` | `commentResolve`, `commentReopen` | The comment's id. |
| `names` | `runChecks` | The checks to run. Empty means all of them. |
| `reviewer` | `requestReview` | The pane to ask. |
| `state`, `note` | `submitReview` | `approved`, `changes` or `dismissed`, and a note of up to 2000 characters. |
| `expectSha` | `approveMerge`, `mergeBranch`, `commentAdd`, `submitReview` | The head the window looked at. Required. When it is not the current head the answer is `headMoved` and nothing is recorded, so a comment or sign-off is never attached to code the person did not see (sections 5 and 7). |
| `offset`, `limit` | `filesPage` | The page: `limit` defaults to 200 and is at most 500. |

## 3. The branch list

`changesTeam` holds one entry in `branches` for each agent pane of the project
whose directory is in a git repository, in the order of the team graph. A pane
whose checkout cannot be read has an entry with only `pane`, `cwd` and `error`,
and `partial` says how many were read, so a window can show "reading 4 of 5"
and still draw the four. `rev` goes up with each push of a watch so a window can
skip one it has seen. A push is sent when something it holds changes, at most
once a second.

Each branch is one pane's checkout:

- `branch`, `base`, `baseSha` and `headSha`. `base` is the branch merges go into.
  `baseSha` is the merge base, and the committed changes of the branch are
  `baseSha..headSha`. A detached head has `detached` and no `branch`.
- `upstream`, `ahead` and `behind`. These count against the upstream, or against
  `base` when there is none. `uncommitted` and `untracked` count files.
- `lastCommit`, `taskId` (from the task board) and `pr` (from the GitHub
  integration, when it is on).
- `files`, `omitted`, `checks`, `reviews`, `comments` and `gate`.

Each file carries `path`, `status` (`A`, `M`, `D`, `R`, `C` or `U`), `added`
and `removed`, and, where they apply: `oldPath`, `similarity` (renames and
copies), `binary` with `sizeBefore` and `sizeAfter`, `generated`, `modeChange`,
`staged`, `owner` and `conflictWith`.

- `files` holds the committed changes since `baseSha` and the uncommitted ones
  together; a file in both is one entry. Most changed first (lines added plus
  removed, then path). At most 2000 are listed and `omitted` counts the rest;
  `filesPage` reads the whole list in order, from the watch's last read when the
  window is watching, so its pages agree with the totals it was pushed.
- A checkout whose changes are too many to list (git's listing is past 16 MiB)
  is an entry with the `error` "too many changes to list". Line counts of untracked
  files are kept between polls and read again only when a file's size or time
  changed; past 64 MiB read in one poll, the rest are listed with no count.
- `generated` is decided by path: lockfiles (`go.sum`, `package-lock.json`,
  `yarn.lock`, `pnpm-lock.yaml`, `Cargo.lock`), directories named `vendor` or
  `node_modules`, and names ending `.min.js`, `.min.css` or `.pb.go`.
- `owner` is the pane that last changed the file on the branch. When the branch
  has one pane it is that pane.
- `conflictWith` lists the other branches that change the same path (the pane
  and its path, which differs when one side renamed it). The graph's overlap
  edges name the pair of panes; this is the list of paths they stand for.

## 4. Diffs

`diffHunks` answers with the file's diff as structured hunks, where `diff`
answers with one block of text. `diff` is unchanged, and a window that does not
find `changesDiff` in the caps keeps using it.

- A hunk has `oldStart`, `oldLines`, `newStart`, `newLines` and the `header` git
  puts after the range. Its `lines` are `ctx`, `add` or `del`, with `oldNo` (ctx
  and del) and `newNo` (ctx and add), from 1.
- `intra` on an `add` or `del` line gives the word-level changes inside it as
  `[start, end)` pairs, counted in Unicode code points of `text`, and is written
  only for a line that pairs with one on the other side. A window may work them
  out itself from adjacent `del` and `add` lines; the server's are the same ones
  for every window.
- `totalLines` counts the whole diff. When the diff is cut, `hunks` holds the
  first lines and `truncated` says how many lines and bytes it kept. The limits
  are 5000 lines and 1 MiB.
- `headSha` is the commit the branch's HEAD was on when the diff was made, and
  `worktree` is true when the new side is the working tree and the file differs
  there from that commit, so the diff holds uncommitted changes. A comment written
  on this diff takes `headSha` as its `atSha`; when `worktree` is set its new-side
  line numbers are the working tree's, which the head may not have. `worktree` is
  never set for `against` `head`.
- A binary file has `binary`, `sizeBefore`, `sizeAfter` and no hunks. A rename
  has `oldPath`. `eolChange` means the line endings changed; if that is the only
  change there are no hunks.
- `diffContext` returns the unchanged lines between two hunks, or after the last
  one, on one side: at most 200 at a time, and `eof` says the file ended. Each
  line carries the number of the side asked for (`oldNo` for `old`, `newNo` for
  `new`).
- A diff whose text git writes past 16 MiB is an `error`, "the diff is too
  large to read". Past 5000 lines or 1 MiB of text it is cut as above.
- An `error` replaces everything but `type`, `req`, `pane` and `path`.
- The path is checked before git or the disk is touched. It must be one of the
  branch's listed files (a renamed file by either of its paths), is cleaned, and
  is refused as `badRequest` if it is absolute, has a `..` element or a drive,
  or would leave the checkout. A file that is a symbolic link is read as the
  link (its target text), never followed, so a link pointing outside the
  checkout shows the path it points to and nothing from there. `diffContext`
  reads the same way.
- A read of the working tree is refused as `badRequest` when a directory above
  the file is a symbolic link or a junction, even one that stays inside the
  checkout (a link to `.git`, say). Limits of that check:
  - git, not Flockdeck, reads the working tree for `diffHunks`. The check is made
    before git runs and again after, and the output is dropped if a directory was
    swapped in between. A process that can write inside the checkout can still
    swap one while git runs and not be noticed if it swaps it back.
  - A Windows reparse point that is not a symbolic link or junction, such as a
    OneDrive placeholder, is refused too, with the same message, which is wrong
    for that case.
  - A hard link to a file outside the checkout reads as a regular file of the
    checkout. A hard link cannot be told from the file itself.

## 5. Comments

A comment points at a line of one side of a file at one head (`atSha`), so it
stays attached to the line it was written about when the branch moves on. The
server does not move it; a window can mark a comment whose `atSha` is not the
branch's `headSha` as outdated.

`commentAdd` finds the owner of the file (`owner`, else the branch's pane),
writes the comment into that pane's inbox as an ordinary message from the
person, and records it. The message names the branch, the file and the line, quotes the
comment and names its author. `to` is that pane. The owner sees it as it would
any message. It carries the person's tag only when the command came from a
window; a comment or sign-off an agent adds through another path is attributed to
that pane and never to the person. `commentAdd` needs `expectSha` (the head the
person looked at) and answers `headMoved` if the branch has moved on. An unknown
`commentId` is `notFound`.

- `delivery` reuses the graph's message states, in the same order: `queued`,
  `notified`, `woken`, `read`. It is absent while the message is being written,
  which a window shows as sending. Delivered is any of `queued`, `notified` and
  `woken`; read is `read`.
- `state` is `open`, `fixed`, `resolved` or `dismissed`. `fixed` is set when the
  owner says so (a later commit on the branch that the owner reports); only the
  person sets `resolved`, with `commentResolve`. `commentReopen` makes a comment
  open again. Open comments block the merge.
- A comment is at most 4096 characters, a branch keeps at most 500, and a path
  must be one of the branch's files. A comment on a line a diff does not have is
  refused as `badRequest`.
- Comments are kept with the project's team data and survive a restart.

## 6. Checks and reviews

**Checks.** The person writes the project's local checks by hand in a file in
Flockdeck's config directory, `changes/<project name>-<8 hex>.json`, as
`{ "checks": [{ "name": "...", "cmd": "..." }] }` (at most 20 checks, a name of
60 characters, a command of 1000). A command never comes from a window, from the
repository or from an agent: the team file, the task board and the branch are
all written by agents and are not read for commands. A `runChecks` that finds no
such file is `notFound`, and its error gives the path to write.

`runChecks` runs the checks, or those in `names`, one at a time in the branch's
checkout through the platform's shell, and answers at once. The results are in
the branch's `checks` with `source` `local`, in the pushes that follow. Each has
the `atSha` it ran against. A commit makes the old results out of date: the
server reports the check as `queued` for the new head, and never as `pass` for a
head it did not run on. Results are kept in memory, so a restart forgets them. A
second `runChecks` for a branch whose checks are still running is `busy`.

A check runs with the person's rights. It runs in the agent's checkout, so
scripts and test code the agent wrote run exactly as if the person had run them.
Flockdeck limits the damage and does not sandbox it:

- The environment holds only the variables a build needs to find its tools and
  a place to write (`PATH`, the system and temp folders, the home folder, the
  language), and nothing else, so no token of Flockdeck's or the person's
  session is passed to it.
- A check is stopped after 10 minutes, and the command and what it started are
  ended as far as Flockdeck's process tree reaches. On Unix that is the command's
  session: a daemon that leaves it with `setsid` survives, and on macOS only the
  process group is signalled. On Windows it is the job object, which ends console
  programs only; a program with a window of its own is left running.
- Windows does not run a `go.bat` or `npm.cmd` of the checkout ahead of the one
  on `PATH` (`NoDefaultCurrentDirectoryInExePath` is set).
- At most 64 KiB of its output is read. The first line of the check's own output
  with something in it is the only text kept, as `summary` (at most 200
  characters, with control and formatting characters removed). Flockdeck does not
  echo anything else.

A tree with uncommitted or untracked files is still checked, as it is. The result
has `worktree` true, the same marker a diff carries, and its summary says it ran
on uncommitted files. A pass on a dirty tree is not a pass for the head commit:
the gate (section 7) does not count it toward `approveMerge`. The head and the
tree are looked at again when a check ends: a result for a head that has moved
is reported `queued`, and one that saw a tree that became dirty is marked
`worktree`. That is an honesty check and not a boundary; a process that wants to
deceive can put the files back before the second look.

Who may start a check. `runChecks`, `requestReview` and `submitReview` are
taken only from a connection that opened as a page of the window on this machine
(with an `Origin`), as `approveBaton` is, and refused as `deskOnly` otherwise.
That raises the bar and does not close it: an agent's pane token is accepted by
the same socket, and an agent can set an `Origin`. Until the window has a
credential an agent cannot get, an agent on this machine can run the person's
checks, and a recorded review is not proof that the person made it. The merge
commands (`approveMerge`, `mergeBranch`, section 7) must not ship before that
credential exists. The check file is in the config directory, so an agent that
can write there can plant commands in it; the person's file is not pinned yet.

A CI result has `source` `ci` and a `url`; a result an agent reports has `source`
`agent`. Neither is built in this version.

**Reviews.** `requestReview` records a `requested` review for the reviewer pane
and puts a message in its inbox. The message comes from Flockdeck, not from the
person, and says that the person asked the pane to review the branch at the
head. It states that the request does not approve or merge anything, and it
quotes any `text` the window sent as a note and not as an instruction. It is
never typed into the pane. A reviewer that is missing or is not an agent pane of
the project is `notFound`.

`submitReview` records the person's own sign-off, `approved`, `changes` or
`dismissed`, with a note. It is taken from a window page on this machine only, with the limit
described above: an agent that sets an `Origin` can send it too. It needs `expectSha` and answers
`headMoved` on a mismatch, and a review replaces the earlier one by the same
reviewer. The branch's pane is told in a message from Flockdeck what was
recorded.

The reviews are kept in `changes/<project name>-<8 hex>.state.json`, which only
Flockdeck writes. It is written to a temporary file and renamed into place, and
it is cleaned when read: entries over the limits, with an unknown state or
without a valid sha are dropped, and fields it does not know are not kept. A file
that is there and cannot be read is not written over: it is kept as
`<name>.state.json.bad`, the commands that record a review answer with an error,
and the branches show no reviews until the person has dealt with it. A missing
file is an empty state. When the state is full (200 reviews), requests and
dismissals, and the reviews of branches that no longer exist, are dropped
first, oldest first; an `approved` or `changes` review of a branch that exists
is never dropped, and a new review is refused instead. A
review has the `atSha` it was given at, and `stale` is set by the server once
the head has moved: a stale approval does not count.

## 7. The gate and the merge

`gate.state` is `blocked` while `blockers` is not empty, `ready` when nothing
blocks and no one has approved, `approved` once the person approved the current
head, and `merged` once the branch is in its base. The blockers, by `kind`:

| Kind | Blocks when |
| --- | --- |
| `checks` | A check is failing, or running, queued or missing for the head. A pass with `worktree` true ran on uncommitted files and counts as missing. |
| `conflict` | Another branch changes one of the same paths, or the branch does not merge into its base without a conflict. |
| `review` | A review that is not stale asks for changes. A missing review does not block. |
| `comments` | A comment is open. |
| `unpushed` | The head is not on the upstream. |
| `empty` | The branch has no commits of its own. |

`approveMerge` takes the pane, `expectSha` and optionally `branch`. It records an
approval of that head and tells the manager. It does not merge. It is refused,
and nothing is recorded, when:

- the pane or branch is unknown (`noBranch`);
- `expectSha` is not the current head (`headMoved`), so a person approves what they
  looked at and not something that arrived since;
- a check is not passing for the head (`checksNotPassing`);
- a conflict exists (`conflicts`);
- a comment is open (`openComments`);
- a current review asks for changes (`reviewChangesRequested`);
- the head is not on the upstream (`unpushed`);
- the branch has no commits of its own (`empty`).

These are checked in that order and the first is reported. The `changesResult`
carries the branch's `gate`. A later commit drops the approval, and so does any blocker that appears after it
(a check that fails, a comment, a conflict, a review asking for changes): the
gate goes back to `blocked` and `mergeBranch` refuses. `unapproveMerge`
withdraws one.

`mergeBranch` takes the pane and `expectSha`. It takes the repository's lock,
reads the head again, and refuses (`notApproved`, or `headMoved`) unless the gate
is `approved` for that head. It then merges `expectSha` itself (`git merge
<expectSha>`, never the branch name, which could have moved) into `base` in the
project's main checkout by fast-forward when it can and by a merge commit when it cannot.
It never forces, never touches a dirty checkout (`notClean`), never merges a
base that has moved in a way a fast-forward or a clean merge cannot take
(`notFastForward`), and records what it did in the activity log. After a merge
the branch's `gate.state` is `merged` and `mergeSha` is the new tip of the base.

## 8. Limits

| Thing | Limit |
| --- | --- |
| Branches in a push | 60 |
| Files listed for a branch | 2000, the rest through `filesPage` (200 a page by default, 500 at most) |
| Comments for a branch | 500, each 4096 characters |
| Diff | 5000 lines and 1 MiB, then `truncated` |
| Lines from one `diffContext` | 200 |
| A review's note | 2000 characters |
| Pushes | at most one a second for each watch |

## 9. Who is sent what

All of it is for the machine Flockdeck runs on. A window reached through the
relay is refused every command here, and `changesTeam`, `changesFiles`,
`diffHunks` and `diffContext` are never sent to one. The only `changesResult` it
is sent is that refusal: `code` is `deskOnly` or `unsupported` and `error` is the
fixed sentence for that code, whatever the desktop wrote; anything else, and an
answer that holds a comment or a gate, is dropped. Its hello does not
list the five caps. A viewer device is refused by
the access check before any command is read, with the notice every viewer gets and
no `changesResult`. The reason is what the data holds: branch names,
paths and file counts say what the project is and what an agent is doing, and a
comment or a diff is source text. A read-only form for a relay window, with paths
and counts and nothing else, is a decision to be made on its own; the default is
none.

## 10. What is not in this contract

- **Events for the activity feed** (committed, pushed, check failed, review
  submitted, comment delivered, merged). They are an addition to the feed's own
  messages and are specified when the feed carries them.
- **The command-line side** of reviews, checks and comments for agents:
  `flockdeck review`, a reported check result, and an agent marking a comment
  fixed. They go through the hook API, not the control socket.
- **Pull requests as the thing merged.** `mergeBranch` merges locally. Merging a
  pull request through GitHub is not part of it.

## 11. Examples

| File | Message |
| --- | --- |
| `built-changes-01-team-mixed.json` | A `changesTeam` of five branches: blocked by a conflict, a failing check and an open comment; ready; blocked by a running check, an unpushed commit and a review that asks for changes; a branch on `main`; and a checkout that could not be read. |
| `built-changes-02-team-empty.json` | A `changesTeam` with no branches. |
| `built-changes-03-files-page.json` | A page of files. |
| `built-changes-04-diff-hunks.json` | A `diffHunks` with two hunks and word-level marks. |
| `built-changes-05-diff-truncated.json` | A cut diff. |
| `built-changes-06-diff-binary.json` | A binary file. |
| `built-changes-07-diff-rename.json` | A renamed file. |
| `built-changes-08-diff-error.json` | A diff that could not be made. |
| `built-changes-09-diff-context.json` | Lines between hunks. |
| `built-changes-10-result-approved.json` to `built-changes-14-result-desk-only.json` | `changesResult`: an approval, a head that moved, a blocked approval, a comment added, a refusal to a relay window. |

## 12. Field reference

The fields of the smaller objects, for the ones the sections above do not walk through.

- `changesTeam`: `project` is the project name. `changesFiles` carries `total`, the number of files the branch has, beside the page it holds.
- `lastCommit`: `sha`, `subject` and `at` (time of the commit).
- `pr`: `number`, `title`, `mergeable` and `reviewDecision`, as GitHub reports them.
- `check`: `name`, `summary`, `startedAt` and `finishedAt`.
- `review`: `at` is when the sign-off was given.
- `comment`: `author` (the person or the pane that wrote it), `createdAt`, and `fixedAtSha`, the head at which the owner reported it fixed.
- `gate`: `approvedSha` is the head that was approved, `approvedBy` who approved it and `approvedAt` when.
- `line`: `t` is the line kind, `ctx`, `add` or `del`.
- `changesResult`: `op` is the command it answers, and `ok` is true when the command did what it said. When it is false, `code` and `error` say why.
