#!/bin/sh
# Builds Deneb's Linux packages into deploy/linux/out/:
#   denebd        a desktop's engine and command line, kernel mode; the
#                 app's own package, "deneb" (MyGo's), depends on it
#   deneb-server  the engine and command line only, user-space mode
# for each architecture given (default amd64 and arm64).
#
#   VERSION=0.5.0 deploy/linux/build-deb.sh [amd64] [arm64]
#
# A test build (TAGS=denebtest) only with DENEB_TEST_PACKAGE=1, as the test
# topology sets: one must never reach a release (deploy/check-release.sh).
#
# dpkg-deb runs in a Debian container (colima shares only the home
# directory with containers, so the staging directory is under out/).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
version=${VERSION:-0.0.0~$(date +%Y%m%d%H%M)}
tags=${TAGS:-}
case ",$tags," in
*,denebtest,*)
	[ "${DENEB_TEST_PACKAGE:-}" = 1 ] || {
		echo "build-deb: TAGS=$tags is a test build; set DENEB_TEST_PACKAGE=1 if it is for the test topology, never for a release" >&2
		exit 1
	}
	;;
esac
out=$here/out
mkdir -p "$out"
[ $# -gt 0 ] || set -- amd64 arm64

desc_engine="Deneb engine for a desktop: kernel mode
 A tun interface, a /32 route per peer and systemd-resolved for
 deneb.internal, so ordinary programs reach the devices of your LANs by
 name. Falls back to user-space mode without systemd-resolved."
desc_server="Deneb engine and command line for a server
 User-space mode: no network interface is created; programs reach the
 devices of your LANs through SOCKS5 on 127.0.0.1:41080."

for arch in "$@"; do
	bin=$out/denebd-linux-$arch
	(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build ${tags:+-tags "$tags"} -trimpath \
		-ldflags "-X main.version=$version" -o "$bin" ./cmd/denebd)
	# GOFLAGS can carry tags too: what counts is what was built.
	[ "${DENEB_TEST_PACKAGE:-}" = 1 ] || "$root/deploy/check-release.sh" "$bin"
	# The licenses of what the binary links (Go, its modules), and ours.
	GOOS=linux GOARCH="$arch" "$root/deploy/notices.sh" ${tags:+-tags "$tags"} ./cmd/denebd >"$out/notices-$arch"
	for pkg in denebd deneb-server; do
		stage=$out/stage-$pkg-$arch
		rm -rf "$stage"
		mkdir -p "$stage/DEBIAN" "$stage/usr/bin" "$stage/usr/lib/systemd/system" "$stage/etc/default" "$stage/usr/share/doc/$pkg"
		install -m 0644 "$root/LICENSE" "$stage/usr/share/doc/$pkg/copyright"
		install -m 0644 "$out/notices-$arch" "$stage/usr/share/doc/$pkg/THIRD_PARTY_NOTICES"
		install -m 0755 "$bin" "$stage/usr/bin/denebd"
		ln -s denebd "$stage/usr/bin/deneb"
		install -m 0644 "$here/denebd.service" "$stage/usr/lib/systemd/system/denebd.service"
		if [ "$pkg" = denebd ]; then
			install -m 0644 "$here/default-deneb" "$stage/etc/default/denebd"
			other=deneb-server desc=$desc_engine recommends="Recommends: systemd-resolved, libnss-resolve"
		else
			install -m 0644 "$here/default-deneb-server" "$stage/etc/default/denebd"
			other=denebd desc=$desc_server recommends=""
		fi
		for s in postinst prerm postrm; do
			install -m 0755 "$here/debian/$s" "$stage/DEBIAN/$s"
		done
		echo /etc/default/denebd > "$stage/DEBIAN/conffiles"
		{
			echo "Package: $pkg"
			echo "Version: $version"
			echo "Architecture: $arch"
			echo "Maintainer: Deneb <packages@deneb.invalid>"
			echo "Section: net"
			echo "Priority: optional"
			echo "Conflicts: $other"
			echo "Replaces: $other"
			[ -z "$recommends" ] || echo "$recommends"
			echo "Installed-Size: $(du -sk "$stage/usr" | cut -f1)"
			echo "Description: $desc"
		} > "$stage/DEBIAN/control"
		chmod 0755 "$stage/DEBIAN"
		deb=${pkg}_${version}_${arch}.deb
		docker run --rm -v "$out:/out" debian:stable-slim \
			dpkg-deb --root-owner-group -Zxz --build "/out/stage-$pkg-$arch" "/out/$deb" >/dev/null
		rm -rf "$stage"
		echo "$out/$deb"
	done
	rm -f "$bin" "$out/notices-$arch"
done
