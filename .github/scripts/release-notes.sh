#!/bin/sh
# Writes the notes a release is signed and published with. Everything comes
# in through the environment, as in regen-followup.sh:
#
#   TAG                the release, v1.2.3 or v1.2.3-rc.1.
#   OUT                the file to write the notes to.
#   GITHUB_REPOSITORY  owner/name, for gh.
#   GH_TOKEN           for gh.
#
# The notes go into the signed manifest, which is what the update chip shows,
# and into the GitHub release, so both get the same text. In order:
#
#   - a release already on GitHub keeps the body it has;
#   - otherwise docs/releases/TAG.md, the customer notes written before the
#     tag was pushed;
#   - otherwise, for a candidate (v1.2.3-rc.1) that has no notes file,
#     the notes GitHub generates from the pull request titles.
#
# Only GitHub's answer that there is no such release counts as there being
# none. Any other failure to ask stops the run: taken for none, a release
# published before would be given fresh notes.
set -eu

tag=${TAG:?TAG is not set}
out=${OUT:?OUT is not set}
err=$(mktemp)
trap 'rm -f "$err"' EXIT

if gh release view "$tag" --json body --jq .body > "$out" 2> "$err"; then
	echo "$tag is already released on GitHub, and keeps its notes"
elif grep -Eq 'release not found|HTTP 404' "$err"; then
	if [ -f "docs/releases/$tag.md" ]; then
		cp "docs/releases/$tag.md" "$out"
	else
		echo "docs/releases/$tag.md does not exist, so the notes are the ones GitHub generates" >&2
		gh api "repos/$GITHUB_REPOSITORY/releases/generate-notes" -f tag_name="$tag" --jq .body > "$out"
	fi
else
	cat "$err" >&2
	echo "::error::could not ask GitHub whether $tag is released already, so nothing has been signed or published" >&2
	exit 1
fi
