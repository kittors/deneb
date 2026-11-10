# M5 · 组网引擎线状态

> 负责目录：`engine/`、`cmd/denebd/`、`test/topo/`、`deploy/linux/`。依据：实现计划 §3 M5 的 Windows、Linux 引擎部分，验收表 [m5-acceptance.md](../m5-acceptance.md) 的 w1–w3、l1–l2。
> 最后更新：2026-11-09 23:50。

## 进度

| 项 | 状态 |
| --- | --- |
| l1 Linux 内核模式：tun 网卡、按对端 `/32` 的路由、过滤；开着 IP 转发时拦住借道的包 | ✅ colima 的 systemd 容器里，见「测试」 |
| l2 systemd-resolved 的 `~deneb.internal`，没有就退回用户态；systemd 服务；服务器包 | ✅ 同上 |
| w1 Windows 空跑检查 | ✅ 测试机上只读跑过，见「做法：Windows」 |
| w2 Windows 内核模式 | 主会话装过两轮。MTU 第二轮验过了（NlMtu 1280）。拆成几块的大包 ping 收不到回应，是过滤器的问题，每个平台都有，23:2x 改好，等真机复测 |
| w3 Windows 撤销和服务 | ✅ 撤销通过；服务 23:1x 照「过程记录」的测法全过（主会话，`test/topo/.accept/m5-11092313-winsvc/`） |

## 做法：Linux

### 网卡、地址、路由（`engine/tun/tun_linux.go`）

- 网卡 `deneb0`（被别的引擎占了就用 `deneb1` 到 `deneb9`）：wireguard-go 的 tun，地址是本机的 `/32`，MTU 1280。
- 每个对端一条 `/32` 路由，DNS 地址一条，标记 `proto 68`；网络图一变就同步（`Kernel.SetPeers`）。
  - 对端指 WireGuard 认识的全部对端，暂停了的局域网里的也算：发给它们的包进网卡、在过滤里停下，不从默认路由出去。
  - macOS 照旧是一条 `/16`，`SetPeers` 在那里什么也不做。
- 只用 netlink（`jsimonetti/rtnetlink`，本来就是间接依赖），不要求系统里有 `ip` 命令。
- 引擎退出时，网卡连同它的路由、resolved 的设置一起消失：tun 在最后一个文件关闭时被内核删掉。崩溃后也一样，不用善后。
- 开之前的检查（`Preflight`）：地址段里不能已有网卡地址，也不能已有落在地址段里的路由（Docker 的网段、别的 VPN 的）。比地址段大的路由（默认路由、`10.0.0.0/8`）不碍事，`/32` 更具体。

### DNS

- 系统查名字经过 systemd-resolved 时（`/etc/resolv.conf` 指向 127.0.0.53，或者 `nsswitch.conf` 的 hosts 一行有 resolve），用 `resolvectl` 设三样：
  - `dns deneb0 <DNS 地址>`；
  - `domain deneb0 ~deneb.internal`；
  - `default-route deneb0 false`。

  只有这个域名的查询交给引擎，别的名字照旧。
- 不经过时（Debian 默认就没装 resolved），退回用户态，见下一节。

### 内核模式起不来时：退回用户态（三个平台一样）

- 三种情况退回用户态（SOCKS5 `127.0.0.1:41080`），日志写一行原因，状态里的模式写 userspace：
  - 地址段已被占用（`tun.ErrConflict`）：地址段里已有别的网卡地址或路由（Docker 的网段、别的 VPN 的），或者和 Tailscale、Clash 用的网段重叠；
  - 没有 root 或管理员权限（`tun.ErrNoPermission`）；
  - Linux 上系统查名字不经过 systemd-resolved（`tun.ErrNoResolver`）。
- 依据是实现计划 §2.3："没有管理员权限、检测到地址冲突时改用用户态"。别的错误照旧报出来，不退。

### 借道（multi-lan-tech §7 第 3 条）

- 机器开着 IP 转发（装了 Docker 就开着）时，别的机器可以把包经这台转进 `deneb0`。引擎读出来，过滤在出口按"源地址不是本机"拦掉（`transit-out`）。不加防火墙规则。

### systemd 服务和包（`deploy/linux/`）

- `denebd.service`：
  - 读 `/etc/default/denebd`（`DENEB_MODE`、`DENEB_CONTROL`、`DENEB_CONTROL_PIN`、`DENEB_FLAGS`）；
  - 状态在 `/var/lib/deneb`，本机接口在 `/var/run/deneb/denebd.sock`；
  - 失败 2 秒后重启。
- `build-deb.sh` 出两个包，amd64、arm64 各一份：
  - `deneb`：桌面用，默认内核模式，推荐装 systemd-resolved、libnss-resolve。窗口等客户端线出 Linux 版再放进来。
  - `deneb-server`：只有引擎和命令行，默认用户态、`-kind server`。
  - 两个包互斥。装上就启用、启动服务；卸载时停服务；purge 时删 `/var/lib/deneb`。
- `denebd preflight`（三个平台都有）：只读，按网络图说清楚内核模式会改什么、哪一样会拦住它。`denebd kernel-undo` 在 Linux 上也能用，只是没什么可撤。

## 做法：Windows（`engine/tun/tun_windows.go`）

- 网卡：Wintun（`wintun.dll` 放在 `denebd.exe` 旁边，wireguard-go 从那里加载），名字和描述都叫 Deneb，地址是本机的 `/32`，网卡上不设 DNS 服务器。
- MTU：开网卡时把 IPv4、IPv6 的 IP 层 MTU（NlMtu）都设成 1280；机器上关了 IPv6 就只设 IPv4。wireguard-go 建 Wintun 网卡时只记下 MTU，不设到网卡上，网卡默认是 65535。那样 Windows 不分片，大包整个交给引擎，隧道带不过去就丢了。
- 路由：每个对端一条 `/32`，DNS 地址一条，同步方式和 Linux 一样（`SetPeers`）。
- DNS：一条 NRPT 规则，注册表 `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig\{8F3B6C2E-1D4A-4E7B-9A5C-DE0EB2026001}`，只管 `.deneb.internal`，指向 DNS 地址；写完清一次 DNS 缓存。别的规则不动。组策略下发了 NRPT 时本机规则不生效，空跑会提醒。
- 绑物理网卡：UDP 用 `IP_UNICAST_IF` 绑在默认路由的网卡上（M1 起就有，`engine/bind`）；Tailscale、Wintun、Deneb 的网卡不当物理网卡。
- 停下时删 NRPT 规则、关网卡（路由跟着网卡走）。崩溃时网卡随进程消失（Wintun 0.14 把网卡和创建它的句柄绑在一起），下次启动按记录删 NRPT 规则。
- Windows 服务（`denebd service install [run 的参数] | uninstall`，要管理员）：
  - 服务名 Deneb，以 LocalSystem 跑内核模式，开机自动启动；
  - 状态默认在 `%ProgramData%\Deneb`，`-state` 可以指到别处，比如原来用户态引擎的状态目录，就还是同一台设备；
  - run 的参数原样带进服务；装的时候先按 run 的规则检查一遍，写错了当场报，不等服务起来才失败；
  - 状态目录是新建的，就只给 SYSTEM 和管理员。ProgramData 下面默认所有用户可读，而目录里有设备密钥。已有的目录不动；
  - 服务没有控制台：日志写到状态目录的 `denebd.log`，崩溃信息也写在那里；启动时日志超过 10 MB，就挪成 `denebd.log.1`；
  - 崩溃后 2 秒重启；
  - 以 SYSTEM 运行时，本机接口的命名管道也放行管理员组，管理员的 `deneb` 命令能用；
  - 还放行桌面的那个用户，普通权限打开的窗口就能连上服务（`-allow-user`，SID 或 `电脑名\用户名`，和 macOS、Linux 的 `-allow-uid` 对应）。管理员账号的窗口没提权时，令牌里的管理员组只用于拒绝，所以要单独放行这个用户。`service install` 没给 `-allow-user` 时自己找：先找装服务的程序所在会话里登录的用户（用管理员密码提权装的，也找得到原来那个人），找不到再找控制台前的用户。经 ssh 装时，程序所在的会话一般没有登录桌面的用户，就落到控制台前的用户；两处都找不到，就只放行管理员，并且说一声。
- 撤销脚本 `deploy/windows/undo.ps1`：删服务、删 Deneb 的 NRPT 规则、删名叫 Deneb 的网卡；`-Purge` 连状态一起删。Tailscale、别的 NRPT 规则、别的网卡、防火墙都不碰。
- 空跑 `denebd preflight -state <状态目录>`：查管理员、`wintun.dll`、有没有同名网卡、地址段里有没有别人的路由和地址、Tailscale 的路由、已有的 NRPT 规则和组策略，再列出会改的三样。

**w1（11-09 21:0x，测试机 DESKTOP-LF6684K，只读）**：新版 `m5-11092106-win` 放在测试机的 `%USERPROFILE%\deneb\m5-preflight\`（和正在跑的引擎分开，没碰它），对着正在跑的引擎的状态目录空跑：

- 本机 `10.48.1.5`，地址段 `10.48.0.0/16`，4 个对端；
- Tailscale 在：它网卡上的 10 条 IPv4 路由都不在 `10.48.0.0/16` 里；
- 本机没有 NRPT 规则，也没有组策略的；
- 已是管理员，`wintun.dll` 在，没有同名网卡；
- 会改的三样：一张 Wintun 网卡、5 条 `/32` 路由、一条只管 `.deneb.internal` 的 NRPT 规则。

装之前的网卡、NRPT、路由、服务快照在仓库 `test/topo/.accept/m5-11092106-win/windows-before.txt`，给 w2、w3 撤销后比对。

## 测试

假拓扑加了 `-m5 debian,debian12,ubuntu,ubuntu26`。每个发行版起一个 systemd 容器（`test/topo/m5/Dockerfile`），装 `deneb` 包，接进拓扑的控制服务器和中转，登录成 kittors 的又一台设备、加进工作室，然后逐项查：

1. `deneb0` 带本机 `/32`，模式是 kernel；每个对端一条 proto 68 的 `/32` 路由。
2. 按名字 ping build（走内核的 ICMP），也发 1400、3000 字节的，那要拆成几块发；按名字连 build 的计时流（bash 的 `/dev/tcp`，不经代理）。
3. resolved：`deneb0` 上只有 `~deneb.internal`，`Default Route: no`；`getent hosts` 解析得到。
4. 打开 IP 转发，另一个容器把地址段路由到这台，ping build：不通，日志里有 transit-out；这台自己照样通。
5. 服务：
   - 启用、在跑；
   - 停下：网卡、路由、resolved 设置全没了；
   - 再起：路由回来、ping 通；
   - `systemctl kill -s KILL`：systemd 拉起来，没有多出 `deneb1`。
6. 去掉 `nsswitch.conf` 里的 resolve：退回用户态，网卡没了，`deneb ping build` 照通；改回去又是内核模式。
7. 在地址段里加一条别人的路由（`<段>.250.0/24 dev eth0`）：退回用户态，日志里有 "the address block is in use here"；删掉路由又是内核模式。
8. 卸掉桌面包、装服务器包：用户态，还是同一台设备（密钥留在 `/var/lib/deneb`），`deneb ping build` 通；purge 以后文件、服务、状态都没了。

结果（11-09 21:2x，和原有 30 项一起完整跑过一遍）：

| 发行版 | 镜像 | `/32` 路由 | 按名字 ping | 停了再起 | 被杀后 systemd 拉起 |
| --- | --- | --- | --- | --- | --- |
| Debian 13 | `debian:stable-slim` | 3 | 0.47ms | 0.1 秒 | 2.4 秒 |
| Debian 12 | `debian:12-slim` | 4 | 0.80ms | 0.1 秒 | 2.5 秒 |
| Ubuntu 24.04 | `ubuntu:24.04` | 5 | 1.0ms | 0.1 秒 | 2.5 秒 |
| Ubuntu 26.04 | `ubuntu:26.04` | 6 | 1.5ms | 0.1 秒 | 2.2 秒 |

- 路由条数不同，是因为四台依次加进同一个工作室，后加的那台对端多。
- "被杀后"约 2 秒是服务文件里的 `RestartSec=2s`。
- 第 7 项（地址段冲突）是后加的，Debian 13、Ubuntu 24.04 单独跑过，见「过程记录」。

用法：

```
go run ./test/topo -m5 debian,debian12,ubuntu,ubuntu26          # 原有各项再加四个发行版
go run ./test/topo -only linux-debian -m5 debian                # 只跑一个
```

## 过程记录

### 11-09 晚上：Linux

- 头几次失败都在测试本身，引擎和包没改：
  - dash 没有 `/dev/tcp`，改用 bash；
  - 网卡没了以后，`resolvectl status deneb0` 不报错、什么也不打印，改看 `resolvectl domain` 和 `resolvectl dns` 的全量输出；
  - 一分钟里重启了五六次，撞上 systemd 默认的启动频率限制，测试里重启前先 `reset-failed`。平时不会这么频繁地重启，服务文件不改这个限制。
  - systemd 255 及以前（Debian 12、Ubuntu 24.04）不打印 `Default Route: no`，而是在 Protocols 一行写 `-DefaultRoute`，两种都认；Ubuntu 24.04 的 `systemctl kill` 会顺带去杀"辅助进程"而报错，改成按主进程号 `kill -9`。
- 改了引擎的两处：
  - resolved 默认在 `deneb0` 上开着 LLMNR，开网卡时连同 mDNS 一起关掉（没人在那里回多播）；
  - 测试用的假网卡（`tun.Fake`）在 Linux、Windows 上不再去同步路由。
- 21:3x：按实现计划 §2.3 补上"地址冲突、没有管理员权限也退回用户态"。
  - 引擎测试 `TestKernelModeFallsBackToUserSpace` 覆盖三种情况。
  - 假拓扑第 7 项，Debian 13、Ubuntu 24.04 都过。
  - 同一轮 engine 包里 `TestLossyDirectGivesWayToRelay`、`TestSmallPacketsTwiceOnLossyLine` 各失败一次，就是 m2/engine.md「已知的限制」里那两个偶尔失败的；单独重跑各 3 遍全过，和这次的改动无关。

### 11-09 夜里：w2、w3 交回来的三件事

主会话 22:33–22:38 在测试机上装了内核模式（记录在 `test/topo/.accept/m5-11092233-win/`），交回三件事。

**1. MTU（w2 没过的那项）**：Deneb 网卡的 NlMtu 是 65535，载荷 1400、3000 字节的 ping 全丢。改法见「做法：Windows」的 MTU 一条，编得过，没在真机上验。重装时这样验：

- `Get-NetIPInterface -InterfaceAlias Deneb` 里 IPv4、IPv6 两行的 NlMtu 都是 1280；
- `ping -f -l 1252` 通（加上头正好 1280），`ping -f -l 1253` 报要分片；
- 不加 `-f` 的 `-l 1400`、`-l 3000` 通；
- 普通程序下一个大文件不卡住。

**2. 第 7 条：NAS 加回后台式机 5–6 s 才恢复**。不是台式机的路由、DNS 缓存或握手，是发命令那头慢。时间线：

| 时刻 | 事 | 出处 |
| --- | --- | --- |
| 22:37:39.02 | 主会话的 ssh 登上 NAS 所在的服务器 | 那台的 sshd 日志 |
| 22:37:41 | 控制服务器记下 `lan.addDevice`（只记到秒） | 控制服务器日志 |
| 22:37:41.902 | 台式机收到新的网络图，加上 NAS 的 `/32` 路由 | 台式机引擎日志 |
| 22:37:42.055 | 清了系统的 DNS 缓存（+0.15 s） | 同上 |
| 22:37:42.129 | 找到直连，rtt 101 ms | 同上 |
| 22:37:42.249 | WireGuard 握手回来（+0.35 s） | 同上 |
| 22:37:42.314 | 名字查得到 | `item7.log` |
| 22:37:42.768 | ping 通 | 同上；上一轮的 ping 在握手回来前发出，没通 |

- 台式机这边从收到变更到 ping 通 0.87 s，其中一部分是脚本每 0.45 s 才测一轮。
- 前面那几秒花在命令上：
  - 从这台 Mac ssh 到那台服务器，本身就要 3.3–3.7 s（23:0x 量了三次）；
  - NAS 上 `deneb lan` 的命令行要 0.5–2 s，因为 NAS 用 `-userview=false` 跑，命令行得自己去控制服务器取视图，再发操作。
- 撤出那次也一样：ssh 22:37:17.35 登上，路由 18.42 撤掉，ping 18.81 不通，对得上"3.5 s"。
- Mac 上不到 0.3 s，多半是从别的起点量的。要比，就两边都从引擎日志里 "routes through Deneb: 1 added" 那一行量到第一次 ping 通，或者两边都在本机发命令。

同一份 `item7.log` 里还有一件没弄清的事：NAS 连着的时候，约 50 次 ping 里有 5 次没通（22:37:11.3、14.8、46.3、46.8、50.3）。

- 这几次都比通的快：没通的那几轮 0.34–0.40 s，通的一轮约 0.45 s，超时的一轮至少 0.7 s（`-w 400` 加上 0.3 s 的间隔）。所以不是回包丢了，是有什么当场回了错误或拒发。
- `checks.ps1` 用 `-w 1500` 时 3/3 都通。
- 要看 ping.exe 那一行原文才知道是什么。重装那轮请把没有 `TTL=` 的输出行原样记下来。

**3. 服务的测法**。原来的 `service install` 有三个问题，23:0x 先改了（见「做法：Windows」的服务一条）：

- 除了 `-state`，带别的 run 参数（`-control` 等）就报 flag provided but not defined，装不上；
- 服务没有控制台，日志全丢；
- 默认状态目录在 ProgramData 下，谁都能读，里面有设备密钥。

建议这样测，要用户点头：

1. 先在测试机上建一个 15 分钟后运行的计划任务，以 SYSTEM 跑 `undo.ps1`，防止服务把 Tailscale 弄断后连不回去。测完删掉它。
2. 照 `kup.ps1` 停掉用户态引擎。
3. `denebd.exe service install -state C:\Users\Administrator\AppData\Local\Deneb` 加上 `kup.ps1` 里那串参数（去掉 `-mode kernel`）。
   - 状态目录用原来那个，还是同一台设备 d_mhtavmihjj，用户的局域网里不会多出一台；
   - 默认的 ProgramData 目录会是一台新设备。
4. 全程开着 `mon.ps1` 盯百度和 ssh。依次查：
   - `sc.exe qc Deneb`：自动启动、LocalSystem、参数对；
   - `sc.exe qfailure Deneb`：崩溃后重启；
   - `denebd status --json`：mode=kernel，已登录；
   - 状态目录的 `denebd.log` 有启动那一行；
   - 跑一遍 `checks.ps1` 和上面的 MTU 几项。
5. `taskkill /F` 杀掉服务进程：
   - 2 s 后服务管理器拉起来；
   - 网卡、NRPT 规则都只有一份，路由回来，ping 通；
   - 记下恢复用了多久。
6. `sc.exe stop Deneb`：网卡、路由、NRPT 规则都没了，和 `snap.ps1` 的快照一致。`sc.exe start Deneb`：都回来。
7. `denebd.exe service uninstall`：服务没了，网卡、路由、NRPT 规则也没了，再和快照比一次。然后删掉第 1 步的计划任务，照 `start.ps1` 起回用户态引擎。

不测重启电脑。开机自启动只看 `sc qc` 里的启动类型。

还有一件留给 M6：服务的命名管道只放行 SYSTEM 和管理员组。普通权限跑的桌面客户端连不上，管理员账号没提权时也一样，因为令牌里的管理员组只用于拒绝。客户端接服务的时候，要像 macOS、Linux 的 `-allow-uid` 那样，放行登录的那个用户。

**23:5x 补上**：管道放行桌面的用户，见「做法：Windows」的服务一条。编得过，单测过了（`hasFlag`），没在真机上验。下次换引擎时验：
- `service install` 打印出放行了谁；
- 用普通权限（不提权）跑 `denebd status`，连得上；
- 换另一个普通用户，连不上。

### 11-09 23:2x：拆成几块的大包收不到回应（w2 第二轮交回）

现象：Windows 内核模式下，不带 `-f` 的 `ping -l 1400`、`-l 3000` 到构建服务器都超时；`-f -l 1252` 通。

原因在过滤器（`engine/filter`），每个平台都有，用户态也一样：

- 过滤器靠记住放行过的第一块，放行同一个包后面的几块，因为后面的几块不带端口。
- 新建一条流，或者 UDP、TCP 的回包，放行时都会记住第一块。ICMP 回包那条路放行了第一块却没记。
- 所以大 ping 的请求能到，回包后面的几块却被丢了两次：被 ping 的那台发出时丢一次，发 ping 的那台收进来时又丢一次。
- 大块 UDP 一直是通的。TCP 自己按 MTU 切小，不会被拆。

真机上对得上。构建服务器（m2-11092136）的计数：

- `out:fragment-without-head` 9 次，日志 14:36:34（第一轮）、15:14:13、15:14:19（第二轮）都有，就是它发出的回包后面的几块；
- 另有 `in:fragment-without-head` 2 次：台式机发来的请求，第一块没到、后面的到了。这是线路丢包。那时它对构建服务器开着"小包发两份"，后面的小块发了两份，1280 字节的第一块只发一份。不是过滤器的问题。

改法：两个方向放行 ICMP 回包时，都记住第一块（两行）。

测试：

- 过滤器单测 `TestFragmentedPing`：两个方向（本机 ping 对端、对端 ping 本机），请求和回包各三块，都放行；没问过的回包，哪一块都不放。
- 引擎测试 `TestKernelModeLargePackets`：内核模式的假网卡对用户态的构建服务器，1400、3000 字节的 ping 和 UDP，按 MTU 1280 切块发出，把回来的几块拼起来逐字节对。
- 假拓扑 Linux 检查加了 `ping -M dont -s 1400`、`-s 3000`，Debian 13 上过了。
- 去掉修复做对照：上面三处都失败。引擎测试里是"in:fragment-without-head"；Debian 13 上 1400 字节的 ping 不通。

要在真机上验，发 ping 和被 ping 的两台都得是新版：旧版的被 ping 那台，在发出回包时就把后面的几块丢了。Windows 新版是 `test/topo/.accept/m5-11092326-win/`。

还留着的限制：同一个包后面的某块要是比第一块先到，还是会被丢，整个包也就丢了。比如中途从中转换到直连，后面的那块走了更快的路。这种情况很少见，没改。（11-11 凌晨补上了，见下面那一节：刚醒时比想的常见。）

### 11-11 凌晨：大包偶尔丢一个、out:malformed（m7-11102317-win 那一轮）

现象：台式机装成服务（内核模式）以后，`ping -l 1400`、`-l 3000` 到构建服务器和 NAS，4 组里 3 组各丢 1 个，以前是全丢。

- 台式机的计数：`in:fragment-without-head` 2，`out:malformed` 13；
- 构建服务器：`in:fragment-without-head` 1。

**丢的是每组的第一个，原因是同一个包的几块走了不同的路：**

- 每组开始前线路已经闲下来，第一个 ping 正好赶上刚醒的那几秒。这时 magicsock 把小包也经中转发一份（`wakeRelay`，`sendWG`），大包只走直连。
- 1400 字节的 ping 拆成两块：第一块 1280 字节，算大包，只走直连；后一块一百多字节，算小包，直连、中转各一份。
- 中转站和构建服务器在同一台机器上，中转那条路常常更快，后一块就比第一块先到了。
  - 过滤器没见过第一块，按 fragment-without-head 丢掉它；
  - 等第一块到了，后一块直连那份再到，对方的 WireGuard 把它当重放丢了，因为同一个序号已经收过；
  - 这个包就拼不起来了。
- 每组只丢第一个，对得上。上一轮（11-09 23:2x）记下的"还留着的限制"，说的就是这种情况。

**改法**：收进来的方向，某块比第一块先到，就先留着。

- 最多等 2 s（`fragHold`）。第一块一放行，就把它接在第一块后面一起交给系统（`Filter.TakeEarly`、`TUN.Write`）；
- 等不到就丢，照旧记作 fragment-without-head；
- 上限：同时留 256 块，一个对端最多 64 块，每块不超过 1500 字节，免得被人拿来占内存；
- 第一块没被记下（分片表里这个对端的额度满了）时，留着的也一起丢。

两种模式都管用：内核模式和用户态共用这个过滤器。

**out:malformed 13**：不是坏包，是 Windows 在 Deneb 网卡上自己发的。

- IPv6 的路由器请求、组播监听之类，因为网卡开了 IPv6，我们给它设过 IPv6 的 MTU；
- IGMP 的组播成员报告（发往 224.0.0.22）。
- 隧道里只有 IPv4，这些本来就该丢。现在分开记成 `ipv6` 和 `protocol`（过滤器不认识的 IPv4 协议），真坏的包才记 `malformed`。

**测试**：

- 过滤器：
  - `TestEarlyFragments`：两块先到、第一块后到，两块按原样交回；等不到的 2 s 后记作丢弃；第一块来晚了，什么也不交回；一个对端最多留 64 块，别的对端不受影响；大块不留；
  - `TestTUNHandsBackEarlyPieces`：经 TUN，先写后一块、再写第一块，系统依次收到第一块和后一块；
  - `TestInbound` 多了 IPv6 和 IGMP 两例；
  - 原有的分片测试按新的回答（`fragment-held`）改了。
- 引擎包整包跑过。
- 要在真机上验：发 ping 的和被 ping 的都要换新版，因为留着等的是收的那一头。下次换引擎时验：
  - 闲一会儿再 ping，不丢；
  - `in:fragment-without-head` 不再涨；
  - `out:malformed` 不再出现，换成 `ipv6`、`protocol`。
