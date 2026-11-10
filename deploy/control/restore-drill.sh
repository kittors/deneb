#!/bin/sh
# Restore drill: brings a copy of one of the control server's daily
# backups up the way a restore would, on this workspace's server code, and
# checks that it holds together (TestRestoreDrill in control/). It never
# touches the live data: the backup is copied into a temporary directory
# only root can read, the check runs there, and the directory goes after.
# Nothing it prints is a secret: counts, the schema, whether the signing key
# was in the copy (backups from M7 on have none).
#
#   deploy/control/restore-drill.sh <backup.db> [seed.json]   # here, on a copy you have
#   HOST=hk-155 deploy/control/restore-drill.sh [deneb-YYYYMMDD.db]
#
# With HOST it runs on that server, so the data never leaves it: it builds
# the check for linux/amd64 here, copies it over, takes the newest backup
# (or the one named) from /opt/deneb-control/data/backup, and checks it with
# the server's own seed. Running it on a server is a change to that server
# (a file in /tmp for a minute): ask first.
set -eu
cd "$(dirname "$0")/../.."
HOST=${HOST:-}
PUBLIC_URL=${PUBLIC_URL:-https://103.42.31.155:47443}
REMOTE=${REMOTE:-/opt/deneb-control}

if [ -z "$HOST" ]; then
	backup=${1:?usage: restore-drill.sh <backup.db> [seed.json]}
	seed=${2:-deploy/control/seed.hk155.json}
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	chmod 700 "$work"
	cp "$backup" "$work/deneb.db"
	chmod 600 "$work/deneb.db"
	DENEB_RESTORE_COPY="$work" DENEB_RESTORE_SEED="$(cd "$(dirname "$seed")" && pwd)/$(basename "$seed")" \
		DENEB_RESTORE_URL="$PUBLIC_URL" go test ./control -run '^TestRestoreDrill$' -count=1 -v
	exit
fi

name=${1:-}
case "$name" in
*[!A-Za-z0-9._-]* | *..*) echo "a backup's file name, not a path: $name" >&2; exit 2 ;;
esac

# The part that runs on the server, in a directory of its own that goes
# when it is done: the newest backup (or the one named) into it, and the
# check on that. HOST=. runs it on this machine instead, with REMOTE pointing
# at a directory laid out like the server's, to try the script out.
on_server() {
	cat <<'EOS'
set -eu
rdir=$1 name=$2 remote=$3 url=$4
trap 'rm -rf "$rdir"' EXIT
cd "$rdir"
gunzip drill.test.gz
chmod 700 drill.test
b=$name
[ -n "$b" ] || b=$(ls -1 "$remote/data/backup" | grep -E '^deneb-[0-9]{8}[.]db$' | sort | tail -n 1)
[ -n "$b" ] || { echo 'no backup yet' >&2; exit 1; }
echo "drill on $b"
mkdir data
chmod 700 data
cp "$remote/data/backup/$b" data/deneb.db
DENEB_RESTORE_COPY="$rdir/data" DENEB_RESTORE_SEED="$remote/data/seed.json" DENEB_RESTORE_URL="$url" \
	./drill.test -test.run '^TestRestoreDrill$' -test.count=1 -test.v
EOS
}

bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
if [ "$HOST" = . ]; then
	go test -c -o "$bin/drill.test" ./control
else
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$bin/drill.test" ./control
fi
gzip -9 "$bin/drill.test"
if [ "$HOST" = . ]; then
	rdir=$(mktemp -d)
	chmod 700 "$rdir"
	cp "$bin/drill.test.gz" "$rdir/"
	on_server | sh -s -- "$rdir" "$name" "$REMOTE" "$PUBLIC_URL"
	exit
fi
rdir=$(ssh "$HOST" 'd=$(mktemp -d /tmp/deneb-drill.XXXXXX) && chmod 700 "$d" && echo "$d"')
scp -q "$bin/drill.test.gz" "$HOST:$rdir/drill.test.gz"
on_server | ssh "$HOST" sh -s -- "$rdir" "$name" "$REMOTE" "$PUBLIC_URL"
