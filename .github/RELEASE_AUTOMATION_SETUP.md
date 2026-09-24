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
      regenerates flockdeck-docs  (docgen)
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

Set **as environment secrets, in an environment named `release`, in each of
three repositories**, not as repository or organization secrets (any workflow
on any branch could read those, and anyone who can push a branch with a
workflow that prints them would have the key):

1. **`Flockdeck/flockdeck`**: the `release` environment already exists and
   only a `v*` tag may deploy to it, which is what release.yml's `release` job
   relies on. Add the two secrets to it. The `followups` job runs in the same
   environment and gets exactly the same protection: a run that is not on a
   `v*` tag is refused before it starts.
2. **`Flockdeck/flockdeck-site`** and 3. **`Flockdeck/flockdeck-docs`**:
   Settings > Environments > New environment > `release`. Deployment
   branches and tags: **Selected branches and tags** > add `main`. Add the two
   secrets to it. `autotag.yml` runs on main only (a `workflow_run` of CI on
   main, a schedule, or a manual dispatch), so it is allowed, and a workflow on
   any other branch is not.

The same key in all three: one app, one key, held in three places. To rotate,
generate a new key, replace all three, delete the old key.

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
does not show as a red run: look for the `followups` job in the release run
(a failed step is annotated), or check that `Flockdeck/flockdeck-site` and
`flockdeck-docs` have an `auto/regen-<tag>` pull request. Re-run the job from
the run's page: it is idempotent, and does nothing where nothing differs. Or
make the pull requests by hand, as before. Tagging afterwards is automatic
either way, once the pull request has merged.

## Not covered

The same pattern for `flockdeck-remote` -> `flockdeck-relay` (a remote tag
opening a pull request that bumps the relay's `go.mod` pin, and the relay
auto-tagging) is not done here. It would reuse `autotag.sh` unchanged and add
a bump script; it needs the app installed on those two repositories too.
