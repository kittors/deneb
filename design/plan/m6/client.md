# M6 · 客户端线（界面、更新器、打包）方案和状态

> 负责：i1 的调研、i4、i5、i6、i7 的界面部分，安装包怎么打（和引擎线对过）。依据：实现计划 §3 M6，[m6-acceptance.md](../m6-acceptance.md)，MyGo 0.7.4 的文档（`docs/distribution.md`、`docs/updates.md`、`docs/architecture.md`），苹果 TN3179（本地网络权限）。
> 最后更新：2026-11-10 01:0x（更新器接进客户端、Windows 和 Linux 的打包脚本）。不改线上，不提交。

## i1 · macOS 的网卡：网络扩展还是特权守护进程（给用户定）

> **搁置**：用户 11-10 说暂时没有 Apple 开发者账号，先放一放。Mac 上先用开发版；下面的对比留着，等有了账号再定。

| | 网络扩展（Network Extension，系统扩展形式） | 特权守护进程（root 跑的 launchd 守护进程） |
| --- | --- | --- |
| 要什么 | Apple 开发者账号，每年 99 美元（中国区 688 元）；Developer ID 证书；"网络扩展"和"系统扩展"两项授权（开发者后台自己勾选，再做一份 Developer ID 的描述文件）；签名、公证 | 同一个开发者账号，用来签 Developer ID 和公证；不用申请授权，不用描述文件 |
| 普通人装的时候 | 拖进"应用程序"→第一次打开，系统弹"已阻止系统扩展"→去"系统设置 › 通用 › 登录项与扩展"打开 Deneb（要密码或指纹）→再弹"Deneb 想添加 VPN 配置"→允许（又一次密码或指纹）。大约点 5 下、认证 2 次；系统设置的 VPN 列表里会多一个 Deneb | 拖进"应用程序"→第一次打开，到引导里"装网卡"那一步，系统提示后台项目要批准→在"登录项与扩展"里打开 Deneb（输一次密码）。大约点 3 下、认证 1 次。另一种做法是 .pkg 安装包，装的时候输一次密码就完事 |
| 能不能解决"本地网络"权限（问题码 `local-network`） | 苹果的说明只写了"应用扩展形式的网络扩展不受限"；我们在 App Store 外发布只能用系统扩展。系统扩展以 root 跑，按"root 跑的程序不受限"这条应当也不受限，但文档没有点名，要在真机上验 | 能。TN3179 写明："launchd 启动的守护进程"和"以 root 跑的程序"自动有本地网络权限。引擎今天碰到的问题，直接就没了 |
| 引擎要改多少 | 大：引擎要编成 C 库，放进一个用 Swift 写的 NEPacketTunnelProvider 里跑；窗口和引擎改走扩展的通道；DNS、路由交给系统的 VPN 框架 | 小：就是现在的 denebd，以 root 跑内核网卡（PoC 在 Mac 上就是这么跑的），本机接口照旧 |
| MyGo 打包要补什么 | 把 `.systemextension` 放进 `Contents/Library/SystemExtensions` 并单独签名（带授权和描述文件）；窗口里调 `OSSystemExtensionRequest`、`NETunnelProviderManager`。MyGo 现在都没有 | 把守护进程的 plist 放进 `Contents/Library/LaunchDaemons`（MyGo 的资源只进 `Contents/Resources`，要加一个钩子或者构建后再放）；窗口里调 `SMAppService.daemon(...).register()` 和状态（几十行 purego）；denebd 作为附带的可执行文件，MyGo 会照常签名 |
| 更新 | 扩展跟着应用一起换，系统在下次启动时换新扩展，有时要用户再批一次 | 守护进程的程序就在应用包里（`BundleProgram`），应用一更新，重启守护进程就是新版本 |
| 别的 | 走苹果的 VPN 框架，系统设置里有开关；能进 Mac App Store（得改成应用扩展） | 引擎以 root 跑，出了漏洞影响更大，要把本机接口的权限管严；Tailscale 开源的 `tailscaled` 在 Mac 上也是这么跑的 |

**推荐：特权守护进程**，用 `SMAppService` 从应用包里注册。
- "本地网络"的问题直接解决，不用等用户点"允许"，也不用赌系统扩展是不是例外。
- 装的时候少一次认证，系统设置里也不会出现一个"VPN"。
- 不用申请授权，不用描述文件，引擎不用改写成扩展。

真要进 Mac App Store 再做网络扩展。两种都要开发者账号（每年 99 美元），签名、公证这一步一样。

## i4 · 自动更新（11-10 接进客户端，只在本机测过，没发布）

用户 11-10 定的：免费开源，许可证 PolyForm Noncommercial 1.0.0；Windows 不签名；放 GitHub Release；macOS 等有了 Apple 开发者账号再说（i1 搁置）。

- **接法**：补丁 `client-patches/m6-updater-packaging.patch`（笔记目录）11-10 01:xx 已合进工作区，没提交。合后三个平台 `go vet`、客户端全部测试、M1 基线帧都过；合前合后各拍 50 帧，42 帧逐像素相同，8 帧差 1 到 14 个像素、每像素 1/255，在渲染噪声以内。
  - 新增 `cmd/deneb/update.go`；`main.go` 只动了两处：开头 `useUpdater()`，`keepEngine` 里接上引擎跟随。
  - 用 MyGo 的更新器插件，窗口是原生界面画的（`native.New`），不要网页视图。
  - 启动 10 秒后查一次，以后一天查一次；有新版才弹它自己的"软件更新"窗口：看说明，安装、跳过这个版本或稍后提醒，装好后"立即重新启动"。中文是 MyGo 自带的。
  - 开发版（`mygo dev`）和 deb 装的不查；托盘、菜单里没有加"检查更新…"，要等设计点头再加。
- **密钥还没生成**：`mygo.json` 先不加 `updates`，这样打出来的包不会自己更新。真要发版那天：
  1. `mygo keygen`，私钥存进密码管理器，再放进 GitHub Actions 的密钥 `MYGO_UPDATER_PRIVATE_KEY`。丢了，已经装上的客户端就再也收不到更新。
  2. `mygo.json` 加 `"updates": {"publicKey": "<mygo-update.pub 的内容>", "github": "kittors/deneb"}`。
  3. 打包时带上私钥，`mygo build` 会多写出更新包和清单 `update-<系统>-<架构>.json`，和安装包一起传到这个版本的 Release。
- **发布前先试**：`DENEB_UPDATE_SOURCE=<文件夹>` 让窗口从一个文件夹里拿更新，文件夹里放 `mygo build` 写出来的那些文件。照样只认同一把钥匙签的，别人放进去的装不上。
- **本机测过**（11-10 00:2x，`/tmp/deneb-updtest`，测试密钥，单独的标识 `dev.deneb.updtest`，演示模式，没碰正在跑的 Deneb 和引擎）：
  - 装 0.1.0，文件夹里放 0.1.1：启动 11 秒后在后台下好、验完签名。0.1.0 照常跑，退出后 1 秒换成 0.1.1，签名完好，再打开就是 0.1.1。
  - 把更新包改坏一个字节：不装，还是 0.1.0，也没留下文件。
  - 没测：增量更新（测试时关了），更新窗口里的按钮没有点过（测的是"以后自动下载"那条）。Windows、Linux 没在真机上试。
- **引擎跟着界面**（`followEngine`，每次启动、引擎一应答就跑一次；安装包里带着引擎才跑）：
  - 比较 `GET /v1/version` 的引擎版本和窗口版本（`App.Version()`，打包时引擎用同一个版本号构建），一样就不动，不一样就 `POST /v1/engine/update`，交给引擎判断：
    - `updated`：服务换好自己，本机接口断一下，窗口自己重连；
    - `same`、`older`、`bad-signature`、`failed`：只记日志；
    - `not-service`：引擎是窗口起的，让它退出（`Shutdown`），`keepEngine` 3 秒内从安装目录起新版；
    - `needs-install`：正式版不签名，服务分不出包里的文件是不是我们的，不肯自己换。窗口以管理员身份重装服务：`denebd.exe service install -import-state "%LOCALAPPDATA%\Deneb"`（引擎线 M7 改的流程：服务的身份只放 `%ProgramData%\Deneb`，已经有了就跳过这一步），Windows 会问一次（UAC），这期间窗口不起自己的引擎。没跑成，就读注册表 `HKLM\SOFTWARE\Deneb` 的 `ServiceInstall`（"时间 原因"）记下来。
  - 单测 `TestFollowEngine` 覆盖了上面每一种。真机上还没走过。
  - 用户在 UAC 上点了"否"：旧服务照跑，下次打开再问。每次更新弹 UAC 时窗口说什么，要和首次引导的设计一起定。
  - Linux 的引擎是单独的 deb，界面包里不带引擎，所以这一段不跑。
- **Linux**：deb 装在 `/opt/deneb`，自己写不进去，MyGo 的更新器不管。不建 apt 源（用户定的），所以要用户去 Release 下新的 deb，界面和引擎一起装。窗口里提醒"有新版本"现在没有，要不要做、怎么说，等设计定。

## 打包（和引擎线对过，11-10 改成不签名、GitHub Release）

- **一条命令**：`deploy/package.sh [windows] [linux]`（不写就两个都打），产物在 `deploy/out/<版本>/`。
  - 版本号取 `cmd/deneb/mygo.json`，窗口、引擎、几个包都是这一个。
  - 需要：wintun（`WINTUN_DIR`，默认 `~/deneb-deps/wintun-0.14.1/wintun`，脚本不替人下载）、Docker（引擎线的 deb 用 dpkg-deb）、makensis（没有就不出 Windows 安装包，会提示）。
  - 发版时再给 `MYGO_UPDATER_PRIVATE_KEY`。
- **Windows**（`windows-amd64/`）：MyGo 的 NSIS 安装包，不签名。
  - 按用户装在 `%LOCALAPPDATA%\Programs\Deneb`，不要管理员权限，窗口能自己更新。
  - `denebd.exe`、`wintun.dll` 放在 `Deneb.exe` 旁边，不放进 `bin\`。原因：更新时 MyGo 把安装目录里的每个文件、文件夹改名挪开再换新的；Windows 能给正在跑的程序改名，不能给装着正在跑的程序的文件夹改名（MyGo 源码里写了）。窗口起的引擎在 `bin\` 里跑，整次更新就会失败回滚。换下来的旧文件，MyGo 下次启动时删掉。
  - 服务在首次引导"装网卡"那一步装（引擎线 M7 改过的流程，`design/plan/m7/engine-security.md` 最后一节）：先停掉窗口起的用户态引擎（`denebd.exe shutdown`），再 `runas "$INSTDIR\denebd.exe" service install -import-state "%LOCALAPPDATA%\Deneb"`，弹一次 UAC。引擎把这两个文件复制到 `%ProgramFiles%\Deneb` 再装服务，设备身份带到 `%ProgramData%\Deneb`，以后只在那里读写。没装上：原因在注册表 `HKLM\SOFTWARE\Deneb` 的 `ServiceInstall`，窗口把用户态引擎起回来。
  - 卸载钩子是引擎线的 `deploy/windows/installer.nsh`，`mygo.json` 里已经接上（`windows.installerInclude`）。
  - 引擎线 11-10 01:0x 按我们说的改好了，进了工作区：
    - 钩子里改用 `$INSTDIR\denebd.exe`；
    - 覆盖安装（`customInit`）和卸载（`customUnInstall`）都先跑 `denebd.exe shutdown`，让窗口起的引擎退出、等进程没了（最多 10 秒），不然它占着文件，写不进也删不掉；
    - 服务里的引擎不理这条命令，它在 Program Files 跑，本来不占安装目录。
  - SmartScreen 会拦没签名的安装包，要点"更多信息 › 仍要运行"，引导页和发布说明里要写清楚。
- **Linux**（`linux-amd64/`、`linux-arm64/`）：三个 deb，放 GitHub Release，不建 apt 源。
  - `deneb_<版本>_<架构>.deb`：MyGo 打的界面，装在 `/opt/deneb`。依赖 `denebd`、`libayatana-appindicator3-1` 和 GTK 3；不带网页视图（`webview: false`），不装 WebKitGTK。
  - 不设 `linux.command`，从应用菜单打开就行；设了会和引擎包的 `/usr/bin/deneb` 抢同一个文件。
  - `denebd_<版本>_<架构>.deb`（台式机的引擎和命令行，systemd 服务）和 `deneb-server_<版本>_<架构>.deb`（服务器）由引擎线的 `deploy/linux/build-deb.sh` 打，脚本调它，放进同一个目录，也记进 `SHA256SUMS`。
- **许可证**：每个包都带我们的 `LICENSE` 和 `THIRD_PARTY_NOTICES`（第三方许可证汇总），汇总由引擎线的 `deploy/notices.sh` 按平台列出链接进去的每个 Go 模块（含 MyGo、purego）和 Go 自己的许可证。
  - Windows：窗口和引擎合成一份，加上 Wintun 的。放在安装目录的 `LICENSE.txt`、`THIRD_PARTY_NOTICES.txt`。
  - Linux 界面包：`/opt/deneb/LICENSE.txt`、`THIRD_PARTY_NOTICES.txt`。引擎包是引擎线的 `/usr/share/doc/<包名>/copyright` 和 `THIRD_PARTY_NOTICES`。
- **在这台 Mac 上跑过**（11-10 00:4x，副本 `~/.cache/deneb-pkg-test`，因为 colima 只共享家目录）：
  - 两个平台都打出来了：Windows 是 `Deneb.exe`、`denebd.exe`、`wintun.dll` 和两个许可证文件；Linux 两个架构各三个 deb。
  - 界面 deb 的依赖、文件位置看过都对；引擎用 `-X main.version` 写进去的版本对得上；Windows 的第三方许可证汇总一共 25 段（Go、23 个模块、Wintun）。
  - 连跑两遍，`SHA256SUMS` 不重复。
  - **没做到**：这台 Mac 没有 makensis，Windows 安装包没编出来，`installer.nsh` 还是没编过。装 makensis（`brew install makensis`）就能在本机出安装包；也可以等用户点头后，在 Windows 测试机上装一次、更新一次、卸载一次。
- **别的**：`resources/` 里的 `Deneb.icns`（mac 的图标，700 KB）也被带进了 Windows 和 Linux 的包，没坏处，但白占地方。挪进 `resources/darwin/` 就只进 mac 的包；图标归 logo 那边，没动。
- **以后**：发版用 `mygo build -upload` 传到 GitHub 的草稿 Release（要 `gh` 和 `updates` 配置），引擎的 deb 用 `gh release upload` 补上。要不要做 GitHub Actions 自动打包，等开始发版再定。

## 这台电脑的 Deneb 由别的账户在用时，窗口说什么（11-10，等用户定文案）

- 什么时候出现：一台电脑上，Deneb 引擎只给一个账户用。Linux 上是装包的那个人，Windows 上是装服务时登录着的那个人。换了别的账户打开窗口，引擎在跑，但不让这个账户连（引擎线的 `localclient.ErrNotAllowed`）。
- 现在窗口会说"启动不了 Deneb 引擎，请重新安装 Deneb"，不对：引擎没坏，重装也没用。
- 补丁 `client-patches/m6-window-not-allowed.patch`（笔记目录，没合）：
  - 接住这个错，不再去起引擎，在"正在连接"那个位置换成一句话。
  - 窗口起用户态引擎时照旧传 M1 服务器地址。正式版引擎自己带着，但开发和测试机上用的测试版（denebtest）不带；不传的话，点"登录"会报"没有配置 Deneb 服务器地址。"（引擎线 11-10 提醒）。
  - 三个平台 vet 和单测都过了（用 `-overlay` 验的，没动工作区）。
- 那一句话，补丁里先用 A，等用户选：
  - A："这台电脑上的 Deneb 正由另一个账户使用，这个账户打不开它"
  - B："Deneb 在这台电脑上是给另一个账户用的，请换回那个账户"
  - 命令行引擎线已经写成："这台电脑上的 Deneb 引擎在为别的用户运行，这个用户用不了它。"

## i5、i6、i7 的设计（11-09 23:53 交付，等用户看）

- 设计写在 DESIGN §7.3（首次打开的引导）、§6.14（导出诊断包、别的代理软件），主树已合，都标了"设计稿，等用户看过"。
- 演示在副本 `/tmp/deneb-m6` 里（M5 的副本再加 M6）：
  - `cmd/deneb/onboard.go`：引导的三步，加上诊断包那一行；
  - `client/demo_m6.go`：事件 `m6`、`m6-fresh`、`notice-surge`、`notice-v2rayn`；
  - 提示条的按钮能换名字（"在访达中显示"）；提示卡片有 `Detail` 时，按各个软件自己的说法讲。
- 交付在 `~/Desktop/Deneb组网demo/M6-装得上/第1批-首次打开/`，6 个场景各有浅色、深色录像和截图：01 登录（邮箱、验证码，错了格子抖一下）、02 装网卡（为什么要权限、等系统允许、装好了；另有"先不装"的截图）、03 新建第一个局域网、04 加入朋友的局域网（粘贴链接、看邀请、加入）、05 导出诊断包、06 别的代理软件的提示（Surge、v2rayN）。
- 12 段录像用 `frame-check.py` 查过，都没有单帧跳变、没有突然跳变；录帧用 `DENEB_M6=<目录> go test -run 'TestM6Frames$' ./cmd/deneb`，交付脚本是 `/tmp/deneb-tools/review/deliver_m6.py`。
- 没做的：
  - 真客户端里的引导、装服务（Windows 的 runas，mac 的 SMAppService）、诊断包接引擎的 `GET /v1/diagnostics`，都等用户看过设计；
  - 正式的登录方式等用户定；
  - 托盘图标、应用图标等 logo 会话换完再跟上。
