# 安装

在每台要连接的电脑上安装 Deneb，用同一个邮箱登录。

1. 在这台电脑上打开 deneb.app，点「下载」。网页会认出系统。
2. 安装：
   - Mac：打开下载的 Deneb.dmg，把 Deneb 拖进「应用程序」。
   - Windows：运行 Deneb-Setup.exe，按提示完成。
   - Linux：窗口和后台服务两个包一起装，在下载目录中运行 `sudo apt install ./deneb.deb ./denebd.deb`（ARM64 换成 deneb-arm64.deb、denebd-arm64.deb）。
3. 首次打开时被系统阻止：
   - Mac：打开「系统设置」>「隐私与安全」，在「安全性」中点按「仍要打开」，再输入登录密码。
   - Windows：出现「Windows 已保护你的电脑」时，点「更多信息」，再点「仍要运行」。
4. 登录：输入邮箱，再输入邮件中的 6 位验证码。
   ![登录 Deneb：输入邮件中的 6 位验证码](step-code)
5. 选「新建局域网」，或选「加入朋友的」，打开朋友发来的邀请链接。
   ![登录后的第一页：新建局域网，或加入朋友的](install-first)

> 同一个邮箱登录的电脑，都显示在「我的设备」里。
>
> 没有屏幕的 Linux 服务器装 deneb-server.deb。在命令行中登录的方法稍后补充。
