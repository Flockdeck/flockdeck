#!/usr/bin/env bash
# Tests verify-cache.sh with package files made here (the real dpkg-deb) and an
# apt-cache that lists the one hash it is told to and records what it is asked.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'chmod -R u+rwx "$work" 2>/dev/null; rm -rf "$work"' EXIT
export SUDO=""
mkdir -p "$work/bin"
export PATH="$work/bin:$PATH"
export CALLS="$work/calls"

# Lists LISTED_HASH for every `show -- SPEC` whose SPEC does not start with a
# dash, and writes every argument list it gets to $CALLS.
cat > "$work/bin/apt-cache" <<'FAKE'
#!/usr/bin/env bash
echo "$*" >> "$CALLS"
[ "$1" = show ] && [ "$2" = -- ] && echo "SHA256: $LISTED_HASH"
exit 0
FAKE
chmod +x "$work/bin/apt-cache"

fail=0
check() {
	local what=$1
	shift
	if "$@"; then echo "ok: $what"; else echo "FAIL: $what"; fail=1; fi
}

# mkdeb NAME VERSION ARCH FILE
mkdeb() {
	local d="$work/pkg"
	rm -rf "$d"
	mkdir -p "$d/DEBIAN"
	printf 'Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: t <t@example.com>\nDescription: t\n' "$1" "$2" "$3" > "$d/DEBIAN/control"
	dpkg-deb --nocheck --build "$d" "$4" > /dev/null 2>&1
}

fresh() {
	cache="$work/cache"
	rm -rf "$cache"
	mkdir -p "$cache/partial"
	: > "$CALLS"
}

verify() { bash "$here/verify-cache.sh" "$cache" > "$work/out" 2>&1; }

# A genuine file is kept, a changed one is removed.
fresh
good="$cache/hello_1.0-1_amd64.deb"
mkdeb hello 1.0-1 amd64 "$good"
export LISTED_HASH=$(sha256sum "$good" | awk '{print $1}')
verify
status=$?
check "a listed file is kept" test -f "$good"
check "and the script exits 0" test "$status" -eq 0
echo x >> "$good"
verify
check "a file whose hash is not listed is removed" test ! -e "$good"

# A package name that apt-cache would take for an option never reaches it.
fresh
evil='-oDir::Etc::sourcelist=/x'
d="$work/evil"
rm -rf "$d"
mkdir -p "$d/DEBIAN"
printf 'Package: %s\nVersion: 1.0\nArchitecture: amd64\nMaintainer: t <t@example.com>\nDescription: t\n' "$evil" > "$d/DEBIAN/control"
dpkg-deb --nocheck --build "$d" "$cache/${evil}_1.0_amd64.deb" > /dev/null 2>&1
export LISTED_HASH=$(sha256sum "$cache/${evil}_1.0_amd64.deb" | awk '{print $1}')
verify
check "a package named like an option is removed" test -z "$(ls "$cache" | grep -- '^-o')"
check "apt-cache is not asked about it" test ! -s "$CALLS"

# A file that cannot be removed fails the script.
fresh
mkdeb hello 1.0-1 amd64 "$cache/hello_1.0-1_amd64.deb"
export LISTED_HASH=0000
mkdir "$work/stuck"
printf '#!/usr/bin/env bash\nexit 1\n' > "$work/stuck/rm"
chmod +x "$work/stuck/rm"
PATH="$work/stuck:$PATH" verify
status=$?
check "a file that cannot be removed makes it exit non-zero" test "$status" -ne 0

# Folders and links are removed, and partial is not.
fresh
mkdir -p "$cache/sub/deeper" "$cache/dir.deb"
touch "$cache/sub/deeper/f"
ln -s /etc "$cache/link"
verify
check "a folder is removed" test ! -e "$cache/sub"
check "a link is removed" test ! -L "$cache/link"
check "a folder named .deb is removed" test ! -e "$cache/dir.deb"
check "partial is kept" test -d "$cache/partial"

# A genuine file under another file name is removed, though its hash is listed.
fresh
mkdeb hello 1.0-1 amd64 "$cache/other_2.0_amd64.deb"
export LISTED_HASH=$(sha256sum "$cache/other_2.0_amd64.deb" | awk '{print $1}')
verify
check "a file under another package's name is removed" test ! -e "$cache/other_2.0_amd64.deb"

# A version with an epoch is written with %3a.
fresh
mkdeb hello 2:1.0-1 amd64 "$cache/hello_2%3a1.0-1_amd64.deb"
export LISTED_HASH=$(sha256sum "$cache/hello_2%3a1.0-1_amd64.deb" | awk '{print $1}')
verify
check "an epoch in the version is written %3a" test -f "$cache/hello_2%3a1.0-1_amd64.deb"

exit $fail
