#!/bin/sh
# Kernel-TUN experiment on this Mac. Run with sudo in a terminal that stays
# open. It changes exactly three things and undoes all of them on exit:
#   1. one utun interface with 10.86.0.1          (gone when the node exits)
#   2. one route 10.86.0.0/16 -> that utun        (deleted by the node on exit)
#   3. /etc/resolver/deneb.internal -> 10.86.0.53 (deleted by this script)
# Clash, Tailscale, system DNS servers, the default route, pf and sysctl are
# not touched. The address block deliberately avoids 100.64/10 (Tailscale
# would switch route modes) and 198.18/15 (Clash). Stop with Ctrl-C, or let
# the test scripts call the node's /shutdown.
set -u
cd "$(dirname "$0")/.."
[ "$(id -u)" -eq 0 ] || { echo "run as: sudo $0"; exit 1; }
DOMAIN=deneb.internal
RESOLVER=10.86.0.53
SNAP=/tmp/deneb-tun
LOG=/tmp/deneb-tun-node.log

snapshot() {
  netstat -rn -f inet > "$SNAP-$1-routes.txt"
  scutil --dns > "$SNAP-$1-dns.txt"
  ifconfig -l > "$SNAP-$1-ifaces.txt"
  chmod 644 "$SNAP-$1-"*.txt
}
made_dir=0
finish() {
  rm -f "/etc/resolver/$DOMAIN"
  [ "$made_dir" = 1 ] && rmdir /etc/resolver 2>/dev/null
  sleep 1
  snapshot after
  echo "deneb: undone. Compare $SNAP-before-*.txt with $SNAP-after-*.txt"
}
trap finish EXIT
trap 'exit 130' INT TERM HUP

# Refuse to start if the block is in use; this changes nothing.
bin/deneb-poc node -config configs/mac.me.json -tun -preflight || exit 1
snapshot before
if [ ! -d /etc/resolver ]; then mkdir /etc/resolver && made_dir=1; fi
echo "nameserver $RESOLVER" > "/etc/resolver/$DOMAIN"
echo "deneb: wrote /etc/resolver/$DOMAIN (only names ending in .$DOMAIN use it)"
bin/deneb-poc node -config configs/mac.me.json -tun -api 127.0.0.1:47777 -socks 127.0.0.1:41080 -bind-if en0 2>&1 | tee "$LOG"
