#!/bin/sh
# The uninstall twin of postinstall.sh: once flockdeck.desktop and the icon
# are gone, refresh the same caches so neither lingers in a menu or search
# after the package is removed.
set -e

command -v update-desktop-database >/dev/null 2>&1 &&
	update-desktop-database -q /usr/share/applications || true

command -v gtk-update-icon-cache >/dev/null 2>&1 &&
	gtk-update-icon-cache -qf /usr/share/icons/hicolor || true
