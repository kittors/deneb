#!/usr/bin/env bash
# Screenshots of the real Deneb window and tray icon on three systems, for
# M5 u1 and u2 and M6 a1: this Mac, the Windows test machine and the Ubuntu
# 26.04 virtual machine. They go into one folder, under one naming rule,
# with comparison images (compose.py):
#
#   <platform>-window.png  the window, its frame without the shadow
#   <platform>-tray.png    the tray icon where the system shows it
#   windows-tray-hidden.png  Windows 11's hidden icons, opened
#   <platform>-screen.png  the whole screen (not this Mac's unless --mac-screen)
#   <platform>-before.png  the screen before the window opened (for compose.py)
#   <platform>-info.txt    sizes, scale, what was run
#   compare-window.png, compare-tray.png, compare-screen.png
#
# with <platform> one of mac, windows, ubuntu.
#
#   test/topo/gui/shoot.sh [--mac <Deneb.app>] [--win <folder with Deneb.exe>]
#       [--linux "<deneb .deb> <denebd .deb>"] [--world fake|fresh|demo|real]
#       [--out <folder>] [--keep-vm] [--mac-screen]
#
# --world is what the window shows. demo (the default): the design's sample
# (DENEB_DEMO=1), in the window's memory: the same on every system, so that
# the screenshots compare, and no engine is touched. fake: client/fakeworld's
# M1 topology (DENEB_FAKE=ready; not on Windows yet, where its local API
# would need a pipe name); fresh: that world before logging in; real: the
# engine the window finds (not on this Mac, whose engine is the user's).
#
# This Mac: a separate instance of the app given, never the dev window.
# Windows: the files are copied to %USERPROFILE%\deneb\gui\<run>, the window
# starts in the console user's desktop session through a scheduled task
# that runs only while that user is logged on, with a limited token; the
# screenshots are taken there the same way. Then the window is closed, the
# tasks and the folder are removed, and the user-space engine is started as
# start.ps1 does if it is not running. The network, the firewall, Tailscale
# and UAC are left alone. Ubuntu: the virtual machine goes back to its
# snapshot, starts without a window on this Mac, gets the packages
# installed, shows the window, and goes back to its snapshot when done
# (unless --keep-vm).
#
# Machines and secrets: the environment, or test/topo/.accept/gui.env
# (ignored by git; see gui.env.example). Nothing private is in this folder.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
env_file=${DENEB_GUI_ENV:-$root/test/topo/.accept/gui.env}
# shellcheck disable=SC1090
[ -f "$env_file" ] && . "$env_file"

WIN_SSH=${WIN_SSH:-Administrator@100.122.56.85}
WIN_ALIAS=${WIN_ALIAS:-100.122.56.85} # the test machine's name in known_hosts
U26_VMX=${U26_VMX:-$HOME/Virtual Machines.localized/Ubuntu 26.04 Deneb.vmwarevm/Ubuntu 26.04 Deneb.vmx}
U26_SECRETS=${U26_SECRETS:-$HOME/VMs/deneb-u26} # password, ssh_ed25519, known_hosts
U26_USER=${U26_USER:-mia}
U26_SNAPSHOT=${U26_SNAPSHOT:-clean-os-no-deneb}
# Its screen while shooting: at the 1280x800 it boots with, GNOME maximizes
# a window of 1180x740 points, which then compares with nothing.
U26_MODE=${U26_MODE:-1920x1200@59.950}
VMRUN=${VMRUN:-/Applications/VMware Fusion.app/Contents/Library/vmrun}

mac_app= win_dir= linux_debs= world=demo out= keep_vm=0 mac_screen=0
while [ $# -gt 0 ]; do
	case $1 in
	--mac) mac_app=$2; shift 2 ;;
	--win) win_dir=$2; shift 2 ;;
	--linux) linux_debs=$2; shift 2 ;;
	--world) world=$2; shift 2 ;;
	--out) out=$2; shift 2 ;;
	--keep-vm) keep_vm=1; shift ;;
	--mac-screen) mac_screen=1; shift ;;
	*) echo "shoot: unknown argument $1 (see the top of $0)" >&2; exit 2 ;;
	esac
done
stamp=$(date +%m%d%H%M%S)
out=${out:-$root/test/topo/.accept/gui-$stamp}
mkdir -p "$out"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
say() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*"; }
die() { echo "shoot: $*" >&2; exit 1; }

# The world as environment variables, NAME=value per line.
world_env() {
	case $world in
	fake) echo DENEB_FAKE=ready ;;
	fresh) echo DENEB_FAKE=fresh ;;
	demo) echo DENEB_DEMO=1 ;;
	real) ;;
	*) die "--world $world: fake, fresh, demo or real" ;;
	esac
}

# ---- this Mac --------------------------------------------------------------

shoot_mac() {
	local app=$1 bin="$1/Contents/MacOS/Deneb" sw sh bar scale pid line i
	[ -x "$bin" ] || die "--mac $app: no $bin"
	[ "$world" != real ] || die "--world real on this Mac would put the window on the user's own engine"
	swiftc -O -o "$tmp/mac-windows" "$here/mac-windows.swift" 2>"$tmp/swiftc.log" || { cat "$tmp/swiftc.log" >&2; die "swiftc"; }
	read -r sw sh bar scale < <("$tmp/mac-windows" screen)
	screencapture -x -R "0,0,$sw,$bar" "$out/mac-before.png"
	# shellcheck disable=SC2046
	env $(world_env) "$bin" >"$out/mac-app.log" 2>&1 &
	pid=$!
	line=
	for i in $(seq 1 50); do
		line=$("$tmp/mac-windows" "$pid" | awk '$2 == 0 && $5 >= 600 { print; exit }')
		[ -n "$line" ] && break
		sleep 0.3
	done
	{
		echo "app $app"
		echo "world $world"
		echo "screen ${sw}x${sh} pt, scale $scale, menu bar $bar pt"
	} >"$out/mac-info.txt"
	if [ -n "$line" ]; then
		sleep 2 # first frames, fades
		set -- $line
		screencapture -x -o -l"$1" "$out/mac-window.png"
		echo "window ${5}x${6} pt at $3,$4" >>"$out/mac-info.txt"
	else
		echo "window: none in 15 s" >>"$out/mac-info.txt"
	fi
	screencapture -x -R "0,0,$sw,$bar" "$out/mac-menubar.png"
	[ "$mac_screen" = 0 ] || screencapture -x "$out/mac-screen.png"
	kill "$pid" 2>/dev/null || true
	wait "$pid" 2>/dev/null || true
	say "mac: done"
}

# ---- the Windows test machine ----------------------------------------------

win() { ssh -o ConnectTimeout=10 -o ProxyCommand=none -o HostKeyAlias="$WIN_ALIAS" -o BatchMode=yes "$WIN_SSH" "$@"; }
win_cp() { scp -q -o ConnectTimeout=10 -o ProxyCommand=none -o HostKeyAlias="$WIN_ALIAS" -o BatchMode=yes "$@"; }

# win_task <name> <script> <arguments> <file it writes when done>: the
# script runs in the console user's desktop session (win-task.ps1).
win_task() {
	win "powershell -NoProfile -ExecutionPolicy Bypass -File $R\\win-task.ps1 -Name $1 -Script $R\\$2 -Arguments \"$3\" -WaitFor $4"
}

shoot_windows() {
	local dir=$1 home pid f env_args=
	[ -f "$dir/Deneb.exe" ] || die "--win $dir: no Deneb.exe"
	home=$(win 'echo %USERPROFILE%' | tr -d '\r')
	R="$home\\deneb\\gui\\$stamp" # no spaces: it goes unquoted into the tasks
	case $world in
	fake) env_args="-Fake ready" ;;
	fresh) env_args="-Fake fresh" ;;
	demo) env_args="-Demo" ;;
	esac
	win "mkdir $R\\app" >/dev/null
	say "windows: copying $(du -sh "$dir" | cut -f1) to $WIN_SSH"
	win_cp "$dir"/* "$WIN_SSH:deneb/gui/$stamp/app/"
	win_cp "$here"/win-*.ps1 "$WIN_SSH:deneb/gui/$stamp/"
	trap 'win "powershell -NoProfile -ExecutionPolicy Bypass -File $R\\win-cleanup.ps1 -Run $R" >"$out/windows-cleanup.txt" 2>&1 || true; rm -rf "$tmp"' EXIT
	win_task DenebGUIShot0 win-shot.ps1 "-Run $R\\before" "$R\\before\\shot.done"
	win_task DenebGUILaunch win-launch.ps1 "-Exe $R\\app\\Deneb.exe -Run $R $env_args" "$R\\window.pid"
	pid=$(win "type $R\\window.pid" | tr -dc '0-9')
	sleep 6 # first frames
	win_task DenebGUIShot win-shot.ps1 "-Run $R\\after -WindowPid $pid" "$R\\after\\shot.done"
	for f in screen window taskbar tray tray-hidden; do
		win_cp "$WIN_SSH:deneb/gui/$stamp/after/$f.png" "$out/windows-$f.png" 2>/dev/null || true
	done
	win_cp "$WIN_SSH:deneb/gui/$stamp/before/screen.png" "$out/windows-before.png" 2>/dev/null || true
	{
		echo "app $dir"
		echo "world $world"
		win "type $R\\after\\shot.txt" | tr -d '\r'
		win "type $R\\window.err.log" 2>/dev/null | tr -d '\r' | tail -5 | sed 's/^/stderr: /'
	} >"$out/windows-info.txt" 2>/dev/null || true
	win "powershell -NoProfile -ExecutionPolicy Bypass -File $R\\win-cleanup.ps1 -Run $R" >"$out/windows-cleanup.txt" 2>&1 || true
	trap 'rm -rf "$tmp"' EXIT
	say "windows: done ($(tr -d '\r' <"$out/windows-cleanup.txt" | tail -1))"
}

# ---- the Ubuntu virtual machine --------------------------------------------

# The machine gets new host keys whenever it boots from the snapshot
# (cloud-init), so each run keeps its own known_hosts: it is this Mac's
# own virtual network.
u26_ip=
u26() {
	ssh -q -i "$U26_SECRETS/ssh_ed25519" -o IdentitiesOnly=yes -o ProxyCommand=none -o BatchMode=yes \
		-o UserKnownHostsFile="$tmp/u26_known_hosts" -o StrictHostKeyChecking=accept-new \
		-o ConnectTimeout=5 "$U26_USER@$u26_ip" "$@"
}
u26_cp() {
	scp -q -i "$U26_SECRETS/ssh_ed25519" -o IdentitiesOnly=yes -o ProxyCommand=none -o BatchMode=yes \
		-o UserKnownHostsFile="$tmp/u26_known_hosts" -o StrictHostKeyChecking=accept-new "$@"
}
# u26_sudo <command>: as root; sudo reads the password from stdin, so it is
# never on a command line.
u26_sudo() { u26 "sudo -S -p '' sh -c '$1'" <"$U26_SECRETS/password"; }
# u26_gui <command>: in the user's desktop session (systemd --user).
u26_gui() { u26 "XDG_RUNTIME_DIR=/run/user/\$(id -u) DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/\$(id -u)/bus $1"; }
u26_shot() { "$VMRUN" -T fusion -gu "$U26_USER" -gp "$(cat "$U26_SECRETS/password")" captureScreen "$U26_VMX" "$1"; }
u26_running() { "$VMRUN" -T fusion list | grep -Fqx "$U26_VMX"; }
u26_back() {
	"$VMRUN" -T fusion stop "$U26_VMX" hard 2>/dev/null || true
	"$VMRUN" -T fusion revertToSnapshot "$U26_VMX" "$U26_SNAPSHOT"
}

shoot_ubuntu() {
	local debs=$1 i bin props=
	[ -f "$U26_VMX" ] || die "no virtual machine at $U26_VMX"
	for f in $debs; do [ -f "$f" ] || die "--linux: no $f"; done
	if [ "$keep_vm" = 0 ]; then
		say "ubuntu: back to the snapshot $U26_SNAPSHOT"
		u26_back
	fi
	u26_running || "$VMRUN" -T fusion start "$U26_VMX" nogui
	# VMware Tools tell the address; at most two minutes, then the last one.
	u26_ip=$(perl -e 'alarm shift; exec @ARGV' 120 "$VMRUN" -T fusion getGuestIPAddress "$U26_VMX" -wait 2>/dev/null) ||
		u26_ip=$(cat "$U26_SECRETS/ip" 2>/dev/null) || die "no address for the virtual machine"
	for i in $(seq 1 60); do u26 true 2>/dev/null && break; sleep 2; done
	u26 true || die "no ssh to $U26_USER@$u26_ip"
	for i in $(seq 1 60); do
		u26_gui 'systemctl --user show-environment' 2>/dev/null | grep -q '^WAYLAND_DISPLAY=' && break
		sleep 2
	done
	u26_gui 'systemctl --user show-environment' | grep -q '^WAYLAND_DISPLAY=' || die "no desktop session for $U26_USER"
	# For this run only (the snapshot comes back after it): a larger screen,
	# no notification banners over the window ("96 updates"), and no
	# first-login welcome window behind it.
	u26_gui "gdctl set --logical-monitor --primary --monitor Virtual-1 --mode $U26_MODE --scale 1 && gsettings set org.gnome.desktop.notifications show-banners false" ||
		say "ubuntu: could not set the screen to $U26_MODE"
	u26 'pkill -u "$(id -u)" -f gnome-initial-setup' || true
	sleep 8 # the desktop settles: the dock, the first notifications
	u26_shot "$out/ubuntu-before.png"
	say "ubuntu: installing the packages"
	u26 'rm -rf /tmp/deneb-debs && mkdir /tmp/deneb-debs'
	# shellcheck disable=SC2086
	u26_cp $debs "$U26_USER@$u26_ip:/tmp/deneb-debs/"
	u26_sudo 'DEBIAN_FRONTEND=noninteractive apt-get install -y /tmp/deneb-debs/*.deb' >"$out/ubuntu-apt.log" 2>&1 ||
		die "apt-get install failed: see $out/ubuntu-apt.log"
	bin=$(u26 "dpkg -L deneb | grep -m1 -E '^/opt/deneb/deneb\$|/bin/deneb\$'")
	for e in $(world_env); do props="$props -E $e"; done
	u26_gui "systemd-run --user --unit=deneb-gui --collect$props $bin" >/dev/null
	sleep 8 # the window opens, first frames
	u26_shot "$out/ubuntu-screen.png"
	{
		echo "packages $debs"
		echo "world $world"
		echo "app $bin"
		u26 'gnome-shell --version; uname -m' 2>/dev/null
	} >"$out/ubuntu-info.txt"
	u26_gui 'systemctl --user stop deneb-gui' 2>/dev/null || true
	if [ "$keep_vm" = 0 ]; then
		u26_back
		say "ubuntu: done, back to the snapshot"
	else
		say "ubuntu: done, left running"
	fi
}

[ -n "$mac_app$win_dir$linux_debs" ] || die "nothing to shoot: give --mac, --win or --linux"
[ -z "$mac_app" ] || shoot_mac "$mac_app"
[ -z "$win_dir" ] || shoot_windows "$win_dir"
[ -z "$linux_debs" ] || shoot_ubuntu "$linux_debs"
python3 "$here/compose.py" "$out"
say "screenshots in $out"
