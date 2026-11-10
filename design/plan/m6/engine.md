# M6 · 组网引擎线状态

> 负责目录：`engine/`、`cmd/denebd/`、`deploy/linux/`、`deploy/windows/`。依据：实现计划 §3 M6，验收表 [m6-acceptance.md](../m6-acceptance.md) 里 i2、i3、i7 的引擎部分；打包怎么分工和客户端线对过，见 [client.md](client.md)「打包」。
> 最后更新：2026-11-10 00:25。

## 进度

| 项 | 状态 |
| --- | --- |
| i2 Windows：服务装在哪、卸载撤干净、引擎随窗口更新、卸载钩子 | ✅ 主会话 11-10 00:1x 在测试机上照「Windows 真机验证」6 步都过了；后来又改了两处（状态锁、重装时先复制再停），等下次换引擎再验 |
| i3 Linux：Debian、Ubuntu 的包，systemd 服务，装包的桌面用户不用 sudo | 假拓扑的 systemd 容器里过了（见「测试」） |
| i7 诊断包（引擎部分） | 本机接口、命令行做好，测试过；界面归客户端线 |

## 用户定的（11-10 00:0x，主会话转）

- Deneb 是免费的开源项目，发行按最简单的来（原话"我们这个是一个开源项目，千万别想这么多"）。
- 许可证是 PolyForm Noncommercial 1.0.0（仓库根目录的 LICENSE）。打包时带上我们的 LICENSE 和第三方的许可证（THIRD_PARTY_NOTICES）。
- Windows 安装包用 NSIS，不签名。
- Linux 的两个包放在 GitHub Release 一起下载，不建 apt 源。
- macOS 搁置：暂时没有 Apple 开发者账号。

## 和客户端线定下的（11-09 23:4x）

- **Windows**：
  - 安装包用 MyGo 自带的 NSIS，按用户装，不要管理员权限，自动更新照常能用；
  - 系统服务在首次引导"装网卡"那一步由窗口用 runas 装，弹一次 UAC；卸载时由安装包的 customUnInstall 撤掉；
  - `denebd.exe`、`wintun.dll` 和 Deneb.exe 并排放在 `$INSTDIR\` 顶层（客户端线 11-10 00:2x 改的）。MyGo 自动更新时，会把安装目录里每个文件、文件夹先改名挪开再换新的。Windows 允许给正在跑的程序改名，却不允许给装着它的文件夹改名，所以引擎放在 `bin\` 里，更新会整次回滚。
- **引擎随窗口更新**：窗口每次启动比一下包里的引擎版本和正在跑的版本，不一样就让服务从包里的文件更新自己。降级拒绝。
- **Linux**：
  - 两个 deb。引擎的包叫 `denebd`，带 systemd 服务和维护脚本；界面的包叫 `deneb`（MyGo 打的），依赖 `denebd`；
  - MyGo 的 deb 不能带安装脚本，所以改系统的事都在引擎的包里。
- **macOS**：客户端线推荐特权守护进程（root 跑不受本地网络权限限制），等用户定，到时引擎再出 macOS 的 `service install`。

## 做法：Windows（i2，`cmd/denebd/service_windows.go`、`update_windows.go`）

**装**：`denebd.exe service install [run 的参数]`，要管理员权限。

- 先把它所在目录（`$INSTDIR`）里的 `denebd.exe`、`wintun.dll` 复制到 `%ProgramFiles%\Deneb`，服务从那里跑。
  - 那里只有管理员能写。按用户装的 `%LOCALAPPDATA%\Programs\Deneb` 用户自己能写；SYSTEM 服务从那里跑，等于任何以这个用户身份跑的程序都能换掉它、拿到 SYSTEM。
- 服务的其余设置和 M5 一样：LocalSystem、内核模式、开机自启、崩溃后 2 s 拉起。
- 现在引擎出错退出（不是崩溃）也会拉起。以前只设了恢复动作，没开"非崩溃失败也算"，出错退出就不再起来。
- 已经装过就原地更新：先停服务，再换文件、改配置、启动，所以可以重复跑。
- 等服务真正跑起来才算装好，最多等 10 s；起来就停了，会报错并指向状态目录里的日志。
- 窗口用 runas 起它，只拿得到退出码，看不到输出。所以装好、没装好和原因也写进状态目录的 `denebd.log`，一行 `[service] …`，窗口读得到。
- 窗口应该这样调，设备身份不变：
  `service install -state "%LOCALAPPDATA%\Deneb" -wait-lock 30s`
  - 服务里的引擎在状态锁上等着；
  - 窗口随后调 `POST /v1/shutdown`，停掉自己的用户态引擎；
  - 锁一放开，服务里的引擎就接管同一份状态和同一个管道；
  - 不带 `-state` 时，状态放在 `%ProgramData%\Deneb`，那是另一台设备，要重新登录。
- 命名管道放行装服务时登录桌面的用户（`-allow-user`，见 [m5/engine.md](../m5/engine.md)「做法：Windows」）。

**卸**：`denebd.exe service uninstall [-purge]`。

- 停服务：引擎停下时自己撤掉网卡、路由和 NRPT 规则。
- 删服务。
- 按状态目录里的撤销记录，补撤崩溃留下的东西。
- 删 `%ProgramFiles%\Deneb`；正在跑的就是那里的 denebd.exe 时，让系统在下次重启时删。
- 状态（设备身份）默认留着，`-purge` 才删。
- 没装过服务时什么也不做，所以卸载程序不管装没装服务都可以跑它。

**安装包的钩子**：`deploy/windows/installer.nsh`，客户端线在 MyGo 配置里用 `windows.installerInclude` 引进去。

- customInit（覆盖安装开始时）和 customUnInstall 都先跑 `$INSTDIR\denebd.exe shutdown`，停下用户态引擎，等它的进程退出。窗口关了它还在跑，占着 denebd.exe，不停就换不掉、删不掉。这是客户端线 00:2x 提的。系统服务里的引擎会拒绝，它本来就从 Program Files 跑，不占这个文件。
- customUnInstall 接着：注册表里有 Deneb 服务时，用 `ExecShellWait runas` 跑 `$INSTDIR\denebd.exe service uninstall`，弹一次 UAC。
- 用户点了"否"：弹一句话，说服务还在、怎么以管理员身份手动卸掉，卸载接着走。静默卸载时这句话自动跳过。
- 文件是带 BOM 的 UTF-8，NSIS 才认中文。
- 这台 Mac 上没有 makensis，还没编过。

**更新**：`POST /v1/engine/update {"path": "…\\bin\\denebd.exe"}`，只有系统服务里的引擎做。

1. 先把那个文件复制到 `%ProgramFiles%\Deneb\denebd.update.exe`，之后都检查这一份，用户改不了它。后缀留着 `.exe`，因为下面要运行它问版本。
2. 验签名：Windows 验过签名和证书链（WinVerifyTrust，不查吊销，那要联网），而且签名者和正在跑的引擎是同一个。
   - 正式版只认签过名的；
   - 测试版（denebtest 构建，没签名）只认同样没签名的。
3. 让新文件自己报版本（`denebd version`），和正在跑的比：
   - 一样（或者语义化版本相等）回 `same`，更旧回 `older`；
   - 测试版的版本号是名字，比不出新旧，只要不一样就换。
4. 换文件：把正在跑的改名成 `.old`（Windows 允许给运行中的程序改名），新的换上去。旁边的 `wintun.dll` 不一样、而且是 WireGuard 签的，也一起换。
5. 起一个 `denebd service restart`：等 1 s 让回复先发出去，然后停服务、再起，服务就跑在新版上了。下次启动时删掉 `.old`。

回的结果是 `updated`、`same`、`older`、`bad-signature`、`needs-install`、`not-service`、`failed` 之一，`version` 是新文件的版本。

- 用户态引擎回 `not-service`，窗口照旧重启它就行。
- **正式版不签名，所以回 `needs-install`**，什么也不复制。没有签名，引擎分不出新文件是我们的，还是用户机器上的恶意程序换进去的；静默换掉，就等于给它一条拿到 SYSTEM 的路。窗口收到以后，再跑一遍装服务那一句（可以重复跑），弹一次 UAC，由用户同意。所以 Windows 上每次引擎更新都弹一次 UAC。
- 测试版（denebtest）仍然对没签名的文件放行，用来测换文件、重启这一套。
- 将来真有了代码签名证书，正式版就能走静默那条。

命令行是 `deneb update <路径>`。

**停用户态引擎**：`POST /v1/shutdown`，命令行是 `deneb shutdown`。命令行会等到引擎的进程退出才返回（`GET /v1/version` 现在带上了进程号），没有引擎在跑也算成功。

- 只停用户态引擎；
- 系统服务里的引擎会报错，由系统来停（服务管理器、launchd、systemd）。
- 测试版的 `/v1/test/shutdown` 照旧，什么模式都停。

## 做法：Linux（i3，`deploy/linux/`）

M5 已有的不变：

- systemd 服务，开机自启，崩溃后 2 s 拉起；
- systemd-resolved 只管 `~deneb.internal`，没有就退回用户态；
- 卸掉时网卡、路由、resolved 设置都撤掉，purge 连状态一起删；
- 服务器包 `deneb-server`，用户态。

这次加了两件：

- **包名**：桌面的引擎包从 `deneb` 改叫 `denebd`，因为 MyGo 打出的界面包叫 `deneb`（按应用名）。
  - 引擎包里有 `/usr/bin/denebd` 和命令行 `/usr/bin/deneb`。
  - 界面包的 `linux.command` 不能也叫 `deneb`，两个包会抢同一个文件。
- **装包的桌面用户不用 sudo**：postinst 在第一次装时定下这个用户，写进 `/var/lib/deneb/desktop-user.env`（`DENEB_ALLOW_UID`），服务读它。
  - 定的顺序：
    - 先看 `SUDO_UID`（`sudo apt install`）或 `PKEXEC_UID`；
    - 都没有，就是图形商店以 root 装的，取控制台前的那个人（`/run/systemd/seats/seat0` 的 `ACTIVE_UID`）。
  - 这个用户的窗口和 `deneb` 命令能直接用引擎，别的用户不行（套接字只给他，引擎还按对方的 uid 再核一次）。
  - 升级不改它。要换人，在 `/etc/default/denebd` 写 `DENEB_FLAGS="-allow-uid N"`。
  - 引擎这边：`-allow-uid` 的默认值读 `$DENEB_ALLOW_UID`。

发给普通人（用户 11-10 定）：

- 两个 deb 放在 GitHub Release 上一起下载，`sudo apt install ./deneb_*.deb ./denebd_*.deb`，不建 apt 源；
- 升级也是下载新的两个包再装一次；
- 装包的人就是 sudo 的那个人，正好被 postinst 放行。

## 许可证（`deploy/notices.sh`）

- 我们的许可证：仓库根目录的 LICENSE（PolyForm Noncommercial 1.0.0）。
- 第三方的：`deploy/notices.sh` 按 GOOS、GOARCH 列出构建链进去的东西，生成 THIRD_PARTY_NOTICES：
  - 每个 Go 模块根目录里的 LICENSE、COPYING、NOTICE、PATENTS；
  - Go 自己的许可证；
  - 用 `-f "名字=文件"` 补的额外文件，比如 wintun.dll 的 `LICENSE.txt`；
  - 哪个模块找不到许可证文件，就报错退出。
- Linux：`build-deb.sh` 给两个包都放了 `/usr/share/doc/<包名>/copyright`（我们的 LICENSE）和 `THIRD_PARTY_NOTICES`（引擎链进去的 20 个模块和 Go）。
- Windows：安装包归客户端线。建议窗口和引擎合成一份：
  `GOOS=windows GOARCH=amd64 deploy/notices.sh -f "Wintun 0.14.1 (wintun.dll)=$HOME/deneb-deps/wintun-0.14.1/wintun/LICENSE.txt" ./cmd/deneb ./cmd/denebd`
  这台 Mac 上跑过，24 个模块都有许可证。

## 诊断包（i7，`engine/diag.go`）

`GET /v1/diagnostics` 回一个 zip，客户端库是 `localclient.Diagnostics`，命令行是 `deneb diag [-o 文件]`。

| 文件 | 内容 |
| --- | --- |
| README.txt | 里面有什么、没有什么 |
| engine.json | 版本、平台、模式、状态目录、日志位置、控制服务器、启动参数 |
| state.json | 窗口看到的那份状态：设备、局域网、问题码；去掉了 userView |
| paths.json | 到每台设备的路径：直连还是中转、往返、丢包、退避、候选地址、收发计数 |
| relays.json | 中转站连接 |
| filter.json | 过滤器按原因计的丢包、流数 |
| netmap.json | 最近一份网络图 |
| system.txt | 内核模式在系统上设的网卡、路由、DNS（和 `denebd preflight` 同一份描述） |
| log.txt | 日志最后 4 MB，从整行开始 |
| goroutines.txt | 协程，查卡住用 |

不含的东西：

- 密钥：存私钥的状态文件不放进来；公钥只留最后 4 位，足够在几份文件里认出同一把；
- 文本里像密钥的 32 字节十六进制、base64 串也一样截短；
- 登录链接和邀请链接里的令牌；
- 账号里的人和邀请（userView）；
- 传输的内容：日志里本来就只有地址和端口。

名字里带 private、secret、token、password、cookie、loginURL、authkey 的字段，值也都去掉。假引擎（`denebd fake`）回一个只有说明文件的 zip，给界面联调用。

## 测试

单测：

- `cmd/denebd`：`TestHasFlag`、`TestCompareVersions`（语义化版本，含预发布号的先后），以及 M5 的 `TestSplitState`、`TestCheckRunFlags`。
- `engine`：
  - `TestRedactText`、`TestRedactJSON`；
  - `TestDiagnosticsHoldNoKeys`：两个真引擎登录后导出诊断包，日志里故意写了公钥和登录、邀请链接。查过十个文件都在，任何一把私钥、两台的公钥和链接令牌都不在任何一个文件里；
  - `TestShutdownIsForUserSpace`：用户态引擎能经本机接口停，内核模式的不行；两种模式的更新都回 `not-service`。

假拓扑：Linux 检查改用 `denebd` 包，以 `SUDO_UID=1500` 装（相当于用户 jie 用 sudo 装），并加了两项：

- jie 不用 sudo，`deneb status` 就能看到已登录；
- 另一个用户 guest 被拒。

四个发行版都过了。Linux 检查里 M5 原有的各项也都过了：按名字 ping（含 1400、3000 字节）、resolved、借道被拦、停起杀、退回用户态、换服务器包、purge。

- 11-09 23:4x：Debian 13、Ubuntu 24.04。那时包名还叫 `deneb-engine`，和最后的版本只差名字；
- 23:5x：改叫 `denebd` 后，Debian 12、Ubuntu 26.04。

Windows 部分三个平台都编得过，在 Mac 上只能跑到单测。在真机上要验的：

- 窗口那一套装法（`-state`、`-wait-lock`，再 `/v1/shutdown`），以及不提权的窗口连得上；
- 装两次；
- 更新：测试版不签名，可以测 same、older、updated 这几种结果；签名那条要等有了证书；
- 卸载钩子：makensis 要能编过；
- 卸载后路由表、NRPT、网卡列表和装之前一致。

## Windows 真机验证（i2，等主会话问过用户再上）

要换测试机（台式机 100.122.56.85，经 Tailscale 连）的引擎。用的版本在 `test/topo/.accept/m6-11100008-win/`：

- `a/`、`b/` 两个测试版（denebtest），版本号是 `0.6.0-test.1`、`0.6.0-test.2`。用语义化版本号，是为了能测"一样"和"更旧"；
- 两个目录里都有官方 0.14.1 的 wintun.dll；
- 另有 LICENSE、THIRD_PARTY_NOTICES.txt、新的 `undo.ps1`（会连 `%ProgramFiles%\Deneb` 一起删）、SHA256SUMS。

复制到测试机的 `%USERPROFILE%\deneb\m6\a`、`…\m6\b`，装的时候把这两个目录当作安装目录 `$INSTDIR`。参数沿用 `kup.ps1` 那一串：`-control … -control-pin … -port 41722 -hostname desktop -name 阿杰的台式机 -kind desktop -socks= -userview=false`，下面记作 `<参数>`。

**先做的**：
- 照上次的做法，`snap.ps1` 存一份装之前的快照，开着 `mon.ps1` 盯百度和 ssh；
- 新的 `undo.ps1` 换到 `%USERPROFILE%\deneb\undo.ps1`，建后备计划任务：25 分钟后以 SYSTEM 跑它（上次的 `svc-install.ps1` 前几行）。

**步骤和该看到什么**：

1. **装（照窗口的顺序）**：
   - `m6\a\denebd.exe service install -state $env:LOCALAPPDATA\Deneb -wait-lock 30s <参数>`，然后马上 `m6\a\denebd.exe debug shutdown` 停掉用户态引擎（它是测试版，有这个接口）；
   - 该看到：
     - 打印出"本机接口放行 …"和登录桌面的那个人；
     - `sc.exe qc Deneb` 的程序路径是 `"C:\Program Files\Deneb\denebd.exe" run -mode kernel -state … -allow-user S-1-5-21-…`；
     - `sc.exe qfailure Deneb` 是 2/2/10 s 重启；
     - `C:\Program Files\Deneb` 里只有 denebd.exe、wintun.dll，`icacls` 显示普通用户只能读；
     - `denebd status --json` 是 kernel、已登录、还是 d_mhtavmihjj、版本 0.6.0-test.1；
     - `%LOCALAPPDATA%\Deneb\denebd.log` 有 `[service] service install: done`；
     - `checks.ps1`、MTU 几项照旧过。
2. **不提权也连得上**：以放行的那个用户、不提权地跑一次 `denebd.exe status --json`，要看到已登录。
   - 放行的用户就是 Administrator 时：`runas /trustlevel:0x20000 "cmd /c C:\Users\Administrator\deneb\m6\a\denebd.exe status --json > C:\Users\Public\deneb-pipe.txt 2>&1"`，几秒后读这个文件。
   - 是别的用户，或者 runas 在 ssh 里起不来：建一个 `/rl LIMITED /it /ru <那个用户>` 的计划任务跑同一句，跑完删掉。
3. **装第二次**：同一句 install 再跑一次。
   - 服务照样在跑，配置不变；网卡、NRPT 规则都只有一份，路由在；
   - 记下中间 ping 断了多久：它会先停服务再起。
4. **更新**：
   - `m6\a\denebd.exe update C:\Users\Administrator\deneb\m6\b\denebd.exe`：回 `updated 0.6.0-test.2`，3 s 内 `status` 的版本变成 test.2，`Program Files` 里的 `.old` 在重启后没了，ping 恢复；
   - 再更新一次同一个文件，回 `same`；拿 `m6\a` 的回 `older`；
   - 给一个不叫 denebd.exe 的路径，回 `failed`。
   - 正式版（没签名）那条 `needs-install` 用测试版测不到：测试版的服务对没签名的文件放行，用来测换文件和重启。
5. **卸载（照卸载程序的做法）**：
   - `m6\b\denebd.exe service uninstall`；
   - 服务没了，`C:\Program Files\Deneb` 没了，网卡、路由、NRPT 规则都没了；日志里有 `[service] service uninstall`；
   - `%LOCALAPPDATA%\Deneb` 里的状态还在；
   - 再拍一次快照和装之前比，要一致。
6. **复原**：照 `start.ps1` 起回用户态引擎（m2-11092227），确认已登录；删掉后备计划任务，停掉 `mon.ps1`。

**撤销办法**：
- 自动：后备计划任务 25 分钟后以 SYSTEM 跑新的 `undo.ps1`：停掉并删除服务，删 `%ProgramFiles%\Deneb`、Deneb 的 NRPT 规则和名叫 Deneb 的网卡。不带 `-Purge`，状态（设备身份）不动。Tailscale 要是被弄断，最多 25 分钟就恢复。
- 手动（ssh 还通时）：`m6\a\denebd.exe service uninstall`，或者 `sc.exe stop Deneb; sc.exe delete Deneb` 再跑 `undo.ps1`。然后照 `start.ps1` 起回用户态引擎。
- 用户态引擎没退、服务里的引擎等锁超时时：服务会每隔 2–10 s 重试，不影响正在跑的用户态引擎。直接卸载服务就行。

这台 Mac 上测不到的：
- NSIS 卸载钩子要等客户端线打出安装包，用真的卸载程序跑；
- 别的 Windows 用户被拒，测试机上没有第二个账号，不建。

## 过程记录

### 11-10 01:3x：Ubuntu 虚拟机里装 deb 发现的三件（a1）

客户端线在 Ubuntu 26.04 虚拟机里测了包（截图在 `test/topo/.accept/m6-11100120-linux/`），有三件归引擎线，都已改好：

1. **登录后一直"正在连接 Deneb…"**：`-userview`（把账号的视图推给窗口）原来只在 macOS 上默认开。
   - 现在三个平台都默认开，可以用 `$DENEB_USERVIEW` 改；
   - 服务器包（deneb-server）的 `/etc/default/denebd` 写了 `DENEB_USERVIEW=false`，因为没有窗口；
   - Windows 的系统服务原来也是关的，装服务的参数里没写它，现在跟着默认开了；
   - 假拓扑的节点自己写了 `-userview=false`，不受影响。
2. **点"登录"报"没有配置 Deneb 服务器地址。"**：主会话定了，正式版编译时就带上 Deneb 自己的控制服务器 `https://103.42.31.155:47443` 和它的指纹（`cmd/denebd/control_default.go`）。
   - 什么都不配的人装上就能登录；
   - 自己部署的人用 `-control`、`-control-pin`、`DENEB_CONTROL`、`DENEB_CONTROL_PIN` 或 Linux 的 `/etc/default/denebd` 改；
   - 只写了我们这台服务器的地址没写指纹，也会补上指纹；别人服务器的指纹不会去猜；
   - 测试版照旧不带；
   - 登录过以后，引擎用状态里记下的那台服务器，和原来一样。
   - 测试：`TestControlFor`。
3. **别的用户被拒时，窗口说"引擎没在运行"**：客户端库新加了 `localclient.ErrNotAllowed`，表示引擎在，但不让这个用户用。
   - 套接字或管道权限不够，或 Windows 上的管道不是这个用户自己的，就报这个错。命令行会说"这台电脑上的 Deneb 引擎在为别的用户运行，这个用户用不了它。"，窗口怎么说由客户端线出设计。
   - 系统引擎拒绝这个用户时，客户端不再退到这个用户自己的用户态引擎，`EnsureRunning` 也不再起一个新的：一台电脑上的引擎属于另一个用户时，这个用户自己再起一个也不是这台电脑的。
   - 测试：`TestNotAllowedIsNotNotRunning`。

测试：
- 引擎、命令、客户端 17 个包全过，三个平台 vet 都过；
- 假拓扑 Debian 13、Ubuntu 24.04 整项检查用新的默认值跑过：桌面包开着 userview，服务器包关着；
- 客户端线 01:4x 在 Ubuntu 26.04 虚拟机里复验，用工作区代码新打的 deb，什么都不配：
  - 请求登录马上拿到 M1 的链接（没有真的登录，免得给账号多加一台设备）；
  - 诊断包的 engine.json 里 `userView` 是 true；
  - 别的用户看到"这台电脑上的 Deneb 引擎在为别的用户运行，这个用户用不了它。"；
  - 卸载后网卡、路由、DNS 和装之前一致。

### 11-10 01:0x：M7 安全审查改了这里的装服务流程

- 安全审查（[m7/engine-security.md](../m7/engine-security.md)）发现，系统服务拿用户的 `%LOCALAPPDATA%\Deneb` 当状态目录，能被普通用户借来提权（junction 重定向 SYSTEM 写的文件）。
- 现在 `service install` 拒绝用户文件夹里的状态目录，设备身份改用 `-import-state "%LOCALAPPDATA%\Deneb"` 带到 `%ProgramData%\Deneb`。装服务的结果改写进注册表 `HKLM\SOFTWARE\Deneb` 的 `ServiceInstall`。
- 服务的管道改在 `\\.\pipe\ProtectedPrefix\Administrators\Deneb\denebd`。
- 上面「Windows 真机验证」里的 `-state $env:LOCALAPPDATA\Deneb -wait-lock 30s` 要换成先 `denebd shutdown`，再 `-import-state`。下次换引擎时照新流程验。

### 11-10 00:2x：真机验证的结果和跟着改的两处

主会话 00:1x 在测试机上照 6 步做完，都对上了（记录在 `test/topo/.accept/m6-11100013-win/`）。引擎线看了台式机的日志（只读复制回来）和构建服务器的计数，有三点。

1. **大包 ping 还不通，是构建服务器那头的旧引擎**：
   - 构建服务器（m2-11092136）的 `out:fragment-without-head` 从 9 次涨到 18 次，正好是这轮 1400 字节 3 次（每次丢 1 块）加 3000 字节 3 次（每次丢 2 块）；日志 16:16:50、16:16:56（UTC）都有。
   - `in:fragment-without-head` 没涨，说明请求是整个到的，台式机这头没问题。
   - 构建服务器换新版后再测，并进下次换引擎。
2. **装服务时状态锁不起作用（已改）**：
   - 台式机日志 00:16:03.501：服务里的引擎起来时，用户态引擎还没退，41722 端口被占，就换了一个端口。
   - 原因：Windows 上的状态锁一直是空的，原先以为命名管道能挡住第二个引擎。可服务以 SYSTEM 跑，联网在监听管道之前，所以 `-wait-lock` 根本没等。两个引擎用同一份身份同时跑了一会儿。
   - 这次碰巧用户态引擎退得快，没出事。要是服务里的引擎先去监听管道，就会失败退出，2 s 后才被拉起。
   - 改法：Windows 上的状态锁改成独占打开的 `engine.lock`，进程退出（包括崩溃）时系统自动放开，和 macOS、Linux 的 flock 一样会等。
3. **重装、更新时断 3 秒多，各段时间**（日志）：

   | 段 | 重装 | 更新 |
   | --- | --- | --- |
   | 旧引擎停下、撤掉网卡 | 0.20 s | 0.16 s |
   | 旧进程退出到新进程建网卡 | 1.97 s | 1.68 s |
   | 建 Wintun 网卡 | 0.37 s | 0.35 s |
   | 到构建服务器通（重装时直连，更新时先走中转） | 0.60 s | 约 0.9 s |

   - 大头在中间那段：旧进程退出、服务管理器、新进程启动。重装时这段里还包括复制 20 MB 的 denebd.exe，以及系统对新程序的扫描。
   - 这次改了三处：
     - 重装时先把新文件复制到旁边，再停服务、改名换上，复制和扫描就挪到断网之前；
     - 等服务停下的轮询从 200 ms 改成 50 ms；
     - 更新时重启前的等待从 1 s 改成 0.3 s。这段不断网，只是让新版早点上来。
   - 另外加了两行日志：新进程一开始写 "[service] process started"，旧引擎全部停完写 "stopped"。下次就能看清中间那 1.7–2 s 花在哪了。
4. ssh 里 `runas /trustlevel` 起不来，是 ssh 会话的限制。窗口是用 ShellExecute 的 runas 提权装服务，自己本来就不提权，不受影响。
5. 客户端线 00:2x 又提了两件，都改了：
   - 引擎从 `$INSTDIR\bin\` 挪到 `$INSTDIR\` 顶层，MyGo 的自动更新才换得了它；
   - 覆盖安装和卸载前先停用户态引擎，为此加了 `deneb shutdown`，会等到进程退出才返回。

新的一对测试版在 `test/topo/.accept/m6-11100105-win/`（`0.6.0-test.3`、`test.4`），带上了这两处修改。下次换引擎时照「Windows 真机验证」再跑一遍，多看三件事：

- 服务的引擎等用户态引擎退了才起，日志里不再有 "picking another"；
- 重装、更新时 ping 断多久；
- "process started"、"stopped" 两行的时间。

### 11-10 00:0x：不签名、GitHub Release、许可证

- 用户定了开源、不签名、GitHub Release、PolyForm Noncommercial。
- 引擎这边跟着改了三处：
  - 正式版的更新回 `needs-install`，窗口重装服务，弹一次 UAC；
  - 加了 `deploy/notices.sh`，deb 里带上许可证；
  - `undo.ps1` 连 `%ProgramFiles%\Deneb` 一起删。
- 编了 Windows 真机验证用的两个测试版，写好了步骤和撤销办法。

### 11-09 23:3x–23:5x：开工

- 主会话转来用户原话"继续我们 其他的阶段的开发"：M5 收尾（服务的管道放行登录的用户），再做 M6 的 i2、i3、i7 引擎部分。
- 和客户端线对了安装包：用 MyGo 自带的 NSIS，首次引导时由窗口装服务，卸载钩子由引擎线写，引擎随窗口更新。界面包和引擎包各一个。
- 写的时候顺带修了 M5 的一处：服务出错退出时，原来不会被拉起来（见「做法：Windows」）。
