# Remote access

Open this window from another device — a laptop away from your desk, a tablet,
a phone — through a relay, without opening a port on this machine or setting up
a VPN.

## How it works

Flockdeck dials *out* to the relay and holds one connection open while it runs.
Each connection a paired browser makes is carried down it and answered by the
same server the window on your desk uses, so the remote window is not a lesser
copy: it is this window, with every pane, every dialog and every keystroke.

A pane open in two windows at once, the one on your desk and a phone say, takes
the size of whichever window last typed into it or focused it. Glancing at it
from the phone leaves the desk's terminal as it was; typing on the phone fits it
to the phone until you type at the desk again.

Nothing on this machine listens for the network. The local server still binds
to loopback only, and its token never leaves the machine — a request that came
through the relay is let in because the relay has already checked that the
device asking is paired with your account, and nothing else can put a request
on that connection. The few things only another launch of the binary may do —
open a project from the command line, quit the instance — still insist on the
token, so a remote window cannot reach them.

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
`flockdeck remote enable -join <code>` — so one paired device can reach both.

## Pairing a device

[[action:remote]] in the command palette — or the **Remote** button in the
rail, which is there whether or not remote access is on — opens a dialog saying whether
the relay is reachable and how many windows are open through it. **Pair a
device** asks the relay for a link and shows it as a QR code: scan it with the
device you want to pair, or open the link on it.

A link works once and expires after a few minutes. Until then, whoever opens it
can drive every agent here, so treat it like a password.

On a phone, when an agent stops to ask a question with set answers, the relay's
own client shows its choices as buttons, so you can answer with a tap rather
than typing into the terminal.

## Notifications on your phone

A paired phone can be told when an agent has been waiting on you for a while,
whether or not the relay's page is open on it, and whether or not a window is
open here: a run left detached reaches you too. On the phone, open a desktop
and press **Notify me when an agent needs me**. On an iPhone or iPad, add the
page to the Home Screen first — Share, then **Add to Home Screen** — and open
it from there: iOS and iPadOS send notifications only to web apps added that
way, from version 16.4. Tapping a notification opens the pane that is waiting.

Here, Settings › **Remote access** says what is sent:

- **Notify paired devices** turns notifications off for every device at once.
- **After waiting** is how long an agent has to have been waiting first: 30
  seconds, unless you choose otherwise. Each wait is told once, and an agent
  that is answered and then asks again is a new wait.
- **Send nothing identifying** has a notification say only "An agent on *this
  machine* needs you", rather than naming the pane and its project.

The relay encrypts each notification for the device it goes to, so the push
service that carries it — Apple's, Google's, Mozilla's or Microsoft's, which
is the browser's to choose — sees only that one was sent, and when. The relay
itself sees what it says, as it sees everything that passes through it. A relay
that does not send notifications, or an account whose plan does not include
them, is said under the switch in the relay's own words.

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
kept here, though a key typed into a remote window passes through it like
anything else typed there; what it holds for this machine is a credential of
its own, kept here in `remote.json` in the state directory and readable only
by you.

The shared relay at `https://remote.flockdeck.ai` has a
[privacy policy](https://flockdeck.ai/privacy.html), which says what it stores
about this machine and your devices, how long it keeps it and how to have it
deleted, and [terms](https://flockdeck.ai/terms.html) for using it.

## Enterprise (coming soon)

For companies: a licence to run the relay on your own infrastructure, with SSO
and support, for a company whose rules don't allow a third party to decrypt
its developers' terminal traffic.

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
cannot take itself off, and would be listed as offline for good. Remove it from
the **Devices** page of a paired device instead: no device can reach it after
that, and its credential stops working, so a copy of Flockdeck restored from a
backup cannot connect with it either.
