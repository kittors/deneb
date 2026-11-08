#!/bin/sh
# Only needed if the kernel-TUN node was killed hard (kill -9) and its normal
# exit path never ran. Safe to run any time: it removes only what
# start-mac-tun.sh adds. (A utun disappears by itself with its process.)
[ "$(id -u)" -eq 0 ] || { echo "run as: sudo $0"; exit 1; }
pkill -f 'deneb-poc node -config configs/mac.me.json -tun' 2>/dev/null
route -q -n delete -inet 10.86.0.0/16 2>/dev/null
rm -f /etc/resolver/deneb.internal
rmdir /etc/resolver 2>/dev/null # succeeds only if empty; it did not exist before
echo cleaned
