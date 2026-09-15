# Remote access

Open this window from another device — a laptop away from your desk, a tablet,
a phone — through a relay, without opening a port on this machine or setting up
a VPN.

## How it works

Flockdeck dials *out* to the relay and holds one connection open while it runs.
Each connection a paired browser makes is carried down it and answered by the
same server the window on your desk uses, so the remote window is not a lesser
copy: it is this window, with every pane, every dialog and every keystroke —
bar the few things done only at the desk, listed below.

A pane open in two windows at once, the one on your desk and a phone say, takes
the size of whichever window last typed into it or focused it. Glancing at it
from the phone leaves the desk's terminal as it was; typing on the phone fits it
to the phone until you type at the desk again. The desk's own window shows a
small phone glyph on a pane's header while a paired device has it open, in its
chat or its terminal, naming the device — so text appearing there is not a
surprise.

Nothing on this machine listens for the network. The local server still binds
to loopback only, and its token never leaves the machine — a request that came
through the relay is let in because the relay has already checked that the
device asking is paired with your account, and nothing else can put a request
on that connection. The few things only another launch of the binary may do —
open a project from the command line, quit the instance — still insist on the
token, so a remote window cannot reach them.

## Self-hosted: running it on a server, not a desk

"Your desktop" above doesn't have to be a desktop. `flockdeck -no-window`
serves headless — no window, and no browser is ever looked for on that
machine — so it runs just as well on a spare box, a home server, a NAS or a
cheap VPS as it does on the machine in front of you. Pair it the same way
(`flockdeck remote enable` then `flockdeck remote pair`) and it's a desktop
in every way that matters here: full interface, chat on a phone, fan out, the
lot. That makes the free app a self-hosted stand-in for a paid "run my coding
agents in the cloud" service, on hardware you already control.

`-detach` also releases the terminal that started it, so an SSH session can
end without ending Flockdeck. Under a process supervisor such as systemd,
plain `-no-window` is usually the better fit: it keeps the terminal as its
interface and leaves managing the process's lifecycle and logs to the
supervisor.

## Done only at the desk

A window reached through the relay is not offered these, and Flockdeck refuses
them from one:

- quitting Flockdeck, or restarting it, including to install an update;
- turning remote access off, or on again against another relay;
- making a code for another desktop to join this account;
- setting or clearing an API key — one typed on a phone would pass through
  the relay, which can read it;
- changing where an API agent sends its prompts, its address, since its key
  goes wherever that says;
- turning the check for updates on or off.

These keep a phone in a pocket from doing any of them by accident. They are
not a security boundary: a remote window can open a shell pane, and from a
shell it can do anything you can at this machine, `flockdeck remote disable`
included. A paired device has everything your user account here has.

## Turning it on

[[action:remote]] in the command palette opens a dialog that turns it on: leave
the relay empty for `https://remote.flockdeck.ai`, or name another, give this
machine a name if its host name is not the one you want, and press **Turn on
remote access**. The same can be done from a terminal:

| Command | What it does |
| --- | --- |
| `flockdeck remote enable` | Enrols this machine with the relay |
| `flockdeck remote pair` | A one-time link and QR code for a device |

The relay is `https://remote.flockdeck.ai` unless `-relay` or `FLOCKDECK_RELAY`
names another. A second desktop joins the same account with a code from
`flockdeck remote pair -desktop` on the first, given to the second — in the
dialog under **Joining an account, or invited?**, or as
`flockdeck remote enable -join <code>`. Every device paired with the account
then reaches every desktop on it: pairing with one is pairing with them all.

## Pairing a device

[[action:remote]] in the command palette — or the **Remote** button in the
rail, which is there whether or not remote access is on — opens a dialog saying whether
the relay is reachable and how many windows are open through it. **Pair a
device** asks the relay for a link and shows it as a QR code: scan it with the
device you want to pair, or open the link on it.

A link works once and expires after a few minutes. Until then, whoever opens it
can drive every agent on every desktop on this account, and open a shell on
any of them, so treat it like a password.

A phone paired with just one desktop opens straight into it; paired with more
than one, it shows a list to choose from first. At 900px and wider — a
tablet, or a browser window that wide — a desktop's list of panes sits in its
own column beside whichever one is open, instead of swapping the whole
screen for it; tapping another row swaps only that side, so the list keeps
its scroll position and any helper group you had open.

On a phone, opening a pane shows a chat with the agent rather than its raw
terminal, for a Claude Code pane and Flockdeck's own chat client — see
**Your agents on your phone**, below. When an agent stops to ask a question
with set answers, it appears as buttons, so you can answer with a tap rather
than typing into the terminal.

## Your agents on your phone

Opening a pane from a paired device shows a chat with the agent, not its raw
terminal, for a Claude Code pane and for Flockdeck's own chat client (the
built-in Anthropic, OpenAI, Google and OpenAI-compatible agents) — anything
else still opens as a terminal, because Flockdeck doesn't yet read what it's
saying. Nothing to turn on: the phone asks the desktop when it opens a pane,
and gets a chat back if there is one to give it. A **Chat**/**Terminal**
switch in the pane's own header moves between the two anyway, and is
remembered there, per device, per pane. A search button beside it opens a
slim bar over the conversation — type to see matching prompts, replies and
tool summaries as you go, and tap one to jump straight to it, paging in
older history if it isn't loaded yet. Switching to **Terminal** still gives a
proper terminal, not a cut-down one: Escape, Tab, the arrows, Enter and
Ctrl+C sit in a row above the keyboard, with Ctrl, Page Up, Page Down, Home
and End behind a **More** button, and the prompt itself stays above the
phone's own keyboard rather than sliding behind it.

Replies render as Markdown — headings, lists, tables, quotes, and code with a
copy button, a wrap toggle and syntax colouring. A long code block or diff
folds to its first dozen-odd lines behind "Show all N lines", and a reply
longer than about a screen and a half folds to about one screen under "Show
more" — the newest reply stays open, an older long one folds, and one still
arriving is never folded mid-stream; jumping to it from search opens
whichever fold is in the way. Each turn's tool calls and thinking fold into
one line, "12 steps · 3 files edited · 4 commands", tapped open to see each
step; an edit shows its diff, with line numbers. A question
or a permission prompt appears as a card with buttons in the chat, rather
than needing the terminal — multiple choice, a typed answer, and Yes/No for a
permission are all covered, including a call that asks several questions at
once: answer them one at a time on the card, then send them all together.
Each reply also carries its own **Copy**, for the whole thing as Markdown,
and **Quote**, which drops a paragraph or so of it into the box, quoted,
ahead of whatever you've already typed. A message you start and don't send
is kept too, per agent, in that browser, so leaving the chat and coming back
doesn't lose it.

While an agent works, "Working for 3m — Bash", naming what it's doing, sits
above the prompt box, and a **Stop** button — the same as pressing Escape —
takes Send's place in the prompt row, with Send back beside it once you type;
once it's idle, quick replies — Continue, Yes, go ahead, Explain that more
simply, Run the tests — cover the common ones without typing. A message you
send says what's happened to it: **Sending…**, then **Sent**, then a quiet
tick for **Delivered** once the agent's own transcript shows it arrived; one
that hasn't after a while says **Not delivered yet**, with **Retry** beside
it. The chat header, and each row in the list below, also says what the
agent has spent in its conversation — a token count and the tightest of its
usage windows for one on a subscription, or a rough cost in dollars for one
paying by the token — coloured once that window is close to running out.

Every open pane also appears in the paired device's list with its latest
reply, or the question it's waiting on, a time, and an unread dot, so you
can see what's happened everywhere without opening each one. Opening the
list again after a while away leads with **Since you last looked** — new
replies, agents that finished, and ones that started waiting, since this
device last had it open — and a tap on any of those jumps to and
highlights the first row it counts, opening its helper group first if that
was folded. It says nothing once there is nothing honest left to count. One
waiting on a permission offers Yes and No right there in the list; one
waiting on a single, short question offers a button for each option;
anything more — several questions at once, a typed answer — still just
opens the chat, the way tapping the row always has. Messages Flockdeck
itself injects — a background task finishing, a session notice — show as
small notes, never as if you had typed them.

A lead agent's own helpers — started by `flockdeck spawn` or a fan-out —
are grouped under its row instead of filling the list with one each,
folded by default into a line such as "3 helpers · 1 waiting"; tap it to
open them as indented rows, and again to fold them back, remembered per
desktop and lead. A helper waiting on you is never hidden by the fold, and
a group with one waiting sorts to the top even while its lead is idle.

A screenshot in the conversation shows as a thumbnail that opens full
screen. The attach button offers your photo library or the camera in one
tap, and the library lets you pick several at once, up to six; each queues
its own thumbnail in a strip above the prompt box, with a spinner while it
uploads and its own Retry if it fails, and Send goes once every picture is
in, as one message carrying all of them — attaching too many too quickly
is refused rather than queued. They're shrunk on the phone before they're
sent, kept on this desktop — in Flockdeck's own folder, never your
project — and removed after about a week; the agent is told each file's
path, the same way typing one would tell it.

Most of this needs a fairly recent Flockdeck on the desktop; paired with an
older one, a pane simply opens as a terminal instead. Even where a pane does
open as a chat, a few parts fall back gracefully on a desktop too old to send
them, rather than breaking: no live timer, a plain "waiting for you — open
the terminal to answer" banner instead of a question or permission card, and
no preview text in the paired device's list. **New agent**, search, and
muting a single pane each need their own, newer understanding from the
desktop too, and simply don't appear against an older one, rather than
sending it a command it would silently drop.

**New agent**, near the top of a desktop's page in the relay's client, starts
one without going to the desk: choose a project already open there, an agent
and model, optionally a fresh worktree, and a first message, then **Start**.
It opens straight into that agent's conversation once it starts, and refuses
a project the desktop does not already have open rather than opening one.
Starting several in quick succession is refused, the same guard that limits
pictures.

## Notifications on your phone

A paired phone can be told when an agent has been waiting on you for a while,
whether or not the relay's page is open on it, and whether or not a window is
open here: a run left detached reaches you too. On the phone, open a desktop
and press **Notify me when an agent needs me**, below its list of panes. On
an iPhone or iPad, add the page to the Home Screen first — Share, then **Add
to Home Screen** — and open it from there: iOS and iPadOS send notifications
only to web apps added that way, from version 16.4. Tapping a notification
opens the pane that is waiting.

Here, Settings › **Remote access** says what is sent:

- **Notify paired devices** turns notifications off for every device at once.
- **After waiting** is how long an agent has to have been waiting first: 30
  seconds, unless you choose otherwise. Each wait is told once, and an agent
  that is answered and then asks again is a new wait. However many agents are
  waiting, the phone is sent one notification that says how many, which
  replaces the one before it, and no more than one a minute. It is sent once
  an agent has waited that long and nobody has used this computer — keyboard
  or mouse, in any application — for two minutes, or its screen is locked;
  a Flockdeck window being in front of you makes no difference. Where the
  operating system's idle time can't be read, typing and clicks in
  Flockdeck's own windows here are what count instead. Nothing is
  sent about a pane you are using on the phone; if it is still waiting two
  minutes after you leave it, you are told then.
- **Send nothing identifying** has a notification say only "An agent on *this
  machine* needs you", rather than naming the pane and its project.

A single agent that is chatty and safe to leave can also be muted from the
bell in its own header on the phone, which leaves it out of what is sent
while it goes on showing as waiting everywhere, including here. Muting lives
on the pane, not the device that asked, and is forgotten on close: it does
not survive a restart of Flockdeck.

Each notification is encrypted here, on this machine, for the device it goes
to, and the relay only passes it on: neither the relay nor the push service
that carries it — Apple's, Google's, Mozilla's or Microsoft's, which is the
browser's to choose — can read what it says, since the key that opens it never
leaves the phone. They see that one was sent, and when; every notification is
the same size, however long the names in it. A relay that does not send
notifications, or an account whose plan does not include them, is said under
the switch in the relay's own words.

## Renaming

A machine is listed on every device under the name it was enrolled with — its
host name, unless you gave another — and a device under whatever its browser
suggested when it was paired. **Rename**, beside this machine and beside each
device in the dialog, gives it a new one. From a terminal,
`flockdeck remote rename <name>` renames this machine, and
`flockdeck remote rename -device <id or name> <name>` a device. A paired device
can rename any of them from its **Devices** page.

## What the relay can see

Traffic is encrypted between your browser and the relay, and between the relay
and this machine. The relay decrypts it to route it, so it is **trusted**: this
is not end-to-end encryption, and whoever runs the relay could read what passes
through it. The relay never sees this machine's local token or the API keys
kept here, though anything typed into a remote window, a shell pane's included,
passes through it; what it holds for this machine is a credential of its own,
kept here in `remote.json` in the state directory and readable only by you.

Reading is not all it could do. The relay is what decides which devices are
paired, so whoever runs it can open a window on any desktop that is connected
to it and use it as a paired device would: type to every agent, and open a
shell. Use a relay you would trust with this machine.

A relay that gives every desktop's window its own address — `remote.flockdeck.ai`
does — keeps a page from one desktop's window from reaching another's: it runs
on a different address, with none of the account's own session, so it cannot
list your devices, open another desktop, or make a code for one to join. A
relay without one instead serves every desktop's window from the same address
as the account itself, which trusts every desktop of an account with the
others' sessions; that is weaker, and is what a relay run without a spare
domain for it falls back to.

The shared relay at `https://remote.flockdeck.ai` has a
[privacy policy](https://flockdeck.ai/privacy.html), which says what it stores
about this machine and your devices, how long it keeps it and how to have it
deleted, and [terms](https://flockdeck.ai/terms.html) for using it.

## The trial, and the subscription

Remote access through the shared relay at `https://remote.flockdeck.ai` is a
subscription, after a free trial for every account that starts when its first
machine is enrolled. The desktop app itself is free, every part of it,
whatever the plan: only reaching it through the shared relay is paid for.

The plan is the relay's to keep, and Flockdeck only shows what the relay says.
[[action:remote]] shows the account's plan and, during the trial, how many days
are left, and so does **Account & plan** in the settings. A paired phone or
browser shows the same on its **Devices** page, which is where you subscribe.

When a trial ends, or a subscription lapses, the relay stops carrying remote
windows to this machine, and the dialog says so in the relay's own words.
Nothing else changes: the window on your desk works exactly as before, and a
paired device can still sign in and open **Devices** to subscribe. Flockdeck
asks the relay again every few minutes, and at once from **Try again**, so
remote access comes back by itself once the account is paid for.

Nothing is deleted straight away. The account, its machines and its paired
devices are kept for 90 days after a trial or subscription ends, and deleted
after that; subscribing before then puts everything back as it was.

A relay other than the shared one may have no plans at all, and then none of
this applies: nothing is shown, and nothing stops.

## Enterprise (coming soon)

For companies: a licence to run the relay on your own infrastructure, with SSO
and support, for a company whose rules don't allow a third party to decrypt
its developers' terminal traffic. The relay's own configuration already
supports this today; see [Self-hosting the relay](https://docs.flockdeck.ai/self-hosting/overview.html)
for how.

## Leaving the agents running for it

A remote window only works while Flockdeck is running here. Closing the window
on this machine still quits it, even with a remote window open — so to leave
the agents running for later, use [[action:detach]] instead, or start with
`flockdeck -detach`. A remote window closing never stops anything.

If a second Flockdeck on this machine connects to the relay as the same
machine, the first one steps aside and says so rather than fighting it for the
connection.

## Unpairing, and turning it off

The dialog lists every paired device with an **Unpair** button; unpairing ends
that device's session at once, including any window it has open. **Turn off
remote access**, at its foot, takes this machine off the relay; if it is the
only machine on the account, the account and its paired devices go with it,
and the dialog says so before it asks. A relay that cannot be reached is not
taken for one that was told: you are offered **Try again**, and only then to
forget it here anyway, which leaves the relay listing this machine, offline.
From a terminal:

| Command | What it does |
| --- | --- |
| `flockdeck remote devices` | What is paired, and each device's id |
| `flockdeck remote revoke <id or name>` | Unpairs one |
| `flockdeck remote rename <name>` | Renames this machine; `-device <id or name>` renames a device |
| `flockdeck remote disable` | Removes this machine from the relay |

A machine that was wiped or lost before remote access was turned off on it
cannot take itself off. Remove it from the **Devices** page of a paired device
instead, rather than wait: no device can reach it after that, and its
credential stops working, so a copy of Flockdeck restored from a backup cannot
connect with it either. Left alone, the relay removes it on its own once it
has gone 30 days without being heard from.

## If a device is lost

1. **Unpair** it in the dialog, or with `flockdeck remote revoke <id or name>`.
   That ends its session at once, and it cannot get back in with the same
   pairing.
2. Look down the dialog's lists for a device you did not pair or a desktop you
   did not add. Flockdeck is not told when a device pairs or a desktop joins,
   so those lists are where either shows. Unpair a device you do not know,
   and remove a desktop you do not know from the **Devices** page of a paired
   device.
3. If the device could have opened a shell here, it could have copied this
   machine's credential out of `remote.json`. Turn remote access off and on
   again: this machine is enrolled afresh, and the old credential stops
   working. If this is the account's only desktop, turning it off also
   unpairs every device, and each of them pairs again with a new link.

A pairing link or join code that was made and not used stops working on its
own after a few minutes.
