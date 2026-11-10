#!/bin/sh
# Entry point of every container in the colima topology.
#   node.sh router <wan-prefix> <lan-prefix> <gateway|-> [symmetric]
#   node.sh node <gateway|-> <denebd run flags...>
#   node.sh control <deneb-control flags...>
#   node.sh derper <derper flags...>
set -e
iface_of() { ip -o -4 addr show | awk -v p="$1" 'index($4, p) == 1 {print $2; exit}'; }

case "$1" in
router)
	# The second network is attached just after the container starts.
	for i in $(seq 50); do
		wan=$(iface_of "$2"); lan=$(iface_of "$3")
		[ -n "$wan" ] && [ -n "$lan" ] && break
		sleep 0.2
	done
	[ -n "$wan" ] && [ -n "$lan" ] || { echo "router: no interface for $2 or $3"; ip -o -4 addr; exit 1; }
	if [ "$4" != "-" ]; then ip route replace default via "$4" dev "$wan"; fi
	# A home router or an ISP's NAT: everything from the LAN leaves with the
	# WAN address. "symmetric" picks a random port for every new flow
	# (endpoint-dependent mapping), the kind of NAT hole punching cannot cross.
	if [ "$5" = "symmetric" ]; then
		iptables -t nat -A POSTROUTING -o "$wan" -j MASQUERADE --random-fully
	else
		iptables -t nat -A POSTROUTING -o "$wan" -j MASQUERADE
	fi
	# Nothing gets in from the WAN unless it answers something sent out.
	iptables -A FORWARD -i "$wan" -o "$lan" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	iptables -A FORWARD -i "$wan" -o "$lan" -j DROP
	echo "router: $lan -> $wan (${5:-full-cone-ish}) up"
	exec sleep infinity
	;;
node)
	gw=$2; shift 2
	if [ "$gw" != "-" ]; then ip route replace default via "$gw"; fi
	exec denebd run "$@"
	;;
control)
	shift
	exec deneb-control "$@"
	;;
derper)
	shift
	exec derper "$@"
	;;
relay)
	# M4: a Deneb relay (cmd/deneb-relay), with admission by the control server.
	shift
	exec deneb-relay "$@"
	;;
esac
echo "node.sh: unknown role $1"; exit 2
