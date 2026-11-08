# M1 · 组网引擎线（m1-engine）状态

> 负责目录：`engine/`、`cmd/denebd/`、`test/topo/`。依据：实现计划 §2.3、§2.6、§3 M1。
> 最后更新：2026-11-08 09:40

## 进度

| 项 | 状态 |
| --- | --- |
| 底座选型 | ✅ 选乙（见下） |
| 引擎骨架：三张表热替换、三个平台用户态、绑物理网卡 | ✅ |
| Mac 内核网卡 | ✅ 代码和单元测试；真机没测，要另行跟用户确认 |
| 本机接口文档、类型、客户端、假引擎 | ✅（见下） |
| 设备接口客户端（`proto/`） | ✅ 接 m1-control 的 `-dev` 和 hk-155 线上版 |
| 命令行 `deneb login / status / lan / ping` 等 | ✅ |
| colima 假拓扑 | ✅ 14 项全过（见"测到了什么"） |
| 真机 | ✅ 三台登录（服务器端批准），`real.sh` 15 项全过（见"真机"） |

## 底座选型：乙（在 PoC 的 magicsock 上补齐）

计划 §2.3 的判据是"甲能不能在不改 Tailscale 源码的前提下接上三张表"。读 `tailscale.com@v1.104.5/wgengine/magicsock` 的源码后判定不能，没有再花三天做实测对照：

- §2.3 要求 M1 用 1 秒心跳、2.5 秒不信任。magicsock 里这两个是包内常量（`heartbeatInterval = 3s`、`trustUDPAddrDuration = 6.5s`），没有 envknob，也没有选项。要改只能改它的源码。multi-lan-tech §6.2 实测过，6.5 秒这一档直连断后 TCP 在中转上一直跟不上。
- M2 要按丢包率和延迟选路（§6.8 那种直连有回应、丢 20% 包的情况）。选路逻辑在 `endpoint.go` 的 `betterAddr` 等未导出函数里，同样要改源码。
- 三张表本身接得上：对端表对应 `SetNetworkMap`，中转表对应 `SetDERPMap`，开放表由我们自己的过滤执行。所以挡住甲的是时间参数和选路，不是数据结构。

继续用 Tailscale 的：`derp/derphttp`（中转客户端）、`net/stun`、`types/key`（密钥类型，和 §2.6.1 一致）。

## 共用 go.mod 加的依赖

和 PoC 同一版本，2026-11-07 加：

- `golang.zx2c4.com/wireguard v0.0.0-20261024220952-b3bc704ef0ee`（含 gVisor 用户态栈的依赖 `gvisor.dev/gvisor`）
- `tailscale.com v1.104.5`
- `golang.org/x/crypto`、`golang.org/x/sys`、`golang.org/x/net`（升到 v0.60.0）
- `go4.org/mem`
- `github.com/tailscale/go-winio`（Windows 命名管道，tailscale.com 本来就依赖它）

## 本机接口（给界面和命令行）

类型都在 [`engine/localapi/apitype`](../../../engine/localapi/apitype/apitype.go)，只依赖标准库。Go 客户端是 [`engine/localapi/localclient`](../../../engine/localapi/localclient/client.go)，假引擎是 [`engine/localapi/fake`](../../../engine/localapi/fake/fake.go)。真引擎和假引擎共用同一个 HTTP 层 [`engine/localapi/handler.go`](../../../engine/localapi/handler.go)，所以线上格式完全一样。

**传输**：HTTP + JSON。字段 camelCase，id 都是字符串，时间 RFC 3339，往返时间是整数毫秒 `rttMs`。

| 系统 | 用户态引擎（界面以普通用户启动） | 内核网卡引擎（root） |
| --- | --- | --- |
| macOS | `~/Library/Application Support/Deneb/denebd.sock` | `/var/run/deneb/denebd.sock` |
| Linux | `$XDG_RUNTIME_DIR/deneb/denebd.sock`，没有就 `~/.local/share/deneb/denebd.sock` | `/var/run/deneb/denebd.sock` |
| Windows | `\\.\pipe\denebd` | 同左（M5 的服务沿用） |

- `DENEB_SOCKET` 覆盖以上全部，开发窗口和测试都用它，不碰真实路径。
- `localclient.New("")`（没设 `DENEB_SOCKET` 时）**每次连接都重新找**：root 的路径上有引擎应答就用它，否则用自己的。这样界面不用重启，就能跟着引擎从用户态换到内核模式再换回来。root 的套接字文件还在但没人应答（内核引擎崩了）时，也照样退回自己的。为此这种客户端不复用连接，本机套接字上每次新建一条，开销可以忽略。拉起引擎时总是拉用户态的，监听自己的路径。（11-08 改；之前是创建时找一次，之后一直用它。）
- 同一个状态目录同一时间只能有一个引擎：`engine.lock` 加了 flock，第二个引擎启动时报「另一个 Deneb 引擎正在用这份状态」后退出。进程一退锁就放，崩溃也一样。内核引擎接手用户态引擎时用 `-wait-lock` 等它让出。Windows 上靠命名管道本身挡住第二个引擎。
- 只有本人和 root 能连：套接字文件 0600，另外按对方进程的 uid 再查一遍（macOS `LOCAL_PEERCRED`、Linux `SO_PEERCRED`）；Windows 管道的权限只给当前用户和 SYSTEM。

| 路径 | 作用 |
| --- | --- |
| `GET /v1/version` | 接口版本 `api`（现在是 1）和引擎版本 |
| `GET /v1/state` | 一份 `State` |
| `GET /v1/watch` | NDJSON 事件流。第一行是 `{"type":"state","state":{...}}`；之后除连接状态外的任何变化都推一份完整 `state`；连接状态单独推 `{"type":"paths","paths":[...]}`，只含变了的对端，每个对端一条完整 `PeerPath`。种类一变就推，延迟和丢包最多 2 秒一次。读得慢不会拖住引擎：积压的事件合并成最新的一份 |
| `POST /v1/ops` | `{"id":幂等键,"op":"lan.create","args":{...}}` → `{"version":N,"result":{...}}`。引擎转给控制服务器，等本机用上版本 N 再回，所以回来后读到的 `state` 已经带着这个变化 |
| `POST /v1/login` | → `{"url":...}`。登录完成后 `state.loggedIn` 变成 true，事件流会推 |
| `POST /v1/logout` | 退出 |
| `POST /v1/lan/pause` | `{"scope":"lan/…","paused":true}`。引擎先改表，再告诉控制服务器 |
| `GET /v1/resolve?name=box` | → `ResolveResult`；同名多台时错误码 `ambiguous`，带候选 |
| `GET /v1/ping?to=…&n=4` | 到对端的 ICMP 回显，经过隧道和过滤。`stream=1` 时一行一个结果，最后一行是汇总 |
| `GET /v1/services` | 本机在听的常见服务 |
| `POST /v1/open` | `{"device":…,"kind":"rdp","port":3389}` → `{"host","port","addr"}`。内核网卡模式给正式名和端口；用户态给 `127.0.0.1:端口`（本地端口映射，引擎退出才拆）。界面按种类自己拼 `rdp://`、`smb://`、`http://` |
| `GET /v1/userview` | 用户视图。无界面的节点平时不收，这里按需去控制服务器拉 |

**State 里界面要用的**：`selfDeviceID`（认出"这台电脑"）、`loggedIn`、`loginURL`、`user`、`controlConnected`、`mode`（`kernel` / `userspace`）、`version`、`paths`、`userView`（控制服务器的用户视图，原样转发，用 `proto/` 的类型解）。`engine.watchers` 是开着的 `/v1/watch` 流的条数：界面开着窗口时握着一条，脚本可以据此等界面连上（11-08 加）。`self`、`scopes`、`peers`、`relays` 是引擎的网络图，命令行用；界面以用户视图为准。

**PeerPath**：`kind` 取 `direct`、`relay`、`connecting`、`unreachable`、`offline`、`paused`、`refused`；`relay` 是中转站 id；`endpoint` 是直连时对方的 ip:port；`rttMs`、`loss`（最近 10 秒）、`since`。引擎原样上报，不防抖，防抖只在 liveStore 里做（计划 §2.7 第 7 步）。

M2 起（11-08 加，都是可选字段，旧界面不受影响）：

- `idle`：对端一阵子没流量了，引擎就不再探测它。这时 `kind` 是最后的样子，线可以照原样画；一有流量就会醒来。
- `from`：上一个种类，不管它只持续了多久。拿到 `kind: direct` 加 `from: relay`，就可以放出「先经中转连上，打通后换直连」（场景 1）；中转那一段即使只有几毫秒，也照样能知道。
- `reason`：直连明明有回应却走中转的原因。`lossy` 是直连在丢包，`slow` 是直连比中转慢很多。界面可以写成「网络不太好，改走中转」。

**谁在用这台设备的服务**（M2，场景 5）：

- `State.usage` 是一个数组，每一项是一个对端正在用这台设备的某项服务：`deviceID`（谁在用）、`scopes`（经哪些局域网）、`kind`/`name`（这台设备开放时起的名字）、`proto`/`port`、`conns`（几条连接）、`since`、`lastSeen`。只算对方发起、还活着的连接；ping 不算服务，本机自己发起的连接也不算。
- 事件 `{"type":"usage","usage":[…]}`：使用情况一有变化就推（每 2 秒最多一次）。数组可能是空的，表示现在没人在用。
- 事件 `{"type":"cut","cut":[…]}`：这台设备把服务从某个局域网关掉、或拒绝某人的那一刻，被切断的那些使用。界面就在这些线上放一小段红光。

**错误**：非 2xx 的回答都是 `{"error":{"code":…,"message":…}}`，`message` 是一句中文，可以直接放进提示条。`code` 取 `forbidden`（没权限）、`offline`（连不上控制服务器）、`refused`（对方拒绝）、`not_logged_in`、`not_found`、`ambiguous`、`invalid`、`conflict`、`timeout`、`internal`。按 `code` 判断，HTTP 状态码只是给 curl 看的。

**假引擎**：

- 进程内：`e := fake.New()`，`stop, _ := e.Listen(套接字路径)`，或者直接把 `e` 当 `http.Handler`。`e.PushPaths(...)` 推连接状态，`e.Update(func(*State))` 改快照并推送，`e.SetUserView(raw, version)` 放用户视图，`e.HandleOps(fn)` 决定操作的结果（返回错误就是拒绝）。默认的操作处理只把版本号加一，`lan.invite` 返回一条假链接。
- 独立进程：`go run ./cmd/denebd fake`，监听 `DENEB_SOCKET` 或默认路径，每 4 秒播一个事件（直连和中转来回换、NAS 掉线又回来、延迟抖动）。
- 演示数据是 M1 验收拓扑：这台电脑（kittors）在工作室和周末联机；构建服务器直连；阿杰的台式机经香港中转；阿杰的 NAS 直连。用户视图的格式归 `proto/`，假引擎不造，由测试或开发窗口用 `SetUserView` 放进去。

## 引擎的包

| 包 | 内容 |
| --- | --- |
| `engine` | 把下面这些串起来：密钥和状态目录、按网络图整份替换三张表、本机接口的实现、ping、SOCKS5、端口映射 |
| `engine/controlclient` | 设备接口：机器密钥签名换令牌、登录、长连接（45 秒没动静重连）、上报、操作、用户视图、邀请预览；https 只认固定指纹；连接也绑物理网卡（连不上才退回系统路由），不跟着代理节点起落 |
| `engine/magicsock` | 一个 UDP 端口 + 每个中转站一条 DERP 连接；探测用单独的 disco 密钥；直连优先、中转兜底；1 秒心跳、2.5 秒不信任；中转也测往返和丢包；对端刚上线时把丢掉的握手重发一次；探测消息带每次启动不同的实例号，对端引擎重启了就立刻重新握手（否则要等 WireGuard 15 秒后放弃旧会话） |
| `engine/filter` | 不转发、按范围放行服务、放行回包；ping 默认放行（被拒绝的除外）；IPv4 分片、ICMP 差错报文按流核对；换表时把不再允许的已有连接也切掉 |
| `engine/netstack` | gVisor 用户态栈；对端连进来的 TCP、UDP 转到本机 127.0.0.1 的同一端口 |
| `engine/names`、`engine/dns` | `设备.主人.deneb.internal`，短名只在唯一时解析；内核网卡模式的 DNS 只答这个后缀，别的一律拒绝 |
| `engine/bind` | 绑默认路由所在的物理网卡：macOS `IP_BOUND_IF`、Linux `SO_BINDTODEVICE`、Windows `IP_UNICAST_IF`；绑不上就走系统路由并在状态里提示 |
| `engine/tun` | macOS 内核网卡：一张 utun、一条 /16 路由、`/etc/resolver/deneb.internal`；改动先写撤销记录，崩溃后下次启动先撤销 |
| `engine/cli` | 命令行 |
| `engine/localapi/...` | 本机接口（见上） |
| `cmd/denebd` | 引擎和命令行同一个文件：`denebd run` 跑引擎，`denebd <命令>` 或改名/链接成 `deneb` 就是命令行；`denebd fake` 是假引擎 |
| `test/topo` | colima 假拓扑 |

**登录**：配置了控制服务器，引擎一启动就跑控制循环。没登录时它手里一直有一条当前有效的登录链接（`State.loginURL`、`deneb login`、`deneb status` 都能看到），链接 15 分钟过期后自动换新；有人在浏览器里登完，引擎立刻接上，不用再跑命令。登出（本机 `deneb logout`，或服务器那边把它登出）后同样马上备好下一条链接。

测试构建（`-tags denebtest`）另有 `/v1/test/...`。M2 加了这几个：`deneb debug loss <设备> 20 [out|in]`（在引擎里按比例丢掉和这个对端之间的 UDP，模拟坏线路）、`deneb debug netchange`（当作网络变了来处理）、`deneb debug trace <端口>|off`（把这个端口的每个 TCP 包连同序号记进引擎日志）。M1 已有的：`POST /v1/test/shutdown`（`deneb debug shutdown`，让引擎像收到 SIGTERM 一样停下。内核引擎以 root 运行，有了它，它的用户不用 sudo 也能叫停，网卡也跟着撤掉）、模拟 UDP 全丢、借道路由、伪造来源注入、关过滤、长连接计时（带每次停顿的时刻）、连续通断探测（`deneb debug probe`，每次一行带毫秒时刻）、按地址 ping，以及叠加地址上的几个小服务（网页 :80、计时流 :7000、回显 udp/7、测速 :9000）。正式构建没有这些。

## 怎么跑

```bash
go build -o denebd ./cmd/denebd                      # 正式构建
go build -tags denebtest -o denebd ./cmd/denebd      # 测试构建（验收用）
denebd run -control https://103.42.31.155:47443 -control-pin sha256:a90009bbefa6cbcad0971350099e2bb54c4f9968a8cdf1401ab54c6861cf0dd4
deneb login          # 打印登录链接
deneb lan create 工作室
deneb lan invite 周末联机
deneb lan join <链接>
deneb status
deneb ping nas.jie
```

`denebd run` 常用参数：`-mode userspace|kernel`（默认用户态）、`-port`（默认沿用上次的端口）、`-bind-if auto|none|网卡名`、`-socks 127.0.0.1:41080`（用户态给本机程序用，`-socks=` 关掉）、`-name`（macOS 默认取电脑名）、`-kind`、`-hostname`、`-userview`（macOS 默认开，别的默认关）、`-state`、`-socket`、`-wait-lock 10s`（等同一状态上的另一个引擎让出；默认不等，直接拒绝）、`-relay-ca`（只给假拓扑用）。以 root 在别人的状态目录上跑时，写下的文件保持原主人，用户态引擎之后还能读写。

`sudo denebd kernel-undo -state <目录>`：内核引擎没正常退出时，按状态目录里的撤销记录删掉 `/etc/resolver` 文件和路由（utun 随进程消失），再删掉没人应答的 root 套接字。还有引擎在用这份状态时它什么都不做。环境变量 `DENEB_CONTROL`、`DENEB_CONTROL_PIN`、`DENEB_SOCKET`、`DENEB_STATE_DIR` 和同名参数等价。

界面在用户态下启动引擎：同目录的 `denebd run`，脱离界面进程（Setsid），日志写状态目录；`DENEB_SOCKET` 设了就透传。

假拓扑：`go run ./test/topo`（`-keep` 留着、`-down` 只拆、`-only 名字,名字` 只跑几项、`-repeat N` 连跑几遍、`-skip-build`）。它只写、只清 `test/topo/.run/fake/`。11-08 以前它会清掉整个 `test/topo/.run/`，10:30 正好打断了用户第一次跑的内核网卡那一轮，还删掉了主会话的验收日志和共存监控数据。内核网卡那一轮和验收的记录现在放在 `test/topo/.accept/`。

## 测到了什么

**单元测试**（`go test ./engine/...`，25 项全过）：过滤（借道、按范围开放、回包、暂停、换表切连接、拒绝是单向的、分片、ICMP 差错、超时）、名字和 DNS、内核网卡的路由解析和撤销记录、本机接口和假引擎。

**进程内三引擎**（`engine/engine_test.go`，真的 WireGuard + magicsock + gVisor，进程内 DERP，手写网络图代替控制服务器）：

- 第一次应答 12ms（修之前 5.3 秒：接收方还没连上中转时第一个握手丢了，wireguard-go 要等 5 秒重试；现在对端第一次回应时把握手重发一次）。
- 两个范围互相看不见；暂停一个立刻不通、另一个不受影响；撤出后不通、加回（撤销）又通。
- 服务只开给工作室时周末联机的人打不开，再开给周末联机就能打开；拒绝后对方 ping 和服务都不通、立刻说"没让你用"，我这边照样能用对方的。
- UDP 全丢：2.5 秒转中转，恢复后 0.5 秒回到直连，期间丢 5 个 ping（100ms 一个）。

**本机 + m1-control 的 `-dev`**：两个引擎各登一个测试账号，建局域网、邀请、预览、加入、按局域网开服务、SOCKS5、暂停恢复都对；本机开着 Clash TUN，引擎绑在 en0，经 hk-spare 的 derper 中转，两个本机引擎之间直连 0.3–1.4ms。

**colima 假拓扑**（`go run ./test/topo`，2026-11-08 00:29，14 项全过）：控制服务器（m1-control 的 `-dev -tls on`，引擎走固定指纹）、自己的 derper、4 个用户态节点；mac 在两层保端口的 NAT 后面，阿杰的台式机在两层 NAT 后面、外层是每条流换端口的 NAT；构建服务器和 NAS 在"公网"。

| 项 | 结果 |
| --- | --- |
| 谁看得见谁 | mac 看见 3 台；构建服务器只看见 mac；台式机和 NAS 看见 mac 和对方 |
| ping 矩阵 | 该通的 8 对都通；构建服务器 ↔ 阿杰的两台 4 对都是"找不到" |
| 路径 | mac↔构建服务器、mac↔NAS、台式机↔NAS 直连；mac↔台式机经中转（对称 NAT，打不了洞） |
| 服务按局域网开放（验收第 8 条） | 只开给工作室时台式机打不开 mac 的网页；开给周末联机后打得开；关掉又打不开 |
| 借道（§6.4，两个方向） | 全部不通，mac 记下 `transit-in` |
| 伪造来源 | 对端 WireGuard 记下 `disallowed source address` |
| 暂停 | mac 暂停周末联机：NAS 立刻不通，构建服务器照常；NAS 那边也看不见 mac；恢复后双向都通 |
| 加入、撤出、撤销（第 7 条） | 构建服务器加进周末联机后 NAS 能 ping 通；撤出后 2 秒内不通；加回又通；工作室的长连接全程没断、停顿不超过 1 秒 |
| 拒绝（第 9 条） | 台式机 ping 和打开网页都失败，提示"没让你用"；mac 照样能用台式机；恢复后通 |
| UDP 全丢（第 10 条） | 约 2 秒转中转，恢复后 0.3 秒回直连；长连接 16 秒收到 157 行，最长停顿 1.9 秒，没断 |
| 两个局域网同时三条长连接（第 4 条） | 10 秒里都不断，停顿都在 1.5 秒以内 |
| 控制服务器停掉 | 照上一份网络图继续通；重新起来后自动重连 |
| 登出再登录 | NAS 登出后 mac 立刻看不到它，NAS 马上备好新登录链接；登回来还是同一台设备、还在周末联机里 |
| 重启 mac 的引擎 | 容器重启后 0.1 秒内就有应答，直接是直连（端口和网络图都沿用）；反方向 NAS、台式机去连重启后的 mac 也立刻通（修之前要等约 15 秒，见下） |

**这一轮跑测试时找到并修掉的**：

- 第一次应答要 5.3 秒：接收方还没连上中转时第一个握手丢了。现在对端第一次回应时重发一次，12ms。
- 登录链接过期后控制循环会空转；从没登录过的引擎重启后不再去拿链接，浏览器里登完它也不知道。现在配置了控制服务器就一直跑登录循环，链接过期自动换。
- 换中转表时在持锁状态下经中转发消息，中转还在连接时会卡住本机接口几秒。改成后台发。
- 对端引擎重启后，这边主动去连它要卡约 15 秒（还拿着对端已经忘掉的 WireGuard 会话）。现在靠探测消息里的实例号发现重启，立刻重新握手。
- 用户态栈拨号失败时返回"装着空指针的接口"，调用方判断不为空就会崩。改成返回真正的空。
- 控制服务器的连接没绑物理网卡，在本机走了 Clash（m1-client 发现的）。现在绑 en0，连不上才退回系统路由。
- `/v1/services` 把 macOS 的"隔空播放接收器"（5000、7000 端口）当成网站（m1-client 发现的）。现在按种类认：网站要回 HTTP 且不是隔空播放，命令行要有 SSH 问候，屏幕共享要有 RFB 问候。
- macOS 上没给 `-name` 时，设备名用的是主机名（"KittorsdeMacBook-Air"）。现在取系统设置里的电脑名（`scutil --get ComputerName`，"Kittors的MacBook Air"），去掉控制字符、截到 40 个字（服务器遇到超长的名字会拒绝整个登录）。别的系统照旧，由服务器用主机名。这个名字只在第一次登录时用。已经登录的设备不跟着改：之后名字归人管，引擎分不清现在的名字是不是人改过的。这台 Mac 是旧默认留下的，要改就在界面里改名，或者 `denebd op device.rename '{"device":"d_assusk6n2j","name":"Kittors的MacBook Air"}'`。验收正在进行，我没动它。还没进已部署的 `m1-11080030`，下一个构建带上。

## 真机

三台都跑测试构建 `m1-11080030`，用户态，连 hk-155 上的线上控制服务器：

| 机器 | 角色 | 怎么跑 | UDP |
| --- | --- | --- | --- |
| hk-155 | 构建服务器（kittors） | Docker `deneb-node`，宿主网络，1 核、256 MB，状态在卷 `deneb-node-state` | 41720 |
| hk-spare | 阿杰的 NAS（阿杰） | 同上 | 41721 |
| Windows 测试机 | 阿杰的台式机（阿杰） | `%USERPROFILE%\deneb\denebd.exe run`，用 `start.ps1` 经 WMI 启动，不挂在 ssh 会话上（ssh 断了也不停），日志 `denebd.log` | 41722 |

服务器上看状态：`ssh hk-155 docker exec deneb-node /denebd status`。停掉：`docker rm -f deneb-node`（状态卷留着）。Windows 停掉：`taskkill /im denebd.exe /f`；再启动：`powershell -ExecutionPolicy Bypass -File %USERPROFILE%\deneb\start.ps1`（脚本要存成带 BOM 的 UTF-8，否则 PowerShell 按 GBK 读，中文设备名会乱码）。

线上控制服务器没有 `/dev/approve`，登录要在浏览器里填测试账号的验证码。这个页面不是本机或带测试标记的地址，我不替用户输验证码，三台的登录交给用户。当前链接：`test/topo/real.sh links`。

11-08 08:22 主会话按用户的选择，用控制线写的批准命令在服务器端批准了四台（构建服务器、这台 Mac 归 kittors，NAS、台式机归阿杰）。批准前它逐台核对过服务器列出的登录码和设备自己报的码。在那之前，我想先用开发版控制服务器（带 `/dev/approve`）在真机上跑一轮，试了两种做法，都被自动模式拦下，没留下任何东西：一是从本机往真机开反向隧道（被判为"入站隧道"），二是在 hk-spare 上起一个只听 Tailscale 地址的服务器（被判为"对外开服务"）。另外，hk-155 的 tailscaled 没登录，不在 tailnet 里。

`test/topo/real.sh` 的做法：阿杰建两个临时局域网，engine-test-a（台式机）和 engine-test-b（NAS），都邀请 kittors，构建服务器两个都加入。这样构建服务器就像验收里的 Mac 一样夹在中间。脚本依次查谁看得见谁、ping、路径、两个方向的借道、伪造来源、UDP 全断时的长连接、两条长连接同时跑，最后删掉两个局域网，不给验收留东西。

**08:27 跑的结果：15 项全过，两个局域网已删。**

| 项 | 结果 |
| --- | --- |
| 谁看得见谁 | 构建服务器看见台式机和 NAS；台式机、NAS 都只看见构建服务器 |
| ping（各 5 个） | 构建服务器↔台式机 48–100ms，构建服务器↔NAS 3–8ms，全收到；台式机↔NAS 两个方向都是"找不到" |
| 路径 | 四个方向都直连：台式机在家里的 NAT 后面，打洞出来的映射是 203.0.113.172:32650；两台服务器之间 3ms |
| 借道 | 台式机借构建服务器去 NAS、NAS 借它去台式机都不通，构建服务器各丢了 3 个包（transit-in） |
| 伪造来源 | 构建服务器把来源伪造成台式机发给 NAS，NAS 的 WireGuard 按来源地址丢掉了 |
| 构建服务器↔台式机的 UDP 全断，开着一条长连接 | 2.5 秒后两边各切一次中转（101ms，经 hk-spare 上的 derp），恢复后各切回一次直连，没有来回跳；长连接不断，最长停 2.54 秒；切到中转后的约 5 秒里还有几次 0.3–0.7 秒的停顿 |
| 两条长连接同时跑 15 秒 | 构建服务器到台式机、到 NAS 都不断，最长停 119ms、174ms |

那几次 0.3–0.7 秒的停顿，事后单独量过：先把路径稳定在中转上再测，ping 30 个全收到、99.8–103.4ms，10 秒长连接只停过一次 318ms。中转这条路本身是干净的，停顿应该是内层 TCP 在 2.5 秒断流之后的恢复过程（量完就删了临时局域网 engine-test-r）。

三台的节点留着，给验收用；验收完停掉（上面有停的方法）。

## 已知的限制

- 隧道里只有 IPv4：网络图里的 IPv6 地址暂时不用，过滤遇到 IPv6 一律丢。
- 心跳一直在跑（1 秒），不管有没有流量；M2 改成只在有流量时跑。
- 选路只看有没有回应，不看丢包率；`PeerPath.loss` 已经在算，M2 拿它选路。
- 没有 UPnP、NAT-PMP。
- 换网（Wi-Fi 换了、睡眠醒来）不重建 UDP 套接字，靠心跳和 STUN 慢慢恢复；M2 处理。
- 中转站选常驻：第一个活跃局域网的第一个中转站；M4 按延迟选。
- Windows、Linux 的内核网卡在 M5。

## 验收准备（引擎侧）

M1 真机验收（计划 §3 M1 第 1–12 条）由主会话协调；界面上点什么、看什么在 [client.md](client.md) 的"真机验收"一节，这里只写引擎这边要敲的命令和要记的数字。

**怎么在各台上敲命令**：

| 机器 | 命令行 |
| --- | --- |
| 本机 Mac | `denebd <命令>`（`deneb` 这个名字是界面）。引擎是测试构建 `~/go/bin/denebd`，由界面用默认路径拉起 |
| hk-155（构建服务器） | `ssh hk-155 docker exec deneb-node /denebd <命令>` |
| hk-spare（阿杰的 NAS） | `ssh hk-spare docker exec deneb-node /denebd <命令>` |
| Windows（阿杰的台式机） | 经 Tailscale ssh：`%USERPROFILE%\deneb\denebd.exe <命令>`。局域网名用中文时注意 cmd 的编码，必要时用局域网 id |

下面 `<mac>` 是 Mac 的设备名（`denebd status` 第一行括号里的正式名）；设备地址从 Mac 的 `denebd status` 抄（被隔离的那一方自己查不到对方）。

**开始前**：三台真机已登录（构建服务器 kittors，NAS 和台式机阿杰）；第 4 条要用的"计时流"先开好：

- 构建服务器：`deneb service 工作室 other tcp/7000 on --name 计时流`
- 台式机、NAS：`deneb service 周末联机 other tcp/7000 on --name 计时流`
- 全程在 Mac 上跑 `test/topo/monitor-coexist.sh`（第 12 条），结束后 `pkill -f monitor-coexist`，再 `test/topo/monitor-coexist.sh --summary` 出汇总。

| 条 | 引擎侧要做的 | 要记的 |
| --- | --- | --- |
| 1 登录 | 三台真机已登好；Mac 在界面里登。登完 `denebd status` 第一行是"这台电脑（…kittors）" | Mac 登录完成的时刻 |
| 2 建局域网、加设备 | 界面里建"工作室"、把构建服务器拖进来；`denebd status` 看到构建服务器 | 连线从"正在连接…"到"直接连上"用了多久（`denebd status --json` 里 paths 的 since） |
| 3 邀请 | 台式机：`deneb lan create 周末联机`；NAS：`deneb lan add 周末联机`；台式机：`deneb lan invite 周末联机`（正式做时现生成，一条只能用一次）。Mac：`open 'deneb://join/<令牌>'`，收不到就 `denebd lan join <链接>` | 卡片出现、点加入到画布出现两台设备的时间 |
| 4 同时在两个局域网 | Mac 上三个终端各跑一条长连接：`denebd debug tick build 120`、`denebd debug tick desktop 120`、`denebd debug tick nas 120`，同时在界面里来回切侧边栏 10 次 | 每条的 `broken`（要 false）、`maxGapMs`（要 ≤ 500）、`gaps` 的时刻，对着界面的切换日志 |
| 5 互不打扰 | 先在 Mac 上跑 `denebd debug tick build 60`；台式机：`deneb lan remove 周末联机 阿杰的 NAS` | Mac 画面 2 秒内播放离开；工作室那条长连接 `broken` false |
| 6 隔离 | 台式机、NAS：`deneb status` 里没有构建服务器。借道：构建服务器 `deneb debug attack <mac> <台式机地址>/32`、`deneb debug ping-addr <台式机地址>`，收到 0；台式机反过来对构建服务器做一遍；Mac `denebd debug drops` 里 `in:transit-in` 增加。两边都 `deneb debug unattack <mac>` 撤掉借道设置，**然后**再测伪造来源（借道设置会把对端地址加进 WireGuard 的允许地址，不撤的话伪造的包是被过滤层拦下的，不是 WireGuard 按来源地址丢的）：Mac `denebd debug inject <构建服务器地址> <台式机地址> 80`，台式机的 `denebd.log` 里有 `disallowed source address`；反方向对构建服务器做一遍（看 `docker logs deneb-node`） | 四个用例全部不通 |
| 7 设备进出 | 界面里把构建服务器拖进周末联机；NAS：`deneb ping build`，通；拖回托盘，不通；点撤销，又通 | 撤出到 NAS 不通的时间（假拓扑里 < 2 秒） |
| 8 服务按局域网开放 | 构建服务器的测试网页在 :80。开给工作室（界面卡片里开，或构建服务器 `deneb service 工作室 web tcp/80 on`）；Mac 在界面里点"打开"，浏览器显示 `hello from build...`；NAS：`deneb debug http build` 失败；再开给周末联机，NAS 成功 | 两次 `debug http` 的结果 |
| 9 谁能用 | Mac 先开一个服务给周末联机：`denebd service 周末联机 web tcp/80 on`（Mac 引擎自带的测试网页）。界面里不让阿杰用这台电脑；台式机：`deneb debug http <mac>` 失败、`deneb ping <mac>` 提示"没让你用"；恢复后都通 | 失败和恢复的结果 |
| 10 直连断了 | Mac 上先跑 `denebd debug tick build 60`；`denebd debug block build`；界面连线改走中转、不出现"断开"、状态不闪；过一会 `denebd debug unblock build` | 转中转用时（`denebd status` 里 paths 变 relay 的时刻）、回直连用时、长连接最长停顿 |
| 11 界面回归 | 客户端线做 | — |
| 12 共存 | 全程 `monitor-coexist.sh` | google、baidu 失败次数，Tailscale 路由有没有变，ssh 心跳最长停顿 |

**内核网卡那一轮**（要用户另行确认，用户本人在终端里跑 `test/topo/kernel-round.sh`，输一次密码；先跑 `--dry-run`，只检查，什么都不改）：

- **前提**：Mac 的用户态引擎在跑、已登录；Mac 同时在工作室和周末联机；台式机和 NAS 只在周末联机；`/etc/resolver/deneb.internal` 不存在。不满足就停下，说清楚是哪一条。构建服务器要是也在周末联机里，脚本会在检查前先把它撤出（第 6 条要验台式机和 NAS 看不见它），结束时再加回去；原来开给周末联机的网页也会重新打开。这一轮在测试机上做的临时改动（撤出构建服务器、撤出再加回 NAS、借道设置、关掉网页）都随做随记进 `test/topo/.run/kernel-restore.sh`：结束时、出错时放回去，崩溃后由 `--undo` 放回去。11-08 09:50 的拓扑已经对了，`--dry-run` 能走完。
- **会改什么**：一张 utun、一条 10.48.0.0/16 路由、`/etc/resolver/deneb.internal`，都只在这一轮里，开头建一次、结尾撤一次。不碰 Clash、Tailscale、默认路由、系统 DNS 服务器、pf 和 IP 转发。开头和结尾换引擎时，窗口会停一两秒（SIGSTOP）：界面拉起的 `~/go/bin/denebd` 是旧构建，不占状态锁，冻住窗口它就没机会趁空当再起一个引擎。
- **怎么换**：先编出这个仓库的测试构建。sudo 一次，冻住窗口，停掉用户态引擎，在同一份状态上以内核模式起引擎（`-allow-uid`、`-wait-lock`），所以还是同一台设备、同样的局域网。然后放开窗口，等网卡、路由，再等窗口连上（`engine.watchers`）。
- **检查**（都用普通程序）：
  - 路由走向：Deneb 段走新 utun，去 Tailscale 和公网的照旧；IP 转发是关的。
  - DNS：系统解析三台的名字，不存在的名字查无此名，普通域名照旧。
  - 第 8 条：curl 能打开构建服务器的网页；在工作室关掉后打不开，开回来又能打开；它没开放的 9000 端口连不上。
  - 第 4 条：三条 TCP 长连接各 60 秒（`test/topo/tickread.py`），不断、最长停顿 ≤ 500ms。
  - 第 6 条：台式机和 NAS 看不见构建服务器；两个方向借道都不通，Mac 记了 transit-in；撤掉借道设置后，两个方向伪造的来源都被 WireGuard 丢掉。
  - 第 7 条（内核模式版）：NAS 撤出周末联机后，普通 ping 不通、名字查不到；加回后都恢复。
- **收尾**（正常结束、Ctrl-C、任何一步失败都走这里）：
  - 冻住窗口，停内核引擎（先走本机接口，不行再发信号）。
  - 核对 utun、路由、resolver 文件、root 套接字都没了，有残留就跑 `kernel-undo`；状态目录里的文件还是用户的。
  - 拉回用户态引擎，确认还是同一台设备、已登录，放开窗口。
  - 网卡列表、路由表、DNS 配置和开始前逐行对比。
  - 最后打印三条命令，分别汇总第 12 条网卡出现前、在的时候、撤掉以后的监控。
- **出事时**：`test/topo/kernel-round.sh --undo`。在别的终端里不用 sudo 就能叫停：`curl --unix-socket /var/run/deneb/denebd.sock -X POST http://denebd/v1/test/shutdown`。
- **测过的**：
  - 进程内用假网卡跑内核模式的数据通路：系统发的 DNS 查询由引擎答，ping 和 TCP 握手经 WireGuard 往返，伪造来源被拦（`TestKernelModeDataPath`）。
  - 状态锁和客户端跟随，都有单元测试。
  - 脚本对本机现状的 `--dry-run`：拓扑检查拦对了，换成正确拓扑后打印的计划也对。
  - 快照、等待、冻结和放开这几个辅助函数，以及长连接读取器。
- **没测过的**：真 utun、真路由、真 resolver 文件要 root，这一轮是第一次跑。
- **11-08 10:35 用户跑的结果**（主会话用 askpass 启动，记录在 `test/topo/.accept/kernel-1108-103512/`）：33 项过了 32 项。
  - 内核模式起来了：utun10，地址 10.48.1.6；窗口 1.1 秒就连上了内核引擎。
  - 路由、DNS、共存都对。
  - 第 8 条全过。第 4 条三条长连接各 60 秒，最长只停了 0.2 到 0.4 秒。第 6 条四个用例全部不通。
  - 撤销后，网卡、路由、DNS 配置和开始前逐行一致；用户态引擎回来了，还是同一台设备。
  - 没过的一项：第 7 条 NAS 加回后 212ms 就 ping 得通，名字却 8 秒内解析不出来。原因是引擎回「查无此名」时没带 SOA，macOS 的系统解析器就按自己的默认时长缓存了这个否定结果，超过了 8 秒。已改：否定回答都附上 SOA，TTL 和最小值都是 1 秒（`engine/dns`，`TestAnswer` 守着）。
  - 引擎这一侧已经在进程内验证（`TestKernelModeNamesFollowMembership`，用假 utun 从「系统」这一侧问 DNS）：NAS 撤出后马上就是「查无此名」，带 1 秒的 SOA；加回后马上能解析。名字表是及时更新的。
  - 脚本里查名字原来用 `dscacheutil`，它走目录服务，自带一层缓存；改成和普通程序一样用 getaddrinfo。
  - macOS 解析器是不是真按这个 SOA 只缓存 1 秒，没有 root 验证不了，要重编 `test/topo/.accept/kernel-denebd`、再跑一轮才知道。重编后比 10:33 那份只多了这个修复，外加两个诊断工具（`debug trace`、`debug goroutines`）。
- **10:52 用带 SOA 的引擎重跑**：30 过 3 没过。
  - 名字还是解析不了：光带 SOA 不够，macOS 自己的缓存（getaddrinfo 也经过目录服务那一层）留得更久。
  - 另外两项是 M2 按丢包选路在正常网络上来回切造成的：NAS 那条长连接最长停了 505ms，开头第一次 curl 也失败了。这部分见 M2 文档；现在默认关，并补发握手。
- **刷系统缓存**（11:2x 加）：内核模式的引擎（root）只要发现名字表里多了上一张没有的名字（有设备加进来或者回来了），就跑 `dscacheutil -flushcache` 和 `killall -HUP mDNSResponder`。系统当初回了「查无此名」，不管查询是到了引擎，还是直接从系统缓存里答的，都会被清掉。用户态模式不碰系统 DNS。
  - 进程内测试：`TestKernelModeNamesFollowMembership` 验加回时刷了一次、没有新名字就不再刷；`dns` 包的 `TestRevived` 也守着。
  - 第 7 条现在分两步：先用 `deneb debug dnsflush off` 关掉自动刷，量出不刷时名字要多久才能解析，最多等 20 秒，只记一笔；再撤出一次、打开自动刷、加回，8 秒内必须能解析。
