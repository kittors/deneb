#!/bin/bash
# kernel-round.sh: the kernel-mode round of the M1 acceptance on this Mac
# (design/plan/m1/engine.md, "内核网卡那一轮"). Run it yourself in a
# terminal, as yourself (no sudo in front): it asks for your password once.
#
#   test/topo/kernel-round.sh             the round, about 3 minutes
#   test/topo/kernel-round.sh --dry-run   check everything and say what it would do; change nothing
#   test/topo/kernel-round.sh --undo      clean up after a round that went wrong
#
# WHAT IT CHANGES on this Mac, and only while it runs:
#   - one utun interface holding this Mac's Deneb address (10.48.x.x),
#     created once at the start and removed once at the end;
#   - one route for the deployment's block (10.48.0.0/16) through it;
#   - the file /etc/resolver/deneb.internal, so that the system resolver
#     asks the engine about *.deneb.internal names, and about nothing else.
# It does not touch Clash, Tailscale or their settings, the default route,
# the system DNS servers, the pf firewall or IP forwarding. The engine
# refuses a block inside 100.64.0.0/10 (Tailscale) or 198.18.0.0/15 (Clash's
# fake IPs), or one an existing interface or route already uses.
# The Deneb window freezes for a second or two at the start and at the end,
# while the engine under it is swapped (so that it cannot start a second
# engine on the same state in between).
#
# On the test machines it opens the services the checks use (build's web
# page to 工作室, the 7000 tick streams) and leaves them open. For the
# duration it takes build out of 周末联机 if it is there (item 6 needs 阿杰's
# devices not to see it), takes 阿杰's NAS out of 周末联机 for a few seconds,
# and sets test-only "attack" routes; all of that is put back, also after a
# failure (and by --undo after a crash).
#
# HOW IT GOES BACK: stopping the kernel-mode engine removes the interface,
# the route and the resolver file. Then the user-space engine runs again on
# the same state: this Mac is the same device, logged in, in the same LANs.
# This happens at the end, on Ctrl-C and on any failure, and the script then
# checks that interfaces, routes and DNS settings match what it saw first.
#
# IF SOMETHING IS LEFT OVER (the engine was killed, the Mac slept mid-round):
#   test/topo/kernel-round.sh --undo
# runs `sudo denebd kernel-undo` (the state directory keeps a record of each
# change), removes what is left, starts the user-space engine again and
# puts back what the round changed on the test machines.
# To stop the round from another terminal without sudo (the engine then
# removes the interface itself, and this script tidies up):
#   curl --unix-socket /var/run/deneb/denebd.sock -X POST http://denebd/v1/test/shutdown
set -u
cd "$(dirname "${BASH_SOURCE[0]}")/../.." || exit 1

UBIN=${DENEB_BIN:-$HOME/go/bin/denebd} # what the window starts: the user-space engine
STATE="$HOME/Library/Application Support/Deneb/engine"
USOCK="$HOME/Library/Application Support/Deneb/denebd.sock"
KSOCK=/var/run/deneb/denebd.sock
TICK_SECS=${TICK_SECS:-60}
RUN=$PWD/test/topo/.accept/kernel-$(date +%m%d-%H%M%S)
KBIN=$RUN/denebd # kernel mode needs this tree's engine, not the window's older one
# A binary built from a tested tree, so that a round run while the tree is
# being changed still gets that engine. Without it the round builds one.
PREBUILT=$PWD/test/topo/.accept/kernel-denebd
MARKER=$PWD/test/topo/.accept/kernel-restore.sh # what to put back on the test machines
WIN_SSH=(ssh -o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519" Administrator@100.122.56.85)

pass=0
fail=0
ok() { echo "ok   $*"; pass=$((pass + 1)); }
bad() { echo "FAIL $*"; fail=$((fail + 1)); }
say() { echo "== $*"; }
die() {
	echo "停下：$*" >&2
	exit 1
}
ms() { perl -MTime::HiRes=time -e 'printf "%d\n", time * 1000'; }

u() { DENEB_SOCKET="$USOCK" "$KBIN" "$@"; } # command line against the user-space engine
k() { DENEB_SOCKET="$KSOCK" "$KBIN" "$@"; } # ... against the kernel-mode engine
# r <machine> <deneb args>: the command line on a test machine. Windows gets
# LAN ids, not Chinese names: cmd.exe would garble them.
r() {
	local n=$1
	shift
	case $n in
	build) ssh -o ConnectTimeout=15 hk-155 docker exec deneb-node /denebd "$@" ;;
	nas) ssh -o ConnectTimeout=15 hk-spare docker exec deneb-node /denebd "$@" ;;
	desktop) "${WIN_SSH[@]}" "%USERPROFILE%\\deneb\\denebd.exe $*" | tr -d '\r' ;;
	esac
}
ensure() { r "$@" >/dev/null 2>&1 || true; } # "already open" is fine
user_pids() { pgrep -u "$(id -u)" -f 'denebd run'; }
kernel_pid() { pgrep -u 0 -f 'denebd run -mode kernel'; }
ui_pid() { pgrep -u "$(id -u)" -f '/Contents/MacOS/Deneb'; }
via() { route -n get "$1" 2>/dev/null | awk '/interface:/ {print $2}'; }
# resolve asks the system resolver the way programs do (getaddrinfo, which
# goes to mDNSResponder), not dscacheutil, which keeps a cache of its own.
resolve() { python3 -I -c 'import socket, sys
try:
    print(socket.getaddrinfo(sys.argv[1], None, socket.AF_INET)[0][4][0])
except OSError:
    pass' "$1"; }
# --noproxy: a Clash proxy in the environment must not get the request.
web() { curl -s --noproxy '*' --connect-timeout 1 --max-time 2 "http://$1/"; }
pings() { ping -c 1 -W 700 "$1" >/dev/null 2>&1; }
works() { "$@" >/dev/null 2>&1; }
fails() { ! "$@" >/dev/null 2>&1; }
no_name() { [ -z "$(resolve "$1")" ]; }
resolves_to() { [ "$(resolve "$1")" = "$2" ]; }
gone() { [ -z "$("$@")" ]; }
# poll <ms> <command...>: run it until it succeeds; print how long that took.
poll() {
	local t0 lim
	t0=$(ms)
	lim=$1
	shift
	while :; do
		if "$@"; then
			echo $(($(ms) - t0))
			return 0
		fi
		[ $(($(ms) - t0)) -ge "$lim" ] && return 1
		sleep 0.2
	done
}

# snapshot <file>: what must look the same afterwards. Host routes (ARP and
# the like) come and go with traffic and are left out.
snapshot() {
	{
		echo "# interfaces"
		ifconfig -l | tr ' ' '\n' | sort
		echo "# routes"
		netstat -rn -f inet | awk 'NR > 4 && $3 !~ /H|W/ {print $1, $2, $NF}' | sort
		echo "# /etc/resolver"
		ls /etc/resolver 2>/dev/null
		echo "# dns"
		scutil --dns | grep -E 'nameserver\[|domain   ' | sort -u
		echo "# ip forwarding $(sysctl -n net.inet.ip.forwarding)"
	} >"$1"
}

# What the state directory says about the network: the block and its
# netstat spelling (10.48.0.0/16 is "10.48/16").
read_network() {
	BLOCK=$(jq -r .network.ipv4 "$STATE/netmap.json" 2>/dev/null)
	BLOCK_NS=$(echo "$BLOCK" | awk -F'[./]' '{print $1 "." $2 "/" $5}')
	DOMAIN=$(jq -r .network.domain "$STATE/netmap.json" 2>/dev/null)
}

leftovers() { # true when something is left over, and says what
	local any=1
	if [ -n "${UTUN:-}" ] && ifconfig "$UTUN" >/dev/null 2>&1; then
		echo "     还在：网卡 $UTUN"
		any=0
	fi
	if [ -n "${BLOCK_NS:-}" ] && netstat -rn -f inet | awk -v b="$BLOCK_NS" '$1 == b' | grep -q .; then
		echo "     还在：$BLOCK 的路由"
		any=0
	fi
	if [ -e /etc/resolver/deneb.internal ]; then
		echo "     还在：/etc/resolver/deneb.internal"
		any=0
	fi
	if [ -S "$KSOCK" ] || [ -n "$(kernel_pid)" ]; then
		echo "     还在：内核模式的引擎或它的套接字 $KSOCK"
		any=0
	fi
	return $any
}

stop_kernel() {
	[ -n "$(kernel_pid)" ] || return 0
	k debug shutdown >/dev/null 2>&1
	poll 10000 gone kernel_pid >/dev/null && return 0
	echo "     引擎没理会停止请求，发 SIGTERM"
	sudo -n kill -TERM $(kernel_pid) 2>/dev/null
	poll 10000 gone kernel_pid >/dev/null && return 0
	echo "     还在，强行结束（留下的交给 kernel-undo）"
	sudo -n kill -KILL $(kernel_pid) 2>/dev/null
	poll 5000 gone kernel_pid >/dev/null
}

# The window would start an engine whenever none answers; freeze it while
# the engine under it is swapped, and always let it go again.
UI=""
freeze_ui() {
	UI=$(ui_pid | head -1)
	[ -n "$UI" ] && kill -STOP "$UI"
}
thaw_ui() {
	[ -n "$UI" ] && kill -CONT "$UI" 2>/dev/null
	UI=""
}

start_user() {
	(DENEB_SOCKET="$USOCK" nohup "$UBIN" run >>"$STATE/denebd.log" 2>&1 </dev/null &)
	poll 10000 works u status >/dev/null
}

# bring_back_user: the window must be frozen around it (see freeze_ui).
bring_back_user() {
	if ! works u status; then
		start_user || echo "     用户态引擎 10 秒内没应答，看 $STATE/denebd.log"
	fi
	local s
	s=$(u status --json 2>/dev/null)
	if [ "$(jq -r .mode <<<"$s")" = userspace ] && [ "$(jq -r .loggedIn <<<"$s")" = true ] &&
		{ [ -z "${SELF_ID:-}" ] || [ "$(jq -r .selfDeviceID <<<"$s")" = "$SELF_ID" ]; }; then
		ok "用户态引擎回来了：还是这台设备（$(jq -r .selfDeviceID <<<"$s")），已登录"
	else
		bad "用户态引擎没回来，或者不是原来那台设备：$(jq -c '{mode, selfDeviceID, loggedIn}' <<<"$s" 2>/dev/null)"
	fi
}

fix_owner() {
	if [ -n "$(find "$STATE" ! -user "$(id -u)" 2>/dev/null)" ]; then
		sudo -n chown -R "$(id -u):$(id -g)" "$STATE"
	fi
	[ -z "$(find "$STATE" ! -user "$(id -u)" 2>/dev/null)" ] && ok "状态目录里的文件都还是你的" ||
		bad "状态目录里有不归你的文件：$(find "$STATE" ! -user "$(id -u)")"
}

# The round's changes on the test machines, written down as they are made,
# so that the end of the round, or --undo after a crash, puts them back.
BUILD_OUT=0 WEB_WEEK=0 NAS_OUT=0 ATTACK_DESKTOP=0 ATTACK_BUILD=0 WEB_OFF=0
mark() {
	eval "$1=$2"
	mkdir -p "$(dirname "$MARKER")"
	printf 'WORK=%s WEEK=%s SELF_NAME=%s BUILD_OUT=%s WEB_WEEK=%s NAS_OUT=%s ATTACK_DESKTOP=%s ATTACK_BUILD=%s WEB_OFF=%s\n' \
		"$WORK" "$WEEK" "$SELF_NAME" "$BUILD_OUT" "$WEB_WEEK" "$NAS_OUT" "$ATTACK_DESKTOP" "$ATTACK_BUILD" "$WEB_OFF" >"$MARKER"
}
restore_remote() {
	[ "$ATTACK_DESKTOP" = 1 ] && r desktop debug unattack "$SELF_NAME" >/dev/null 2>&1 && echo "     撤掉了台式机上的借道设置"
	[ "$ATTACK_BUILD" = 1 ] && r build debug unattack "$SELF_NAME" >/dev/null 2>&1 && echo "     撤掉了构建服务器上的借道设置"
	[ "$WEB_OFF" = 1 ] && ensure build service "$WORK" web tcp/80 on && echo "     构建服务器的网页重新开给工作室"
	if [ "$NAS_OUT" = 1 ]; then
		ensure nas lan add "$WEEK"
		ensure nas service "$WEEK" other tcp/7000 on --name 计时流
		echo "     NAS 加回周末联机"
	fi
	if [ "$BUILD_OUT" = 1 ]; then
		ensure build lan add "$WEEK"
		[ "$WEB_WEEK" = 1 ] && ensure build service "$WEEK" web tcp/80 on
		if poll 8000 build_in_week >/dev/null; then
			ok "构建服务器加回了周末联机（和开始前一样）"
		else
			bad "构建服务器没加回周末联机：ssh hk-155 docker exec deneb-node /denebd lan add $WEEK"
		fi
	fi
	rm -f "$MARKER"
}
build_in_week() { u status --json 2>/dev/null | jq -e --arg w "lan/$WEEK" '.peers[] | select(.name == "build") | .scopes | index($w)' >/dev/null; }
build_only_work() { [ "$(k status --json 2>/dev/null | jq -r '.peers[] | select(.name == "build") | .scopes | join(",")')" = "lan/$WORK" ]; }

# How many packets the desktop's WireGuard dropped for a forged source.
win_disallowed() {
	local ps='$ProgressPreference="SilentlyContinue"; (Select-String -Path (Join-Path $env:USERPROFILE "deneb\denebd.log") -Pattern "disallowed source" -SimpleMatch).Count'
	"${WIN_SSH[@]}" "powershell -NoProfile -EncodedCommand $(printf '%s' "$ps" | iconv -f UTF-8 -t UTF-16LE | base64)" 2>/dev/null |
		tr -d '\r' | sed 's/.*<\/Objs>//' | grep -E '^[0-9]+$' | tail -1
}

peer() { jq -r --arg n "$1" ".peers[] | select(.name == \$n) | $2" <<<"$ST"; }

BUILD_IN_WEEK=0
preflight() {
	[ "$(uname)" = Darwin ] || die "只在 macOS 上跑"
	[ "$(id -u)" != 0 ] || die "用你自己的账号跑，前面别加 sudo：脚本只在要用的那一步要一次密码"
	for c in go jq curl python3 perl dscacheutil; do
		command -v "$c" >/dev/null || die "需要 $c"
	done
	[ -x "$UBIN" ] || die "找不到 $UBIN（界面拉起的就是它）"
	[ -z "$(kernel_pid)" ] && [ ! -S "$KSOCK" ] || die "已经有内核模式的引擎在跑，或者它的套接字还在：先跑 $0 --undo"
	[ ! -e /etc/resolver/deneb.internal ] || die "/etc/resolver/deneb.internal 已经有了（上次没撤干净？先跑 $0 --undo）"
	[ ! -e "$MARKER" ] || die "上一轮在测试机上的改动还没放回去：先跑 $0 --undo"
	ST=$(DENEB_SOCKET="$USOCK" "$UBIN" status --json 2>/dev/null) || die "用户态引擎没在 $USOCK 上应答：先打开 Deneb 窗口"
	[ "$(jq -r .mode <<<"$ST")" = userspace ] || die "引擎不在用户态"
	[ "$(jq -r .loggedIn <<<"$ST")" = true ] || die "这台 Mac 还没登录"
	SELF_ID=$(jq -r .selfDeviceID <<<"$ST")
	SELF_NAME=$(jq -r .self.name <<<"$ST")
	SELF_ADDR=$(jq -r '.self.addrs[0]' <<<"$ST")
	read_network
	case $BLOCK in 10.* | 172.1[6-9].* | 172.2?.* | 172.3[01].*) ;; *) die "地址段 $BLOCK 不在预期的私有网段里" ;; esac
	WORK=$(jq -r '.scopes[] | select(.name == "工作室") | .id | sub("^lan/"; "")' <<<"$ST")
	WEEK=$(jq -r '.scopes[] | select(.name == "周末联机") | .id | sub("^lan/"; "")' <<<"$ST")
	[ -n "$WORK" ] && [ -n "$WEEK" ] || die "这台 Mac 要同时在「工作室」和「周末联机」里"
	local n want got topo=1
	for n in build desktop nas; do
		[ -n "$(peer $n .name)" ] || die "这台 Mac 看不见 $n"
		want=$WEEK
		[ $n = build ] && want=$WORK
		got=$(peer $n '[.scopes[] | sub("^lan/"; "")] | sort | join(",")')
		if [ $n = build ] && [ "$got" = "$(printf '%s\n%s\n' "$WORK" "$WEEK" | sort | paste -sd, -)" ]; then
			BUILD_IN_WEEK=1 # taken out for the round, put back at the end
		elif [ "$got" != "$want" ]; then
			echo "     $n 和这台 Mac 共有的局域网是 $got，应当是 $want"
			topo=0
		fi
	done
	[ $topo = 1 ] || die "拓扑不对：构建服务器该在工作室里，台式机和 NAS 只该在周末联机里"
	BUILD_ADDR=$(peer build '.addrs[0]')
	BUILD_FQDN=$(peer build .fqdn)
	DESKTOP_ADDR=$(peer desktop '.addrs[0]')
	DESKTOP_FQDN=$(peer desktop .fqdn)
	NAS_ADDR=$(peer nas '.addrs[0]')
	NAS_FQDN=$(peer nas .fqdn)
	TS_IF=$(via 100.100.100.100)
	NET_IF=$(via 1.1.1.1)
	MON=$(ps -o args= -p "$(pgrep -f monitor-coexist.sh | head -1)" 2>/dev/null | awk '{print $3}')
}

show_plan() {
	say "准备好了"
	echo "     这台 Mac：$SELF_NAME（$SELF_ID），地址 $SELF_ADDR，地址段 $BLOCK"
	echo "     对端：构建服务器 $BUILD_FQDN $BUILD_ADDR；台式机 $DESKTOP_FQDN $DESKTOP_ADDR；NAS $NAS_FQDN $NAS_ADDR"
	echo "     现在去 Tailscale 的走 $TS_IF，去公网的走 $NET_IF；IP 转发 $(sysctl -n net.inet.ip.forwarding)"
	echo "     窗口进程：$(ui_pid | head -1 || true)；第 12 条的监控：${MON:-没在跑（不影响本轮，只是少了共存的数据）}"
	echo "     会改：一张 utun、一条 $BLOCK 路由、/etc/resolver/$DOMAIN；结束时全部撤掉并和开始前对比"
	[ "$BUILD_IN_WEEK" = 1 ] && echo "     构建服务器现在也在周末联机里：检查前先把它撤出（第 6 条要验台式机和 NAS 看不见它），结束时加回去"
	return 0
}

checks() {
	if [ "$BUILD_IN_WEEK" = 1 ]; then
		say "把构建服务器暂时撤出周末联机"
		[ "$(r nas debug http build | jq -r .ok)" = true ] && WEB_WEEK=1
		mark BUILD_OUT 1
		r build lan remove "$WEEK" >/dev/null || bad "构建服务器撤不出周末联机"
		poll 8000 build_only_work >/dev/null && ok "构建服务器现在只在工作室里" || bad "8 秒后构建服务器还在周末联机里"
	fi

	say "路由、DNS 和共存"
	[ "$(via "$BUILD_ADDR")" = "$UTUN" ] && ok "去 $BLOCK 的走 $UTUN" || bad "去 $BUILD_ADDR 走的是 $(via "$BUILD_ADDR")"
	[ "$(via 100.100.100.100)" = "$TS_IF" ] && ok "去 Tailscale 的仍走 $TS_IF" || bad "去 Tailscale 的改走 $(via 100.100.100.100)"
	[ "$(via 1.1.1.1)" = "$NET_IF" ] && ok "去公网的仍走 $NET_IF" || bad "去公网的改走 $(via 1.1.1.1)"
	[ "$(sysctl -n net.inet.ip.forwarding)" = 0 ] && ok "系统 IP 转发是关的" || bad "系统 IP 转发开着"
	local f a
	for f in "$BUILD_FQDN $BUILD_ADDR" "$DESKTOP_FQDN $DESKTOP_ADDR" "$NAS_FQDN $NAS_ADDR"; do
		set -- $f
		a=$(resolve "$1")
		[ "$a" = "$2" ] && ok "系统解析 $1 → $a" || bad "系统解析 $1 得到「$a」，应为 $2"
	done
	no_name "nobody-$$.$DOMAIN" && ok "不存在的 .$DOMAIN 名字查无此名" || bad "不存在的名字居然解析出来了"
	[ -n "$(resolve www.baidu.com)" ] && ok "普通域名照旧能解析" || bad "普通域名解析不了"

	say "第 8 条：服务按局域网开放，普通程序直接访问"
	ensure build service "$WORK" web tcp/80 on
	local out t
	poll 4000 works web "$BUILD_FQDN" >/dev/null
	out=$(web "$BUILD_FQDN")
	case $out in "hello from"*) ok "curl http://$BUILD_FQDN/ ：$out" ;; *) bad "curl http://$BUILD_FQDN/ 打不开：$out" ;; esac
	fails curl -s --noproxy '*' --connect-timeout 1 --max-time 2 "http://$BUILD_FQDN:9000/" && ok "构建服务器没开放的 9000 端口连不上" || bad "没开放的 9000 端口能连上"
	mark WEB_OFF 1
	r build service "$WORK" web tcp/80 off >/dev/null
	t=$(poll 4000 fails web "$BUILD_FQDN") && ok "在工作室关掉网页后 ${t}ms 打不开" || bad "关掉 4 秒后还能打开"
	r build service "$WORK" web tcp/80 on >/dev/null
	mark WEB_OFF 0
	t=$(poll 4000 works web "$BUILD_FQDN") && ok "重新开放后 ${t}ms 又能打开" || bad "重新开放 4 秒后还打不开"

	say "第 4 条：同时在两个局域网，三条长连接各 ${TICK_SECS} 秒（这时可以在窗口里来回切侧边栏）"
	ensure build service "$WORK" other tcp/7000 on --name 计时流
	ensure desktop service "$WEEK" other tcp/7000 on --name tick
	ensure nas service "$WEEK" other tcp/7000 on --name 计时流
	local line
	while read -r line; do
		if [ "$(jq -r .broken <<<"$line")" = false ] && [ "$(jq -r .maxGapMs <<<"$line")" -le 500 ]; then
			ok "长连接 $(jq -r '"\(.host)：\(.lines) 行，最长停 \(.maxGapMs)ms，序号跳了 \(.seqGaps) 次"' <<<"$line")"
		else
			bad "长连接 $line"
		fi
	done < <(python3 -I test/topo/tickread.py "$TICK_SECS" "$BUILD_FQDN" "$DESKTOP_FQDN" "$NAS_FQDN")

	say "第 6 条：隔离"
	[ -z "$(r desktop status --json | jq -r '.peers[] | select(.name == "build") | .name')" ] && ok "台式机看不见构建服务器" || bad "台式机看得见构建服务器"
	[ -z "$(r nas status --json | jq -r '.peers[] | select(.name == "build") | .name')" ] && ok "NAS 看不见构建服务器" || bad "NAS 看得见构建服务器"
	local before after got1 got2
	before=$(k debug drops | jq '.drops["in:transit-in"] // 0')
	mark ATTACK_DESKTOP 1
	r desktop debug attack "$SELF_NAME" "$BUILD_ADDR/32" >/dev/null
	got1=$(r desktop debug ping-addr "$BUILD_ADDR" | jq .received)
	r desktop debug unattack "$SELF_NAME" >/dev/null && mark ATTACK_DESKTOP 0
	mark ATTACK_BUILD 1
	r build debug attack "$SELF_NAME" "$DESKTOP_ADDR/32" >/dev/null
	got2=$(r build debug ping-addr "$DESKTOP_ADDR" | jq .received)
	r build debug unattack "$SELF_NAME" >/dev/null && mark ATTACK_BUILD 0
	after=$(k debug drops | jq '.drops["in:transit-in"] // 0')
	if [ "$got1" = 0 ] && [ "$got2" = 0 ] && [ "$after" -gt "$before" ]; then
		ok "借道两个方向都不通，Mac 丢了 $((after - before)) 个（transit-in）"
	else
		bad "借道：台式机收到 $got1，构建服务器收到 $got2，Mac 的 transit-in $before → $after"
	fi
	# The attack routes widen WireGuard's allowed addresses; with them gone,
	# a forged source must be dropped by WireGuard itself.
	local n0 n1 since
	n0=$(win_disallowed)
	since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	k debug inject "$BUILD_ADDR" "$DESKTOP_ADDR" 80 >/dev/null
	k debug inject "$DESKTOP_ADDR" "$BUILD_ADDR" 80 >/dev/null
	sleep 2
	n1=$(win_disallowed)
	[ -n "$n1" ] && [ "${n1:-0}" -gt "${n0:-0}" ] && ok "伪造成构建服务器发给台式机：台式机的 WireGuard 丢了" || bad "台式机没记下伪造的包（$n0 → $n1）"
	ssh hk-155 "docker logs --since $since deneb-node 2>&1" | grep -q 'disallowed source' &&
		ok "伪造成台式机发给构建服务器：构建服务器的 WireGuard 丢了" || bad "构建服务器没记下伪造的包"

	say "第 7 条（内核模式）：设备撤出、加回，普通程序跟着变"
	pings "$NAS_ADDR" && ok "开始时 ping 得通 NAS" || bad "开始时就 ping 不通 NAS"
	mark NAS_OUT 1
	r nas lan remove "$WEEK" >/dev/null
	t=$(poll 4000 fails pings "$NAS_ADDR") && ok "NAS 撤出周末联机后 ${t}ms ping 不通" || bad "NAS 撤出 4 秒后还 ping 得通"
	t=$(poll 8000 no_name "$NAS_FQDN") && ok "$NAS_FQDN ${t}ms 后查不到了（DNS 缓存 5 秒）" || bad "撤出 8 秒后名字还查得到"
	# First, for the record: how long does macOS keep "no such name" if
	# nobody flushes its caches? (The engine normally does; off for now.)
	k debug dnsflush off >/dev/null
	r nas lan add "$WEEK" >/dev/null
	ensure nas service "$WEEK" other tcp/7000 on --name 计时流
	mark NAS_OUT 0
	t=$(poll 4000 works pings "$NAS_ADDR") && ok "加回后 ${t}ms 又 ping 得通" || bad "加回 4 秒后还 ping 不通"
	if t=$(poll 20000 resolves_to "$NAS_FQDN" "$NAS_ADDR"); then
		echo "     记一笔：不刷系统缓存时，名字 ${t}ms 后才能解析"
	else
		echo "     记一笔：不刷系统缓存时，名字 20 秒后还解析不了"
	fi
	# Then the real check: out again, and back with the engine flushing
	# the system's caches as it does when a name comes back.
	mark NAS_OUT 1
	r nas lan remove "$WEEK" >/dev/null
	poll 4000 fails pings "$NAS_ADDR" >/dev/null
	poll 8000 no_name "$NAS_FQDN" >/dev/null
	k debug dnsflush on >/dev/null
	r nas lan add "$WEEK" >/dev/null
	ensure nas service "$WEEK" other tcp/7000 on --name 计时流
	mark NAS_OUT 0
	t=$(poll 4000 works pings "$NAS_ADDR") && ok "再加回后 ${t}ms 又 ping 得通" || bad "再加回 4 秒后还 ping 不通"
	t=$(poll 8000 resolves_to "$NAS_FQDN" "$NAS_ADDR") && ok "名字 ${t}ms 后又能解析（引擎刷了系统缓存）" || bad "加回后名字解析不了"
}

T_START=$(date +%s)
T_UP=""
T_DOWN=""
KEEP=""
UTUN=""

cleanup() {
	local rc=$?
	trap - EXIT
	trap '' INT TERM # this part must finish: it puts the Mac back
	say "撤销"
	freeze_ui
	stop_kernel
	T_DOWN=$(date +%s)
	if leftovers; then
		echo "     有残留，跑 kernel-undo"
		sudo -n "$KBIN" kernel-undo -state "$STATE"
	fi
	if leftovers; then
		bad "还有残留（见上）。手动撤：sudo $KBIN kernel-undo -state \"$STATE\""
	else
		ok "网卡、路由、/etc/resolver/$DOMAIN、内核引擎的套接字都没了"
	fi
	fix_owner
	bring_back_user
	thaw_ui
	snapshot "$RUN/after.txt"
	if diff -u "$RUN/before.txt" "$RUN/after.txt" >"$RUN/diff.txt"; then
		ok "网卡列表、路由表、DNS 配置和开始前一致"
	else
		bad "和开始前不一样（$RUN/diff.txt）："
		sed -n '3,$p' "$RUN/diff.txt"
	fi
	[ -n "$KEEP" ] && kill "$KEEP" 2>/dev/null
	sudo -k
	trap - INT TERM
	if [ -e "$MARKER" ]; then
		say "放回这一轮在测试机上的改动"
		restore_remote
	fi
	echo
	echo "$pass 项通过，$fail 项没过。整份记录：$RUN/round.log"
	if [ -n "$T_UP" ] && [ -n "${MON:-}" ]; then
		echo "第 12 条按时段汇总（网卡在的时段是 $T_UP–$T_DOWN）："
		echo "  test/topo/monitor-coexist.sh --summary $MON $T_START $T_UP"
		echo "  test/topo/monitor-coexist.sh --summary $MON $T_UP $T_DOWN"
		echo "  test/topo/monitor-coexist.sh --summary $MON $T_DOWN $(($(date +%s) + 120))   # 撤掉以后，过两分钟再跑"
	fi
	[ "$rc" = 0 ] && [ "$fail" = 0 ] && exit 0
	exit 1
}

get_engine() {
	if [ -x "$PREBUILT" ]; then
		cp "$PREBUILT" "$KBIN" || die "复制 $PREBUILT 失败"
		say "用预先编好的引擎（$(basename "$PREBUILT")，$(stat -f '%Sm' -t '%m-%d %H:%M' "$PREBUILT") 编的）"
	else
		say "编译这一轮用的引擎（测试构建）"
		go build -tags denebtest -ldflags "-X main.version=m1-kernel-$(date +%m%d%H%M)" -o "$KBIN" ./cmd/denebd || die "编译失败"
	fi
}

kernel_up() { # the kernel-mode engine answers, in kernel mode, on our address, with its route
	local s
	s=$(k status --json 2>/dev/null) || return 1
	[ "$(jq -r .mode <<<"$s")" = kernel ] && [ "$(jq -r '.self.addrs[0] // ""' <<<"$s")" = "$SELF_ADDR" ] &&
		[ "$(jq '[.engine.problems[]? | select(.code == "datapath")] | length' <<<"$s")" = 0 ] &&
		[ "$(via "$BUILD_ADDR")" != "$NET_IF" ]
}
ui_on_kernel() { [ "$(k status --json 2>/dev/null | jq -r .engine.watchers)" -ge 1 ] 2>/dev/null; }

round() {
	preflight
	mkdir -p "$RUN"
	snapshot "$RUN/before.txt"
	show_plan
	if [ "${1:-}" = dry ]; then
		echo "（--dry-run：到此为止，什么都没改）"
		rm -rf -- "${RUN:?}"
		exit 0
	fi
	exec > >(tee -a "$RUN/round.log") 2>&1
	get_engine
	say "要一次密码（sudo）"
	sudo ${SUDO_ASKPASS:+-A} -v || die "没拿到 sudo"
	(while kill -0 $$ 2>/dev/null; do
		sudo -n -v 2>/dev/null
		sleep 30
	done) &
	KEEP=$!
	trap cleanup EXIT
	trap 'echo; echo "收到中断，开始撤销"; exit 130' INT TERM

	say "换成内核模式的引擎（窗口会停一两秒）"
	freeze_ui
	local pids
	pids=$(user_pids)
	[ -n "$pids" ] && kill -TERM $pids
	poll 10000 gone user_pids >/dev/null || die "用户态引擎 10 秒内没停"
	: >>"$RUN/denebd-kernel.log"
	sudo -n /bin/sh -c 'exec "$0" run -mode kernel -allow-uid "$1" -state "$2" -wait-lock 10s >>"$3" 2>&1' \
		"$KBIN" "$(id -u)" "$STATE" "$RUN/denebd-kernel.log" &
	local t
	t=$(poll 15000 works k status) || die "内核模式的引擎 15 秒内没应答，看 $RUN/denebd-kernel.log"
	thaw_ui
	t=$(poll 30000 kernel_up) || die "内核模式的引擎没把网卡和路由备好，看 $RUN/denebd-kernel.log"
	T_UP=$(date +%s)
	UTUN=$(comm -13 <(sed -n '/^# interfaces/,/^# routes/p' "$RUN/before.txt" | grep -v '^#') <(ifconfig -l | tr ' ' '\n' | sort) | grep '^utun' | head -1)
	ok "内核模式起来了：网卡 $UTUN，地址 $SELF_ADDR，$(grep -o '\[tun\] utun[0-9]* up.*' "$RUN/denebd-kernel.log" | tail -1)"
	[ -z "$(user_pids)" ] && ok "没有别的引擎在用这份状态" || bad "还有用户态引擎在跑：$(user_pids)"
	if t=$(poll 20000 ui_on_kernel); then
		ok "窗口连上了内核模式的引擎（${t}ms）"
	else
		echo "     注意：窗口 20 秒内没连上内核模式的引擎；检查照做"
	fi
	checks
}

undo() {
	[ "$(id -u)" != 0 ] || die "用你自己的账号跑，前面别加 sudo"
	read_network
	mkdir -p "$RUN"
	get_engine
	say "撤销上一轮留下的东西"
	sudo ${SUDO_ASKPASS:+-A} -v || die "没拿到 sudo"
	kill -CONT $(ui_pid) 2>/dev/null # a round killed mid-swap leaves the window frozen
	freeze_ui
	stop_kernel
	sudo -n "$KBIN" kernel-undo -state "$STATE"
	if [ -e /etc/resolver/deneb.internal ] && grep -q 'Written by Deneb' /etc/resolver/deneb.internal; then
		sudo -n rm /etc/resolver/deneb.internal && echo "     删掉了 /etc/resolver/deneb.internal"
	fi
	leftovers && bad "还有残留（见上）" || ok "网卡、路由、/etc/resolver/deneb.internal、内核引擎的套接字都没了"
	fix_owner
	bring_back_user
	thaw_ui
	sudo -k
	if [ -e "$MARKER" ]; then
		say "放回上一轮在测试机上的改动"
		# shellcheck disable=SC1090
		. "$MARKER"
		restore_remote
	fi
	rm -rf -- "${RUN:?}"
	[ "$fail" = 0 ]
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
	case "${1:-}" in
	"") round ;;
	--dry-run) round dry ;;
	--undo) undo ;;
	*)
		echo "用法：$0 [--dry-run | --undo]" >&2
		exit 2
		;;
	esac
fi
