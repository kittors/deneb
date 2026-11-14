#!/bin/sh
# Puts a release on the download site, the web app's /download/ on hk-spare
# (deploy/updates.md): the installers under names that stay the same from
# version to version, which the web app's download buttons link to, and the
# signed update archives and manifests the installed apps read.
#
#   deploy/web/publish-downloads.sh [-n] <version>
#
#   -n   check everything and list what would go up; upload nothing
#
# From deploy/out/<version>/, as deploy/release.sh -n macos windows leaves it
# on a Mac (DENEB_UPDATE_FEED set, or WEB_HOST in the site file, so that
# the apps look for updates here):
#   darwin-<arch>/   the .dmg, the update archive, its deltas, the manifest
#   windows-amd64/   the setup .exe, the update archive, its deltas, the
#                    manifest
#   linux-<arch>/    the .deb packages; when there is no such folder, those
#                    of the GitHub release v<version>, drafts included (gh)
#
# On the site, in DOWNLOAD_DIR:
#   Deneb.dmg, Deneb-Setup.exe       the Mac (Apple silicon) and Windows
#                                    installers
#   deneb.deb, denebd.deb,           the Linux app, its engine, and the
#   deneb-server.deb                 engine for servers, amd64; the arm64
#                                    ones end in -arm64.deb
#   each file also under its own versioned name
#   update-<os>-<arch>.json          last: its URLs point at the site
#
# Nothing is overwritten that the apps may be reading: every file goes up
# under a temporary name and is renamed in place, after its SHA-256 on the
# site has been compared with the one here; no manifest changes before every
# file it names is on the site. (ssh -n everywhere: in the loop that reads
# the list, an ssh reading stdin ate the list on 11-14, and the manifests
# went up naming archives that were not there.) The manifest a version replaces
# is kept as update-<os>-<arch>.before-<version>.json. Signatures are
# checked with the key of cmd/deneb/mygo.json before anything goes up, and
# again on the site's copies, downloaded over HTTPS, at the end.
set -eu
cd "$(dirname "$0")/../.."
dry=
if [ "${1:-}" = -n ]; then
	dry=1
	shift
fi
version=${1:?usage: deploy/web/publish-downloads.sh [-n] <version>}
SITE=${SITE:-deploy/web/site.hkspare.env}
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_HOST:?in $SITE}"
HOST=${HOST:-hk-spare}
DOWNLOAD_DIR=${DOWNLOAD_DIR:-/srv/deneb-web/download}
base=https://$WEB_HOST/download/
out=deploy/out/$version
[ -d "$out" ] || {
	echo "no $out: build the release first (deploy/release.sh -n macos windows)" >&2
	exit 1
}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
verify() { go run deploy/web/verify-update.go "$@"; }

# list holds what goes up: "<local file> <name on the site>" a line.
list=$stage/list
: >"$list"
add() { printf '%s %s\n' "$1" "$2" >>"$list"; }
manifests=

# The signed update of a platform: its archive and deltas under their own
# names, and its manifest, its URLs moved to the site, for the end.
update() {
	dir=$1 plat=$2
	m=$dir/update-$plat.json
	[ -f "$m" ] || {
		echo "no $m: the release was built without its signing key" >&2
		exit 1
	}
	verify "$m" "$dir"
	python3 -I - "$m" "$stage/update-$plat.json" "$base" <<'EOF'
import json, sys, posixpath
src, dst, base = sys.argv[1:]
m = json.load(open(src, encoding="utf-8"))
m["url"] = base + posixpath.basename(m["url"])
for d in m.get("deltas") or []:
    d["url"] = base + posixpath.basename(d["url"])
m.pop("previous", None)
json.dump(m, open(dst, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
EOF
	python3 -I - "$stage/update-$plat.json" <<'EOF' | while read -r f; do add "$dir/$f" "$f"; done
import json, sys, posixpath
m = json.load(open(sys.argv[1], encoding="utf-8"))
for s in [m] + (m.get("deltas") or []):
    print(posixpath.basename(s["url"]))
EOF
	verify "$stage/update-$plat.json" "$dir"
	manifests="$manifests $plat"
}

# one finds the single file matching a pattern in a folder.
one() {
	set -- $1
	[ $# = 1 ] && [ -f "$1" ] || {
		echo "expected one file, found: $*" >&2
		exit 1
	}
	printf '%s' "$1"
}

for dir in "$out"/darwin-*; do
	[ -d "$dir" ] || continue
	arch=${dir##*/darwin-}
	f=$(one "$dir/*.dmg")
	add "$f" "${f##*/}"
	[ "$arch" = arm64 ] && add "$f" Deneb.dmg
	update "$dir" "darwin-$arch"
done
if [ -d "$out/windows-amd64" ]; then
	f=$(one "$out/windows-amd64/*-setup-*.exe")
	add "$f" "${f##*/}"
	add "$f" Deneb-Setup.exe
	update "$out/windows-amd64" windows-amd64
fi
debs=$stage/debs
mkdir -p "$debs"
found=
for f in "$out"/linux-*/*.deb; do
	[ -f "$f" ] && cp "$f" "$debs/" && found=1
done
if [ -z "$found" ]; then
	gh release download "v$version" --repo kittors/deneb --pattern '*.deb' --dir "$debs"
fi
for f in "$debs"/*.deb; do
	[ -f "$f" ] || continue
	n=${f##*/}
	add "$f" "$n"
	# deneb_0.1.8_amd64.deb → deneb.deb; …_arm64.deb → deneb-arm64.deb
	pkg=${n%%_*} arch=${n##*_}
	arch=${arch%.deb}
	case $arch in
	amd64) add "$f" "$pkg.deb" ;;
	arm64) add "$f" "$pkg-arm64.deb" ;;
	esac
done

echo "publish-downloads: $(wc -l <"$list" | tr -d ' ') files, then the manifests of:$manifests"
while read -r f n; do
	printf '  %-40s %10s bytes\n' "$n" "$(wc -c <"$f" | tr -d ' ')"
done <"$list"
if [ -n "$dry" ]; then
	echo "publish-downloads: -n, nothing uploaded"
	exit 0
fi

# put uploads one file under a temporary name, checks it, renames it.
put() {
	f=$1 n=$2
	sum=$(shasum -a 256 "$f" | cut -d' ' -f1)
	have=$(ssh -n "$HOST" "sha256sum '$DOWNLOAD_DIR/$n' 2>/dev/null | cut -d' ' -f1" || true)
	if [ "$have" = "$sum" ]; then
		echo "  $n: already there"
		return
	fi
	scp -q "$f" "$HOST:$DOWNLOAD_DIR/.$n.up"
	got=$(ssh -n "$HOST" "sha256sum '$DOWNLOAD_DIR/.$n.up' | cut -d' ' -f1")
	if [ "$got" != "$sum" ]; then
		ssh -n "$HOST" "rm -f '$DOWNLOAD_DIR/.$n.up'"
		echo "$n: the copy on the site is not the file sent" >&2
		exit 1
	fi
	ssh -n "$HOST" "chmod 0644 '$DOWNLOAD_DIR/.$n.up' && mv -f '$DOWNLOAD_DIR/.$n.up' '$DOWNLOAD_DIR/$n'"
	echo "  $n: up"
}

ssh -n "$HOST" "test -d '$DOWNLOAD_DIR'" || {
	echo "no $DOWNLOAD_DIR on $HOST: run deploy/web/nginx-hkspare.sh install first" >&2
	exit 1
}
while read -r f n; do
	put "$f" "$n"
done <"$list"
# Before any manifest changes: every file a manifest names must be on the
# site already, byte for byte, or the apps would be sent to a 404.
for plat in $manifests; do
	python3 -I - "$stage/update-$plat.json" <<'PY' | while read -r n; do
import json, sys, posixpath
m = json.load(open(sys.argv[1], encoding="utf-8"))
for s in [m] + (m.get("deltas") or []):
    print(posixpath.basename(s["url"]))
PY
		f=$(awk -v n="$n" '$2 == n { print $1; exit }' "$list")
		sum=$(shasum -a 256 "$f" | cut -d' ' -f1)
		have=$(ssh -n "$HOST" "sha256sum '$DOWNLOAD_DIR/$n' 2>/dev/null | cut -d' ' -f1" || true)
		if [ "$have" != "$sum" ]; then
			echo "$n is not on the site as built: the manifests stay as they are" >&2
			exit 1
		fi
	done || exit 1
done
for plat in $manifests; do
	ssh -n "$HOST" "cd '$DOWNLOAD_DIR' && if [ -f update-$plat.json ] && [ ! -f update-$plat.before-$version.json ]; then cp -p update-$plat.json update-$plat.before-$version.json; fi"
	put "$stage/update-$plat.json" "update-$plat.json"
done

# What the apps will see, over HTTPS.
for plat in $manifests; do
	verify "${base}update-$plat.json"
done
for n in Deneb.dmg Deneb-Setup.exe deneb.deb denebd.deb; do
	grep -q " $n\$" "$list" || continue
	code=$(curl -s -o /dev/null -w '%{http_code}' -r 0-0 "$base$n")
	echo "  $n over HTTPS: $code"
done
echo "publish-downloads: $version is on the site"
