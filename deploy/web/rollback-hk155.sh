#!/bin/sh
# Puts back the deneb-web that ran before the last deploy-hk155.sh (the
# image kept as deneb-web:prev), or with -remove takes the Web version's
# container away altogether. Take the nginx site out first
# (nginx-hk155.sh remove), and the control server's -web-url (WEB_LIVE=no,
# then deploy/control/deploy-hk155.sh), so that nothing points at it.
#
#   deploy/web/rollback-hk155.sh            # the previous image
#   deploy/web/rollback-hk155.sh -remove    # no deneb-web at all
set -eu
HOST=${HOST:-hk-155}
SITE=${SITE:-deploy/web/site.hk155.env}
CONTROL_PORT=${CONTROL_PORT:-47443}
NAME=deneb-web
CONTROL_DATA=/opt/deneb-control/data

cd "$(dirname "$0")/../.."
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_PORT:?in $SITE}"
if [ "${1:-}" = "-remove" ]; then
	ssh "$HOST" "docker rm -f $NAME && echo 'deneb-web removed; its images stay'"
	exit 0
fi
ssh "$HOST" "
	set -e
	docker inspect deneb-web:prev >/dev/null
	pin=\$(cat $CONTROL_DATA/tls/pin.txt)
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--read-only \
		--cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 128 \
		--log-opt max-size=10m --log-opt max-file=3 \
		deneb-web:prev -static /app -listen 127.0.0.1:$WEB_PORT \
		-control https://127.0.0.1:$CONTROL_PORT -control-pin \$pin
	sleep 2
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Status}}'
	docker logs --tail 5 $NAME"
