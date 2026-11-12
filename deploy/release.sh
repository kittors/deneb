#!/bin/sh
# Puts a release of Deneb on GitHub as a draft, signed for updates
# (design/plan/m6/client.md, "M7 · 打开自动更新"): deploy/package.sh builds
# it, MYGO_UPDATER_PRIVATE_KEY signs the Windows app's update, and the files
# go up to the draft release v<version>. A person looks at the draft and
# publishes it; installed Windows apps then update to it, the latest
# published release (cmd/deneb/update.go). .github/workflows/release.yml
# runs it for a tag pushed.
#
#   deploy/release.sh [-n] [windows] [linux]      (default both)
#
#   -n   build, sign and check, and list what would go up; upload nothing
#
# What goes up, from deploy/out/<version>/:
#   windows-amd64/  the installer, the update archive and its deltas, and
#                   update-windows-amd64.json, the manifest apps read: last,
#                   so that it never names a file not up yet
#   linux-<arch>/   the .deb packages: the window's, the engine's, the
#                   server's
#   SHA256SUMS      of the files above
# Not the Linux archives, install script, PKGBUILD and manifests that MyGo
# writes too: the window they install has no engine (the denebd .deb has
# it), and the .deb, in /opt, is updated by a newer .deb, not by itself. Not
# macOS, which waits for an Apple developer account (i1).
#
# Needs what deploy/package.sh needs, and:
#   updates in cmd/deneb/mygo.json: its publicKey, and github kittors/deneb
#   MYGO_UPDATER_PRIVATE_KEY  the secret key of that public one (mygo keygen)
#   CHANGELOG.md   a section "## <version>": the update window's notes
#   gh             signed in, or GH_TOKEN (GitHub Actions); not for -n
# In GitHub Actions the tag pushed (GITHUB_REF_NAME) must be v<version>.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
conf=$root/cmd/deneb/mygo.json
repo=kittors/deneb

fail() {
	echo "release: $*" >&2
	exit 1
}

dry=
if [ "${1:-}" = -n ]; then
	dry=1
	shift
fi
[ $# -gt 0 ] || set -- windows linux
for target in "$@"; do
	case $target in
	windows | linux) ;;
	*)
		echo "usage: $0 [-n] [windows] [linux]" >&2
		exit 2
		;;
	esac
done

version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$conf" | head -n 1)
[ -n "$version" ] || fail "no version in $conf"
tag=v$version

# Everything checked before anything is built: a release that went up
# wrong reaches every installed app.
if [ "${GITHUB_REF_TYPE:-}" = tag ] && [ "${GITHUB_REF_NAME:-}" != "$tag" ]; then
	fail "the tag ${GITHUB_REF_NAME:-} is not $tag, the version of $conf"
fi
grep -q '"updates"' "$conf" ||
	fail "no updates in $conf: the apps of this release would never update (design/plan/m6/client.md, \"M7 · 打开自动更新\")"
grep -q '"publicKey"[[:space:]]*:[[:space:]]*"[^"]' "$conf" || fail "no updates.publicKey in $conf"
grep -q "\"github\"[[:space:]]*:[[:space:]]*\"$repo\"" "$conf" || fail "updates.github in $conf is not $repo"
[ -n "${MYGO_UPDATER_PRIVATE_KEY:-}" ] ||
	fail "no MYGO_UPDATER_PRIVATE_KEY: unsigned, the release has no update for the apps installed"
# The update window shows the version's section; MyGo signs nothing without it.
ver_re=$(printf '%s' "$version" | sed 's/[.]/\\./g')
grep -Eq "^## \[?$ver_re\]?([[:space:]]|\$)" "$root/CHANGELOG.md" 2>/dev/null ||
	fail "no section \"## $version\" in CHANGELOG.md: the notes of the update window"
[ -n "$dry" ] || command -v gh >/dev/null || fail "no gh: brew install gh, or apt install gh"

"$root/deploy/package.sh" "$@"
out=$root/deploy/out/$version

files=
manifests=
for target in "$@"; do
	case $target in
	windows)
		d=$out/windows-amd64
		for f in "$d"/*-setup-*.exe "$d"/*-windows-amd64.zip "$d"/*-windows-amd64.tar.gz "$d"/*.delta; do
			[ -f "$f" ] && files="$files $f"
		done
		[ -f "$d/update-windows-amd64.json" ] || fail "no $d/update-windows-amd64.json: was it signed?"
		manifests=$d/update-windows-amd64.json
		;;
	linux)
		for f in "$out"/linux-*/*.deb; do
			[ -f "$f" ] && files="$files $f"
		done
		;;
	esac
done
[ -n "$files" ] || fail "nothing to upload in $out"

# The release's checksums: of what goes up, and nothing else.
sums=$(mktemp -d)/SHA256SUMS
for f in $files $manifests; do
	(
		cd "$(dirname "$f")"
		if command -v sha256sum >/dev/null; then sha256sum "$(basename "$f")"; else shasum -a 256 "$(basename "$f")"; fi
	)
done >"$sums"

echo "release: Deneb $version, to the draft release $tag of $repo:"
for f in $files $sums $manifests; do
	echo "  $f"
done
if [ -n "$dry" ]; then
	echo "release: -n: nothing uploaded"
	exit 0
fi

# The draft first, its notes the version's section; one there already is
# kept, its files replaced. A published release is never changed: apps
# may be updating from it.
if draft=$(gh release view "$tag" --repo "$repo" --json isDraft --jq .isDraft 2>/dev/null); then
	[ "$draft" = true ] || fail "$tag is published already: bump the version for another release"
else
	notes=$(mktemp)
	awk -v v="$version" '
		/^## / { if (on) exit; h = $2; gsub(/[][]/, "", h); on = (h == v); next }
		on
	' "$root/CHANGELOG.md" >"$notes"
	gh release create "$tag" --repo "$repo" --draft --verify-tag --title "Deneb $version" --notes-file "$notes"
fi
# shellcheck disable=SC2086 # the paths hold no spaces: deploy/out/<version>/...
gh release upload "$tag" --repo "$repo" --clobber $files "$sums"
[ -z "$manifests" ] || gh release upload "$tag" --repo "$repo" --clobber $manifests
echo "release: up in the draft $tag: look at it, then publish it (gh release edit $tag --repo $repo --draft=false); apps update to it once it is published"
