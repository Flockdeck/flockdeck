# CI checks

* [agent-compat workflow](agent-compat.md) - Weekly check that the built-in agent catalog still matches the real agent CLIs and model APIs.
* [deploy-drift workflow](deploy-drift.md) - Daily check that code merged to main in the Flockdeck repositories has been tagged for deploy.
* [design-tokens workflow](design-tokens.md) - Contrast and colour-vision checks on the design token files.
* [docker-image workflow](docker-image.md) - Builds, scans, signs and pushes the multi-arch container image on a version tag.
* [flaky-soak workflow](flaky-soak.md) - Nightly repeated test runs of the concurrency-heavy packages, with an issue reporting flakes.
* [helm-chart workflow](helm-chart.md) - Lints and renders the Helm chart and validates the manifests.
* [linux-packages workflow](linux-packages.md) - Builds the deb and rpm packages and installs them in clean containers.
* [PR review workflows](pr-review.md) - Two-stage pull-request review: an unprivileged diff job, then a gated job that posts the comment.
* [purge-downloads workflow](purge-downloads.md) - Manual workflow that deletes published release versions from the download site.
* [release-followups-dry-run workflow](release-followups-dry-run.md) - Manual dry run of the docs regeneration that follows a release.
* [release workflow](release.md) - Tag-triggered pipeline that tests, builds, signs and publishes a release.
* [test workflow](test.md) - Format, vet, test and race checks on three operating systems.
