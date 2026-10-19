---
type: Build Step
title: Linux deb and rpm packages
description: Packages built by cmd/release from the nfpm config and smoke-tested in clean containers.
resource: .github/scripts/linux-package-smoke.sh
tags: [linux, packaging]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/scripts/linux-package-smoke.sh]
---

`cmd/release` builds the packages (see the [release tool](/tools/release.md)) from the files under `build/linux/` and `build/appicon.png`. The installed files are the `flockdeck` binary in `/usr/bin`, a `.desktop` entry, a 512x512 icon, and `LICENSE` and `THIRD-PARTY-NOTICES.md` under `/usr/share/doc/flockdeck`.

`.github/scripts/linux-package-smoke.sh <deb|rpm> <package> <version>` installs the package in a bare distro image as root, checks that each file exists with the expected mode and that the version matches, removes the package, and checks that nothing of Flockdeck is left behind.

CI: [linux-packages workflow](/ci/linux-packages.md).
