#!/bin/sh
# Runs a command with the key that signs Deneb's updates in
# MYGO_UPDATER_PRIVATE_KEY, read from this Mac's login keychain, where it
# is kept instead of in a file: macOS asks the person at this Mac before
# handing it out, so nothing signs a release without them (deploy/updates.md).
#
#   sh deploy/with-key.sh sh deploy/package.sh macos
#   sh deploy/with-key.sh sh deploy/relay/release.sh
#   sh deploy/with-key.sh --setup [key file]   moves the key from its file
#                                              into the keychain
#
# Run it in Terminal: the keychain's dialog asks for the login password;
# answer 允许 (Allow), never 始终允许 (Always Allow), which would hand the
# key to any program after.
set -eu
item=deneb-update-key

case ${1:-} in
--setup)
	file=${2:-"$HOME/Library/Application Support/mygo/update-keys/mygo-update.key"}
	[ -f "$file" ] || { echo "没有私钥文件：$file" >&2; exit 1; }
	key=$(cat "$file")
	security delete-generic-password -s "$item" >/dev/null 2>&1 || true
	# Only /usr/bin/true, which never asks, may read it without the dialog:
	# every other program, security itself included, has to be allowed.
	security add-generic-password -a deneb -s "$item" -T /usr/bin/true -w "$key"
	echo "已存入钥匙串。接下来会弹出确认框：输入登录密码，点“允许”，不要点“始终允许”。"
	start=$(date +%s)
	back=$(security find-generic-password -s "$item" -w) || { echo "没能读回来，私钥文件留着" >&2; exit 1; }
	if [ "$back" != "$key" ]; then
		echo "读回来的和文件不一样，私钥文件留着" >&2
		exit 1
	fi
	if [ $(($(date +%s) - start)) -lt 2 ]; then
		# Read back at once: nothing asked, the keychain hands it to anyone.
		security delete-generic-password -s "$item" >/dev/null 2>&1 || true
		echo "读的时候没有弹确认框，保护没有生效：已从钥匙串删掉，私钥文件留着" >&2
		exit 1
	fi
	echo "一致，读之前问过你了。"
	printf '私钥在密码管理器里有备份吗？有就删掉文件 %s [y/N] ' "$file"
	read -r yes </dev/tty || yes=
	case $yes in
	y | Y)
		rm "$file"
		echo "私钥文件已删除。以后签名：sh deploy/with-key.sh <命令>"
		;;
	*) echo "私钥文件留着。备份好以后自己删：rm \"$file\"" ;;
	esac
	;;
"" | -h | --help)
	sed -n '2,15p' "$0"
	exit 2
	;;
*)
	start=$(date +%s)
	key=$(security find-generic-password -s "$item" -w 2>/dev/null) ||
		{ echo "钥匙串里没有 $item，或者没有允许：先在终端里运行 sh deploy/with-key.sh --setup" >&2; exit 1; }
	if [ $(($(date +%s) - start)) -lt 2 ]; then
		echo "提醒：钥匙串没问你就交出了私钥，可能以前点过“始终允许”。重新运行 sh deploy/with-key.sh --setup 可以恢复保护。" >&2
	fi
	MYGO_UPDATER_PRIVATE_KEY=$key
	export MYGO_UPDATER_PRIVATE_KEY
	exec "$@"
	;;
esac
