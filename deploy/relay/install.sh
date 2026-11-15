#!/bin/sh
# Deneb relay, the one-line install (design/plan/m7/relay-install.md §4):
#
#   curl -fsSL https://deneb.app/relay | sh -s -- <join code> <version>:<sha256>
#   wget -qO- https://deneb.app/relay | sh -s -- <join code> <version>:<sha256>
#   curl -fsSL https://deneb.app/relay | sh -s -- uninstall    (or status, update)
#
# It does three things: finds the CPU, downloads that build of deneb-relay
# with curl or wget and checks it, and runs `deneb-relay install`, which
# does the rest. <sha256> is that of the release's SHA256SUMS, whose line
# for this CPU the program must match: Deneb puts it in the command, so a
# download changed on the way (or over plain HTTP, on a machine without
# root certificates) is caught. More flags for install (--yes,
# --firewall=skip, --tcp-port=443 …) follow.
#
# POSIX sh only: BusyBox's ash runs it, and nothing beyond what BusyBox
# has: uname, mktemp, sha256sum (or shasum, or openssl), curl or wget.
set -eu

# The download site's relay folder. deploy/relay/release.sh writes it in;
# DENEB_RELAY_BASE overrides it (tests, a mirror).
BASE=${DENEB_RELAY_BASE:-@BASE@}

say() { printf '%s\n' "$*"; }
die() {
	say "✗ $1"
	shift
	for l in "$@"; do say "  $l"; done
	exit 1
}

code= check= cmd=install extra=
for a in "$@"; do
	case $a in
	deneb-join-*) code=$a ;;
	install | uninstall | status | update) cmd=$a ;;
	--*) extra="$extra $a" ;;
	*:*) check=$a ;;
	*) die "不认识的参数：$a" "请在 Deneb 里重新复制安装命令" ;;
	esac
done

# uninstall, status and update are the installed program's.
if [ "$cmd" != install ]; then
	for p in deneb-relay /usr/local/bin/deneb-relay "${HOME:-/nonexistent}/.local/bin/deneb-relay"; do
		if command -v "$p" >/dev/null 2>&1; then
			# Not the rest of this script, which sh is still reading from
			# stdin; deneb-relay asks its questions on the terminal.
			# shellcheck disable=SC2086 # extra is flags, split on purpose
			exec "$p" "$cmd" $extra </dev/null
		fi
	done
	die "这台没有装中转站"
fi

[ "$(uname -s)" = Linux ] || die "中转站只能装在 Linux 上，这台是 $(uname -s)"
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
armv7* | armv8l) arch=armv7 ;;
*) die "暂不支持这种 CPU：$(uname -m)" "现在有 amd64、arm64、armv7（树莓派 2 及以后的 32 位系统）" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --retry 2 --connect-timeout 20 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -T 20 -O "$2" "$1"; }
else
	die "要有 curl 或 wget 才能下载" "Debian、Ubuntu：apt install curl；Alpine：apk add curl；CentOS：yum install curl"
fi

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/.*= *//'
	else
		die "没有算 SHA-256 的工具（sha256sum）" "Alpine、BusyBox 都自带；装 coreutils 也行"
	fi
}

# A folder it may run programs from: /tmp is mounted noexec on some.
dir=
for d in "${TMPDIR:-/tmp}" /var/tmp /dev/shm "${HOME:-/nonexistent}"; do
	t=$(mktemp -d "$d/deneb-relay.XXXXXX" 2>/dev/null) || continue
	printf '#!/bin/sh\nexit 0\n' >"$t/x"
	chmod +x "$t/x"
	if "$t/x" 2>/dev/null; then
		dir=$t
		break
	fi
	rm -rf "$t"
done
[ -n "$dir" ] || die "找不到能运行程序的临时目录（/tmp、/var/tmp 都不让运行？）"
trap 'rm -rf "$dir"' EXIT INT TERM

# get downloads a file of the site; over HTTPS, or, when that fails and
# the files are checked against the command's SHA-256, over HTTP.
get() {
	if fetch "$BASE/$1" "$dir/$2" 2>"$dir/err"; then
		return 0
	fi
	case $BASE in
	https://*)
		if [ -n "$check" ]; then
			fetch "http://${BASE#https://}/$1" "$dir/$2" 2>>"$dir/err" && return 0
		fi
		;;
	esac
	die "下载 $1 失败：$(head -n 1 "$dir/err")" "这台要能访问外网；要走代理的，请先设好 HTTPS_PROXY；系统时间不对或者缺根证书时 HTTPS 也会失败"
}

if [ -n "$check" ]; then
	version=${check%%:*}
	want=${check#*:}
	get "$version/SHA256SUMS" sums
	[ "$(sha256 "$dir/sums")" = "$want" ] || die "下载的校验文件不对，可能被改过" "请在 Deneb 里重新复制安装命令再试"
else
	get latest latest
	version=$(head -n 1 "$dir/latest")
	say "! 命令里没有校验值：只靠 HTTPS 保证下载没被改过"
fi

name=deneb-relay-linux-$arch
sum=$(grep " \*\{0,1\}$name\$" "$dir/sums" 2>/dev/null | cut -d' ' -f1 || true)
get "$version/$name" deneb-relay
if [ -n "$check" ]; then
	[ -n "$sum" ] || die "这一版没有 $arch 的程序"
	[ "$(sha256 "$dir/deneb-relay")" = "$sum" ] || die "下载的程序校验值不对，可能被改过" "请在 Deneb 里重新复制安装命令再试"
fi
chmod +x "$dir/deneb-relay"
"$dir/deneb-relay" version >/dev/null 2>&1 || die "下载的程序在这台机器上运行不了（$(uname -m)）"
if [ -n "$check" ]; then
	say "✓ 已下载 deneb-relay $version（校验通过）"
else
	say "✓ 已下载 deneb-relay $version"
fi

set +e
# shellcheck disable=SC2086 # extra is flags, split on purpose
"$dir/deneb-relay" install ${code:+--join "$code"} $extra </dev/null
rc=$?
if [ "$rc" -ne 0 ] && [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
	say "  要装成系统服务，在命令的 sh 前面加 sudo 再运行一次"
fi
exit "$rc"
