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
# As deploy-hk155.sh: the Web version's flags, if it is live (dropped below
# for an image from before them).
cd "$(dirname "$0")/../.."
WEB_SITE=${WEB_SITE:-deploy/web/site.hkspare.env}
WEB_FLAGS=
if [ -f "$WEB_SITE" ]; then
	case $WEB_SITE in /*) ;; *) WEB_SITE=./$WEB_SITE ;; esac
	. "$WEB_SITE"
	if [ "${WEB_LIVE:-no}" = yes ]; then
		WEB_FLAGS="-web-url https://${WEB_HOST:?in $WEB_SITE} -trust-proxy ${WEB_PROXY_IP:?in $WEB_SITE}"
	fi
fi

ssh "$HOST" "
	set -e
	docker inspect deneb-control:prev >/dev/null
	# An image from before the Web version would not start with its flags.
	web='$WEB_FLAGS'
	if [ -n \"\$web\" ] && ! docker run --rm deneb-control:prev -h 2>&1 | grep -q -- -web-url; then
		echo 'deneb-control:prev knows no -web-url: it runs without, and invite links name this server again'
		web=
	fi
	stamp=\$(date -u +%Y%m%d-%H%M%S)
	mkdir -p $REMOTE/logs
	docker logs $NAME > $REMOTE/logs/$NAME-\$stamp-rolled-back.log 2>&1 || true
	docker stop -t 10 $NAME >/dev/null 2>&1 || true
	docker rm -f $NAME >/dev/null 2>&1 || true
	if [ $RESTORE = yes ]; then
		last=\$(ls -1dt $REMOTE/backups/data-* | head -1)
		mv $REMOTE/data $REMOTE/backups/rolled-back-\$stamp
		# Dated by now: deploy-hk155.sh drops these after two months.
		touch $REMOTE/backups/rolled-back-\$stamp
		cp -a \$last $REMOTE/data
		# The log and the daily backups go on with the server, which keeps
		# them 30 days and two months, rather than two months more in the
		# data set aside.
		for d in logs backup; do
			if [ -d $REMOTE/backups/rolled-back-\$stamp/\$d ]; then
				rm -rf $REMOTE/data/\$d
				mv $REMOTE/backups/rolled-back-\$stamp/\$d $REMOTE/data/\$d
			fi
		done
		echo \"restored \$last\"
	fi
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--cpus 1 --memory 256m --memory-swap 256m --pids-limit 256 \
		--log-opt max-size=10m --log-opt max-file=3 \
		-v $REMOTE/data:/data \
		-v /etc/ssl/certs/ca-certificates.crt:/etc/ssl/certs/ca-certificates.crt:ro \
		deneb-control:prev -listen :$PORT -data /data -seed /data/seed.json -public-url $PUBLIC_URL \$web
	sleep 3
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Status}}'
	docker logs --tail 10 $NAME"
