#!/bin/sh
# Build for all three machines and copy binaries plus each device's config.
# Nothing is installed: files go to /tmp/deneb-poc on hk-155 and hk-spare, and to
# %USERPROFILE%\deneb-poc on the Windows box.
set -e
cd "$(dirname "$0")/.."
. scripts/env.sh
# Stripped and gzipped: the Windows box sits behind a slow uplink.
go build -o bin/deneb-poc .
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/deneb-poc-linux-amd64 .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/deneb-poc-windows-amd64.exe .
tar -C bin -czf bin/win.tgz deneb-poc-windows-amd64.exe
ssh -o BatchMode=yes "$HK155" "pkill -f '^./deneb-poc-linux-amd64'; mkdir -p /tmp/deneb-poc"
scp -q bin/deneb-poc-linux-amd64 configs/box.lin.json "$HK155:/tmp/deneb-poc/"
ssh -o BatchMode=yes "$HKSPARE" "pkill -f '^./deneb-poc-linux-amd64'; mkdir -p /tmp/deneb-poc"
scp -q bin/deneb-poc-linux-amd64 configs/nas.jie.json "$HKSPARE:/tmp/deneb-poc/"
ssh $WIN_OPTS "$WIN_HOST" 'taskkill /F /IM deneb-poc-windows-amd64.exe >NUL 2>&1 & if not exist deneb-poc mkdir deneb-poc'
scp -q $WIN_OPTS bin/win.tgz configs/box.jie.json "$WIN_HOST:deneb-poc/"
ssh $WIN_OPTS "$WIN_HOST" 'cd deneb-poc && tar -xzf win.tgz && del win.tgz'

echo deployed
