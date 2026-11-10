#!/bin/bash
# askpass-wake.sh: the password prompt for `sudo -A pmset relative wake`
# in m2-realworld.sh. macOS needs root to schedule a wake; this shows the
# system's own dialog with the reason and hands what the user typed to sudo
# on stdout, nowhere else. Cancel, or no answer within 5 minutes, makes the
# sleep test stop before the Mac sleeps.
exec /usr/bin/osascript \
	-e 'activate' \
	-e 'set r to display dialog "Deneb 的睡眠测试要让这台 Mac 真的睡 10 分钟，再自动唤醒，看所有连接 30 秒内能不能恢复。预约自动唤醒要管理员权限。" & return & return & "请输入这台 Mac 的登录密码。点取消就不睡。" default answer "" with hidden answer with title "Deneb 测试" buttons {"取消", "好"} default button "好" cancel button "取消" with icon caution giving up after 300' \
	-e 'if gave up of r then error number -128' \
	-e 'text returned of r'
