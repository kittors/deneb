# 三个平台的界面截图（M5 u1、u2，M6 a1）

在这台 Mac、Windows 测试机和 Ubuntu 26.04 虚拟机上，把真的 Deneb 窗口和托盘图标截下来，按同一个名字规则放进一个目录，再拼出对照图。

```
test/topo/gui/shoot.sh --mac <Deneb.app> --win <放 Deneb.exe 的目录> \
    --linux "<deneb_*.deb> <denebd_*.deb>" [--world demo|fake|fresh|real] [--out <目录>]
```

三个平台可以只给一个或两个。不给 `--out` 时，结果放在 `test/topo/.accept/gui-<时间>/`，这个目录不进仓库，因为截图里有测试机桌面上的私人文件名。

## 截出来的东西

| 文件 | 内容 |
| --- | --- |
| `<平台>-window.png` | 窗口，只要边框以内，不带阴影 |
| `<平台>-tray.png` | 托盘图标所在的那段系统栏，见下面「托盘」 |
| `windows-tray-hidden.png` | Windows 11 打开后的"隐藏的图标" |
| `<平台>-screen.png` | 整个屏幕；这台 Mac 默认不截，要截加 `--mac-screen` |
| `<平台>-before.png` | 窗口打开前的屏幕（Mac 是菜单栏），找窗口位置用 |
| `<平台>-info.txt` | 程序、数据、尺寸、缩放 |
| `compare-window.png` | 窗口并排，都换算到 1 倍（点、DIP） |
| `compare-tray.png` | 托盘那段并排，放大到 2 倍 |
| `compare-screen.png` | 整屏并排，同一个高度 |

`<平台>` 是 `mac`、`windows`、`ubuntu`。对照图里的顺序是 macOS、Windows、Ubuntu。

## 窗口里显示什么（`--world`）

- `demo`（默认）：设计稿的样例数据（`DENEB_DEMO=1`），在窗口自己的进程里。三个平台一样，截图才比得了；也不碰任何引擎。
- `fake`：`client/fakeworld` 的 M1 拓扑（`DENEB_FAKE=ready`）。Windows 上现在用不了：它的本机接口用的是套接字路径，Windows 要管道名，窗口一起来就退了。
- `fresh`：同一个假世界，还没登录。
- `real`：窗口连它找到的引擎。这台 Mac 上不允许，因为这台的引擎是用户自己的。

## 各平台怎么做

**这台 Mac**：

- 直接运行给的 .app 里的程序，是一个单独的实例。开发窗口是 `mygo dev` 起的 "Deneb Dev"，数据目录不一样，碰不到它。
- 窗口用 `screencapture -l` 按窗口号截，窗口号由 `mac-windows.swift` 查（用 `swiftc` 现编）。
- 截完就结束这个实例。

**Windows 测试机**：

- 文件经 scp 复制到 `%USERPROFILE%\deneb\gui\<时间>\`。
- 窗口和截图都靠计划任务跑在控制台登录用户的桌面会话里（`win-task.ps1`）：只在用户登录时运行、令牌不提权，和人双击打开一样。截图按物理像素截（`win-shot.ps1` 按每个显示器感知 DPI）。
- 窗口的位置取 DWM 给的边框（不含阴影），托盘取任务栏里的通知区。
- 收尾（`win-cleanup.ps1`）：
  - 关掉窗口，删掉计划任务和这次的目录；
  - 用户态引擎没在跑就照 `start.ps1` 起回来。
- 不改网络、防火墙、Tailscale、UAC。
- 不传引擎（`denebd.exe`、`wintun.dll`）和安装包，除非 `--world real`。在一个新目录里运行、要监听端口的引擎，第一次运行时 Windows 防火墙会自动给那个路径建入站阻止规则；这个工具每次换目录，就会每次多两条（11-11 M5 托盘会话在 `deneb\m5ui` 上遇到过）。
- 别的线同时在测试机上开着 Deneb 窗口时，它们的窗口和任务栏按钮也会出现在整屏截图里。窗口截图只截这次起的那个进程的窗口，收尾也只关这次起的进程。

**Ubuntu 虚拟机**（VMware Fusion）：

- 先回到快照 `clean-os-no-deneb`，不开 Fusion 窗口地启动，等桌面会话起来。
- 截一张窗口打开前的屏幕，经 ssh 装两个 deb，在用户的桌面会话里用 `systemd-run --user` 起窗口，用 `vmrun captureScreen` 截屏。
- 最后关机，回到快照；加 `--keep-vm` 就留着它跑。
- 窗口的位置：GNOME 在 Wayland 下不告诉别人窗口在哪，`compose.py` 从打开前后两张屏幕的差别里，找变化最大的那一块。
- 虚拟机每次从快照启动都换一次主机密钥（cloud-init），所以每次运行用自己的 known_hosts。

## 托盘

三个平台都只截系统栏里放托盘图标的那一段，图标在不在靠人眼看。那里的时钟、网速一类会自己变，比较前后差别说明不了问题。

- macOS：菜单栏刘海右边那段。菜单栏太挤，或者装了收起图标的菜单栏管理工具时，新图标可能看不见。
- Windows：任务栏的通知区。Windows 11 默认把新程序的图标放在"显示隐藏的图标"里，所以还会把它打开再截一张（`windows-tray-hidden.png`）：
  - 按 Win+B（焦点到通知区）、回车打开，截完按 Esc 关上；
  - 这几下按键发到测试机当前的桌面上，所以别人正在用测试机的时候别跑这个工具。
- Ubuntu：顶栏时钟右边那段，AppIndicator 的图标在那里。

## 配置

机器的地址和凭据从环境变量或 `test/topo/.accept/gui.env` 读，这个文件不进仓库。可设的项见 `gui.env.example`，默认值在 `shoot.sh` 开头。

- Windows：用 `WIN_SSH` 指定 ssh 目标，`WIN_ALIAS` 是它在 known_hosts 里的名字。和这台 Mac 在同一个局域网时，填局域网地址传得更快。
- Ubuntu：虚拟机用户的密码、ssh 密钥放在 `U26_SECRETS` 目录，默认是 `~/VMs/deneb-u26`，在仓库外。sudo 的密码从标准输入读，不出现在命令行上。

## 要的工具

- 这台 Mac：Xcode 命令行工具（`swiftc`、`screencapture`），Python 3 加 Pillow，VMware Fusion。
- Windows 测试机：OpenSSH 服务端、Windows PowerShell 5.1。
