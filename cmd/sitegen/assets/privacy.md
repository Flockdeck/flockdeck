# Privacy policy

*Last updated: 13 September 2026*

Flockdeck is made by Jim Wright, an individual based in the United Kingdom.
This policy explains what personal data is involved when you use the
Flockdeck desktop app, the Flockdeck relay at remote.flockdeck.ai, and this
website, and what your rights are. Jim Wright is the controller of that data
under the UK General Data Protection Regulation (UK GDPR) and the Data
Protection Act 2018.

If you have a question or a request, email **privacy@flockdeck.ai**.

## The short version

- **The desktop app runs on your computer.** It has no accounts, no analytics,
  no telemetry and no crash reporting. Your code, your terminals and your API
  keys stay on your machine, unless you use a built-in API agent or remote
  access, as described below.
- **The relay is optional.** It is used only if you turn on remote access. It
  keeps what it needs to connect your devices to your desktops: names, random
  identifiers and timestamps. It keeps no email address, password, real
  name, IP address or browser details.
- **This website** sets no cookies, runs no analytics, and loads nothing from
  anyone else.
- **Nobody's data is sold,** and nothing is used for advertising.

## The desktop app

The app runs entirely on your computer. It keeps its settings, layouts,
conversations with built-in API agents, any API keys you give it, and a
record of which models routing chose for a fan-out's tasks (the rules' names
and the models, never the tasks) in Flockdeck's configuration folder on your
computer. The routing record also names each fan-out's project folder, by its
full path, and the pane each task ran in.

The same folder also holds:

- the folders you have opened recently, by their full paths and when each
  was last used, and which were open when the app last quit;
- a record of the running app, with its process number, the local address
  its window uses and the token that address asks for, so that starting it
  again joins it;
- the settings it hands each Claude Code pane, which name that address;
- updates it has downloaded and not yet installed;
- if you turn on remote access, the relay's address, the random identifiers
  of this desktop and its account, the name you gave the desktop, and the
  token it signs in to the relay with.

If the app fails to start, it writes
the error to a file there, `error.log`, which can include the paths of files
and folders on your computer. Nothing there is sent anywhere by Flockdeck.

Outside that folder, the app writes to one file of another program's. When a
fan-out carries Claude Code's folder trust over to the worktrees it creates, it
records in Claude Code's own configuration file (`~/.claude.json`) that those
worktrees are trusted, as the folder they came from already was.

To show what each agent has spent, the app reads what the agents report about
their own usage on your computer: the tokens, cost estimate and usage limits
Claude Code hands its status line, and the token counts of the built-in API
agents. It keeps those figures in memory only. Flockdeck sends them nowhere
itself; if you turn on remote access, they are part of the state of your
panes that your own paired devices receive through the relay, as
[What passes through](#what-passes-through) describes.

The app makes these network connections of its own.

- **Update checks.** It asks dl.flockdeck.ai, where Flockdeck's releases are
  published, whether a newer version exists. This happens when it starts and
  then every six hours. When a new version exists, it downloads it from
  there. If dl.flockdeck.ai can't be reached, it asks GitHub instead, which
  mirrors every release. Nothing identifying is sent either way, but whichever
  it reaches sees your IP address, as it would for any download; see
  [GitHub's privacy
  statement](https://docs.github.com/site-policy/privacy-policies/github-general-privacy-statement).
  dl.flockdeck.ai keeps no access logs. You can turn update checks off in
  Settings or with `FLOCKDECK_UPDATE=off`.
- **The relay**, only after you turn on remote access. See below.
- **Your git remotes**, when you push, pull or fetch from the review panel.
  These are the remotes your repository already has.
- **Model providers**, only if you use a built-in API agent (Claude API,
  OpenAI API, Gemini API, or an endpoint you name). When you store a key
  with `flockdeck keys`, the app makes one request with it to that
  provider, asking for its list of models, to check the key is accepted;
  the request carries nothing else. The app then sends your
  conversation to the provider you chose, using your own API key. That
  includes the files and command output the agent reads, the working
  folder's path, and your operating system's name. What the provider does
  with it is governed by your agreement
  with them.

Flockdeck also starts programs you choose, such as Claude Code, Codex or
Gemini CLI, and opens its window in a browser already installed on your
computer. Those programs make their own connections under their own terms and
privacy policies. Flockdeck does not control them.

## The relay (remote access)

Remote access lets you use your desktop's Flockdeck from another device, such
as a phone, tablet or laptop, through the relay. It is off until you turn it
on.

### What is stored

For each account:

- a random account identifier;
- the date it was created.

The account is not linked to an email address, a name or a password.

For each desktop you connect:

- a random identifier;
- the name you gave it (by default, your computer's name);
- when it was added and when it last connected;
- a code that proves it is yours, stored only as a one-way hash.

For each device you pair:

- a random identifier;
- the name you gave it, or one guessed from its browser (such as "iPhone
  Safari");
- when it was paired, when it was last used and when its sign-in expires;
- its sign-in token, stored only as a one-way hash;
- if you turn on notifications on it, its notification subscription: the
  address its browser's push service gave it, and the two keys the browser
  gave for encrypting messages to it.

The relay also stores pairing codes, as hashes, until they are used. One that
is never used expires after ten minutes, and is deleted within ten more.

### What passes through

Your devices and your desktop reach the relay over encrypted connections
(TLS). To route your traffic, the relay decrypts it: your terminal output,
what you type, and the state of your panes pass through it. That state
includes what each pane's agent has spent, its usage limits, and which model
routing chose for it. Remote access is **not end-to-end encrypted.**

The relay does not record, store or log the content of that traffic.
It never receives your API keys, unless you type one in through remote access.

Notifications are the exception. When an agent has been waiting on you for a
while and you haven't touched this computer — no keyboard or mouse input in
any application for two minutes, or its screen is locked — your desktop
encrypts a notification for each device you turned notifications on for, with
that device's own keys, before it leaves your computer. Whether you are at
this computer is worked out here, on this computer, and is never sent
anywhere, encrypted or not. The relay adds its signature and posts the
notification to the device's push service, and neither can read it. It says
which pane needs you, with its project, and on which desktop; if you choose
notifications without names, it says only how many agents need you, and on
which desktop.

When a paired device reaches your desktop, the relay passes your desktop the
device's IP address, its identifier and its name, along with the headers its
browser sends with every request, such as its browser type and language. That
lets your own Flockdeck tell your devices apart. Your desktop keeps them only
in memory.

### IP addresses and logs

- **Rate limiting.** The relay holds IP addresses in memory, for a little over
  an hour at most, to limit repeated sign-up and pairing attempts. It does not
  write them to its database.
- **Relay logs.** The relay's own logs record events such as "desktop
  connected" or "device paired" against the random identifiers above, not
  names or addresses. A desktop tells the relay which version of Flockdeck
  it runs when it connects, and that version is logged with the connection.
- **Front-end server logs.** The load balancer in front of the relay and
  this website may keep standard access logs, which include IP addresses,
  temporarily, for security and troubleshooting. The web server behind it
  that serves this website keeps no access logs.

### How long it is kept

- **A paired device** is deleted when you remove it, when it signs out, or 30
  days after it was last used.
- **A desktop** is kept until you turn remote access off on it or remove it
  from another of your devices. One that never connects to the relay
  is removed after seven days, and one not heard from for 30 days is removed
  the same way, whether or not it ever connected. When the last desktop in
  an account is removed, the account and all its devices go with it.
- **Pairing codes** expire after ten minutes.
- **A device's notification subscription** is deleted when the device is
  removed or signs out, when its browser's push service says it no longer
  exists, or when the relay's notification key changes. Turning
  notifications off on the device deletes it too.

To delete everything the relay holds about you, turn remote access off on each
of your desktops (`flockdeck remote disable`). If a desktop can no longer be
reached, remove it from one of your paired devices, or email us.

### Cookies

The relay sets a cookie on a device you pair, that keeps it signed in. It is
strictly necessary for remote access to work, lasts up to 30 days, and is
removed when you sign out. Opening a desktop's own window (**Full
interface**) sets a second cookie, `__Host-fdr_desk`, scoped to that
desktop's own address alone, so a page from one desktop cannot use another's
session; it carries no permission of its own, is checked against your account
on every request, and stops working the moment the device or the desktop is
removed. Getting there uses a one-time code, kept in the relay's memory for
60 seconds and good once, never written to a cookie or stored any longer. The
relay's web client also remembers three display preferences in your
browser's local storage: notifications, zoom and fit to screen. None of this
is used for tracking, or shared with anyone.

## This website

flockdeck.ai sets no cookies, runs no analytics and loads nothing from third
parties; its fonts are served from the site itself. Its web server keeps no
access logs, though the load balancer in front of it may, as described
above. Downloads from dl.flockdeck.ai
may get its content delivery network's security cookie, as
[Who else is involved](#who-else-is-involved) describes.

## Why this data is used (lawful basis)

- **To provide remote access,** which you asked for: the account, desktop and
  device records, the sign-in cookie, and the notification subscription of a
  device you turned notifications on for. The basis is performance of a
  contract (the relay's [terms](terms.html)).
- **To keep the relay secure and working:** rate limiting, logs and access
  logs. The basis is our legitimate interest in running a secure and reliable
  service.

## Who else is involved

The relay and this website are hosted by DigitalOcean, in its London region.
DigitalOcean provides the servers, the database and DNS, and serves releases
and update downloads from dl.flockdeck.ai through its content delivery
network, which answers from locations around the world. That network is
Cloudflare's, and Cloudflare sets a short-lived security cookie, `__cf_bm`,
on downloads from dl.flockdeck.ai to tell people from bots. It is strictly
necessary, it is set by Cloudflare rather than by Flockdeck, and the app's
update checks send no cookies. GitHub mirrors every release. TLS certificates come from Let's Encrypt,
which receives no personal data about you. These providers process data on
our behalf or as independent services, under their own terms.

If you turn on notifications on a device, each notification goes through the
push service that device's browser uses: Google's for Chrome and most
Android browsers, Apple's for Safari, Mozilla's for Firefox, and Microsoft's
for Edge on Windows. It receives the notification encrypted, the device's
push address, and when it was sent, and it cannot read the notification. You
chose that service when you chose your browser, and it works under its own
terms.

DigitalOcean and GitHub are United States companies. Where your data is
handled outside the UK, it is protected by the safeguards UK law requires,
such as the UK International Data Transfer Addendum or the UK–US data bridge.

No personal data is sold, rented or shared for advertising.

## Your rights

Under UK data protection law you have the right to:

- ask for a copy of your data;
- have it corrected;
- have it deleted;
- object to its use, or ask for its use to be restricted;
- take it elsewhere.

Most of this you can do yourself, by renaming or removing desktops and devices
in Flockdeck. For anything else, email **privacy@flockdeck.ai**. Because
accounts carry no email address, we may ask you to prove a desktop or device
is yours, for example from the desktop itself.

If you're unhappy with how your data is handled, you can complain to the
Information Commissioner's Office at [ico.org.uk](https://ico.org.uk). We'd
appreciate the chance to put it right first.

## Children

Flockdeck is a developer tool and isn't aimed at children under 13.

## Changes

If this policy changes, the new version will be posted here with a new date.
Material changes will also be noted in the release notes.
