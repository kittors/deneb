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
# deneb-control:prev (rollback-hk155.sh puts it back).
#
# The server's log is in data/logs, a file a day kept 30 days (PRIVACY.md);
# the container's own log has only what comes before those files open. The
# saved logs keep 30 days too, the copies of the data (without the logs and
# the daily backups) two months, the last one always. A redeploy restarts
# the server: engines reconnect within seconds, and login links not used
# yet stop working.
#
# The image is empty, with no CAs: the host's bundle goes in read-only, so
# the server can reach the mail service (login codes) and the alert bot over
# TLS. Their settings are files in the data directory, never in this
# repository: data/mail.json and data/alert.json, 0600, owned by nobody
# (design/plan/m7/control-security.md §6, control-ops.md §3).
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
# The Web version, once it is up (deploy/web/, WEB_LIVE=yes in its site
# settings): invite links open it, and browsers' addresses come through it.
WEB_SITE=${WEB_SITE:-deploy/web/site.hkspare.env}
WEB_FLAGS=
if [ -f "$WEB_SITE" ]; then
	case $WEB_SITE in /*) ;; *) WEB_SITE=./$WEB_SITE ;; esac
	. "$WEB_SITE"
	if [ "${WEB_LIVE:-no}" = yes ]; then
		WEB_FLAGS="-web-url https://${WEB_HOST:?in $WEB_SITE} -trust-proxy ${WEB_PROXY_IP:?in $WEB_SITE}"
	fi
fi
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
		find $REMOTE/logs -maxdepth 1 -name '$NAME-*.log' -mtime +30 -delete
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
		# The log and the daily backups stay with the server, which keeps
		# them 30 days and two months: a copy holding them would keep them
		# two months more. The copy is dated by when it was made (cp -a
		# keeps the data's own date).
		rm -rf $REMOTE/backups/data-\$stamp/logs $REMOTE/backups/data-\$stamp/backup
		touch $REMOTE/backups/data-\$stamp
		ls -1dt $REMOTE/backups/data-* | tail -n +11 | xargs -r rm -rf
		find $REMOTE/backups -maxdepth 1 \\( -name 'data-*' -o -name 'rolled-back-*' \\) -mtime +60 -exec rm -rf {} +
		echo "backup: $REMOTE/backups/data-\$stamp"
	fi
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--cpus 1 --memory 256m --memory-swap 256m --pids-limit 256 \
		--log-opt max-size=10m --log-opt max-file=3 \
		-v $REMOTE/data:/data \
		-v /etc/ssl/certs/ca-certificates.crt:/etc/ssl/certs/ca-certificates.crt:ro \
		$TAG -listen :$PORT -data /data -seed /data/seed.json -public-url $PUBLIC_URL $WEB_FLAGS
	sleep 3
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Status}}'
	docker logs --tail 5 $NAME
	docker exec $NAME deneb-control logs -n 20 || true
	cat $REMOTE/data/tls/pin.txt"
