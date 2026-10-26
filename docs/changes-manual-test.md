# Changes view: manual test script

The tests in `ui/test` draw the Changes view with a fake socket. This script is for the parts they cannot show: the real desktop app, a real repository and a real merge. It takes about 30 minutes. The commands are the server's, described in [changes-api.md](changes-api.md) sections 5 to 7.

Run it against a scratch repository, never a real one: step 9 merges into the base branch, and step 11 writes a check file into Flockdeck's config directory, which you delete at the end.

## Set up

Needs git 2.38 or later (older git cannot tell the gate about conflicts) and a build with the merge gate (API 1.10). Steps 10 and 11 need API 1.11 (the `partlyUpdated` and `mergedNotRecorded` codes, PR #676).

```
git init --bare scratch-remote.git
git clone scratch-remote.git scratch
cd scratch
git checkout -b main
printf 'one\ntwo\nthree\nfour\nfive\n' > notes.txt
git add notes.txt && git commit -m "notes" && git push -u origin main
git worktree add ../scratch-a -b agent/a
cd ../scratch-a
git push -u origin agent/a
```

Open the folder `scratch-a` as a project in Flockdeck and start one agent pane in it (any agent; it does not need to do anything, you make the commits yourself in a terminal). Open the Changes view. `main` stays checked out in `scratch`, which is where the merge will happen.

Expect: one row, `agent/a`, with no files and no gate blockers except `empty` ("no commits of its own").

## Comments

1. In `scratch-a`, change line 2 of `notes.txt` to `TWO`, commit and push (`git commit -am two && git push`). Pick the branch and the file.
   Expect: a diff with a "+" beside each line, and a "Comment on this file" button.
2. Press "+" on the added line. Type a comment, press Ctrl+Enter.
   Expect: the box shows "Sending...", then the comment appears under the line with "You to <agent name>", then "Delivered" (or "Read" once the agent reads it) and "Open". The agent's pane gets a message that names the branch, the file and the line. The file row and the gate show "1 open comment", and the gate is blocked.
3. Press Resolve.
   Expect: the comment says "Resolved" and offers Reopen; the gate row says no open comments and the blocker goes. Press Reopen: it is open and the gate is blocked again. Resolve it once more.
4. Press "+" on a line, type text, then commit another change in `scratch-a` before you press Send.
   Expect: the box closes with a note that the branch changed, and the text is still there when you open the same "+" again. Sending it against the new head works. Existing comments now say "on an earlier commit" (a warning chip with "the line may have changed" while they are open).
5. In `scratch-a` edit line 4 of `notes.txt` and do not commit. Look at the diff.
   Expect: a note that the file has uncommitted changes. Lines on the new side have no "+"; old-side lines and "Comment on this file" still do. Commit and push; the "+" returns.

## Approve and withdraw

6. Resolve every comment. The gate should read "Ready".
   Expect: "Approve merge" is on. Press it.
   Expect: a question that names `agent/a`, the first 8 characters of the head, and `main`, and says it does not merge. The keyboard is on Cancel. Escape closes it and sends nothing. Open it again and press "Confirm and approve".
   Expect: "Approved. The manager has been told.", the gate says Approved with your name and the short head, and the buttons are "Merge into main" and "Withdraw approval".
7. Press Withdraw approval.
   Expect: the gate goes back to Ready and the keyboard is on "Approve merge". Approve again.
8. In `scratch-a`, commit and push a new change while the gate is approved.
   Expect: the gate drops to blocked or ready for the new head; "Merge into main" is gone or off with "the approval is for an earlier commit". Approve the new head again. Then add a new comment on the branch from the window (step 2 again) and check the gate blocks and any open question closes with "the question was closed".

## Merge

9. With the gate approved, press "Merge into main".
   Expect: a question that says it merges exactly that commit into `main` in the checkout that has `main` checked out, so the working tree of `scratch` changes, makes a merge commit if it cannot fast-forward, pushes nothing, runs no hooks and does not sign. Cancel first (nothing happens), then confirm.
   Expect: "Merged.", the gate says Merged with the merge commit. In `scratch`, `git log` shows the commit and nothing was pushed (`git status` says ahead of origin/main).
   Then repeat with these starting points, each on a fresh branch off `main` (`git worktree add ../scratch-b -b agent/b`, a new pane):
   a. A change that does not conflict, after you commit an unrelated line in `scratch` first (so `main` has moved). Expect a merge commit, or a plain fast-forward if `main` has not moved.
   b. A change to the same line that `main` changed, committed in `scratch` before you approve. Expect the gate to be blocked with a conflict blocker and Approve off. If you approve first and then change `main`, expect the approval to drop with the same blocker.
   c. An uncommitted edit to `notes.txt` in `scratch` (the base checkout) when you press Confirm and merge. Expect a refusal in the desk's words ("not clean" or similar) and no change to `scratch`. Press it again after `git checkout notes.txt`.

## Warnings

10. `mergedNotRecorded`: before you press "Confirm and merge" on a ready branch, make Flockdeck's `changes` folder in its config directory read-only (on Windows: `icacls <folder> /deny %USERNAME%:(W)`, and `/remove:d %USERNAME%` afterwards). The merge succeeds but cannot be written down.
    Expect: a "Warning" box in the gate with the desk's sentence and the branch name, a Dismiss button, and "Merge into main" off while it is up. A new push does not clear it. Dismiss puts the keyboard on the main button.
11. `partlyUpdated` needs git to fail part way through a merge, which cannot be provoked reliably by hand. Skip it; the unit tests draw it. If you do see it, expect the same warning box, with Merge still available.

## Checks

12. Press "Run checks" with no check file.
    Expect: an error with the path of the file to write. Create it there:
    `{ "checks": [{ "name": "unit", "cmd": "git --version" }] }`
13. Press "Run checks" again.
    Expect: a question titled "Run these checks?" listing the project, the check name, the command in a box, and that the commands run in this branch's checkout. Cancel (Escape also works) sends nothing and the keyboard goes back to "Run checks". Press it again and confirm.
    Expect: "Checks started.", then the check appears as running and then passing for the head. A new commit makes it queued for the new head.
14. Edit the check file to `"cmd": "git --version && echo hi"` while the question is open, then press Confirm.
    Expect: the question shows the new command with a note that the file changed, and nothing runs until you confirm again.
15. Put a control character in a command. In the JSON write a tab as `\t` (allowed) and a line break as `\n` (refused):
    `"cmd": "echo a\nb"`
    Expect: no question; an error that names the check and the character, and nothing runs. Do the same with a zero-width space (U+200B) and a right-to-left override (U+202E): both are refused. Put a no-break space (U+00A0) in a command: either it is refused the same way, or the question shows `<U+00A0>` in its place. Write down which.
16. Restart Flockdeck and press "Run checks" with the original file.
    Expect: the question appears again, because the confirmation is only kept in memory.

## Clean up

Delete the check file, restore the `changes` folder's permissions, remove the worktrees (`git worktree remove`) and delete `scratch`, `scratch-a`, `scratch-b` and `scratch-remote.git`. Comments and approvals are kept in `changes/<project>-<8 hex>.*.json`, so delete those for the scratch project too.
