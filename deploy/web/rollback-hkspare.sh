#!/bin/sh
# Puts back the deneb-web that ran before the last deploy-hkspare.sh (the
# image kept as deneb-web:prev), or with -remove takes the Web version's
# container away altogether. Take the nginx site out first
# (nginx-hkspare.sh remove), and the control server's -web-url (WEB_LIVE=no,
# then deploy/control/deploy-hk155.sh), so that nothing points at it.
#
#   deploy/web/rollback-hkspare.sh            # the previous image
#   deploy/web/rollback-hkspare.sh -remove    # no deneb-web at all
set -eu
HOST=${HOST:-hk-spare}
CONTROL_HOST=${CONTROL_HOST:-hk-155}
SITE=${SITE:-deploy/web/site.hkspare.env}

cd "$(dirname "$0")/../.."
if [ "${1:-}" = "-remove" ]; then
	ssh "$HOST" 'docker rm -f deneb-web && echo "deneb-web removed; its images stay"'
	exit 0
fi
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_PORT:?in $SITE}" "${CONTROL_URL:?in $SITE}"
if [ -z "${CONTROL_PIN:-}" ]; then
	CONTROL_PIN=$(ssh "$CONTROL_HOST" cat /opt/deneb-control/data/tls/pin.txt)
fi
ssh "$HOST" sh -s -- "$WEB_PORT" "$CONTROL_URL" "$CONTROL_PIN" <<'EOF'
set -eu
port=$1 control=$2 pin=$3
docker inspect deneb-web:prev >/dev/null
docker rm -f deneb-web >/dev/null 2>&1 || true
docker run -d --name deneb-web \
	--network host \
	--restart unless-stopped \
	--read-only \
	--cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 128 \
	--log-opt max-size=10m --log-opt max-file=3 \
	deneb-web:prev -static /app -listen "127.0.0.1:$port" -control "$control" -control-pin "$pin"
sleep 2
docker ps --filter name=^deneb-web$ --format '{{.Names}} {{.Status}}'
docker logs --tail 5 deneb-web
EOF
