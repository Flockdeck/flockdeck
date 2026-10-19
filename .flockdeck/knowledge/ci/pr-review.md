---
type: CI Check
title: PR review workflows
description: Two-stage pull-request review: an unprivileged diff job, then a gated job that posts the comment.
resource: .github/workflows/pr-review-post.yml
tags: [ci, review, security]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/pr-review-post.yml]
---

`pr-review.yml` runs on pull requests (opened, synchronize, reopened) and calls `reusable-pr-diff.yml` with the PR number, head SHA and base ref to produce a `pr-review-diff` artifact. It holds no secrets.

`pr-review-post.yml` runs on `workflow_run` completion of "PR review" (only when it succeeded):

1. `check-permission` asks GitHub for the PR author's collaborator permission. `admin` or `write` is trusted; any other result, or a failed lookup, is untrusted.
2. `gate` runs only for untrusted authors and uses the `pr-review-external` environment, which needs approval.
3. `post` calls `reusable-pr-review-post.yml`, which takes the diff artifact, generates a review with an LLM API key secret and posts it, using a GitHub App installation token when the app secrets are set, else `GITHUB_TOKEN`.

The split keeps secrets away from code in a pull request. A failed permission lookup falls to the gate rather than skipping it.
