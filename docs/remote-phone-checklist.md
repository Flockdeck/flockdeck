# Real-phone test checklist

Every automated test of the sealed work uses a fake desktop or a fake phone. This list is what to check once, by hand, on a real phone through the real relay, before the sealed phone app is released. Use a desktop with a project that has a git remote, one structured pane and one terminal pane. Note the desktop and relay versions at the top of the run.

Setup for every tier: pair the phone, press Verify beside it in Settings, Remote, and compare both codes. Watch the device log in the state directory while you test. No entry should hold message text, a path or a file name.

## Before any tier

- [ ] A device whose key is not verified opens the sealed socket and is closed with "verify"; the phone says to verify it at the desk.
- [ ] After verifying, the socket opens and the phone shows its tier from the hello.
- [ ] Airplane mode on and off: the phone reconnects and re-handshakes with no manual step.
- [ ] A desktop older than API 1.37 shows an update prompt, not a broken screen.
- [ ] In a capture of the traffic, sealed frames are all multiples of 1,024 bytes and unreadable.

## Tier A: read

The device is full and Control agents is off.

- [ ] A structured pane's conversation opens with rows, tool output and a picture. Scrolling back loads older rows.
- [ ] Search a word that is in the conversation and one that is not.
- [ ] A long conversation (a few thousand rows) opens without the socket dropping. This exercises the relay's 4,096-frame and 4 MiB limits.
- [ ] The changes list, a file's diff with many hunks, and CI status for a branch with a running check.
- [ ] Team view: needs-you list, live lines, a goal and mail. Tapping through opens the pane. There is no task board.
- [ ] History of a project, the worktree and branch list, recent projects.
- [ ] GitHub: a PR and an issue with a long body and many comments are cut, not broken.
- [ ] The device list shows names and last seen, no keys.
- [ ] Something that looks like an API key in a conversation shows as redacted on the phone.
- [ ] Every action control (send box, Stop, answer buttons, start, commit) is shown as off, with where to turn it on at the desk.
- [ ] Make the device a viewer without watch panes at the desk: the socket closes with "access". Give it watch panes: it reads panes, and start, team, history and device list say they need a full device.

## Tier B: Control agents on

At the desk: Settings, Remote, Control beside the phone. Read the wording and confirm.

- [ ] The phone shows tier B without a manual reload (its socket was cut and it reconnected).
- [ ] Send a message to a structured pane; it arrives. Send one with a photo from the phone camera; the picture arrives and shows in the conversation.
- [ ] Send to a shell pane: refused.
- [ ] Stop a running turn. Queue two prompts, edit one, remove one, send one now.
- [ ] A question card: answer an option. A plan card: approve once, then reject once on another. A tool request: the phone cannot answer it here.
- [ ] Run a one-line shell command in a structured pane, then stop it.
- [ ] Start an agent in a known project, and once in a new worktree on a named branch. A branch that exists is refused. Start five quickly in a row: the sixth waits (`busy`).
- [ ] Resume a past conversation. Move a structured pane to another conversation and to a clean one.
- [ ] Team view: send a message to an agent; it reads as from you.
- [ ] Change push delay and "send nothing identifying"; the next push follows, on every paired device.
- [ ] A device log line exists for each of these, with command and pane and no text.

Switching off while the phone is open:

- [ ] Turn Control off at the desk mid-conversation. The phone drops to tier A within seconds, and a send is refused with "control".
- [ ] Turn it on again, then re-pair the phone or change its role: it comes back off.
- [ ] `flockdeck remote approvals off` ends the grant for every device.

## Tier C: desk confirm

Control agents is on. Keep a Flockdeck window open on the desktop, and also try once with none open.

- [ ] Commit from the phone with a message. The phone shows "waiting". The desk shows a card with the device name, repo, branch, file count and the first line of the message. Allow: the commit exists and the phone says allowed.
- [ ] Push and pull the same way. Deny one: nothing runs and the phone says denied.
- [ ] Let one expire (2 minutes): the phone says expired and nothing runs.
- [ ] Ask, then turn Control off at the desk before answering: the phone says off, and a yes at the desk runs nothing.
- [ ] Ask, close the phone app, then allow at the desk: nothing runs.
- [ ] Ask again right after an allow: it asks again.
- [ ] A repo with a git hook or credential helper: the desk shows its own warning as well.
- [ ] Comment on a PR and on an issue. The desk shows the number and the first 300 characters; allow posts exactly that text.
- [ ] With no desktop window open the phone says there is no desk to ask.
- [ ] The phone cannot answer the card itself, even with a modified client.

## Desk only

- [ ] The phone offers no merge, worktree removal, PR creation or pairing link, and a hand-written request for one is refused.
- [ ] A device can unpair itself from its own screen; the desk shows a notice and the phone is signed out.
- [ ] A device cannot rename or unpair another device.

## Record

Write the desktop and relay versions, the phone and browser, and each item that failed with what the phone showed. A failure on a limit (frames, buffer, padding) needs the relay log for the same minute.
