#!/bin/sh
# Call a node's loopback control API: scripts/api.sh mac|lin|jie|nas '/status'
# Use POST paths as '-X POST /lan?id=weekend&on=0'.
cd "$(dirname "$0")/.."
. scripts/env.sh
who=$1; shift
method=GET
if [ "$1" = "-X" ]; then method=$2; shift 2; fi
url="http://127.0.0.1:$API_PORT$1"
case $who in
  mac) curl -s -X "$method" "$url" ;;
  lin) ssh -o BatchMode=yes "$HK155" "curl -s -X $method '$url'" < /dev/null ;;
  nas) ssh -o BatchMode=yes "$HKSPARE" "curl -s -X $method '$url'" < /dev/null ;;
  jie) ssh $WIN_OPTS "$WIN_HOST" "curl.exe -s -X $method \"$url\"" < /dev/null ;;
  *) echo "usage: $0 mac|lin|jie|nas /path" >&2; exit 2 ;;
esac
