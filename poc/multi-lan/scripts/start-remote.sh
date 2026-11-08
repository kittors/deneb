#!/bin/sh
# Start the remote nodes. hk-155 and hk-spare run detached (nohup); the Windows node
# lives inside this SSH session, so this script blocks and stopping it
# (Ctrl-C) stops the Windows node too. Extra args are passed to both nodes.
cd "$(dirname "$0")/.."
. scripts/env.sh
ssh -o BatchMode=yes "$HK155" "pkill -f '^./deneb-poc-linux-amd64'; cd /tmp/deneb-poc; nohup ./deneb-poc-linux-amd64 node -config box.lin.json -api 127.0.0.1:$API_PORT $* > node.log 2>&1 < /dev/null & echo hk-155 node started"
ssh -o BatchMode=yes "$HKSPARE" "pkill -f '^./deneb-poc-linux-amd64'; cd /tmp/deneb-poc; nohup ./deneb-poc-linux-amd64 node -config nas.jie.json -api 127.0.0.1:$API_PORT $* > node.log 2>&1 < /dev/null & echo hk-spare node started"
ssh $WIN_OPTS "$WIN_HOST" "taskkill /F /IM deneb-poc-windows-amd64.exe >NUL 2>&1 & cd deneb-poc && deneb-poc-windows-amd64.exe node -config box.jie.json -api 127.0.0.1:$API_PORT $*"
