# Contributor workflow: manual setup checklist

Implements `flockdeck-planning/06-contributor-workflow.md`. The workflow
files (`.github/workflows/reusable-pr-diff.yml`,
`reusable-pr-review-post.yml`, `pr-review.yml`, `pr-review-post.yml` here;
`reusable-compat-check.yml` + `compat-check.yml` in `flockdeck-relay` /
`flockdeck-remote`; `pr-review.yml` in `flockdeck-relay`, `flockdeck-remote`,
`flockdeck-site`) are all in place and will run once the following pieces —
none of which can be done from a CLI/agent session — are set up:

## 1. GitHub App (plan item (a))

Register a GitHub App under the `Flockdeck` org (Settings > Developer
settings > GitHub Apps > New GitHub App):
- Permissions: `Contents: Read`, `Pull requests: Write` on every repo it's
  installed on.
- Install it on all six repos (`flockdeck`, `flockdeck-docs`,
  `flockdeck-relay`, `flockdeck-remote`, `flockdeck-site`; skip
  `flockdeck-billing`, which has no GitHub remote yet).
- Generate a private key, and store both the App ID and the private key as
  **org-level** Actions secrets (not per-repo), per plan (a):
  - `PR_REVIEW_APP_ID`
  - `PR_REVIEW_APP_PRIVATE_KEY`
  These are read by `reusable-pr-review-post.yml`'s `review-app-id` /
  `review-app-private-key` secrets. Until they're set, the post job falls
  back to the calling job's own `GITHUB_TOKEN`, which only works for
  same-repo (non-fork) PRs.

## 2. LLM API key

`reusable-pr-review-post.yml`'s "Generate review with Gemini" step now calls
Gemini 2.5 Flash for real. What's still needed: add `PR_REVIEW_LLM_API_KEY`
(org- or repo-level, your call) as a Gemini API key from
https://aistudio.google.com/apikey. Until it's set, the review call fails
with a 401/403 and the step just logs a warning and skips posting a comment
— it won't fail the PR check.

## 3. Environment protection gate (plan b.4)

In `flockdeck` and `flockdeck-docs` (the two public repos), create a
`pr-review-external` environment (Settings > Environments) with required
reviewers, so a human approves before `pr-review-post.yml` runs for a
first-time or forked-repo contributor. Until this environment exists, the
job runs ungated.

## 4. Cross-repo reusable-workflow access (plan (c))

`flockdeck-relay`'s `reusable-compat-check.yml` is a **private** reusable
workflow being called from `flockdeck-remote`, a different private repo.
Unlike the two reusable workflows in this (public) repo, a private one needs
its repo's Settings > Actions > General > "Access" set to allow
"Repositories in the same organization can use this workflow" (or the
org-level equivalent) before `flockdeck-remote`'s `compat-check.yml` can
call it.

## 5. Reverse-direction credential for (c), and retiring both PATs

`flockdeck-remote`'s `compat-check.yml` needs a token that can read
`flockdeck-relay` (the reverse direction of today's
`FLOCKDECK_REMOTE_TOKEN`). Until the GitHub App is live and installed on
both repos, add a PAT-style secret `FLOCKDECK_RELAY_TOKEN` in
`flockdeck-remote` scoped to read `flockdeck-relay`'s contents.

Once the App (step 1) is installed on both `flockdeck-relay` and
`flockdeck-remote` with `contents: read`, per plan (a)/(c.4)/phasing step 4:
- retire `FLOCKDECK_REMOTE_TOKEN` in `flockdeck-relay`'s `ci.yml` /
  `Dockerfile` in favour of an App installation token, and
- retire `FLOCKDECK_RELAY_TOKEN` in `flockdeck-remote`'s `compat-check.yml`
  the same way.
Neither retirement is done in this change — the existing
`FLOCKDECK_REMOTE_TOKEN` usage is left untouched since removing it now,
before the App exists, would break `flockdeck-relay`'s CI.

## Everything else is already wired

- The untrusted/trusted split, `pull_request` vs `pull_request_target`
  choice per repo, and the diff-artifact hand-off are implemented and don't
  need further setup once the secrets/environment above exist.
- The compat check's trigger (`pull_request`, path-filtered to Go files) and
  the `go mod edit -replace` + relay-test-suite mechanism are implemented
  and will run as soon as `FLOCKDECK_RELAY_TOKEN` (or the App) exists.
