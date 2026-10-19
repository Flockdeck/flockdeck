# Build and release steps

* [Container image](docker-image.md) - Multi-stage Dockerfile producing a headless Flockdeck on Alpine.
* [Helm chart](helm-chart.md) - Chart that runs the container image plus a port-proxy sidecar.
* [Linux deb and rpm packages](linux-packages.md) - Packages built by cmd/release from the nfpm config and smoke-tested in clean containers.
* [Makefile targets](makefile.md) - Local build, test, cross-compile and package targets.
* [Release artifacts and publishing](release-artifacts.md) - Files a release consists of, how they are signed, and the order they are published in.
