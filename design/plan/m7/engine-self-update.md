# 系统网卡自己更新：不输密码，窗口不闪

> 主会话 11-15 审过，照着实现了。用户 11-15 定了方向：后台服务先核对新版本的签名，再自己换上，不用输密码；换的过程中窗口保持原样，不闪；确实需要密码时，提示里写明是 Deneb 在更新。审的时候定下的：§3 Windows 做，和 macOS 同一套；§5 窗口不闪由引擎线一起做；§6 加一条加固：服务是签过名的版本时，包里没有签名的 denebd 按"签名不对"处理，不转去要密码。

"系统网卡"是界面上对后台服务的叫法（DESIGN §6 装网卡）。下面说"服务"都指它：macOS 上 root 跑的 launchd 任务，Windows 上的系统服务，程序都是 `denebd`。

## 0. 现在怎样

- macOS：`cmd/denebd/service_darwin.go` 的 `serviceSelfUpdate` 一律回 `needs-install`。窗口（`cmd/deneb/update.go` 的 `followEngine`）就调 `installService`，用 osascript 的 `with administrator privileges` 重装一遍。每次更新都要输密码；系统弹窗的标题是 osascript，也没有一句话说是谁、为什么。
- Windows：服务按 Authenticode 判断新文件是不是同一发布者签的。发布版没有签名，所以也回 `needs-install`，每次更新弹一次 UAC。
- 窗口：引擎一停，局域网标题下面那句马上变成"连不上这台电脑上的 Deneb，正在重试…"。新引擎起来、已登录但还没拿到局域网时（`LoggedIn && !Loaded`），整个窗口回到初始的"正在连接 Deneb…"，拿到后再回来，这就是"闪"。

## 1. 签名：打包时给 denebd 单独签

- **钥匙**：应用更新的那把 Ed25519。公钥在 `cmd/deneb/mygo.json`，私钥只在环境变量 `MYGO_UPDATER_PRIVATE_KEY` 里。
- **签什么**：照 MyGo 的做法，签 `denebd` 文件的 SHA-256（32 个字节），和中转站程序一样（`cmd/deneb-relay/update.go`）。
- **签名放哪**：和程序放在一起，进应用包，随应用更新一起到用户机器上：
  - macOS：`Deneb.app/Contents/Resources/denebd.sig`
  - Windows：`denebd.exe.sig`，在 `Deneb.exe` 旁边

  内容是一小段 JSON：

  ```json
  {"version": "0.1.9", "sha256": "<十六进制>", "signature": "<base64>"}
  ```

  `version` 只用来早点拒绝和写日志。真正的版本，是验过签名以后运行一次程序问出来的（§2 第 5 步），所以把旧版本的签名文件改个版本号也混不进来。
- **工具**：新加 `deploy/sign-engine.go`（`//go:build ignore`，和 `deploy/relay/manifest.go` 一样用 `go run`）。它从环境变量读私钥，先核对私钥和 `mygo.json` 的公钥是一对，再签名、写 `.sig`。私钥不打印、不落盘。
- **`deploy/package.sh`**：macOS、Windows 编完 `denebd` 后马上调用它，签名写进 `cmd/deneb/resources/<平台>/`，再交给 `mygo build` 打包。结束时照现在的 `cleanup` 一起删掉。没有 `MYGO_UPDATER_PRIVATE_KEY` 时不签，打出来就是没签名的版本（开发版），更新照旧走管理员认证。
- **CI 发版**：`release.yml` 已经把 `MYGO_UPDATER_PRIVATE_KEY` 交给 `deploy/release.sh`，再到 `package.sh`，所以 Windows 的 `denebd.exe` 在 CI 上就会签好。macOS 包是主会话在 Mac 上用 `package.sh macos` 打的，设好同一个环境变量就行。
- **denebd 怎么带公钥**：`cmd/denebd` 里写一个公钥列表常量，现在只有一把。加一个测试，保证 `mygo.json` 里的公钥在列表里（中转站有同样的测试）。

## 2. macOS：服务自己换

服务以 root 运行。窗口调 `UpdateEngine(包里 denebd 的完整路径)`，服务按下面的顺序做，任何一步不对就停下，回一个结果，旧程序照旧运行：

1. **先看签名文件**：路径必须是绝对路径、文件名是 `denebd`。读旁边的 `denebd.sig`：没有，回 `needs-install`，窗口走管理员认证，开发版就是这样；读不懂，回 `bad-signature`。
2. **加锁**：和安装、卸载用同一把锁（`install.lock`），同时只有一次。
3. **只收普通文件，整份读进内存**（`readPlain`）：打开时用 `O_NOFOLLOW`（末尾是符号链接就不打开）和 `O_NONBLOCK`（FIFO 不会卡住），打开后确认是普通文件、只有这一个名字（不是硬链接）、不超过 256 MB，再整份读进内存。`.sig` 也这样读，最多 64 KB。窗口给的路径可能指向只有 root 能读的文件（比如设备私钥，再配一个 `.sig`），也可能是 FIFO、`/dev/zero` 这类东西；这样读，root 不会把它们复制到别人读得到的地方，也不会一直往系统盘写（主会话 11-15 审出来的）。
4. **在内存里验签名，验过才写**：这份内容的 SHA-256 要和 `.sig` 里的一样，Ed25519 签名要能用程序里任一把公钥验过。不对就回 `bad-signature`，什么都没写。验过了，才写成 `/Library/PrivilegedHelperTools/Deneb/denebd.update`：这个目录要求 root 拥有、别人不可写，每一级上级目录也是（`macProtectedDir`），写完 fsync，权限 0755（验过的发布版，别人读到也无妨）。之后只运行这一份，用户那边再改原文件也没用。
5. **问版本**：签名对了，才运行一次 `denebd.update version`，最多等 10 秒，结果必须和 `.sig` 里的 `version` 一样。
6. **比版本**：
   - 和正在跑的一样：回 `same`。
   - 比正在跑的旧：回 `older`，不降级。
   - 正在跑的是开发版（版本号不是数字）、新的是发布版：接受，签过名的发布版总比开发版可信。
   - 两个都认不出：回 `failed`。
7. **记下要换**：先在程序旁边写下 `update.json`（`/Library/PrivilegedHelperTools/Deneb/update.json`），内容是 `{"from": 旧版本, "to": 新版本, "prev": 旧程序的位置, "tries": 0}`，再换文件。
8. **换文件**：把 `denebd` 硬链接成 `denebd.prev`，留着回退；再把 `denebd.update` 原地改名成 `denebd`。改名是原子的，任何时候 `denebd` 都是一个完整的程序，旧的或新的。
9. **重启**：回 `updated` 给窗口，然后照正常关机的流程退出：撤掉路由、DNS 和网卡。launchd 的任务是 `KeepAlive`、间隔 2 秒，马上用新程序再起来。

新程序起来以后：

10. **最先做的事**（还没碰网卡和网络）：读 `update.json`。如果是给自己（`to` 是自己的版本）写的，`tries` 加一写回。超过 3 次，说明新程序起不来、一直被 launchd 拉起：把 `denebd.prev` 改名回 `denebd`，在 `update.json` 记下"`to` 版本起不来，已经回到 `from`"，然后退出，launchd 就会起旧的。
11. **完全起来以后**（本机接口在听、引擎启动成功）：删掉 `update.json` 和 `denebd.prev`。
12. **回退以后**：旧程序照常运行，`update.json` 里记着哪一版起不来。同一个版本再递过来时，直接回新加的结果 `rolled-back`（"这一版在这台电脑上起不来，已经用回原来的版本"），不再试；递来别的版本才再试。窗口就是这样知道回退了的：换完以后等到一个新的进程回应（按进程号认，免得把还没停的旧进程当成新的），版本不是新的，就再问一次，拿到 `rolled-back` 就说一句（§5）。不用在 `VersionInfo` 里另加一项。

中途断电（每一步之后都想过）：

| 停在哪 | 下次开机 |
| --- | --- |
| 验签名、写下来、问版本的时候 | 旧程序起来，看到孤零零的 `denebd.update`，删掉 |
| 写了 `update.json`、还没改名 | 旧程序起来，`to` 不是自己，说明没换成：删掉 `update.json` 和 `denebd.update` |
| 改了名、还没退出 | 新程序起来，照第 10、11 步走 |
| 新程序起到一半 | `tries` 照常累计，超过 3 次就回退 |

## 3. Windows 用同一套（主会话 11-15 定了：做）

做法：把 `update_windows.go` 里按 Authenticode 判断的 `sameSigner`，换成 §2 第 3 到 6 步的读法和签名检查，签名文件是 `denebd.exe.sig`。改名、`service restart` 都不变，再加上第 7、10 到 12 步的记录和回退。

- **只收本机盘符上的路径**（`localDrivePath`）：`\\server\share\…` 这类共享，`\\?\`、`\\.\`、`\??\` 这类设备路径，相对路径，带 `.`、`..` 或数据流（`名字:流`）的，都拒绝。路上每一级都不跟着走（`Lstat`）：任何一级是符号链接或目录联接也拒绝，因为它可能通到共享上。打开以后再确认一遍：是磁盘上的普通文件、只有这一个名字、不超过 256 MB，最终路径不在别的电脑上。SYSTEM 去读别人的共享时，会用本机账号去认证，这是常见的提权路子（主会话 11-15 审出来的）。
- **`wintun.dll`**：照旧要 WireGuard 的 Authenticode 签名，也照上面读进内存。Windows 验 Authenticode 要一个文件，所以先写成只有 SYSTEM 和管理员能打开的 `wintun.update.dll`（`writePrivate`），在那里验过再换上：没验过的内容不落在别人读得到的地方。

- **好处**：发布版更新不再弹 UAC，只有第一次装网卡弹一次。两个平台一个做法、一套测试。不用买代码签名证书（一年几百美元起；要 SmartScreen 直接放行还得 EV 证书）。
- **坏处**：
  - 这把私钥泄露的后果更大了。现在泄露，别人能发假的应用更新，但换系统服务还要用户在 UAC 里点"是"；做了以后，带着泄露私钥签名的程序能悄悄以 SYSTEM 身份运行。macOS 照用户的方向做了以后也一样。
  - 私钥现在放在两处：CI 的 secret；打 macOS 包的那台 Mac 上的一个文件，在 MyGo 生成它时放的 `~/Library/Application Support/mygo/update-keys/` 里，不只在环境变量里。建议把那个文件挪进钥匙串，打包时再读进环境变量。挪不挪由用户定。
  - 这是我们自己的签名，Windows 不认识：Defender、SmartScreen 照旧把 `Deneb.exe` 当未知发布者，这一点和现在一样，不会更好也不会更坏。
- **建议**：做。和 macOS 同一套，风险也是同一把钥匙的风险，不多出新的。以后有了 Authenticode 证书，两样都查。

## 4. 确实要密码时，说清是 Deneb

什么时候还要密码：第一次装网卡；包里的 `denebd` 没有签名（开发版）；正在跑的服务是这项改动以前的版本，还不会自己换，只会回 `needs-install`。所以换到第一个会自己换的版本时，还要再输最后一次密码，之后的更新就不用了。这些情况照旧走管理员认证，但提示要写清楚。

- **macOS**：`do shell script` 加 `with prompt`。系统的认证窗里，原来那句默认的话换成我们的：
  - 第一次装："Deneb 要安装系统网卡。"
  - 更新："Deneb 要更新系统网卡。"

  后面"输入密码以允许此操作"是系统自己加的。照 AGENTS §2.5 写，一句话，不加解释。窗口的标题和图标仍是 osascript，这一点改不了。要换成 Deneb 自己的，得用 `SMAppService` 或 `SMJobBless`，都要求 Apple 开发者账号签名，和 i1 一起等账号。
- **Windows**：UAC 窗里只有程序名、发布者和一个"是"按钮，不能加我们的话。能改的只有程序名：给 `denebd.exe` 加一个版本资源（`FileDescription` 写 "Deneb 系统网卡"），UAC 就显示它，不显示 `denebd.exe`。发布者没有签名，仍是"未知发布者"。做了 §3 以后，只有第一次装网卡才会看到。版本资源这一次没加：要多一个打包工具（goversioninfo 一类），而 UAC 现在只在第一次装网卡时出现，以后要再加。

## 5. 窗口不闪（cmd/deneb 的要求，谁来做等主会话定）

窗口知道是自己在换引擎：发出 `UpdateEngine` 拿到 `updated`，或者开始了管理员认证（`needs-install`）。这段时间里：

- **画面保持原样**：局域网、设备、连线、颜色、开着的卡片都不动。不回到"正在连接 Deneb…"，也不显示"连不上这台电脑上的 Deneb"。
- **连线状态先不变**：新引擎起来以后要重新连上各台设备。每台设备先用换之前最后的状态，新引擎报出"直接连上"或"经中转"以后再换成新的；最多等 10 秒，10 秒后照实显示。
- **时间线**：

  | 换引擎开始以后 | 窗口上 |
  | --- | --- |
  | 0 到 10 秒 | 什么都不变。正常两三秒就换好了，看不出来 |
  | 10 到 60 秒 | 局域网名字下面那句换成"正在更新网卡…"，灰点，和装网卡时的"正在装网卡…"同一个样子。别的不变 |
  | 换好了 | 那句回到平常的样子。不另外提示"已更新" |
  | 服务回退了（§2 第 12 步） | 那句回到平常的样子，提示条显示"网卡没更新成功，用回了原来的版本"（4 秒后收起）。网络照旧能用 |
  | 60 秒还没有引擎回应 | 才照现在的样子显示"连不上这台电脑上的 Deneb，正在重试…" |

- **管理员认证那条路**：等用户输密码时，那句写"正在更新网卡…"。用户点了取消，那句回到平常的样子，不报错，下次打开 Deneb 再问。
- 交付时照 AGENTS §1.3 录屏，逐帧看。macOS 和 Windows 各录一遍：自己换（不输密码）、要输密码、取消、新程序起不来（测试开关）。

## 6. 各种情形

| 情形 | 怎样 |
| --- | --- |
| 换到一半断电，或者某一步失败 | §2 的表：任何时候 `denebd` 都是完整的程序，开机后旧的或新的接着跑，留下的半成品自己清掉 |
| 新程序起不来 | 起 3 次不成就换回旧的（§2 第 10 步），窗口说一句（§5），同一个版本不再试 |
| 正在跑的是没签名的开发版 | 服务里照样带着公钥，照样验签名：签过名的发布版能换上它；包里的也是开发版、没有签名时，回 `needs-install`，走管理员认证 |
| 版本相同 | 回 `same`，什么都不做 |
| 降级（包里的比正在跑的旧，比如装回了旧的 Deneb） | 回 `older`，不换。窗口和更新的引擎照样能一起用 |
| 本机的恶意程序往应用包里放假的 denebd，借服务以 root 运行 | 签名验不过，回 `bad-signature`：不运行它，也不转去管理员认证（不然等于让用户替恶意程序输密码），窗口说"安装包里的系统网卡不对，请重新下载 Deneb" |
| 正在跑的服务是签过名的版本，包里的 denebd 却没有签名（被换成了没签名的） | 同上，回 `bad-signature`，不转去要密码。服务装上或换上时，把签名一起放在自己旁边（`denebd.sig`），据此知道自己是签过名的。开发者要装开发版，自己运行 `sudo denebd service install`（主会话 11-15 定的加固） |
| 窗口给的路径是符号链接、硬链接、FIFO、设备或目录，或者文件超过 256 MB（比如本机恶意程序指向只有 root 能读的设备私钥，再配一个 `.sig`） | 读之前就拒绝，回 `bad-signature`，什么都不写（§2 第 3 步）。验签名在内存里做，验过才写到磁盘上，没验过的内容不会落在别人读得到的地方 |
| Windows：路径在共享上、是设备路径，或者路上有链接、目录联接 | 读之前就拒绝，回 `bad-signature`，SYSTEM 不去碰别的电脑（§3） |
| 拿一个签过名的旧版本来降级 | 运行问出的版本旧，回 `older` |
| 两个更新同时来 | `install.lock` 只放一个过去，另一个回 `failed`（"另一次安装正在进行"） |
| 用户态引擎（没装网卡） | 照旧回 `not-service`，窗口停掉它、起新的 |
| Linux deb | 不变，跟着 apt 更新 |
| 以后换签名密钥 | 分两版发：先发一版，程序里的公钥列表同时有新旧两把，仍用旧钥匙签；等大家都换上这一版，再发用新钥匙签的。没装上第一版的机器验不过新签名，回 `needs-install`，输一次密码就过去了 |

## 7. 要改的地方

- `deploy/sign-engine.go`（新）、`deploy/package.sh`。
- `cmd/denebd`：
  - `selfupdate.go`（新，两个平台共用）：公钥列表、签名、读进内存验过再写、版本的规矩、`update.json` 和启动时的回退检查；
  - `plainfile.go`、`plainfile_unix.go`、`plainfile_windows.go`（新）：只收本机磁盘上的普通文件，整份读进内存（`readPlain`）；Windows 只收本机盘符上的路径（`localDrivePath`），`wintun.dll` 验之前写成只有 SYSTEM 和管理员能打开的文件（`writePrivate`）；
  - `update_darwin.go`（新）：macOS 的自己换，按 §2；
  - `update_windows.go`：换成同一套签名，加上记录和回退；`service_windows.go` 在服务开始时检查；
  - 装服务时把签名放在程序旁边，清掉以前的记录（两个平台）；
  - 本机接口加一个结果 `rolled-back`。
- `cmd/deneb`：
  - `service_darwin.go`：`with prompt`，装和更新各一句；
  - `update.go`：换之前开始保持画面，按结果说话，等新的进程；
  - `engineswap.go`（新）：§5 的"画面保持原样"和时间线；`sync.go`、`view.go`、`main.go` 各接一行；
  - 假引擎（`DENEB_FAKE`）下按 ⌥⌘6 到 ⌥⌘9 演一遍：换得快、换得慢、换回旧的、签名不对；录屏时用 `DENEB_FAKE_SWAP=quick|slow|rollback|badsig`，启动 5 秒后自己演。

## 8. 测试

- 单测（`cmd/denebd`，路径都指到临时目录，用会回答 `version` 的小脚本当引擎）：
  - 签名：对的、错的、缺的、程序被改过的、另一把钥匙的、旧版本的签名改了版本号的；
  - 版本：相同、更旧、开发版对发布版；没有签名时，服务签过名就拒绝，没签过名就要密码；
  - 换文件（macOS）：新旧程序和签名都在该在的地方；
  - 回退：`tries` 计数、超过 3 次换回、换回后同一个版本回 `rolled-back`；
  - 断电：记下了但没换成的，旧程序起来时清掉；
  - 只收普通文件：符号链接、硬链接、FIFO（不会卡住）、目录、设备、超过上限的文件、签名文件是链接，都拒绝，暂存的目录里什么都不留；签名不对时，暂存的目录只读也照样回 `bad-signature`，说明是先验后写。这几个测试在改之前的代码上是红的：用 `go test -overlay` 换回旧的 `selfupdate.go` 跑过；
  - Windows（CI 的 Windows 上跑）：共享、设备路径、目录联接、符号链接（这个账号能建时）、硬链接、目录、超过上限的文件都拒绝；`selfUpdate` 收到共享或设备路径直接回 `bad-signature`，服务旁边不多出文件；`wintun.update.dll` 只有 SYSTEM 和管理员能打开。路径的规矩（`localDrivePath`）在三个系统上都跑。
- 窗口（`cmd/deneb`）：§5 时间线的每一段（`TestHoldThroughSwap`），按进程号等新的引擎（`TestWaitRestarted`）。
- 签名工具：用一次性的钥匙签了再验，对得上；钥匙格式不对时不签、不打印它。
- 录屏：`TestSwapFrames` 在测试窗口里演六段（两秒换好、十几秒才换好、新版本起不来、要输密码时、签名不对、改之前），浅色深色各一段，逐帧看，交到桌面"M7-公测"第 7 批。
- 真机：Mac 上从一版更新到下一版（主会话打两版签过名的包），录屏；Windows 测试机同样一遍。
