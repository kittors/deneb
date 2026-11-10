# M4 · 控制服务器线（m4-control）方案和状态

> 负责目录：`proto/`、`control/`、`cmd/deneb-control/`、`deploy/control/`；中转站程序如果归我，再加 `cmd/deneb-relay/`、`deploy/relay/`（§6）。依据：实现计划 §3 M4、§2.6.3；定价文档 §2.3（丙：公共的兜底，自己的拖进来）、§2.4（中转站能看到什么）；DESIGN 场景 2、§6.4；multi-lan-tech §3.6。
> 最后更新：2026-11-08。M1–M3 见 [`../m1/control.md`](../m1/control.md)、[`../m2/control.md`](../m2/control.md)、[`../m3/control.md`](../m3/control.md)。

## 进度

| 项 | 状态 |
| --- | --- |
| 方案（本文） | ✅ 初稿，标 ❓ 的要主会话定 |
| 中转站程序由谁做 | ✅ 引擎线同意：控制线做 `cmd/deneb-relay`、`deploy/relay/`，引擎线做设备这一侧（design/plan/m4/engine.md） |
| proto | ✅ 按 §8 加了（只加不改），引擎线、界面线都同意了名字 |
| 服务器端 | ✅ 登记、拖进拖出、列表排序、准入、同步、用量、视图、管理命令，测试见 §11；没部署 |
| 中转站程序 `cmd/deneb-relay` | ✅ 写好、测过，镜像在本机 Docker 里跑通（§12）；引擎线的假拓扑 own-relay 一项用它跑通（拖进后改用、挡 UDP 后经它通、停掉改走公共、重启后换回）；被拒原因在假拓扑里还没测，造法已告诉引擎线；没部署 |
| 不填表单的配置流程（§13）和 Web 接口（§14） | ✅ 方案写好；服务器的加入码、建站、按地区取名、安装命令和 deneb-relay `--join` 做完、测过；Web 接口（`/api/v1`）定了，等 Web 线开工再做；没部署 |
| 管理中转站：负载、权重、权限、踢人（§16） | ✅ 方案写好；服务器和 deneb-relay 做完、测过；"用户无感"和引擎线对上了；没部署 |
| 验收用的两个 Deneb 中转站（hk-155、hk-spare） | 到时候先问主会话 |

## 1. 中转站的三种来源

| 来源 | 谁登记 | 谁能用 | 流量算谁的 |
| --- | --- | --- | --- |
| **公共**（`public`） | 我们，用管理套接字 | 所有登录着、登录没过期的设备 | 我们；每条连接限速，卡片"更多"里写明 |
| **我的服务器**（`server`） | 主人在客户端里新建，在自己的服务器上起 Deneb 的中转站 | 主人把它拖进去的局域网；主人分享出去的那一条 | 主人；不限速 |
| **家里的设备**（`home`） | 主人把自己的一台设备拖进来当中转站 | 同上 | 主人 |

- hk-spare 上现成的 derper（`hk`，种子里的"我的服务器"，主人 kittors，也是部署的默认中转站）不动：它没有准入，照旧当兜底，等 Deneb 自己的公共中转站上线时再定换不换。
- **家里的设备当中转站**要引擎替别人转发（类似 Tailscale 的 peer relay），工作量大。11-09 用户问清楚后定：挪到公测以后再定，因为它要别的设备连得到它（公网 IPv4 加自动开端口，或公网 IPv6 且光猫放行），国内很多家庭做不到；登记、准入按同一套规则留好位置。

## 2. 登记和归属

不填表单（§13）：主人点"用我自己的服务器"，拿到一行安装命令，在自己的服务器上跑一下，中转站自己报上地址、端口、地区、证书指纹，管理端列表里就多出"我的服务器（香港）· 刚上线"。

- **加入码**（`relay.join`）：一次性、1 小时内有效、绑定到这个人；里面带着控制服务器地址和指纹，所以中转站只要这一个参数。用掉、过期、撤销（`relay.revokeJoin`）都作废。码在服务器上只存哈希，只显示这一次。
- 中转站用码登记（`/relay/v1/register`）时才建记录：来源"我的服务器"，主人是码的主人，名字取它报的地区（`香港`），报不出地区就用它的地址，主人以后想改再改（`relay.rename`，不是必填）。登记换来一个长期密钥，存在中转站的数据目录里。
- **重装**：数据目录还在，重启就行，不用码；数据丢了（换机器、重装系统），在卡片上点"重新安装"（`relay.join {relay}`），拿到绑定这个中转站的码，跑新命令；还是同一个中转站，拖进的局域网和用量都在，旧密钥随即失效。
- 主人能删除（`relay.delete`，从所有局域网里一起拿掉）。每人最多 10 个中转站，最多 5 个没用的加入码。
- **公共的**：部署的人走同一套：`deneb-control relay-add [-name 东京] [-rate 2MB/s]` 打出一行安装命令；`relay-token -relay r_…` 打出重装用的命令，`relay-rate` 改限速，`relays` 列出全部。
- 兼容：`relay.create {name}`（先建记录再给码）和 `relay.token`（=重装码）还在，引擎线的假拓扑在用；码一样是 1 小时、一次性。
- 记录：id（`r_…`）、名字（地点）、来源、主人、TLS 名、IPv4/IPv6、DERP 端口、STUN 端口、**证书指纹**（自签证书，引擎按指纹认，和控制服务器一样）、**每条连接限速**（字节/秒，0 不限，只有公共的设）、密钥的哈希、登记时间。加入码另放一张表。在线与否不进数据库：中转站每分钟同步一次，3 分钟没同步算离线。

## 3. 拖进局域网

- `relay.add {lan, relay}`：场景 2 把中转站拖到两台连不上的设备之间，或拖进区域。**中转站对整个局域网生效**，画面只是把它画在需要它的那条线上（DESIGN §6.4）。只有主人能拖，而且只能拖进他自己在的局域网（定价文档 §2.3）。
- `relay.remove {lan, relay}`：拖回托盘。主人和局域网的管理者（拥有者、管理员）都能拿掉：中转站的主人能看到谁经它连、用了多少，局域网有权不要。撤销就是再 `relay.add`。
- 主人离开或被移出局域网时，他的中转站从这个局域网里一起拿掉；删局域网时一起删（`lan_relays` 已经级联）。
- 公共的不用拖：永远在每个局域网的兜底里。❓ 要不要允许把某个公共的"拖到前面"（比如选东京不选香港），到场景 2 的画法定了再说。

## 4. 每个范围的中转站列表，设备连哪几个

- **局域网的列表** = 拖进来的（按拖进来的顺序）+ 兜底。**兜底** = 部署的默认中转站（种子里排了位置的，现在是 `hk`）+ 其余公共的。没有公共的也没有默认的时只有拖进来的。
- **分享的列表** = 分享方自己的 + 兜底（M3 是"自己的，没有才公共的"，改成两样都给，自己的连不上能改走公共的）。
- **准入带来的新问题**：设备 X 在局域网 A（拖进了 kittors 的 R1）和局域网 B（只有公共的 P）里。如果 X 只连 R1，B 里的 Y 没法经 R1 找它（R1 不放 Y 进）。所以：
  - **引擎**：对自己所在的每个范围，连上列表里第一个连得通的（自带的在前，所以自带的优先；连不上就是公共的，胶囊写"改走公共中转站"，不出现"断开"）。几个范围选中同一个就只连一次。上报时在 `Report.Relays` 里报**现在连着的全部**，常驻的（`HomeRelay`）排第一。
  - **服务器**：给每个看的人单独算对端走哪：`Peer.Relays` = 对端报的那些里、落在双方共同（没暂停的）范围的列表里的，按对端的顺序；`Peer.HomeRelay` 是其中第一个。这样经过的中转站一定放双方进去。老引擎只报 `HomeRelay`，就只用它，它不在共同范围里（又不是公共的）就不给。
- `NetMap.Relays` 是这台设备各范围列表里的全部中转站，每个带指纹和限速。

## 5. 准入（§2.6.3）

中转站每接进一个客户端，拿到它的节点公钥后问一次控制服务器：

- 这把公钥属于一台登录着、登录没过期的设备，否则拒绝（公共的也拒绝"没登录 Deneb 的"）。
- **公共的**：放行。
- **我的服务器、家里的设备**：这台设备所在的某个范围（局域网，或生效中的分享）的列表里有它，才放行。按 §4 的列表，这正是"主人拖进去的局域网"和"主人分享出去的那一条"，外加部署把它设成默认的情况。
- 局域网里拿掉中转站、分享收回、设备登出或过期后，新连接被拒；已经连着的，中转站每分钟同步时一起复查，不该再用的断掉。
- **控制服务器连不上时**：公共的放行（宁可多服务，也不让人连不上）；自带的拒绝（主人的流量不能被别人用）。代价是控制服务器宕机时连不进自带的新连接，已经连着的不受影响（计划 §3 M4 风险）。

## 6. 中转站程序（Deneb 自己的中转站镜像）——提议控制线来做

查过 Tailscale v1.104.5 的 `derp/derpserver`（go.mod 里已有），要的东西大多现成：

- **准入**：`SetVerifyClientURL` 每接进一个客户端就 POST `{"NodePublic","Source"}`，要 200 和 `{"Allow":true}`；`SetVerifyClientURLFailOpen` 分来源设（公共的放行，自带的拒绝）。它用的是 `http.DefaultClient`，中转站程序把它的 Transport 换成钉控制服务器指纹、带自己密钥的那个就行。
- **限速**：`UpdateRateLimits(RateConfig{PerClientRateLimitBytesPerSec, …})` 正是按连接限速，数值从控制服务器同步。
- **复查和计量**：把 `/derp` 的 Hijack 包一层，拿到每条连接，按字节计数，需要时关掉；`Accept` 的 ctx 一路传到准入请求，靠 ctx 里的值把节点公钥对上连接。
- STUN 用 `net/stunserver`；证书自签，指纹登记时报上去；镜像 `FROM scratch`，部署脚本照 `deploy/control/` 的写法；高位端口。

所以中转站程序是 derpserver 外面一层薄壳，几乎全是和控制服务器打交道（登记、准入、同步、用量）。引擎线同意：**控制线做** `cmd/deneb-relay` 和 `deploy/relay/`；**引擎线做引擎这边**：按指纹认中转站（`Relay.Pin`）、§4 的连法和上报、"改走公共中转站"，以后家里的设备转发；假拓扑用 deneb-relay 起两台带准入的中转站。

## 7. 控制服务器给中转站的接口（`/relay/v1`）

除登记外都带 `Authorization: Bearer <密钥>`（TLS 钉指纹，密钥在服务器上只存哈希）：

| 接口 | 作用 |
| --- | --- |
| `POST /relay/v1/register` | 带登记口令：报 TLS 名、地址（不报就用请求的来源地址）、端口、指纹，换长期密钥；返回 id、名字、来源、限速 |
| `POST /relay/v1/admit` | derpserver 的格式：`{"NodePublic":"nodekey:…","Source":"1.2.3.4"}` → `{"Allow":bool}`。拒绝也回 200 |
| `POST /relay/v1/sync` | 每分钟一次：带上现在连着的公钥和上次同步以来每把公钥的收发字节，回不该再连的公钥和最新限速；顺便算在线 |

## 8. proto（只加不改）

- `Relay`：加 `Pin`、`RateLimit`。
- `Report`：加 `Relays []RelayID`（现在连着的，常驻的排第一）；`Peer`：加 `Relays []RelayID`（§4）。
- `UserView.Relays`：除了我的局域网和分享用的，再加我自己的；`UserView.MyRelays []RelayView`：我名下的，含拖进了哪些局域网、登记过没有、在不在线、这个月的用量（按局域网、按分享合计）。卡片上的"来源、能看到什么、用量"由界面按 `Source`、`Owner`、`RateLimit` 和这里拼（"内容全程加密，Mia 看不到；Mia 能看到谁在经它连、用了多少"）。
- 操作：`relay.create {name}` → `{relay, relayToken}`、`relay.token {relay}` → `{relayToken}`、`relay.rename {relay, name}`、`relay.delete {relay}`、`relay.add {lan, relay}`、`relay.remove {lan, relay}`；`OpArgs.Relay`，`OpOutput.Relay`、`OpOutput.RelayToken`。
- `proto/relay.go`：`/relay/v1` 的请求、应答类型。`Features` 加 `relays`。
- 设备能力：`LoginRequest.Caps`、`Node.Caps`、常量 `CapHandshakeDedupe`（§18）。

## 9. 用量

- 表 `relay_usage`：中转站、设备、日期（UTC）、收、发。只按设备记总量，不记谁和谁（定价文档 §2.3、§2.4）。中转站报的是节点公钥，服务器收到时换成设备；对不上的丢掉，记日志。
- "这个月替周末联机转了多少"：这个月里，局域网里每台设备经这个中转站**发出**的字节相加（只算发出，一份流量不算两遍），只算列表里有这个中转站的局域网；分享同样按分享合计。一台设备在两个这样的局域网里时两边都算，界面写"约"。
- 公共的用量只给我们看（限速和排障），不进用户视图。

## 10. 测试

- 准入的属性测试：随机世界里加中转站（公共的、我的服务器、默认的）、拖进的局域网、分享、登出和过期的设备；每台设备 × 每个中转站，准入要和"公共的，或在它某个范围的列表里"一致；网络图里每个对端的 `Relays`，双方都要被放进。
- 列表：拖进来的在前、兜底在后；分享的是分享方的 + 兜底；主人离开局域网后他的中转站拿掉了。
- 口令只能用一次、过期不能用；密钥换了旧的失效；接口不带密钥一律 401。
- 同步：复查拒掉该拒的；用量按设备、按天累计，按局域网、按分享合计。

## 11. 已做的（2026-11-08，没部署）

- **proto**：`Relay.Pin`、`Relay.RateLimit`；`Peer.Relays`；`Report.Relays`；`UserView.MyRelays`（`RelayView`：拖进了哪些局域网、登记过没有、口令到期时间、在线、离线时的最后报到、`Seeded`）；`LANView.AddedRelays`；`RelayUsage`（`GET /device/v1/relay-usage`，卡片打开时取，不推）；六个 `relay.*` 操作，`OpArgs.Relay`、`OpOutput.Relay`、`OpOutput.RelayToken`；`proto/relay.go`（`/relay/v1/register`、`admit`、`sync`）；`FeatureRelays`、`M4Features`。
- **数据库**：迁移 5：`relays` 加 `pin`、`rate_limit`、口令和密钥的哈希、`created_at`；`devices.relays`；`relay_usage`（中转站、设备、UTC 日期、发、收；删中转站一起删）。
- **列表**：兜底 = 默认的（按位置）+ 其余公共的；局域网 = 拖进来的（已登记的）+ 兜底；分享 = 分享方自己的（不算默认的）+ 兜底。没登记的拖进来也记着，只是不上列表。
- **网络图**：`Relays` = 兜底 + 各范围列表；`Peer.Relays` 按看的人算（§4），`HomeRelay` 是第一个；中转站的 `Owner` 只给看得见这个人的设备，默认中转站的主人不外露（视图同样）。
- **准入**：登录着、没过期的设备；公共的都放；自带的只放它在其列表上的设备。`/relay/v1/admit` 用 derpserver 的格式，拒绝也回 200；`sync` 复查已连接的、记用量、回最新名字和限速；中转站 3 分钟没同步算离线，上线、离线各发一个新版本。
- **操作**：`relay.create`（每人最多 10 个，口令 7 天）、`relay.token`、`relay.rename`、`relay.delete`、`relay.add`（主人，只能拖进自己在的局域网）、`relay.remove`（主人或局域网拥有者）；成员离开或被移出时他的中转站一起拿掉；种子里的中转站不能改名、删、重发口令；公共的不用拖。
- **管理命令**：`deneb-control relays`、`relay-add -name 东京 -rate 2MB/s`、`relay-token -relay r_…`、`relay-rate -relay r_… -rate 1MB/s`（只有公共的限速）。
- **测试**：随机世界属性测试按 §10 独立重算列表、`Peer.Relays`、准入（每台设备 × 每个中转站；对端的每个中转站双方都放进），并要求覆盖到被过滤的对端中转站、没登记的、被拒的；`TestRelays` 走完"我的服务器"的一生（新建、拖进、登记、准入、拿出、同步拒掉、分享、用量、上线离线、改名、换机器重装、离开局域网、删除、上限）；`TestAdminRelays` 走公共的（新建、登记、没进局域网也能用、登出被拒、限速改了网络图和同步都跟着变）。`go test -race ./control ./proto ./cmd/deneb-control` 2966 个全过。
- **部署前要做**：拿 hk-155 的数据备份跑一遍 `TestProdCopy`（迁移 5），和 M3 一样。

## 12. 中转站程序（2026-11-08，没部署）

- `cmd/deneb-relay`：derpserver + stunserver。第一次启动用登记口令（`-token` 或 `DENEB_RELAY_TOKEN`）换密钥，和自签证书（ECDSA，十年）、DERP 密钥一起存在 `-data`（0600）；之后启动不要口令，每次启动后的第一次同步带上地址、端口、指纹（`RelayInfo`），换了机器或证书网络图会跟着变。密钥失效（中转站被删、在别处重新登记）时，有新口令就重新登记，没有就退出并说明。
- `/derp` 自己处理（照 derpserver.Handler 的写法）：包住连接计字节、能关；把连接放进 Accept 的 context，准入请求经 `http.DefaultClient` 出去时由自己的 Transport 带上密钥、走钉指纹的连接，并记下节点公钥。被拒时先发一个 DERP 健康帧 `deneb-refused:<原因>` 再关（握手阶段只有 Accept 一个 goroutine 在写，安全）；控制服务器连不上时公共的放行、自带的拒绝（`SetVerifyClientURLFailOpen`）。
- 每分钟同步：报连着的公钥和每把公钥的收发增量（报成功才记账，失败下次一起报），关掉服务器说不该再用的，限速变了就 `UpdateRateLimits`。
- **钉指纹的坑（已告诉引擎线）**：Tailscale 的 `tlsdial.Config` 遇到 `InsecureSkipVerify` 或 `VerifyConnection` 会 panic，所以 `proto.PinnedTLSConfig` 不能直接给 `derphttp.Client.TLSConfig`。做法：URL 写 `http://host:port/derp`，用 `SetURLDialer` 在拨号里自己做钉指纹的 TLS，derphttp 就在这条 TLS 连接上说 HTTP。测试里的 `pinnedDERP` 就是这么写的。
- `deploy/relay/`：`build.sh`、`Dockerfile`（scratch，nobody，`-healthcheck -data /data` 从数据目录读端口）、`deploy.sh`（`HOST`、`IPV4`、第一次给 `TOKEN`；先用 `ss` 只读检查两个端口空闲；host 网络，1 CPU、256 MB；保留上一个镜像、日志和数据备份）。默认端口 DERP 47445/tcp、STUN 47478/udp，到时候在 hk-155、hk-spare 上先确认空闲。
- 测试：`TestOwnRelay`（真 derphttp 客户端经钉指纹的 TLS 连进来：工作室的设备进得来、别人的被拒并收到原因、指纹不对连不上、包能转、用量按设备记且不重复记、拿出局域网后同步关掉连接、换地址重启不用口令、删除后同步报密钥失效）、`TestPublicRelay`（公共的：不在局域网也能进、登出被拒、限速跟着服务器变）、`TestFirstStartNeedsToken`。`control/controltest` 是给这些测试用的开发版控制服务器和假设备。本机 Docker 里控制服务器、中转站两个容器跑通：登记、限速、健康检查、重启不重新登记。

## 13. 不填表单的配置流程（主会话 2026-11-08 转来用户的要求）

1. **公共中转站**：默认就有，自动兜底，不用配（§4 的兜底）。自己部署的版本，部署的人用 `deneb-control relay-add` 拿一行命令，在服务器上跑。
2. **用自己的服务器当中转站**：管理端（以后桌面端一样）点"用我自己的服务器" → `relay.join` → 显示一行命令，例如：
   `docker run -d --name deneb-relay --restart unless-stopped --network host -v deneb-relay:/data deneb-relay:v0 --join deneb-join-… --region auto`
   - 命令由服务器拼好，界面只管显示和复制。镜像名是部署设置（`deneb-control -relay-image`）：现在还没有公开的镜像仓库，自己的部署先在服务器上用 `deploy/relay/build.sh` 构建同名镜像；以后推到仓库后改成仓库地址。
   - `curl | sh` 的安装脚本（不用 Docker、装成 systemd 服务）要先定程序从哪里下载（公开仓库，或控制服务器自己提供下载）；curl 认自签证书可以用 `--pinnedpubkey sha256//<指纹的 base64>`。这一步先不做，记为后续。
   - 中转站拿码登记：地址（不报就用来源地址）、端口、证书指纹、**地区**都由它自己报。地区按顺序取：`-region <地名>` → 有 `-region auto`（安装命令里带着）时问云服务器的元数据（阿里云、腾讯云、AWS、GCP、Azure，一共最多 0.5 秒）→ 环境变量 `TZ`。服务器把这些线索翻成地名（`cn-hongkong`、`ap-east-1`、`Asia/Hong_Kong` 都是"香港"，表在 `control/relayregion.go`）；都没有就用地址当名字。元数据只在 `auto` 时问：阿里云的地址 100.100.100.200 落在 Tailscale 这类 VPN 用的网段里，开发机上手动跑的中转站不该往外发这种请求。按 IP 查地区更通用，但要下载一份 IP 库，得先问用户，记为后续。
   - 登记成功：世界变了，管理端马上收到新的视图：`MyRelays` 多一项（`since` = 登记时间，`online`），界面写"我的服务器（香港）· 刚上线"；这张码从 `RelayJoins` 里消失。
3. **给哪些局域网用**：拖到局域网上 `relay.add {lan, relay}`，拖走 `relay.remove {lan, relay}`，撤销就是反过来再做一次。网络图里要的字段都在：`Scope.Relays`（顺序：拖进来的在前、兜底在后）、`Relay.Pin`、`Relay.RateLimit`、`Peer.Relays`。
4. **卡片上的状态**：在不在线（`RelayView.online`，离线时 `lastSeen`）；这个月替每个局域网转了多少（`GET relay-usage` 的 `lans`，只按设备记总量，不记谁和谁）；"内容全程加密，中转站看不到内容"（界面固定文案）；公共中转站每条连接限速（`Relay.rateLimit`）。别人的中转站，局域网成员看到来源、主人、限速；在不在线以引擎实际用的为准（本机接口 `scopes[].relay`，不一样就写"改走公共中转站"）。
5. **安全**：
   - 码泄露：1 小时过期、只能用一次、管理端随时撤销（`relay.revokeJoin`）；只存哈希。拿到别人的码只能把一台服务器登记成"他的中转站"，不会自动进任何局域网：新中转站会以"刚上线"和地址出现在主人的列表里，不认识就删掉。重装码绑定的是已有的中转站，泄露的后果更大（别人能顶替它），所以同样 1 小时、一次性、能撤销，而且一用掉，主人那台的旧密钥就失效、卡片会显示离线，主人能察觉。
   - 中转站下线：3 分钟没同步算离线，卡片显示离线和最后报到时间；设备自动改走公共的。重新上线不用做任何事。
   - 重装：数据目录在就直接重启；丢了就用重装码。中转站被删除后它的密钥失效，程序报错退出，说明要重新拿码。

## 14. Web 管理端要用的接口（给 Web 线；2026-11-08 已做好，本机测过，没部署）

Web 管理端是浏览器里的程序，不经引擎，直接连控制服务器的 `/api/v1`。数据格式和设备接口一样（`proto` 里的类型，路径常量在 `proto/web.go`），差别只在登录方式和"哪台设备"：

- **登录**（邀请卡里一步一屏）：`POST /api/v1/login/email {email}` → `{step: "code", account: "登录到阿杰（jie@deneb.test）"}`（这个部署不发邮件，验证码是测试账号的）；`POST /api/v1/login/code {email, code}` → `{step: "done", account, person}`，同时发会话 cookie `deneb_session`（HttpOnly、SameSite=Lax、HTTPS 时 Secure，30 天，服务器只存哈希）。`POST /api/v1/login` 两步合一（有没有 code）也行。邮箱没开通 → 404；没填验证码、验证码不对 → 400；同一个邮箱错 5 次 → 429，15 分钟后再试。`POST /api/v1/logout` 注销。没登录的请求一律 401（`ErrorBody`，`code: unauthorized`）。
- **同源**：Web 程序要和控制服务器同源（由控制服务器或同一个反向代理提供；开发时用开发服务器的代理），cookie 才带得上。控制服务器现在用自签证书，浏览器会先提示证书不受信任；对普通人要换成有域名的正式证书，部署时再定。
- **防跨站**：所有 POST 必须 `Content-Type: application/json`（否则 415），并过 CrossOriginProtection（`Sec-Fetch-Site`/`Origin` 是别的站就 403）。
- `GET /api/v1/userview` → `proto.UserView`（这个人的视图，同设备拿到的一样）。
- `GET /api/v1/stream` → NDJSON，每行一个 `proto.StreamFrame`：先是当前的 `userView`，之后有变化才推，15 秒一个 `keepAlive`；没有网络图。
- `POST /api/v1/ops` → `proto.Op` → `proto.OpResult`；操作和设备接口完全一样，以"这个人"的身份做，同一个 op id 重发拿到第一次的结果。在网页里建的局域网里没有设备，网页里接受邀请也不带设备进来。
- `GET /api/v1/relay-usage` → `proto.RelayUsage`。
- 邀请链接的预览，不登录也能看（邀请落地页用）：`GET /api/v1/invites/<token>` → `proto.InvitePreview`（谁邀请、哪个局域网、过期 / 已经在里面 / 位置满了…，还有 `members`：已加入的人、不带邮箱，`devices`：在用的设备数；这两项只在链接还能用或问的人已在里面时给）；`POST /api/v1/invite {invite: <链接或 token>}` 一样。登录后接受：`POST /api/v1/ops {op: "invite.accept", args: {invite: <token 或整条链接>}}`；点名给某人的邀请用 `args.invitation: <邀请 id>`。网页里接受不带设备。
- 中转站相关就是这几样：
  - "用我自己的服务器"：`relay.join {}` → `result.relayJoin = {id, code, command, expiresAt}`，显示 `command`。
  - "重新安装"：`relay.join {relay}`，同上。
  - 等待安装的码：`UserView.relayJoins[]`（`id`、`relay`、`createdAt`、`expiresAt`，不含码本身）；撤销 `relay.revokeJoin {join}`。
  - 列表和卡片：`UserView.myRelays[]`、`UserView.relays[]`（名字、来源、主人、限速）、`relay-usage`；改名 `relay.rename`，删除 `relay.delete`；拖进拖出 `relay.add` / `relay.remove`；权重、容量、踢人见 §16。
- 部署管理员的身份和运营页的接口：见 §19（2026-11-09 做好，本机测过，没部署）。

## 15. 加入码做了的（2026-11-08，没部署）

- **proto**：`relay.join {relay?, name?}` → `result.relayJoin {id, code, command, relay, expiresAt}`；`relay.revokeJoin {join}`；`UserView.relayJoins[]`（没用的码，不含码本身）；`RelayView.since`（第一次登记的时间，"刚上线"）；`RelayInfo.region`（线索）；`proto.MakeRelayJoinCode`、`ParseRelayJoinCode`（`deneb-join-` + base64url 的 `{c: 控制服务器, p: 指纹, t: 口令}`）。
- **服务器**：迁移 6 把码挪进 `relay_joins` 表（一张码能代表还没建的中转站），`relays` 去掉口令列、加 `registered_at`。码 1 小时、一次性、只存哈希；新中转站的码每人最多 5 张；"重新安装"的码每个中转站一张，新的顶掉旧的；过期的由 30 秒一次的清扫删掉并推新视图。用码登记时才建记录（名字：给的 → 地区 → 地址）；重装码登记的是原来那个，局域网、用量、`since` 都不变，旧密钥失效。`relay.create`、`relay.token` 也改成发这种码（1 小时）。安装命令由服务器拼：镜像名 `-relay-image`（默认 `deneb-relay:v0`），码里带 `PublicURL` 和服务器自己的证书指纹（`deneb-control` 先读证书再起服务器）。
- **管理命令**：`deneb-control relay-add [-name] [-rate]` 打出公共中转站的一行安装命令（中转站登记后才出现在 `relays` 里），`relay-token -relay` 打出重装命令。
- **deneb-relay**：`--join <码>`（或 `DENEB_RELAY_JOIN`）一个参数就够：控制服务器、指纹、口令都从码里取；`-region` 见 §13。数据目录在时重启不再登记，码可以一直留在命令里。
- **部署脚本**：`deploy/relay/deploy.sh` 收 `JOIN`（或 `TOKEN`）和 `REGION`（默认 香港，不问元数据）。
- **测试**：`TestRelayJoin`（码和命令的样子、视图里等待安装、用码建站并按地区取名、码只能用一次、没地区用地址、给了名字用名字、重装保留局域网且旧密钥失效、重装码新的顶掉旧的、撤销、一小时后失效并被清扫、最多 5 张）、`TestRegionName`、deneb-relay 的 `TestJoin`（只给码就能登记）、`TestDetectRegion`。`go test -race ./proto ./control/... ./cmd/deneb-control ./cmd/deneb-relay` 全过。

## 16. 管理中转站：负载、权重、权限、踢人（主会话 2026-11-08 转来用户的要求）

"管理员"指中转站的主人（"我的服务器"）；公共中转站的管理员是部署的人，用管理套接字做同样的事。

1. **负载一眼看清**：中转站每分钟同步时带上健康情况（`RelaySyncRequest.health`）：连接数、当前带宽（这一分钟的收发合计除以秒数）、CPU（占它能用的 CPU 的比例，容器限了 1 核就按 1 核算）、内存（用了多少、上限多少）、运行了多久。服务器再按同步带来的公钥算出现在有几台设备、几个人在用。打开卡片时取 `relay-usage`，每个中转站多了：`load`（以上数字和"满载"）、`connected`（现在谁在用：我看得见的设备，看不见的只写人）、`kicks`（被踢的）。这个月的流量照旧在 `lans`、`shares`、`devices` 里。仍然只记"谁在用、用了多少"，不记"谁和谁"。
2. **立刻生效**：中转站另开一个长轮询 `GET /relay/v1/watch?since=<版本>`，服务器一有变化（踢人、拿出局域网、设备登出……）就返回，中转站马上同步一次（两次之间至少隔 1 秒），不用等满一分钟。
3. **权重**：一个局域网用几个中转站时，**服务器给每台设备单独排列表**：拖进来的那几个，按权重用"加权一致性哈希"（rendezvous）给每台设备挑一个排第一，3:1 的权重大约就是 3/4 的设备常驻在前一个。引擎照旧"连列表里第一个连得通的"，不用改。权重 0 是备用：只在别的都不行时才用。离线（3 分钟没同步）或满载的排到这一层最后，自动少分、不分。兜底那一层（默认的 + 公共的）也一样：默认的按部署的顺序在前，公共的按权重分。一台设备的结果只随权重、拖进拖出、健康变化而变，不会来回跳。
   - **满载**：主人可以设容量（同时最多几台设备，`relay.setCapacity`，0 不限）；到了容量，或 CPU 到 90%，就算满载；降到容量的 90% 以下、CPU 75% 以下才恢复，免得来回抖。
   - 调整：`relay.setWeight {relay, weight}`（0–100，默认 1）、`relay.setCapacity {relay, capacity}`；公共的用 `deneb-control relay-weight`、`relay-capacity`。
   - 控制服务器刚重启时，还没收到同步的中转站先当作在线，3 分钟内没同步才算离线，免得一重启所有设备都换中转站。
4. **权限**：谁能用由三样定：按局域网（拖进拖出），按分享（主人分享出去的设备自动能用），按人、按设备（踢人和撤销）。默认就是主人的局域网和主人分享出去的设备，准入规则不变，只是再减去被踢的。
5. **随时踢人**：`relay.kick {relay, person | device}`：立刻生效：服务器把这个中转站从被踢设备的网络图里拿掉（踢人就是他的所有设备），中转站经 watch 马上同步、断开它的连接，以后再连就拒绝，原因 `deneb-refused:kicked`；引擎换下一个中转站。`relay.unkick {kick}` 撤销，恢复准入。每次踢人都记下：踢的是谁（人或设备）、何时、谁踢的；撤销也记下何时、谁撤的。卡片上列出生效中的和最近 30 天撤销的。
6. **用户无感**：用户从来不用选中转站。引擎先试直连；只有直连不通，或者直连明显比中转差时，才走中转；走哪个中转站由服务器排好的列表决定（自带的在前、按权重分、坏了自动换）。和引擎线对过，现在就是这样（design/plan/m4/engine.md 有细节）：
   - **走中转**：还没有直连（刚连上、换网、睡醒）时先经中转通，同时探直连，通了就换；直连没回应时，有流量 1 秒后两路都发、3 秒后才显示改走中转（没流量是 2.5 秒、6.5 秒）；直连明显更差也换：8 秒里丢包到 15% 以上（引擎 m2-11090006 起，原来是 20%）而中转确实干净，或者直连往返持续 5 秒超过"中转往返 × 2 + 50 毫秒"。两边都不好就留在直连。
   - **回直连**：没回应造成的，一回答就回；因丢包换的，至少待 20 秒、直连 10 秒里丢包不到 2% 就回（中转自己变差也回）；因延迟换的，直连往返低于"中转往返 × 1.5 + 30 毫秒"就回。
   - 权重排序、`kicked`、中转站断得快，引擎都不用改：`kicked` 走现有的被拒处理（记下原因、换下一个、退避）。

接口（Web 线做管理端时直接用，经 `/api/v1`，同设备接口一样）：

| 做什么 | 接口 |
| --- | --- |
| 看负载、谁在用、被踢的、这个月的流量 | `GET relay-usage` → `relays[].load`、`connected`、`kicks`、`lans`… |
| 调权重、容量 | `relay.setWeight {relay, weight}`、`relay.setCapacity {relay, capacity}` |
| 踢人、撤销 | `relay.kick {relay, person}` 或 `{relay, device}` → `result.kick`；`relay.unkick {kick}` |
| 卡片上常显的 | `myRelays[].weight`、`capacity`、`full`、`online`（Deneb 中转站按同步算；不同步的旧 derper 按"有在线设备报告连着它"算） |

**做了的（2026-11-08，没部署）**：

- proto：`RelaySyncRequest.health`（`RelayHealth`：连接数、带宽、CPU、内存和上限、运行时长）；`GET /relay/v1/watch`（`RelayWatchResult`）；`relay.setWeight`、`relay.setCapacity`、`relay.kick`（`result.kick`）、`relay.unkick`，`OpArgs.weight`、`capacity`、`kick`；`RelayView.weight`、`capacity`、`full`；`RelayMonth.load`（`RelayLoad`）、`connected`（`RelayClient`）、`kicks`（`RelayKick`）；`RelayDenyKicked`。
- 服务器：迁移 7（`relays.weight`、`capacity`，`relay_kicks` 表）。每台设备的列表：自带的按权重排（加权 rendezvous），备用的在后，默认的、公共的（按权重）在后，下线的、满载的（它本来就在上面的不算）放最后；被踢的不进列表、不进网络图、准入拒绝（`kicked`），对端的 `Peer.relays` 也不给它被踢的那个。满载按容量或 CPU 算，带回差；状态变了就发新版本。服务器刚启动时，登记过的中转站先当在线。每次变化唤醒中转站的 watch。`relay-usage` 带上负载、现在谁在用（看得见的设备，看不见的只写人）、生效中和 30 天内撤销的踢人记录。管理命令：`relays` 显示负载、权重、踢人数；`relay-set -weight/-capacity`、`relay-kick -person/-device`、`relay-unkick -kick`。
- deneb-relay：每次同步带上健康情况（CPU 用 getrusage 算，按 cgroup 的 cpu.max 折算；内存读 /proc/self/statm，上限读 cgroup 的 memory.max）；一直挂着 watch，有变化就马上同步（两次至少隔 1 秒）。
- 测试：属性测试的随机世界加了权重 0–3、随机下线和满载、随机踢人（人或设备、有的撤销了），独立重算每台设备的列表顺序、准入、对端中转站，并要求覆盖"顺序因设备而不同"和"因踢人被拒"；`TestRelayWeights`（3:1 约 3/4 在前、同一台设备结果稳定、备用和下线的位置、满载的回差）、`TestRelayKick`、`TestRelayLoad`、`TestRelayWatch`、管理命令的权重、容量、踢人、撤销；deneb-relay 的 `TestKickAtOnce`（踢掉后连接立刻断、重连收到 `kicked`、负载上报到卡片）。

## 17. 验收用的两个中转站（11-09 02:02 起在跑，留着跑一天）

| | 公共（香港） | kittors 的"我的服务器" |
| --- | --- | --- |
| 机器 | hk-155 | hk-spare |
| id、名字 | `r_5uppwwtduq`，香港 | `r_lklwn3cnor`，198.51.100.4（一行命令装的，那台机器上问不到云元数据、没有 `TZ`，就用了地址；主人可以改名） |
| 地址 | `103.42.31.155:47445`（DERP over TLS），STUN `47478/udp` | `198.51.100.4:47445`，STUN `47478/udp` |
| 指纹 | `sha256:376b3976268b09abfde345aa0125de8aac534abe9ff9b42f230b3dcad189f527` | `sha256:70885bb8763b9173d06edc950374b1354e1c37b02bfbdde375d3fdcc9803a4ac` |
| 限速 | 每条连接 2 MB/s（默认值，`deneb-control relay-rate -relay r_5uppwwtduq -rate …` 改） | 不限 |
| 怎么起的 | `deneb-control relay-add -name 香港 -rate 2MB/s` 拿加入码，`HOST=hk-155 IPV4=103.42.31.155 JOIN=… deploy/relay/deploy.sh` | kittors 的 `relay.join` 给的那一行 `docker run … --join … --region auto`，原样在 hk-spare 上跑 |
| 在哪些局域网 | 每个局域网的兜底，排在旧的 `hk` 后面 | 现在哪个都不在。r2 要用时拖进工作室：kittors 的 `relay.add {lan: l_h6c4ou3xb7, relay: r_lklwn3cnor}`，拿走 `relay.remove` |

- 停、起：`ssh <机器> docker stop deneb-relay`、`ssh <机器> docker start deneb-relay`；日志 `docker logs deneb-relay`。停掉以后，连着它的设备马上改走列表里下一个；控制服务器 3 分钟没收到同步才把它标成离线，排到列表最后。两个容器都是主机网络，只占 47445/tcp、47478/udp。
- 彻底拿掉：`docker rm -f deneb-relay`（hk-spare 那个再 `docker volume rm deneb-relay`），再删登记：kittors 的用 `relay.delete`；公共的现在还没有删除的管理命令，停着就行，要删得加。
- 默认顺序（主会话 11-09 定）：公共的"香港"排兜底第一，旧的 `hk`（hk-spare 上的 derper）排它后面，当最后一道兜底，不删。
  - 做法：在种子的 `relays` 里把 `r_5uppwwtduq` 放到 `hk` 前面，再重启控制服务器（同一个程序，不换代码）。种子会重写名字、来源、地址、端口这几列，所以照它现在的登记原样写，这样实际只改了默认位置。
  - 用线上数据的副本跑过 `TestProdCopy`：每个范围都走 `[r_5uppwwtduq hk]`，设备都照常。
  - 现在的状态（11-09 02:47）：本地的 `deploy/control/seed.hk155.json` 已经改好；新种子传到了 hk-155 的 `data/seed.json.new`，还没换上，线上照旧是 hk 在前。重启那一步被这边的自动模式拦了，等用户决定。旧种子在 `/tmp/deneb-m4-acc/seed-order/seed-prev.json`。
  - 注意：默认中转站不按在线、满载重排。公共的那台停了也还排第一，引擎连不上会自己试下一个（hk）。
- 真机验收的工具：`cmd/deneb-relay/accept_test.go`（`DENEB_ACCEPT=1` 才跑，参数写在文件开头）；它登录三台测试设备、建一个测试局域网，测完删掉、登出。测速要在没有本地代理的地方跑（比如 hk-155 上），这台 Mac 走 Clash 时，发出去的和收到的数都被代理的缓冲撑大，不准。

## 18. 设备能力（Caps，引擎线 11-09 要的，本机测过，没部署）

- 用途：长时间空闲后，同一条握手消息直连和中转各发一份。只有对端在网络图里声明了 `handshake-dedupe`，才这样两路发；声明的意思是那台设备会在交给 WireGuard 之前扔掉重复的握手消息。
- proto：`LoginRequest.Caps []string`（`caps`），引擎每次启动报一遍；`Node.Caps`，对端的条目和 `Self` 都带，`Self` 上的是服务器收下的回显；常量 `proto.CapHandshakeDedupe = "handshake-dedupe"`。
- 服务器：登录时整理一遍，只留 1 到 64 个字符、由 a-z、0-9、`-`、`.` 组成的名字，排序去重，最多 32 个；格式不对的扔掉，不拒绝登录。整理后存进 `devices.caps`（迁移 9），重启不丢。能力变了算新版本，对端马上收到新网络图；同样的能力换个顺序不算变。
- 混着版本：现在部署的服务器不拒绝不认识的字段，新引擎带着 `caps` 登录不会出错，只是网络图里没有 `caps`，引擎就当对端什么都没声明，照旧只走一条路。服务器要部署这一版才会把能力抄过去；部署要主会话批准，跟别的服务器改动一起做。
- 测试：`TestCaps` 覆盖整理、`Self` 回显、老引擎的条目里没有 `caps`、同样的声明不出新版本、清掉声明出新版本、登出再登录取新的、最多 32 个、重启不丢。

## 19. 运营页：部署管理员和接口（给 Web 线；2026-11-09 做好，本机测过，没部署）

DESIGN §14.12 的 M4-13。部署管理员在运营页上管公共中转站，做法和普通人管自己的中转站一样（§16）。普通人看得到公共中转站，但只能看（DESIGN §14.8）。

**谁是部署管理员**：在服务器本机用管理命令定，没有网页表单。改了立刻生效：那个人的视图里马上多出或少掉运营页。

```
deneb-control operators                     # 列出部署管理员
deneb-control operators -add <邮箱>          # 设成部署管理员
deneb-control operators -remove <邮箱>       # 取消
```

线上要加 `docker exec deneb-control` 前缀。身份存在数据库里（迁移 10，`people.operator`），重启也还在。开发服务器（`-dev`）同样用这条命令，加 `-data <开发数据目录>` 指到它的管理套接字。

**接口**（都要登录；不是部署管理员的，查数字回 403，做操作也回 403）：

- **有没有运营页**：`UserView.operator`。只有部署管理员的视图里有，有它就在侧边栏"部署"下显示"运营"。它跟着 `/api/v1/stream` 实时推，里面两样：
  - `relays[]`：公共中转站的卡片，格式同 `myRelays[]`（`RelayView`）：`online`、`lastSeen`、`since`（"刚上线""已经运行 30 天"）、`weight`、`capacity`、`full`、`registered`、`tokenExpiresAt`。
  - `joins[]`：公共中转站还没用掉的加入码，就是井里"等待安装"的格子，格式同 `relayJoins[]`。
  - 名字、地址、端口、每条连接限速在 `UserView.relays[]` 里（部署管理员的视图包含全部公共中转站）。
- **卡片上的数字**：`GET /api/v1/operator/relay-usage` → `proto.OperatorUsage`。
  - `people`、`devices`：现在经公共中转站中转的人数、设备数，同一个人或同一台设备在几台上都只算一次，就是标题第二行"现在 128 个人、342 台设备在经它们中转"。
  - `relays[]`（`RelayMonth`）：
    - `bytes`：这个月一共转了多少，每个字节只在发送那一侧算一次。
    - `load`：现在几台设备（`devices`）、几个人（`people`）、带宽（`bytesPerSec`）、CPU、内存、`full`；格子第二行"86 个人在用"用 `load.people`。
    - `top[]`："用得最多的"，这个月收发合计最多的人，最多 5 个，从多到少，每项 `{person: {id, name, displayName, initial}, bytes}`，不带邮箱。
    - `users`：这个月用过它的总人数，"还有 81 个人"就是 `users − top 的个数`。
    - `kicks[]`："不让用的"，最近 30 天的，格式同 §16。
    - `lans`、`shares`、`devices` 留空，没有 `connected`。不按局域网分（DESIGN §14.12），也不说谁的设备、谁和谁连。
- **操作**：都走 `POST /api/v1/ops`，和管自己的中转站是同样的操作，只是对象换成公共中转站的 id：
  - 加一台："+ 加一台公共中转站"发 `relay.join {public: true}`（可带 `name`，不带就用它报的地点）。返回 `result.relayJoin = {id, code, command, expiresAt}`，显示 `command`：1 小时内有效，只能用一次。码在 `operator.joins[]` 里，撤销用 `relay.revokeJoin {join}`。命令跑完，新的一台出现在 `operator.relays[]` 里，每条连接限速 2 MB/s，权重 0，也就是先当备用。
  - 分配条和"多用 / 少用 / 只当备用"：`relay.setWeight {relay, weight}`，权重 0 到 100，0 就是只当备用。同时最多几台设备：`relay.setCapacity {relay, capacity}`。改名：`relay.rename`。
  - 换机器重装：`relay.join {relay}`，还是同一台中转站。
  - 踢人：`relay.kick {relay, person}`（也可以给 `device`），返回 `result.kick`；撤销用 `relay.unkick {kick}`。被踢的人的设备，列表里马上去掉这台，改走别的公共中转站或部署的默认中转站；中转站下次同步就断开他们，再连会被拒，理由是 `kicked`。哪个部署管理员踢的都能撤销。
  - 公共中转站拖不进局域网，也删不掉，部署管理员也一样：`relay.add` 回 403"公共中转站…不用拖"，`relay.delete` 回 403"公共中转站拿不掉：要下线一台，在服务器上停掉它…"。
  - 部署管理员管不了别人自己的中转站。
- **种子文件里列出的公共中转站**（`relays[].default` 为 true，`seeded` 表示种子文件建的）：所有局域网都按种子里的顺序先试它们，然后才轮到按权重分的公共中转站。所以它们不进分配条，权重、容量、名字、重装都由种子文件管（这几样操作回 403），只能踢人和撤销。

**还等用户定、做成一处就能改的**（`control/operator.go` 开头的常量）：新装好的公共中转站先当备用（`newPublicRelayWeight = 0`，改成 1 就装好立刻分到设备）；"用得最多的"列几个人（`operatorTopPeople = 5`）。

**测试**：`TestOperator` 覆盖普通人被拒；用管理命令设成部署管理员后运营页出现；加两台（等待安装 → 登记后先当备用、2 MB/s）；调权重以后阿杰的 NAS 的列表顺序跟着变；容量、改名；踢阿杰，他的 NAS 改走东京，香港拒绝并断开他，别人不受影响；撤销后回来；数字（总量、前几名、人数、负载、踢人记录，不出现设备 id 和邮箱）；重装码和撤销；拖不进、删不掉、管不了别人的中转站；取消身份后页面和权限一起没了。`TestOperatorSeedRelay` 覆盖种子里的公共中转站只能踢人。

## ❓ 要主会话定的

1. 公共中转站要不要也能拖到前面？推荐先不做，永远兜底。
2. 验收时在 hk-155（公共的）、hk-spare（kittors 的"我的服务器"）各起一个 deneb-relay：用 `deploy/relay/deploy.sh`，端口 47445/tcp、47478/udp（先只读确认空闲），不碰现成的 derper。等主会话说可以再部署。
3. 中转站镜像放哪里：现在没有公开的镜像仓库，安装命令里的 `deneb-relay:v0` 要先在服务器上构建。要让"复制一行命令就能装"对别人也成立，得推到一个公开仓库（比如 GitHub 的 ghcr.io，推送要用户同意），或者让控制服务器自己提供程序下载（再配一个 `curl | sh` 安装脚本）。
4. 按 IP 查地区：要下载一份 IP 地区库（几 MB，有许可要求），得用户同意。现在靠云元数据和 `TZ`，查不到就用地址当名字。
