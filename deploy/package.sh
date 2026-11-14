#!/bin/sh
# Builds a release of Deneb into deploy/out/<version>/, for a GitHub
# Release (design/plan/m6/client.md, "打包"): the window and its engine,
# all of the version of cmd/deneb/mygo.json, so that the window can tell
# whether its engine is the one it ships.
#
#   windows  windows-amd64/: MyGo's installer, per user, unsigned, with the
#            engine among the app's files (denebd.exe, wintun.dll beside
#            Deneb.exe), which the window installs as the service
#            (deploy/windows)
#   linux    linux-<arch>/, amd64 and arm64: the window's deneb_<v>_<arch>.deb
#            (MyGo's, in /opt/deneb), which depends on the engine's
#            denebd_<v>_<arch>.deb; and deneb-server_<v>_<arch>.deb for
#            servers (deploy/linux/build-deb.sh)
#
#   macos    native Apple silicon or Intel .app and .dmg, with its engine;
#            the app installs its root-owned launchd service after macOS
#            administrator authentication. Build this target on macOS.
#
#   deploy/package.sh [macos] [windows] [linux] (default windows linux)
#
# The app's files hold Deneb's LICENSE and THIRD_PARTY_NOTICES, the
# licenses of everything its programs link (deploy/notices.sh); the
# engine's packages hold theirs.
#
# Environment:
#   WINTUN_DIR   the wintun/ folder of wintun-$WINTUN_VERSION.zip from
#                https://www.wintun.net (default
#                ~/deneb-deps/wintun-$WINTUN_VERSION/wintun)
#   MYGO_UPDATER_PRIVATE_KEY  the key that signs updates (mygo keygen);
#                without it, or without updates in mygo.json, the apps
#                build without the files that update them
#   TAGS         more build tags for the engine; never denebtest: test
#                builds are made apart (test/topo/.accept/) and never
#                released, which deploy/check-release.sh makes sure of
#   DENEB_UPDATE_FEED  where the apps look for updates: the download
#                site's folder of signed manifests, "https://<site>/download/"
#                (deploy/updates.md). Default: from WEB_HOST in
#                deploy/web/site.hkspare.env when that file is there, which
#                keeps the site's address out of the repository; with
#                neither, the apps update from GitHub releases
#
# Windows installers need NSIS's makensis; without it MyGo leaves the
# installer out and says so. The Debian packages of the engine are made
# with dpkg-deb in Docker.
set -eu
# makensis aborts at once (std::bad_alloc) without a UTF-8 locale, as in a
# shell that never set LANG; give it one.
case "${LC_ALL:-${LANG:-}}" in
*UTF-8* | *utf8* | *UTF8*) ;;
*) export LC_ALL=en_US.UTF-8 LANG=en_US.UTF-8 ;;
esac
root=$(cd "$(dirname "$0")/.." && pwd)
app=$root/cmd/deneb
res=$app/resources
version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$app/mygo.json" | head -n 1)
[ -n "$version" ] || { echo "package: no version in $app/mygo.json" >&2; exit 1; }
tags=${TAGS:-}
case ",$(printf '%s' "$tags" | tr ' ' ',')," in
*,denebtest,*)
	echo "package: TAGS has denebtest: a test build is never released; test machines get theirs apart (test/topo/.accept/)" >&2
	exit 1
	;;
esac
wintun_version=${WINTUN_VERSION:-0.14.1}
wintun=${WINTUN_DIR:-$HOME/deneb-deps/wintun-$wintun_version/wintun}
out=$root/deploy/out/$version
[ $# -gt 0 ] || set -- windows linux

platforms=
staged=
# What this script puts among the app's resources goes again when it ends,
# however it ends: they are for the packages, not for `mygo dev`.
cleanup() {
	for p in $staged; do
		rm -f "$res/$p/denebd" "$res/$p/denebd.exe" "$res/$p/wintun.dll" "$res/$p/LICENSE.txt" "$res/$p/THIRD_PARTY_NOTICES.txt"
		rmdir "$res/$p" 2>/dev/null || true
	done
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# notices goos goarch [-f name=file]... package...: the platform's
# LICENSE.txt and THIRD_PARTY_NOTICES.txt among the app's resources.
notices() {
	goos=$1 goarch=$2
	shift 2
	dir=$res/$goos-$goarch
	mkdir -p "$dir"
	staged="$staged $goos-$goarch"
	cp "$root/LICENSE" "$dir/LICENSE.txt"
	GOOS=$goos GOARCH=$goarch "$root/deploy/notices.sh" ${tags:+-tags "$tags"} "$@" >"$dir/THIRD_PARTY_NOTICES.txt"
}

for target in "$@"; do
	case $target in
	macos)
		[ "$(go env GOHOSTOS)" = darwin ] || {
			echo "package: macos needs macOS for app signing and the disk image" >&2
			exit 1
		}
		arch=$(go env GOHOSTARCH)
		notices darwin "$arch" ./cmd/deneb ./cmd/denebd
		(cd "$root" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build ${tags:+-tags "$tags"} -trimpath \
			-ldflags "-X main.version=$version" -o "$res/darwin-$arch/denebd" ./cmd/denebd)
		"$root/deploy/check-release.sh" "$res/darwin-$arch/denebd"
		platforms="$platforms,darwin/$arch"
		;;
	windows)
		[ -f "$wintun/bin/amd64/wintun.dll" ] || {
			echo "package: no $wintun/bin/amd64/wintun.dll: unpack wintun-$wintun_version.zip from https://www.wintun.net there, or set WINTUN_DIR" >&2
			exit 1
		}
		notices windows amd64 -f "Wintun $wintun_version (wintun.dll)=$wintun/LICENSE.txt" ./cmd/deneb ./cmd/denebd
		# Beside Deneb.exe, not in a folder: see bundledEngine.
		(cd "$root" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ${tags:+-tags "$tags"} -trimpath \
			-ldflags "-X main.version=$version" -o "$res/windows-amd64/denebd.exe" ./cmd/denebd)
		"$root/deploy/check-release.sh" "$res/windows-amd64/denebd.exe"
		cp "$wintun/bin/amd64/wintun.dll" "$res/windows-amd64/wintun.dll"
		command -v makensis >/dev/null || echo "package: no makensis (brew install makensis, apt install nsis): no Windows installer this time" >&2
		platforms="$platforms,windows/amd64"
		;;
	linux)
		for arch in amd64 arm64; do
			notices linux $arch ./cmd/deneb
		done
		platforms="$platforms,linux/amd64,linux/arm64"
		engine_debs=1
		;;
	*)
		echo "usage: $0 [macos] [windows] [linux]" >&2
		exit 2
		;;
	esac
done

# The update feed goes into the app at link time (cmd/deneb/update.go,
# updateFeed): MyGo adds MYGO_LDFLAGS after its own -ldflags.
feed=${DENEB_UPDATE_FEED:-}
if [ -z "$feed" ] && [ -f "$root/deploy/web/site.hkspare.env" ]; then
	feed=$(sed -n 's/^WEB_HOST=//p' "$root/deploy/web/site.hkspare.env" | tr -d '"' | head -n 1)
	[ -z "$feed" ] || feed=https://$feed/download/
fi
case $feed in
"" | https://*/) ;;
*) echo "DENEB_UPDATE_FEED must be https://<site>/<folder>/, not $feed" >&2; exit 2 ;;
esac
if [ -n "$feed" ]; then
	echo "the apps look for updates at the download site (DENEB_UPDATE_FEED)"
else
	echo "no DENEB_UPDATE_FEED: the apps look for updates in GitHub releases"
fi

# MyGo replaces each platform's folder of $out with what it builds.
(cd "$root" && MYGO_LDFLAGS="${MYGO_LDFLAGS:+$MYGO_LDFLAGS }${feed:+-X main.updateFeed=$feed}" go tool mygo build -platform "${platforms#,}" -o "$out" cmd/deneb)
if [ -n "${engine_debs:-}" ]; then
	# The engine's packages beside the window's, and in the release's
	# checksums, named as MyGo names its files there, once.
	debs=$(VERSION=$version TAGS=$tags "$root/deploy/linux/build-deb.sh" amd64 arm64) || exit 1
	sums=$out/SHA256SUMS
	touch "$sums"
	for deb in $debs; do
		name=${deb##*/}
		arch=${name##*_}
		arch=${arch%.deb}
		mv "$deb" "$out/linux-$arch/"
		sum=$( (command -v sha256sum >/dev/null && sha256sum "$out/linux-$arch/$name" || shasum -a 256 "$out/linux-$arch/$name") | cut -d' ' -f1)
		grep -v "  $name\$" "$sums" >"$sums.new" || true
		printf '%s  %s\n' "$sum" "$name" >>"$sums.new"
		mv "$sums.new" "$sums"
	done
fi
# Nothing built for tests in the release, whatever brought it in.
"$root/deploy/check-release.sh" "$out"
echo "package: Deneb $version in $out"
