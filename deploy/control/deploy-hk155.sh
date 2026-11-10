#!/bin/sh
# Deploys the control server into hk-155's Docker (implementation.md §0.1):
# host network on a high TCP port, at most 1 CPU and 256 MB, data in
# /opt/deneb-control/data. It changes nothing on the host's network,
# firewall, nginx or other containers: with --network host, Docker adds no
# rules, and the server only listens on its port.
#
#   deploy/control/deploy-hk155.sh          # build, upload, (re)start
#
# Redeploying keeps the data directory, so the TLS key and with it the pin
# clients ship stay the same; the replaced container's log goes to
# /opt/deneb-control/logs, a copy of the data taken while the old server is
# stopped to /opt/deneb-control/backups, and the old image is kept as
# deneb-control:prev (rollback-hk155.sh puts it back). A redeploy restarts
# the server: engines reconnect within seconds, and login links not used
# yet stop working.
set -eu
HOST=${HOST:-hk-155}
PORT=${PORT:-47443}
PUBLIC_URL=${PUBLIC_URL:-https://103.42.31.155:$PORT}
SEED=${SEED:-deploy/control/seed.hk155.json}
NAME=deneb-control
TAG=deneb-control:v0
REMOTE=/opt/deneb-control

cd "$(dirname "$0")/../.."
test -f "$SEED" || { echo "no seed at $SEED" >&2; exit 1; }
ARCHES=amd64 deploy/control/build.sh

# The port must be free, unless our own container is the one holding it.
ssh "$HOST" "
	if [ -z \"\$(docker ps -q --filter name=^$NAME\$)\" ] && [ -n \"\$(ss -ltnH 'sport = :$PORT')\" ]; then
		echo 'port $PORT is taken on $HOST:'; ss -ltnpH 'sport = :$PORT'; exit 1
	fi
	mkdir -p $REMOTE/image/bin/amd64 $REMOTE/image/skel $REMOTE/data"
scp -q deploy/control/bin/amd64/deneb-control "$HOST:$REMOTE/image/bin/amd64/deneb-control"
scp -q deploy/control/Dockerfile "$HOST:$REMOTE/image/Dockerfile"
scp -q deploy/control/skel/.keep "$HOST:$REMOTE/image/skel/.keep"
scp -q "$SEED" "$HOST:$REMOTE/data/seed.json"

ssh "$HOST" "
	set -e
	chown -R 65534:65534 $REMOTE/data
	chmod 700 $REMOTE/data
	chmod 600 $REMOTE/data/seed.json
	# The image running now stays as deneb-control:prev, for
	# rollback-hk155.sh.
	if docker inspect $NAME >/dev/null 2>&1; then
		docker tag \$(docker inspect -f '{{.Image}}' $NAME) deneb-control:prev
	fi
	docker build -q -t $TAG $REMOTE/image
	stamp=\$(date -u +%Y%m%d-%H%M%S)
	mkdir -p $REMOTE/logs $REMOTE/backups
	if docker inspect $NAME >/dev/null 2>&1; then
		# A container's log goes with it: keep the old one's, logins and
		# approvals included, before replacing it.
		docker logs $NAME > $REMOTE/logs/$NAME-\$stamp.log 2>&1
		ls -1t $REMOTE/logs/$NAME-*.log | tail -n +31 | xargs -r rm -f
		# Stopped, the database is quiet: copy it as it is, for a rollback
		# to the exact state before this deploy.
		docker stop -t 10 $NAME >/dev/null
		rm -f $REMOTE/data/admin.sock # the new server makes its own
		if ! cp -a $REMOTE/data $REMOTE/backups/data-\$stamp; then
			# No backup, no deploy: bring the old server back up.
			docker start $NAME
			echo 'backup failed; the old server is running again' >&2
			exit 1
		fi
		ls -1dt $REMOTE/backups/data-* | tail -n +11 | xargs -r rm -rf
		echo "backup: $REMOTE/backups/data-\$stamp"
	fi
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--cpus 1 --memory 256m --memory-swap 256m --pids-limit 256 \
		--log-opt max-size=10m --log-opt max-file=3 \
		-v $REMOTE/data:/data \
		$TAG -listen :$PORT -data /data -seed /data/seed.json -public-url $PUBLIC_URL
	sleep 3
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Status}}'
	docker logs --tail 20 $NAME
	cat $REMOTE/data/tls/pin.txt"
