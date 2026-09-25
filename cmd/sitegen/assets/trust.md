# Trust & privacy

*Last updated: 25 September 2026*

This is the plain-language version of Flockdeck's privacy story: what's
collected, what isn't, and how that compares to what else is on the market.
The [privacy policy](privacy.html) has the exhaustive, legal version; this
page is the short one, for skimming or for linking to.

## Nothing to opt out of

Flockdeck's privacy design is structural, not a setting you turn on. There's
no "privacy mode," because there's nothing to turn off:

- **No accounts, no analytics, no telemetry and no crash reporting** in the
  desktop app, on the free tier or any other.
- **Spend and usage estimates are worked out on your own machine**, from what
  your agents report about their own usage, and go no further than your own
  paired devices, and only if you turn on remote access.
- **The relay never sees your API keys**, unless you type one in yourself
  through remote access, and never sees the local token the app uses on your
  own machine.
- **Push notifications are end-to-end encrypted.** Your desktop encrypts each
  one with the receiving device's own keys before it leaves your computer;
  neither the relay nor its push service can read it.
- **This website** sets no cookies, runs no analytics and loads nothing from
  anyone else.
- **Two optional features send content to a third party, TypeSafe AI**, and
  only if you turn them on: they are off by default, need a `TYPESAFE_API_KEY`
  you supply yourself, and are described in full in the [privacy
  policy](privacy.html#the-desktop-app). Everything above holds while they are
  off.
- **Downloads keep short-lived access logs.** The app, the installer scripts
  and the update check are served from dl.flockdeck.ai, which keeps standard
  access logs (IP address, path, time, response code and user agent) for 30
  days, stored privately and used only for security, abuse and fixing
  problems: not for analytics, marketing or profiling, and not shared except
  as the law requires. The desktop app itself still collects nothing, and
  none of your code or terminal content is part of a download. See the
  [privacy policy](privacy.html#downloads-and-update-checks).
- **Nobody's data is sold**, and nothing is used to train a model, anyone
  else's or Flockdeck's own — there is no model here to train.

None of that is a paid feature. It's what the free tier does, because it's
what the app does.

## Where that differs from what's on the market

As of April 2026, GitHub changed Copilot's policy: Free, Pro and Pro+ plans
now train on your prompts and code **by default** — opt-out, not opt-in. Only
Business and Enterprise plans are exempt automatically. Several other coding
assistants offer a "zero data retention" mode you can turn on, sometimes only
on a paid or team plan.

The Flockdeck app itself collects nothing to begin with — on the free tier, same as
any other — so there is no default to opt out of. That isn't a promise about
a future release; it's what the app already does, and the desktop app's source
is available, so you can read it and check.

## What is, and isn't, end-to-end encrypted, said plainly

Remote access is genuinely useful, and most of what it carries is now
end-to-end encrypted the way push notifications are: your terminal output and
what you type are encrypted with keys the relay hands out but never holds, so
the relay carries that traffic to your other devices without being able to
read it — not even a relay you run yourself.

The state of your panes still isn't: what each pane's agent has spent, its
usage limits, and which model routing chose for it reach the relay decrypted,
over TLS, so it can route them. It doesn't record, store or log any of it.

This defeats an honestly-run relay. Against one that's been actively
compromised and tampered with to swap the keys it hands out at pairing, that
alone isn't enough — a swap like that needs an out-of-band check, and Remote
access now has one: pairing shows a fingerprint on both the device and the
desktop, and comparing them by eye is what catches a relay that has swapped
keys. If your organisation's rules don't allow a third party in that position
at all, run the relay yourself: see [self-hosting the
relay](https://docs.flockdeck.ai/self-hosting/overview.html), or the coming
[Enterprise](./#enterprise) licence.

## The billing service, when it's live

A paid plan for the shared relay is coming, not live yet — see the
[FAQ](./#faq). It's already built so that the relay learns only an account's
plan and the date it's paid until, through the billing service's own
connection to it — never an email address, a name or a country. That data
stays in the billing service alone, kept only as long as UK tax law requires
sale records to be kept, then deleted.

## Read more

- [Privacy policy](privacy.html) — what's kept, why, for how long, and your
  rights over it.
- [Terms of service](terms.html)
- [Licences](licences.html) — every third-party component, with its licence
  in full.
- The desktop app's source is available for noncommercial use under the
  PolyForm Noncommercial licence:
  [github.com/Flockdeck/flockdeck](https://github.com/Flockdeck/flockdeck).

Questions, or a security report: **privacy@flockdeck.ai**.
