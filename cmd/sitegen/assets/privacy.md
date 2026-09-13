# Privacy policy

*Last updated: 12 September 2026*

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
  identifiers, timestamps and each paired browser's user-agent string. It
  keeps no email address, password, real name or IP address.
- **This website** sets no cookies, runs no analytics, and loads nothing from
  anyone else.
- **Nobody's data is sold,** and nothing is used for advertising.

## The desktop app

The app runs entirely on your computer. It keeps its settings, layouts,
conversations with built-in API agents, any API keys you give it, and a
record of which models routing chose for a fan-out's tasks (the rules' names
and the models, never the tasks) in Flockdeck's configuration folder on your
computer. Nothing there is sent anywhere by Flockdeck.

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
  OpenAI API, Gemini API, or an endpoint you name). The app then sends your
  conversation to the provider you chose, using your own API key. That
  includes the files and command output the agent reads, and the working
  folder's path. What the provider does with it is governed by your agreement
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
- its browser's user-agent string;
- when it was paired, when it was last used and when its sign-in expires;
- its sign-in token, stored only as a one-way hash.

The relay also stores pairing codes, as hashes, until they are used. One that
is never used expires after ten minutes, and is deleted within ten more.

### What passes through

Your devices and your desktop reach the relay over encrypted connections
(TLS). To route your traffic, the relay decrypts it: your terminal output,
what you type, and the state of your panes pass through it. That state
includes what each pane's agent has spent, its usage limits, and which model
routing chose for it. Remote access is **not end-to-end encrypted.**

The relay does not record, inspect, store or log the content of that traffic.
It never receives your API keys, unless you type one in through remote access.

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
  names or addresses.
- **Front-end server logs.** The servers in front of the relay and this
  website may keep standard access logs, which include IP addresses,
  temporarily, for security and troubleshooting.

### How long it is kept

- **A paired device** is deleted when you remove it, when it signs out, or 30
  days after it was last used.
- **A desktop** is kept until you turn remote access off on it or remove it
  from another of your devices. When the last desktop in an account is
  removed, the account and all its devices go with it.
- **Pairing codes** expire after ten minutes.

To delete everything the relay holds about you, turn remote access off on each
of your desktops (`flockdeck remote disable`). If a desktop can no longer be
reached, remove it from one of your paired devices, or email us.

### Cookies

The relay sets one cookie, on a device you pair. It keeps that device signed
in. It is strictly necessary for remote access to work, lasts up to 30 days,
and is removed when you sign out. The relay's web client also remembers three
display preferences in your browser's local storage: notifications, zoom and
fit to screen. Neither is used for tracking, and neither is shared with
anyone.

## This website

flockdeck.ai sets no cookies, runs no analytics and loads nothing from third
parties; its fonts are served from the site itself. The web server may keep
standard access logs, as described above. Downloads from dl.flockdeck.ai
may get its content delivery network's security cookie, as
[Who else is involved](#who-else-is-involved) describes.

## Why this data is used (lawful basis)

- **To provide remote access,** which you asked for: the account, desktop and
  device records, and the sign-in cookie. The basis is performance of a
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
