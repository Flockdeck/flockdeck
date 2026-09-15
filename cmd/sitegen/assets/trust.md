# Trust & privacy

*Last updated: 15 September 2026*

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

Flockdeck has nothing to opt into, because it collects nothing to begin with
— on the free tier, same as any other. That isn't a promise about a future
release; it's what the app already does, and the desktop app is open source,
so you can read the source and check.

## What isn't end-to-end encrypted, said plainly

Remote access is genuinely useful, and most of what it carries is never
recorded anywhere along the way — but it isn't end-to-end encrypted the way
push notifications are. Your terminal output and what you type reach the
relay over TLS, and the relay decrypts that traffic to route it to your other
devices. It doesn't record, store or log the content, but a compromise of the
relay itself could expose traffic while it's in flight. If your organisation's
rules don't allow a third party to decrypt developers' terminal sessions, run
the relay yourself: see [self-hosting the
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
- The desktop app is open source under the MIT licence:
  [github.com/jmwri/flockdeck](https://github.com/jmwri/flockdeck).

Questions, or a security report: **privacy@flockdeck.ai**.
