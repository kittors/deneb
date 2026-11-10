#!/bin/sh
# Puts back the control server that ran before the last deploy-hk155.sh:
# the image kept as deneb-control:prev, on the data as it is now, or with
# -restore on the copy of the data the deploy took before it started the
# new server (what happened since is then lost).
#
#   deploy/control/rollback-hk155.sh            # old image, data as it is
#   deploy/control/rollback-hk155.sh -restore   # old image, data as it was
#
# The old image reads a newer database: migrations only add columns. But an
# older server ignores what they mean (a device waiting for approval looks
# like one let in), so once the newer server has been used for that, roll
# back with -restore.
set -eu
HOST=${HOST:-hk-155}
PORT=${PORT:-47443}
PUBLIC_URL=${PUBLIC_URL:-https://103.42.31.155:$PORT}
NAME=deneb-control
REMOTE=/opt/deneb-control
RESTORE=no
[ "${1:-}" = "-restore" ] && RESTORE=yes

ssh "$HOST" "
	set -e
	docker inspect deneb-control:prev >/dev/null
	stamp=\$(date -u +%Y%m%d-%H%M%S)
	mkdir -p $REMOTE/logs
	docker logs $NAME > $REMOTE/logs/$NAME-\$stamp-rolled-back.log 2>&1 || true
	docker stop -t 10 $NAME >/dev/null 2>&1 || true
	docker rm -f $NAME >/dev/null 2>&1 || true
	if [ $RESTORE = yes ]; then
		last=\$(ls -1dt $REMOTE/backups/data-* | head -1)
		mv $REMOTE/data $REMOTE/backups/rolled-back-\$stamp
		cp -a \$last $REMOTE/data
		echo \"restored \$last\"
	fi
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--cpus 1 --memory 256m --memory-swap 256m --pids-limit 256 \
		--log-opt max-size=10m --log-opt max-file=3 \
		-v $REMOTE/data:/data \
		deneb-control:prev -listen :$PORT -data /data -seed /data/seed.json -public-url $PUBLIC_URL
	sleep 3
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Status}}'
	docker logs --tail 10 $NAME"
