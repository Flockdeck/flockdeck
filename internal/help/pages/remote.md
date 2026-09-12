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

Enrolling a machine is done from a terminal, because it is the step that
decides where the traffic goes:

| Command | What it does |
| --- | --- |
| `flockdeck remote enable` | Enrols this machine with the relay |
| `flockdeck remote pair` | A one-time link and QR code for a device |

The relay is `https://remote.flockdeck.ai` unless `-relay` or `FLOCKDECK_RELAY`
names another. A second desktop joins the same account with a code from
`flockdeck remote pair -desktop` on the first, given to
`flockdeck remote enable -join <code>` on the second, so one paired device can
reach both.

## Pairing a device

[[action:remote]] in the command palette — or the **Remote** chip in the top
bar, which appears once the machine is enrolled — opens a dialog saying whether
the relay is reachable and how many windows are open through it. **Pair a
device** asks the relay for a link and shows it as a QR code: scan it with the
device you want to pair, or open the link on it.

A link works once and expires after a few minutes. Until then, whoever opens it
can drive every agent here, so treat it like a password.

## What the relay can see

Traffic is encrypted between your browser and the relay, and between the relay
and this machine. The relay decrypts it to route it, so it is **trusted**: this
is not end-to-end encryption, and whoever runs the relay could read what passes
through it. The relay never sees this machine's local token or your API keys;
what it holds for this machine is a credential of its own, kept here in
`remote.json` in the state directory and readable only by you.

## Leaving the agents running for it

A remote window only works while Flockdeck is running here. Closing the window
on this machine still quits it, even with a remote window open — so to leave
the agents running for later, use [[action:detach]] instead, or start with
`flockdeck -detach`. On macOS and Linux, one started from a terminal still stops
when that terminal closes; [What comes back, and what keeps running](#persistence)
says how to avoid it. A remote window closing never stops anything.

If a second Flockdeck on this machine connects to the relay as the same
machine, the first one steps aside and says so rather than fighting it for the
connection.

## Unpairing, and turning it off

The dialog lists every paired device with an **Unpair** button; unpairing ends
that device's session at once, including any window it has open. From a
terminal:

| Command | What it does |
| --- | --- |
| `flockdeck remote devices` | What is paired, and each device's id |
| `flockdeck remote revoke <id>` | Unpairs one |
| `flockdeck remote disable` | Removes this machine from the relay |
