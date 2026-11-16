#!/bin/sh
# Builds a release of deneb-relay for the download site
# (design/plan/m7/relay-install.md §9), into deploy/out/<version>/relay/,
# which goes up as the site's /download/relay/ folder:
#
#   <version>/deneb-relay-linux-amd64, -arm64, -armv7   the programs
#   <version>/SHA256SUMS   the install command carries its SHA-256
#   relay.json             the latest release's signed manifest: relays
#                          update themselves from it; put up last
#   latest                 the latest version, for a command without a hash
#   install.sh             the install script, the site's /relay, its
#                          download folder written in
#
#   deploy/relay/release.sh [-n]
#
# The version is cmd/deneb/mygo.json's: the relay follows Deneb's releases.
# MYGO_UPDATER_PRIVATE_KEY signs it (deploy/relay/manifest.go); -n builds
# it unsigned, for a look. The site comes from DENEB_RELAY_SITE
# (https://<host>) or WEB_HOST in deploy/web/site.hkspare.env; it is
# written into the programs (where they look for updates) and the script.
set -eu
dry=
[ "${1:-}" = -n ] && dry=-n
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' cmd/deneb/mygo.json | head -n 1)
[ -n "$version" ] || { echo "no version in cmd/deneb/mygo.json" >&2; exit 2; }
site=${DENEB_RELAY_SITE:-}
if [ -z "$site" ] && [ -f deploy/web/site.hkspare.env ]; then
	host=$(sed -n 's/^WEB_HOST=//p' deploy/web/site.hkspare.env | tr -d '"' | head -n 1)
	[ -z "$host" ] || site=https://$host
fi
case $site in
https://?*) ;;
*) echo "DENEB_RELAY_SITE must be https://<host> (or WEB_HOST in deploy/web/site.hkspare.env)" >&2; exit 2 ;;
esac
base=${site%/}/download/relay
# The control server's ways in, as deploy/package.sh takes them
# (DENEB_CONTROL_ENTRANCES, deploy/control-entrances.json): a relay whose
# join code names Deneb's own server goes by all of them.
entrances_file=${DENEB_CONTROL_ENTRANCES:-deploy/control-entrances.json}
entrances=
if [ -f "$entrances_file" ]; then
	entrances=$(base64 <"$entrances_file" | tr -d '\n')
elif [ -n "${DENEB_CONTROL_ENTRANCES:-}" ]; then
	echo "no $entrances_file" >&2
	exit 2
fi
out=deploy/out/$version/relay
rm -rf "$out"
mkdir -p "$out/$version"
for target in amd64 arm64 arm/7; do
	goarch=${target%/*}
	goarm=
	name=$goarch
	case $target in arm/*) goarm=${target#*/}; name=armv$goarm ;; esac
	CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=$goarm go build -trimpath \
		-ldflags "-s -w -X main.relayVersion=$version -X main.updateFeed=$base/${entrances:+ -X main.builtinEntrances=$entrances}" \
		-o "$out/$version/deneb-relay-linux-$name" ./cmd/deneb-relay
done
go run deploy/relay/manifest.go $dry "$out" "$version"
sed "s|@BASE@|$base|" deploy/relay/install.sh >"$out/install.sh"
chmod 755 "$out/install.sh"
ls -l "$out" "$out/$version"
