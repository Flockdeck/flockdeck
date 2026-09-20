#!/bin/sh
# Run by the .deb/.rpm after flockdeck.desktop and the hicolor icon are laid
# down, so a desktop environment picks both up immediately instead of only
# after its own periodic rescan. Neither tool is guaranteed to be installed --
# a minimal or non-desktop machine has no menu to update anyway -- so a
# missing one is not an install failure.
set -e

command -v update-desktop-database >/dev/null 2>&1 &&
	update-desktop-database -q /usr/share/applications || true

command -v gtk-update-icon-cache >/dev/null 2>&1 &&
	gtk-update-icon-cache -qf /usr/share/icons/hicolor || true
