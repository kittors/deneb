#!/bin/bash
# askpass.sh: the password prompt for `sudo -A` when a round is started
# without a terminal (by the main session, so the user does not have to run
# it by hand). It shows macOS's own dialog with the reason and hands what
# the user typed to sudo on stdout; it writes nothing anywhere else. Cancel,
# or no answer within 5 minutes, makes sudo fail, and the round stops before
# changing anything.
#
#   SUDO_ASKPASS=$PWD/test/topo/askpass.sh test/topo/kernel-round.sh
exec /usr/bin/osascript \
	-e 'activate' \
	-e 'set r to display dialog "Deneb 的内核网卡测试要在这台 Mac 上建一张虚拟网卡、加一条 10.48.0.0/16 的路由、写 /etc/resolver/deneb.internal，测完全部撤掉。" & return & return & "请输入这台 Mac 的登录密码。" default answer "" with hidden answer with title "Deneb 测试" buttons {"取消", "好"} default button "好" cancel button "取消" with icon caution giving up after 300' \
	-e 'if gave up of r then error number -128' \
	-e 'text returned of r'
