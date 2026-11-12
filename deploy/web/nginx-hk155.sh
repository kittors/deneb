#!/bin/sh
# Puts the Web version's site into hk-155's nginx, or takes it out: one
# file, /etc/nginx/conf.d/<WEB_HOST>.conf, made from nginx.conf.template.
# No other file is touched, and a file of that name that this script did
# not make is left alone. nginx -t must pass before nginx reloads, which is
# graceful: no connection to any other site drops. If it fails, the file
# goes back to what it was and nginx is not reloaded.
#
#   deploy/web/nginx-hk155.sh show       # the file as it would be, printed here
#   deploy/web/nginx-hk155.sh install    # put it in, check, reload
#   deploy/web/nginx-hk155.sh remove     # take it out, check, reload
#
# The certificate must be there before install (cert-hk155.sh).
set -eu
HOST=${HOST:-hk-155}
SITE=${SITE:-deploy/web/site.hk155.env}

cd "$(dirname "$0")/../.."
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_HOST:?in $SITE}" "${WEB_PORT:?in $SITE}" "${NGINX_IP:?in $SITE}"
MARK='# deneb-web: made by deploy/web/nginx-hk155.sh'
CONF=/etc/nginx/conf.d/$WEB_HOST.conf

render() {
	sed -e "s|@WEB_HOST@|$WEB_HOST|g" -e "s|@WEB_PORT@|$WEB_PORT|g" -e "s|@NGINX_IP@|$NGINX_IP|g" \
		deploy/web/nginx.conf.template
}

case "${1:-}" in
show)
	render
	;;
install)
	render | ssh "$HOST" "
		set -e
		new=\$(mktemp)
		cat > \$new
		if [ -e $CONF ] && ! grep -qF '$MARK' $CONF; then
			rm -f \$new; echo '$CONF is there and not ours: nothing done'; exit 1
		fi
		if [ ! -s /etc/letsencrypt/live/$WEB_HOST/fullchain.pem ]; then
			rm -f \$new; echo 'no certificate for $WEB_HOST yet: run deploy/web/cert-hk155.sh first'; exit 1
		fi
		old=
		if [ -e $CONF ]; then old=\$(mktemp); cp -p $CONF \$old; fi
		install -m 0644 \$new $CONF
		rm -f \$new
		if nginx -t; then
			systemctl reload nginx
			[ -z \"\$old\" ] || rm -f \$old
			echo 'nginx reloaded with $CONF'
		else
			if [ -n \"\$old\" ]; then mv \$old $CONF; else rm -f $CONF; fi
			echo 'nginx -t failed: $CONF is back as it was, nginx not reloaded'
			exit 1
		fi"
	;;
remove)
	ssh "$HOST" "
		set -e
		if [ ! -e $CONF ]; then echo 'no $CONF: nothing to do'; exit 0; fi
		grep -qF '$MARK' $CONF || { echo '$CONF is not ours: nothing done'; exit 1; }
		saved=\$(mktemp)
		mv $CONF \$saved
		if nginx -t; then
			systemctl reload nginx
			rm -f \$saved
			echo 'nginx reloaded without $CONF'
		else
			mv \$saved $CONF
			echo 'nginx -t failed without it: $CONF is back, nginx not reloaded'
			exit 1
		fi"
	;;
*)
	echo "usage: $0 show|install|remove" >&2
	exit 2
	;;
esac
