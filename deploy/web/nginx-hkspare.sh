#!/bin/sh
# Puts the Web version's site into hk-spare's nginx, or takes it out: one
# file in /etc/nginx/conf.d/ (NGINX_CONF, by default zz-<WEB_HOST>.conf),
# made from nginx.conf.template. No other file is touched, and a file of
# that name that this script did not make is left alone. nginx -t must pass
# before nginx reloads, which is graceful: no connection to any other site
# drops. If it fails, the file goes back to what it was and nginx is not
# reloaded.
#
#   deploy/web/nginx-hkspare.sh show       # the file as it would be, printed here
#   deploy/web/nginx-hkspare.sh install    # put it in, check, reload
#   deploy/web/nginx-hkspare.sh remove     # take it out, check, reload
#
# The certificate must be there before install (cert-hkspare.sh). install
# also refuses to make the new site the default one of an address it shares
# with the other sites (see site.example.env, NGINX_CONF): after a look,
# NGINX_ORDER_CHECKED=yes goes on anyway.
set -eu
HOST=${HOST:-hk-spare}
SITE=${SITE:-deploy/web/site.hkspare.env}

cd "$(dirname "$0")/../.."
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_HOST:?in $SITE}" "${WEB_PORT:?in $SITE}" "${NGINX_LISTEN_80:?in $SITE}" "${NGINX_LISTEN_443:?in $SITE}"
NGINX_CONF=${NGINX_CONF:-zz-$WEB_HOST.conf}
CONF=/etc/nginx/conf.d/$NGINX_CONF
MARK='# deneb-web: made by deploy/web/nginx-hkspare.sh'

# listens turns a ;-separated list into one "listen …;" line each.
listens() {
	printf '%s\n' "$1" | tr ';' '\n' | while IFS= read -r v; do
		v=$(printf '%s' "$v" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
		if [ -n "$v" ]; then printf '\tlisten %s;\n' "$v"; fi
	done
}

render() {
	L80=$(listens "$NGINX_LISTEN_80") L443=$(listens "$NGINX_LISTEN_443") H=$WEB_HOST P=$WEB_PORT awk '
		/@LISTEN_80@/ { print ENVIRON["L80"]; next }
		/@LISTEN_443@/ { print ENVIRON["L443"]; next }
		{ gsub(/@WEB_HOST@/, ENVIRON["H"]); gsub(/@WEB_PORT@/, ENVIRON["P"]); print }' deploy/web/nginx.conf.template
}

case "${1:-}" in
show)
	render
	;;
install)
	tmp=$(mktemp)
	trap 'rm -f "$tmp"' EXIT
	render > "$tmp"
	new=$(ssh "$HOST" mktemp)
	scp -q "$tmp" "$HOST:$new"
	# ssh hands the remote shell one line to parse again: the mark, with its
	# spaces and #, goes in single quotes or it is a comment there.
	ssh "$HOST" sh -s -- "$new" "$CONF" "$WEB_HOST" "'$MARK'" "${NGINX_ORDER_CHECKED:-no}" <<'EOF'
set -eu
new=$1 conf=$2 host=$3 mark=$4 checked=$5
trap 'rm -f "$new"' EXIT
if [ -e "$conf" ] && ! grep -qF "$mark" "$conf"; then
	echo "$conf is there and not ours: nothing done"; exit 1
fi
if [ ! -s "/etc/letsencrypt/live/$host/fullchain.pem" ]; then
	echo "no certificate for $host yet: run deploy/web/cert-hkspare.sh first"; exit 1
fi
# nginx makes the first server on an address its default unless one says
# default_server; conf.d comes before sites-enabled, each in name order.
# The new site must come after every other site in conf.d, and no site in
# sites-enabled may share an address without default_server.
# listens prints the listen directives of the files named, comments left out.
listens() { cat "$@" 2>/dev/null | sed 's/#.*//' | grep -E '(^|[;{[:space:]])listen[[:space:]]' || true; }
if [ "$checked" != yes ]; then
	for f in /etc/nginx/conf.d/*.conf; do
		[ -f "$f" ] && [ "$f" != "$conf" ] || continue
		[ -n "$(listens "$f")" ] || continue
		if [ "$(printf '%s\n%s\n' "$f" "$conf" | LC_ALL=C sort | head -n 1)" = "$conf" ]; then
			echo "$conf would come before $f, and nginx would make it the default site where they share an address: give NGINX_CONF a name that sorts after"; exit 1
		fi
	done
	if [ -d /etc/nginx/sites-enabled ] && listens /etc/nginx/sites-enabled/* | grep -qv default_server; then
		echo "a site in sites-enabled listens without default_server: $conf, read before it, could become its default. Look, then NGINX_ORDER_CHECKED=yes"; exit 1
	fi
fi
old=
if [ -e "$conf" ]; then old=$(mktemp); cp -p "$conf" "$old"; fi
install -m 0644 "$new" "$conf"
if nginx -t; then
	systemctl reload nginx
	[ -z "$old" ] || rm -f "$old"
	echo "nginx reloaded with $conf"
else
	if [ -n "$old" ]; then mv "$old" "$conf"; else rm -f "$conf"; fi
	echo "nginx -t failed: $conf is back as it was, nginx not reloaded"; exit 1
fi
EOF
	;;
remove)
	ssh "$HOST" sh -s -- "$CONF" "'$MARK'" <<'EOF'
set -eu
conf=$1 mark=$2
if [ ! -e "$conf" ]; then echo "no $conf: nothing to do"; exit 0; fi
grep -qF "$mark" "$conf" || { echo "$conf is not ours: nothing done"; exit 1; }
saved=$(mktemp)
mv "$conf" "$saved"
if nginx -t; then
	systemctl reload nginx
	rm -f "$saved"
	echo "nginx reloaded without $conf"
else
	mv "$saved" "$conf"
	echo "nginx -t failed without it: $conf is back, nginx not reloaded"; exit 1
fi
EOF
	;;
*)
	echo "usage: $0 show|install|remove" >&2
	exit 2
	;;
esac
