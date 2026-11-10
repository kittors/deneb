#!/bin/sh
# Deploys the watchdog (cmd/deneb-watchdog) onto one machine, in Docker:
# host network, listening on nothing, at most 0.2 CPU and 64 MB. It changes
# nothing on the host's network, firewall or other containers: with
# --network host and no published port Docker adds no rules, and the
# watchdog only makes outbound requests.
#
#   HOST=hk-spare deploy/watchdog/deploy.sh
#   HOST=hk-155 BACKUP_DIR=/opt/deneb-control/data/backup deploy/watchdog/deploy.sh
#
# Its file is deploy/watchdog/watch.$HOST.json (CONFIG to choose another):
# the machine's name, the checks, and where alerts go. It is not in the
# repository (it holds addresses and the bot's key); watch.example.json
# shows the shape. It goes to /opt/deneb-watchdog/watch.json, 0600, owned
# by nobody, whom the watchdog runs as. BACKUP_DIR, if set, is mounted
# read-only at /backup for a backup or disk check.
#
# Before starting it, a dry run makes every check once and prints them;
# if one fails it stops (FORCE=1 starts it anyway). docker ps and the
# listening ports are printed before and after, to compare.
#
# To take it away: ssh $HOST docker rm -f deneb-watchdog
set -eu
HOST=${HOST:?HOST=<machine> deploy/watchdog/deploy.sh}
CONFIG=${CONFIG:-deploy/watchdog/watch.$HOST.json}
BACKUP_DIR=${BACKUP_DIR:-}
NAME=deneb-watchdog
TAG=deneb-watchdog:v0
REMOTE=/opt/deneb-watchdog

cd "$(dirname "$0")/../.."
test -f "$CONFIG" || { echo "no file at $CONFIG (see deploy/watchdog/watch.example.json)" >&2; exit 1; }
go run ./cmd/deneb-watchdog -config "$CONFIG" -validate
ARCHES=amd64 deploy/watchdog/build.sh

case "$BACKUP_DIR" in
*[!A-Za-z0-9/._-]*) echo "BACKUP_DIR: a plain path, please" >&2; exit 2 ;;
esac
mounts="-v $REMOTE/watch.json:/etc/deneb-watchdog/watch.json:ro -v /etc/ssl/certs/ca-certificates.crt:/etc/ssl/certs/ca-certificates.crt:ro"
if [ -n "$BACKUP_DIR" ]; then
	mounts="$mounts -v $BACKUP_DIR:/backup:ro"
fi

snapshot() {
	ssh "$HOST" "echo '== docker'; docker ps --format '{{.Names}} {{.Image}} {{.Status}}' | sort; echo '== listening'; ss -ltnuH | awk '{print \$1, \$5}' | sort -u"
}
echo "### before"
snapshot

ssh "$HOST" "mkdir -p $REMOTE/image/bin/amd64"
scp -q deploy/watchdog/bin/amd64/deneb-watchdog "$HOST:$REMOTE/image/bin/amd64/deneb-watchdog"
scp -q deploy/watchdog/Dockerfile "$HOST:$REMOTE/image/Dockerfile"
scp -q "$CONFIG" "$HOST:$REMOTE/watch.json.new"
ssh "$HOST" "
	set -e
	chown 65534:65534 $REMOTE/watch.json.new
	chmod 600 $REMOTE/watch.json.new
	mv $REMOTE/watch.json.new $REMOTE/watch.json
	docker build -q -t $TAG $REMOTE/image >/dev/null
	echo '### dry run'
	if ! docker run --rm --network host $mounts $TAG -config /etc/deneb-watchdog/watch.json -once; then
		if [ '${FORCE:-}' != 1 ]; then
			echo 'a check failed: not started (FORCE=1 starts it anyway)' >&2
			exit 1
		fi
	fi
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--cpus 0.2 --memory 64m --memory-swap 64m --pids-limit 64 \
		--log-opt max-size=5m --log-opt max-file=2 \
		$mounts \
		$TAG >/dev/null
	sleep 3
	docker logs --tail 10 $NAME"

echo "### after"
snapshot
