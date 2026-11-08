#!/bin/sh
# Stop every prototype node. Nothing else needs undoing: the nodes changed no
# system setting anywhere.
cd "$(dirname "$0")/.."
. scripts/env.sh
pkill -f 'deneb-poc node' 2>/dev/null
ssh -o BatchMode=yes "$HK155" "pkill -f '^./deneb-poc-linux-amd64'; true"
ssh -o BatchMode=yes "$HKSPARE" "pkill -f '^./deneb-poc-linux-amd64'; true"
ssh $WIN_OPTS "$WIN_HOST" 'taskkill /F /IM deneb-poc-windows-amd64.exe >NUL 2>&1 & exit 0'
echo stopped
