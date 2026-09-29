# Release follow-ups: manual setup checklist

After a desktop release is published, the site and the docs are regenerated
for it and **tagged**, which is what deploys them, with no manual steps. This
is what has to be set up by hand for that to run; none of it can be done from a
CLI/agent session, and no key or token belongs in a command, a file or a
message: set them where they are held, in the way the other secrets are set
(Terraform Cloud / terrawost).

## What runs, and what it needs

```
v0.3.41 tagged in Flockdeck/flockdeck
  release.yml: test, build, release            (as before; nothing changed)
  release.yml: followups  (needs: release, not for -rc tags, environment: release)
      regenerates flockdeck-site  (sitegen -release v0.3.41, signature checked)
      regenerates flockdeck-docs  (docgen; and, if any page changed, moves
          .docs-generated-from to "<tag> <commit the tag points at>", which
          the docs repo's "Generated files match" guard needs)
      if -- and only if -- either differs from its main:
          force-pushes auto/regen-v0.3.41, opens or updates ONE pull request,
          enables auto-merge (variable AUTO_MERGE_REGEN)
  ...that repo's CI ("Check the site") passes on the PR, and it merges
Flockdeck/flockdeck-site, Flockdeck/flockdeck-docs: main gets a commit
  CI passes on it on main
  autotag.yml: pushes the next patch tag (site v0.9.N+1, docs v0.1.N+1),
      only if something that ships changed since the last tag
  that tag's CI builds and pushes the bare-semver image; Flux pins it;
  the cluster rolls it out
```

Everything that reads or writes another repository uses a GitHub App
installation token, because **GitHub does not start workflows for anything
made with the default `GITHUB_TOKEN`**: a pull request opened with it would
never run the required check "Check the site" (and could not merge), and a tag
pushed with it would never run the tag-triggered image build.

## 1. A new GitHub App: the release bot

Register a **new** GitHub App under the `Flockdeck` org (Settings > Developer
settings > GitHub Apps > New GitHub App). Do **not** reuse the PR-review app
(`PR_REVIEW_APP_ID` / `PR_REVIEW_APP_PRIVATE_KEY`): that one comments on pull
requests, and this one writes to repositories. Neither should be able to do
the other's job.

- Name: e.g. `flockdeck-release-bot`. Homepage URL: anything. Webhook: **off**
  (untick "Active"). "Where can this app be installed": **Only on this
  account**.
- Repository permissions, and nothing else (Metadata: Read-only is added
  automatically):
  - **Contents: Read and write**: push the regeneration branch, and push the
    release tags.
  - **Pull requests: Read and write**: open and update the pull request,
    enable auto-merge, merge.
  - **Checks: Read-only**: only for the fallback that waits for CI where
    GitHub's own auto-merge cannot (see section 4).
  - No organization permissions. No account permissions. No events.
- Install it on **`flockdeck-site` and `flockdeck-docs` only** (Install App >
  Only select repositories). It is not installed on `flockdeck`, `flockdeck-relay`
  or `flockdeck-remote`; the followups job asks for a token for those two
  repositories and no others, for an hour, with only the permissions above.
- Generate a private key (Private keys > Generate). Keep the App ID from the
  app's page.

## 2. Secrets: names, and where

Two secrets, with these exact names. They are read by
`actions/create-github-app-token`, pinned by commit SHA in each workflow.

| Name                           | Value                          |
| ------------------------------ | ------------------------------ |
| `RELEASE_BOT_APP_ID`           | the App ID                     |
| `RELEASE_BOT_APP_PRIVATE_KEY`  | the private key (the .pem)     |

Set them in three places, and not all the same way. An environment secret can
be restricted to one branch or tag; a repository secret is readable by a
workflow on any branch, so anyone who can push a branch with a workflow that
prints it would have the key.

1. **`Flockdeck/flockdeck`: environment secrets, in `release`.** The
   environment already exists and only a `v*` tag may deploy to it, which is
   what release.yml's `release` job relies on. Add the two secrets to it. The
   `followups` job runs in the same environment and gets exactly the same
   protection: a run that is not on a `v*` tag is refused before it starts.
   This job mints the token for **both** `flockdeck-site` and `flockdeck-docs`
   itself (regeneration, pull requests, merges), so nothing about that
   depends on how the other two repositories hold secrets.
2. **`Flockdeck/flockdeck-docs`: environment secrets, in a new `release`
   environment.** Settings > Environments > New environment > `release`.
   Deployment branches and tags: **Selected branches and tags** > add `main`.
   Add the two secrets to it. `autotag.yml` runs on main only (a
   `workflow_run` of CI on main, a schedule, or a manual dispatch), so it is
   allowed, and a workflow on any other branch is not. The repository is
   public, so this works on the Free plan.
3. **`Flockdeck/flockdeck-site`: plain repository secrets** (Settings >
   Secrets and variables > Actions > Repository secrets). There is no
   environment here, see below, so its `autotag.yml` has no `environment:`
   line where docs' has one; the two files differ on that one line on purpose.
   These copies are only for the site's own auto-tag; the regeneration pull
   requests do not use them.

The same key in all three: one app, one key, held in three places. To rotate,
generate a new key, replace all three, delete the old key.

### `flockdeck-site` is private, so it holds a plain repository secret

Decided, and accepted for now: `flockdeck-site` stays private, and its copy of
the two secrets is a plain repository secret. The Flockdeck org is on the
**Free** plan, and on it a private repository has no environments (so no
deployment-branch restriction and no environment secrets) and no branch
protection or rulesets. `flockdeck-site` has none of these, as its API calls
answer "Upgrade to GitHub Pro or make this repository public". A workflow that
named a `release` environment there would fail, or run with no secrets, which
is why the site's `autotag.yml` has no `environment:` line.

**The exposure.** A repository secret is readable by a workflow on any branch,
so anyone with write access to `flockdeck-site` can push a branch whose
workflow prints the key. The key is the release bot's: it mints tokens for
every repository the app is installed on, so that access would also give
Contents and Pull requests write on **`flockdeck-docs`**, which is public and
holds docs.flockdeck.ai. Today the owner is the only person with write access
to `flockdeck-site`, which is what makes this acceptable. **Revisit it before
anyone else is given write access there.**

**How to close it**, when that changes:

1. **A second app for the site only**: Contents: Read and write, installed on
   `flockdeck-site` alone, with its own `RELEASE_BOT_APP_ID` and
   `RELEASE_BOT_APP_PRIVATE_KEY` as the site's repository secrets. A leak then
   costs the site's tags, not the docs. The regeneration pull requests still
   use the first app's token, from flockdeck's `release` environment, so this
   changes only the site's copy, which is for its `autotag.yml`.
2. **Make `flockdeck-site` public.** It is a static marketing site and privacy
   policy already served to the world, and its source is generator output.
   That gives it environments, branch protection, a required check and
   GitHub's own auto-merge (and the script's wait-for-CI fallback stops being
   needed). Then move its secrets into a `release` environment restricted to
   `main`, as docs has, and put the `environment: release` line back.

Until they are set the `followups` job's "Mint the release bot's token" step
fails and says so; nothing else is affected (see the failure modes below).

## 3. Repository settings

- **`flockdeck`, variable `AUTO_MERGE_REGEN`** (Settings > Secrets and
  variables > Actions > Variables; optional). Unset or anything but
  `false`/`0`/`no`/`off` means the generated pull requests turn on auto-merge.
  `false` means they are opened and left for a person to merge. It is read on
  each release; no code change.
- **`flockdeck-site` and `flockdeck-docs`, Settings > General > Pull
  Requests**: tick **Allow auto-merge**. (Untick nothing else.) Optionally
  tick "Automatically delete head branches", so `auto/regen-*` branches do not
  accumulate.
- **`flockdeck-docs`**: `main` already requires the check `Check the site`,
  which is what auto-merge waits for. Nothing to change.
- **Tag rules**: `flockdeck-site` and `flockdeck-docs` have no tag ruleset now.
  If one is ever added for `v*`, add the release bot as a bypass actor, or the
  auto-tag push is refused.

## 4. A note on `flockdeck-site`

`flockdeck-site` is private, on a plan that has no branch protection or
rulesets, so it has no required check for GitHub's auto-merge to wait on:
`gh pr merge --auto` there would merge at once, before CI. The script does
not depend on it. When `--auto` is refused it waits for the PR's check runs
itself (up to 30 minutes), merges only once `Check the site` has passed and
nothing has failed, and merges only the commit it waited for
(`--match-head-commit`). That is the reason for Checks: Read-only. If the site
repository is made public, or its plan gains protection, add
`Check the site` as a required status check on `main` and auto-merge takes over
without a code change.

## Trying it without a release

`release-followups-dry-run.yml` (Actions > release-followups-dry-run > Run
workflow, input `tag`) regenerates **flockdeck-docs** for an existing tag and
reports whether it differs from docs' main. It pushes nothing and needs no
secret. A release whose site and docs are up to date says
`docs is already what vX.Y.Z generates: nothing to do`; an older tag says
`docs differs from what ... generates` with the diff stat.

For the private site, and for a local run of either, the same script:

```sh
gh release download v0.3.40 -p checksums.txt -p checksums.txt.sig -D /tmp/cs
git worktree add ../fd-at-v0.3.40 v0.3.40      # the generators run from the tag
MODE=dry-run TAG=v0.3.40 TARGET=site TARGET_DIR=../flockdeck-site \
  SRC_DIR=../fd-at-v0.3.40 CHECKSUMS=/tmp/cs/checksums.txt \
  sh .github/scripts/regen-followup.sh
MODE=dry-run TAG=v0.3.40 TARGET=docs TARGET_DIR=../flockdeck-docs \
  SRC_DIR=../fd-at-v0.3.40 sh .github/scripts/regen-followup.sh
```

A dry run leaves the generated files in the target checkout (`git checkout -- .`
puts it back); it commits and pushes nothing. Run it on Linux or macOS if
you can: on a Windows checkout with `core.autocrlf` the docs repository holds
CRLF, which the script sees past (it compares what git would commit, not what
its stat cache says), but a plain `git status` there will show every file as
modified.

## If a release was not followed up

The followups job cannot fail or undo the release, so a missing follow-up
does not show as a red run. Its last step opens (or, on a repeat failure,
comments on) one issue in this repository titled `Release follow-ups failed
for vX.Y.Z`. Otherwise look for the `followups` job in the release run
(a failed step is annotated), or check that `Flockdeck/flockdeck-site` and
`flockdeck-docs` have an `auto/regen-<tag>` pull request. Re-run the job from
the run's page: it is idempotent, and does nothing where nothing differs. Or
make the pull requests by hand, as before. Tagging afterwards is automatic
either way, once the pull request has merged.

## Purging released versions

`purge-downloads.yml` takes released versions off `dl.flockdeck.ai` for good:
out of the bucket, and out of the CDN caches in front of it. Use it when a
release must no longer be downloadable from the site at all, such as the
releases published before the relicensing. It is not how a bad release is
withdrawn from the updater: that is `recalled.json` (see
`internal/selfupdate/site.go`), and the script refuses any version
`recalled.json`, `releases.json` or `latest.json` names.

### Running it

Always from `main`, and always a dry run first. A dry run is the default. It
reads the bucket and reports every file it would delete and every address it
would purge, and it changes nothing. It also checks the CDN credentials (see
"The credential preflight" below), so a bad token shows here and not in the
live run:

```sh
gh workflow run purge-downloads.yml --ref main -f versions="v0.2.10 v0.2.11"
```

Read the run's summary. Then run it live, with the count typed out exactly.
N is how many *different* versions are named:

```sh
gh workflow run purge-downloads.yml --ref main -f versions="v0.2.10 v0.2.11" \
  -f dry_run=false -f confirm="purge 2 versions"
```

A long list is easiest from a file: `-f versions="$(cat versions.txt)"`,
with one version to a line. A run takes at most 100 versions. Every run,
dry or live, waits for a reviewer to approve it in the `purge-downloads`
environment, and takes turns with a release in the `release` concurrency
group. A run that fails can be run again with the same versions. A version
already gone from the bucket is not an error: it is purged from the caches
again, by prefix, and checked gone.

By hand, the same script runs against the bucket with the same settings as
environment variables (`scripts/purge-downloads.sh`, whose header lists them):
`sh scripts/purge-downloads.sh v0.2.10 v0.2.11`, which is a dry run, and then
`--live --confirm "purge 2 versions"`.

### What it does, and what it does not

Before anything is deleted, a run (dry or live) checks the CDN credentials, as
described under "The credential preflight". Then, for each version, it:

1. lists every version of every file under `<version>/`, and every delete
   marker. The bucket is versioned, so a plain delete would only hide the
   bytes behind a marker, where they would stay for 30 days;
2. deletes each of them by version id, and then lists the prefix again: anything
   still there fails the run;
3. purges each deleted file from DigitalOcean's CDN by its path, and the
   version by `<version>/*` as well, which is what serves dl.flockdeck.ai;
4. purges each file from a Cloudflare zone of ours by its exact URL, 30 to a
   request, only if the run says `cloudflare=true`. It is off by default,
   because there is no such zone today; and
5. requests every exact URL at `https://dl.flockdeck.ai` and fails unless each
   answers 403 (the bucket is private, so a missing file is 403, not 404).

Everything under a version was uploaded as `immutable` and cached for a year,
which is why steps 3 to 5 exist. Deleting from the bucket alone would leave the
files downloadable from the edges for up to a year.

It does **not**:

- touch GitHub releases or tags. Those are separate, protected by the
  `release tags` ruleset in terrawost, and already removed for the MIT-era
  versions;
- touch `latest.json`, `/latest/`, or any version it was not given;
- remove a copy anyone has already downloaded, or a mirror, a package cache
  or an archive someone else keeps;
- withdraw the licence those copies were released under. Whatever was
  published under MIT stays MIT for whoever holds a copy. This only stops
  distributing it from here.

### The credential preflight

The purge deletes from the bucket first and purges the CDN second, and the
delete cannot be undone. So before it deletes anything, in a dry run as well as
a live one, it asks each CDN API a read-only question, with the token in a
header file as the purge does (it is never on a command line or in the
output). The first pilot showed why: DigitalOcean rejected `DO_API_TOKEN` with
HTTP 401 only *after* a version had been deleted, so the files were gone from
the bucket and still served from the caches, and the dry run before it had
passed, since a dry run never talked to DigitalOcean. The preflight would have
refused that run, and the dry run, before anything was deleted.

| Answer to `GET /v2/cdn/endpoints/<id>` | The run |
| --- | --- |
| 200 with an endpoint | goes on (`cdn_preflight`: `ok`) |
| 401 | **refused**: DigitalOcean does not accept the token |
| 403 | goes on (`ok-scope-limited`): the token is valid but may not read the endpoint |
| 404 | **refused**: `DO_CDN_ENDPOINT_ID` is wrong |
| anything else, a timeout, a 200 that is not an endpoint | **refused** (fail closed), saying what came back |

With a Cloudflare zone, the same is asked of `GET /user/tokens/verify` (the
token must be active; 401 or 403 refuses) and `GET /zones/<id>` (200 goes on;
403 goes on as `ok-scope-limited`, since a Cache Purge token cannot read a
zone; 404 and anything else refuse). `--no-cdn-purge` skips the preflight, and
`--no-cloudflare` skips Cloudflare's part. A dry run that lacks a CDN setting a
live run needs says `not-checked`, and checks nothing. The result is in the
run's output and in its job summary and JSON as `cdn_preflight`: `ok`,
`ok-scope-limited`, `refused`, `not-checked` or `skipped`.

**A 403 is expected of a token that has only the delete scope.** DigitalOcean's
API reference says reading an endpoint needs `cdn:read` and purging its cache
needs `cdn:delete`; it lists no 403, only 401, 404, 429 and 500, and the
scopes page says that adding a non-read scope also requires the resource's read
scope, with `cdn:read` among those `cdn:delete` requires. So a token made in
the control panel with `cdn:delete` should carry `cdn:read` too, and the GET
should be a 200; a 403 is what a token without it is reported to get, and is
accepted. Neither has been seen against the real API, since no real credential
is used to develop this.

**What a preflight cannot prove**, and says so:

- that the DigitalOcean token may *purge* the cache. Only a purge proves
  `cdn:delete`. A 200 proves `cdn:read` and a 403 proves nothing about it;
- that the Cloudflare token may purge the zone, or (on a 403) that the zone id
  is right;
- that the Spaces key may *delete*. Every run lists the bucket first, which
  proves list and read; only a delete proves delete.

**First use of a new key or token: pilot one version.** Make the first live run
with a new Spaces key or API token a single version you are sure of, and read
the result, before purging the rest. That is the one check that proves the
delete and purge rights, and it costs one version if they are wrong.

### Troubleshooting

**`DigitalOcean rejected DO_API_TOKEN (HTTP 401)`** (before a delete, in the
dry run or the live run, saying nothing has been deleted). DigitalOcean does
not accept the token at all: it has expired or been revoked, or it was pasted
with a trailing space or a line break, or truncated, or the secret holds the
wrong value (another token, or the Spaces secret). Make a new token, with an
expiry, and store it whole in the `purge-downloads` environment secret
`DO_API_TOKEN`. Then dry-run again. A token missing a scope is not a 401.

**The bucket delete worked and the purge failed** (the run reports `FAILED`
after deleting, for example a 401 from a run before the preflight existed, or
a 5xx): the files are gone from the bucket but the caches may still serve
them. Fix the credential, and run the *same versions* again. A version with
nothing left in the bucket is not an error: it is purged from DigitalOcean by
prefix (`<version>/*`), from Cloudflare by prefix if it is enabled, and its
`checksums.txt` and `manifest.json`, which every release has, are requested
until they answer 403 or 404. The run succeeds only when they do.

### One-time setup (the owner)

None of this can be done from this repository or an agent session. It is
Terraform in terrawost, applied by the owner, plus two things made by hand.

**1. The `purge-downloads` environment in `Flockdeck/flockdeck`.** Settings >
Environments > New environment > `purge-downloads`:

- Required reviewers: the owner. Leave "Prevent self-review" **off**, or the
  owner cannot approve their own run. Untick "Allow administrators to bypass".
- Deployment branches and tags: **Selected branches and tags** > add the
  branch `main`, and nothing else. The workflow refuses any other ref too.

It is a new environment, not `release`, because `release` only lets a `v*`
tag deploy, and a purge runs from `main`. It must not share `release`'s
secrets either: the signing key has no business in a purge.

**2. Its secrets.** Each is the least that works:

| Secret | What, and its scope |
| --- | --- |
| `DO_SPACES_KEY`, `DO_SPACES_SECRET` | A **new** Spaces key, `flockdeck-downloads-purge`, with a grant on the bucket `flockdeck-downloads` alone, `readwrite` (read, list, write and delete; Spaces has no delete-only grant). Not the release's key, so it can be revoked on its own when the purge is done. |
| `DO_SPACES_BUCKET`, `DO_SPACES_REGION` | `flockdeck-downloads`, `lon1` |
| `DO_API_TOKEN` | A DigitalOcean API token with **custom scopes: `cdn:delete` only**. That is the scope the cache-purge endpoint requires. `cdn:update` does not cover it. (DigitalOcean adds the read scopes `cdn:delete` requires, such as `cdn:read`, so the preflight's GET may well be a 200; a 403 is accepted too. See "The credential preflight".) Give it an expiry. The DigitalOcean provider cannot create API tokens, so make it by hand (API > Tokens > Generate, Custom scopes), and pass it to Terraform as a sensitive variable. |
| `DO_CDN_ENDPOINT_ID` | The id of `digitalocean_cdn.downloads`, the endpoint in front of the bucket. It is not secret, but it is read the same way as the rest. |
| `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID` | Only if dl.flockdeck.ai is in a Cloudflare zone of Flockdeck's own. A token limited to **Zone > Cache Purge > Purge** on that zone only, and the zone's id. See the note below: as far as this repository can tell there is no such zone, and then these are left unset, which is why the `cloudflare` input is off by default. |

**On Cloudflare.** dl.flockdeck.ai answers `Server: cloudflare` and
`cf-cache-status`, but flockdeck.ai's nameservers are DigitalOcean's, and
`dl` is a CNAME to `flockdeck-downloads.lon1.cdn.digitaloceanspaces.com`.
DigitalOcean's Spaces CDN is itself served through Cloudflare, so those
headers come from DigitalOcean's CDN, and DigitalOcean's purge (step 3) is
what empties it. If that holds, there is no zone to hold a token for.
So `cloudflare` is off by default, and step 5 still checks what anyone is
actually served. If a Cloudflare zone of Flockdeck's own is ever put in front
of it, add the two secrets and turn `cloudflare` on. Cloudflare's docs now
allow purging by URL and by prefix on every plan, up to 100 URLs a request.
The script sends 30, the older Free-plan limit, and uses a prefix only for a
version whose files are already gone from the bucket.

**3. The Terraform.** A **proposal** for `svc/flockdeck-site` in terrawost, in
the style of `release.tf`. It is not applied by anything here. The owner
adapts it and applies it:

```hcl
# purge.tf -- PROPOSAL, adapt before applying.
#
# The secrets Flockdeck/flockdeck's purge-downloads workflow purges released
# versions from dl.flockdeck.ai with, in an environment of their own: only
# main may deploy to it, and the owner approves every run. Not the release
# environment, which only a v* tag may deploy to and which holds the signing
# key.

data "github_user" "owner" {
  username = "jmwri" # check: the owner's GitHub login
}

resource "github_repository_environment" "purge_downloads" {
  repository          = "flockdeck"
  environment         = "purge-downloads"
  can_admins_bypass   = false
  prevent_self_review = false # the owner starts the run and approves it

  reviewers {
    users = [data.github_user.owner.id]
  }

  deployment_branch_policy {
    protected_branches     = false
    custom_branch_policies = true
  }
}

resource "github_repository_environment_deployment_policy" "purge_downloads_main" {
  repository     = "flockdeck"
  environment    = github_repository_environment.purge_downloads.environment
  branch_pattern = "main"
}

# Its own key, scoped to the downloads bucket as downloads_ci is, so that it
# can be revoked the day the purge is done without touching releases.
resource "digitalocean_spaces_key" "downloads_purge" {
  name = "flockdeck-downloads-purge"

  grant {
    bucket     = digitalocean_spaces_bucket.downloads.name
    permission = "readwrite"
  }
}

# Made by hand (the provider cannot make API tokens): custom scopes,
# cdn:delete only, with an expiry.
variable "purge_do_api_token" {
  type      = string
  sensitive = true
}

# One resource each, as release.tf writes its secrets.
resource "github_actions_environment_secret" "purge_spaces_key" {
  repository  = "flockdeck"
  environment = github_repository_environment.purge_downloads.environment
  secret_name = "DO_SPACES_KEY"
  value       = digitalocean_spaces_key.downloads_purge.access_key
}

resource "github_actions_environment_secret" "purge_spaces_secret" {
  repository  = "flockdeck"
  environment = github_repository_environment.purge_downloads.environment
  secret_name = "DO_SPACES_SECRET"
  value       = digitalocean_spaces_key.downloads_purge.secret_key
}

resource "github_actions_environment_secret" "purge_spaces_bucket" {
  repository  = "flockdeck"
  environment = github_repository_environment.purge_downloads.environment
  secret_name = "DO_SPACES_BUCKET"
  value       = digitalocean_spaces_bucket.downloads.name
}

resource "github_actions_environment_secret" "purge_spaces_region" {
  repository  = "flockdeck"
  environment = github_repository_environment.purge_downloads.environment
  secret_name = "DO_SPACES_REGION"
  value       = digitalocean_spaces_bucket.downloads.region
}

resource "github_actions_environment_secret" "purge_do_api_token" {
  repository  = "flockdeck"
  environment = github_repository_environment.purge_downloads.environment
  secret_name = "DO_API_TOKEN"
  value       = var.purge_do_api_token
}

resource "github_actions_environment_secret" "purge_cdn_endpoint" {
  repository  = "flockdeck"
  environment = github_repository_environment.purge_downloads.environment
  secret_name = "DO_CDN_ENDPOINT_ID"
  value       = digitalocean_cdn.downloads.id
}

# Only if dl.flockdeck.ai is ever in a Cloudflare zone of Flockdeck's own,
# which needs the cloudflare provider in providers.tf (none today). With
# provider 4.x:
#
# data "cloudflare_api_token_permission_groups" "all" {}
#
# resource "cloudflare_api_token" "purge_downloads" {
#   name = "flockdeck-downloads-purge"
#   policy {
#     permission_groups = [data.cloudflare_api_token_permission_groups.all.zone["Cache Purge"]]
#     resources         = { "com.cloudflare.api.account.zone.${var.cloudflare_zone_id}" = "*" }
#   }
# }
#
# ...and two more secrets as above: CLOUDFLARE_API_TOKEN from
# cloudflare_api_token.purge_downloads.value, and CLOUDFLARE_ZONE_ID.
```

Check `prevent_self_review` and `can_admins_bypass` against the pinned github
provider (6.13) before applying; neither has been planned here.

Once the purge is done, destroy the Spaces key and revoke the API token, or
leave both in place with the environment's reviewers as the guard.

## Not covered

The same pattern for `flockdeck-remote` -> `flockdeck-relay` (a remote tag
opening a pull request that bumps the relay's `go.mod` pin, and the relay
auto-tagging) is not done here. It would reuse `autotag.sh` unchanged and add
a bump script; it needs the app installed on those two repositories too.
