#!/bin/sh
# Start this Mac's node: in both LANs, SOCKS5 for local apps on 127.0.0.1:41080.
# Pass -bind-if en0 to pin underlay traffic to Wi-Fi and step around a proxy TUN.
cd "$(dirname "$0")/.."
. scripts/env.sh
exec bin/deneb-poc node -config configs/mac.me.json -api 127.0.0.1:$API_PORT -socks 127.0.0.1:41080 "$@"
