# When something is wrong

The things that go wrong most often, and what they mean.

## It will not start at all

Started from a shortcut, Flockdeck has no terminal to print to, so a failure to
start is also written to `error.log` in the state directory ([Settings](#settings) says
where that is). Running `flockdeck` from a terminal shows the same message there.
What goes wrong as it stops (a layout it could not save, an update it could
not put in place, a restart that did not come back) is written there too, and
so are a few notes for diagnosing a problem later: a window link that was
refused, and windows that disagree about a pane's size (see below).

When the layout cannot be saved, Quit and Restart say why and stop there
rather than lose what changed; asking a second time goes ahead anyway.

## macOS or Windows will not open a Flockdeck I downloaded

Release downloads are not signed with an Apple Developer ID or a Windows
code-signing certificate (their checksums are signed with Flockdeck's release
key, which the updater checks). A copy you downloaded in a browser, from
`dl.flockdeck.ai` or the GitHub mirror, carries the browser's download mark, so the first start may be
stopped. On macOS, allow it under System Settings → Privacy & Security, or run
`xattr -d com.apple.quarantine` on the binary. On Windows, choose More info →
Run anyway, or run `Unblock-File` on the program in PowerShell. The install
scripts download with curl and PowerShell, which leave no such mark.

## A pane says its CLI was not found on PATH

The agent's command is not on your `PATH`, so the pane shows that message where
its terminal would be. Install it (the picker shows where to get every agent it
knows about, and Claude Code is at
[claude.com/claude-code](https://claude.com/claude-code)), then press
**Restart** on the pane. The command is looked for each time a pane starts, but
in the `PATH` Flockdeck itself was started with: if the installer added a new
directory to `PATH` rather than using one already on it, quit Flockdeck and
start it again so that it sees the change.

## An agent is greyed out in the picker

For a CLI agent, its command is not on your `PATH`; the line under it says
where to get it. For an API agent, no key was found: nothing in the
environment variables it looks at, and nothing in Flockdeck's own store. `flockdeck
keys set <agent>` reads one from stdin, and [Agents and models](#agents) covers the
rest. The OpenAI-compatible endpoint ships with no address, because none would
be right for everybody: pick it, and the picker asks for one. It shows as
available once it has a key, or at once when that address is on this machine
and needs none. Unavailable agents are shown rather than hidden, so that an
agent you have not installed is a visible choice.

## A pane is dead, with a Restart button

The process exited. [[action:restartPane]] starts it again in the same directory
and, where the agent can, resumes the same conversation. If it exits immediately every time, run the
agent's own command yourself in that directory to see what it says. A bad
model id and an expired login both look like this from outside.

## An agent stopped to ask about trusting a folder

A worktree is a directory the agent has never seen, and some agents (Claude
Code among them) ask before working in one. Answer it once and it will not ask
again for that directory; to avoid it entirely when fanning out, use the trust
checkbox in the [Fan out](#fanout) dialog, which carries over the answer already given for
the project. It carries Claude Code's "Allow external CLAUDE.md file imports?"
answer too, if the project was ever asked it.

## A pane stopped saying what its agent is doing

Where an agent reports its own lifecycle, the status in its header is those
reports, sent back to the application over loopback. When they stop, only the
reporting has stopped: the agent carries on working, and its terminal is still
the truth.

**Restart** the pane. It is launched with a freshly written settings file
pointing at the address this run is listening on, and with this run's token
in its environment, where the hooks read it; a pane still carrying an earlier
run's address and token is missing both. If it
happens repeatedly with Claude Code, run `claude --debug` in that directory: a
hook that cannot reach the application, or that is turned away by it, says so
on its standard error, and that is where Claude Code shows it.

## A pane's status is vague, or late

Check what is running in it. An agent with no lifecycle of its own to report is
read from its terminal instead (the bell, a quiet timer and, if its entry has
`patterns`, the lines it prints). That is a guess, and a guess is sometimes a beat behind and sometimes
wrong. [Agents and models](#agents) says which agents report and which are read. There
is nothing to fix here. It is the price of running an agent that was never
built to be watched.

## A Claude pane shows no usage limit

Claude Code hands its five-hour and weekly windows only to its status line,
only on a Pro or Max plan, and only after the first answer in a session. By
default Flockdeck reads them only where you have a status line of your own;
**Settings › Agents & models › Claude Code's usage limits › Always** reads them in every
Claude pane. That choice, and any change to your own status line, applies to a
pane when it starts, so use [[action:restartPane]] on one already running.
[Spend and limits](#spend) has the rest.

## A fan-out shows no routed rows

Routing is off until you turn it on, in **Settings › Routing**. When
it is on, it still leaves a line alone when no rule matches its task, when the
run is on a model whose size it does not know (Claude Code's **Default** among
them), and when the agent has no model in the tier the rule asks for. The
dialog says so when routing can do nothing for the run's model.
[Agents and models](#agents) has the rules.

## Push, pull or fetch fails over ssh

The Changes panel's git has nowhere to ask you anything, so ssh is told not
to ask: it fails at once, rather than waiting for an answer nobody can give.
Two failures come from that, and the panel says which it was.

- **ssh has not been told to trust the host.** The first connection to a
  server asks whether to trust it. Run `git fetch` once from a terminal in
  that checkout and answer yes; the panel connects after that.
- **The remote refused the ssh key.** A key with a passphrase cannot be
  unlocked without asking for it. Add it to your ssh agent with `ssh-add`, and
  the panel uses it from there. If it still fails, the remote does not know the
  key, and `git fetch` from a terminal says more.

An ssh command of your own (`core.sshCommand` in git's config, or
`GIT_SSH_COMMAND` or `GIT_SSH` in the environment) is left as it is, without
that setting. Where ssh can ask for a passphrase in a window of its own, as on
some Linux desktops, `git config core.sshCommand ssh` in the checkout is how
to keep that window.

## The window looks like a browser tab

The interface is a local web page shown in a native window, built with Wails,
which embeds the platform's own webview (WebView2 on Windows, WebKit on
macOS, WebKitGTK on Linux) directly inside the binary. If the platform has no
working webview it opens as an ordinary tab in your default browser instead,
which works but looks less like an application.

## Nothing is exposed to the network

The server binds to `127.0.0.1` on a random port, and every request (the page,
the assets and both WebSockets) must carry a token generated fresh for each
run. A second launch of the binary reaches the running instance through that
same loopback address.

The window is opened on a one-time link rather than on the token itself, and
loads it inside Flockdeck's own process. Where it falls back to your default
browser, the link is handed to the browser's launcher instead; either way it
can be used once, within about a minute. If a window ever shows "This link has
already been used" and you did not just open one yourself, someone else on
this computer may have. Quit Flockdeck and start it again, which ends that
session along with whichever window got to it first. "This link has expired"
and "This link isn't valid" are answered the same way.

Remote access, once you turn it on, is a connection this machine makes out to
the relay. A request arriving through it is let in without the token, because
the relay has already checked that the device asking is paired. [Remote
access](#remote) has the rest.

## Desktop notifications never appear

Check **Settings › General › Desktop notifications** first: off, nothing is
raised. The browser asks for permission on your first interaction with the window. If
it was refused, grant it in the browser's site settings for this window's
address, for this run only: the address changes each time Flockdeck starts,
and the question comes back with it. Notifications are only raised while the window is *not* in front.

## A pane's text is garbled, or in the wrong place

A pane has one terminal however many windows show it, and that terminal is one
size: the size of the window last used for it (typed into, or whose terminal
was focused or tapped), or, until one has been, the smallest of them. Every
window draws the pane at that size. A window too small for it shows the pane
cropped, with **Viewing N×M · fit to this window** over it, N×M being the
pane's own size: press it, or click or type in the pane there, and the pane is sized
for that window instead. A pane is never smaller than 20×5 or larger than
500×200.

When windows disagree about a pane's size, a line saying
so (sizes only, never what the pane shows) is written to `error.log`, at most
once a minute per pane; in the window's developer tools,
`window.flockdeckPanes()` lists each pane's terminal, pty and box sizes. Both
are worth including if you report a pane that stays garbled.

## An agent seems to think it is a child of another session

It is not: each pane is a separate top-level session with its own session id,
its own generated settings file where the agent takes hooks, and an environment
scrubbed of the markers a
parent agent session would otherwise pass down. That covers every agent's markers, not
only the ones belonging to whatever is in that pane, since Flockdeck may itself
have been launched from inside one of them. If a pane is behaving as though it
inherited something, restart it, and the state that survives a restart is the
conversation, deliberately.

## Everything stopped when I closed the window

Closing the window quits the application. Use [[action:detach]] in the command
palette to close the window and leave the agents running, and
`flockdeck` to come back to them.
