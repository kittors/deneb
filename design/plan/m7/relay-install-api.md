# 中转站安装重做：控制服务器这一半（接口）

> 控制线，2026-11-15。照 [relay-install.md](relay-install.md) 第 5.3、6 节做的，用户 11-15 七件都按推荐。这一份是中转站和控制服务器之间的接口，引擎线照着改中转站程序，客户端线照着出卡片的原型。本机测过，没上线。

## 0. 一句话

- **报到**多报几样：装法、本机网卡上的地址、端口映射的结果、系统防火墙。同一台机器再来报到，不新建。
- **加入码** 24 小时有效，用一次就作废。
- **外网检查**：控制服务器在报到后、中转站请求时、搬了地址时、以后每 10 分钟，从外面连它：TCP 做一次 TLS 握手并核对证书指纹，UDP 发一个 STUN 请求，IPv4、IPv6 都查。hk-spare 上的看门狗也从那边查一次。
- **设备的实际结果**：设备上报连不上哪些中转站，服务器按主人在线的设备汇总。
- **状态和原因**进中转站的卡片（`RelayView.status`），也是 `/relay/v1/check` 的回答。可以用的变成连不上时，卡片上的 `alert` 变成 true，客户端据此弹警示。
- **卸载**：中转站退出时告诉控制服务器。Deneb 里删掉中转站以后，它的密钥再来，一律回 410，程序就停。
- **安装命令**改成引导脚本的一行，带这一版程序的 SHA-256。下载站地址从部署配置来，不进仓库。Docker 的命令留作可选。

## 1. 中转站程序要改的（引擎线）

| 接口 | 改了什么 |
| --- | --- |
| `POST /relay/v1/register`（原有） | `RelayInfo` 多几个字段（§2）。回答多一个 `existing`。加入码过期回 401，`message` 是"加入码已过期，请在 Deneb 里重新生成"；不对或者用过，是"加入码不对，或者已经用过、撤销了：请在 Deneb 里重新生成"。照原样打印 `message` 就行。 |
| `POST /relay/v1/sync`（原有） | `info` 原来只在启动后第一次同步时带；现在**任何一项变了**就带：地址、端口、端口映射续上或者丢了、防火墙、`restarts`。服务器只在真变了时才写库。没写地址的中转站（服务器拿请求的来源地址当它的地址），以后来源地址变了，服务器自己跟着改，不用带 `info`。 |
| `POST /relay/v1/check`（新） | 请求体 `{}`。服务器马上从各个位置检查，最多等 20 秒，回 `RelayStatus`（§3）。`install` 最后一步用它打印结论。同一个中转站 10 秒内再问，回上一次的结果；正在查的，等它查完；全服务器同时最多查 8 个。 |
| `GET /relay/v1/check`（新） | 不检查，回卡片上现在的 `RelayStatus` 和上次检查的时间。`status` 用它。它不算中转站在线：服务没在运行时，看到的就是"不在线"。 |
| `POST /relay/v1/leave`（新） | 请求体 `{"reason": "uninstall"}`，回 `{}`。卸载时调用。 |
| 所有带密钥的接口 | 中转站在 Deneb 里被删掉、被卸载、或者在别处重新安装以后，原来的密钥再来，回 **410**，`code` 是 `relay_removed`。程序收到 410 就停止服务、原地空等，不退出：退出会被系统服务一直拉起来，变成"反复出错"。410 记 30 天，之后回 401（不认识这个密钥）。 |

### 1.1 回答里的 `existing`

报到时，如果这台机器已经是这个主人的中转站了，就不新建，回原来那个的编号和名字，`existing: true`。`install` 打印："这台已经是中转站'东京'了"。怎么算同一台：

- 证书指纹一样（同一份数据目录）；
- 或者地址一样、DERP 端口也一样；被卸载过的（端口记成 0），只看地址。

只在同一个主人的中转站里找；公共中转站只在公共中转站里找。原来的密钥作废，原来那份程序再来会收到 410。

### 1.2 410 的三种说法

| 原因 | `message` |
| --- | --- |
| Deneb 里删掉了 | 这个中转站已经在 Deneb 里删掉了 |
| 程序报了卸载 | 这个中转站已经卸载了 |
| 在别处重新安装了（新密钥顶掉了旧的） | 这个中转站已经重新安装过，这一份不再用了 |

## 2. 报到时多报的（`RelayInfo`）

都可以不带，旧程序不带也照常工作，只是检查给不出那么具体的原因。

| 字段 | 意思 | 服务器拿它做什么 |
| --- | --- | --- |
| `install` | 装法：`systemd`、`systemd-user`（没有 root）、`openrc`、`procd`、`cron`、`docker`、`foreground`（手动跑） | 卡片上显示；`systemd-user`、`cron` 提示系统防火墙要请管理员放行 |
| `local` | 本机网卡上的地址，不含回环 | 判断是网卡上就有公网地址、云服务器 1:1 NAT，还是在路由器后面；路由器后面时告诉主人"转发到 192.168.1.5" |
| `portMap` | 端口映射的结果：`{method: upnp\|natpmp\|pcp, external, derpPort, stunPort}`；试了没成是 `{error}` | `external` 是 100.64/10 或私网地址，就是运营商大 NAT，没有公网 IPv4；没成，原因写进建议 |
| `firewall`、`firewallOpen` | 认出的系统防火墙（`ufw`、`firewalld`、`nftables`、`iptables`），以及端口有没有放行 | 没放行时，建议里给出放行的命令 |
| `restarts` | 最近一小时里，这次之前启动过几次，不算这次：一直正常跑是 0，刚装好起来也是 0 | 3 次以上，卡片上报"中转站反复出错" |
| `ipv6`（原有） | 网卡上有公网 IPv6 时，请填上 | 服务器连 hk-155 只走 IPv4，不填就不知道它有 IPv6 |

原有的 `region` 也用上了：`aliyun:`、`aws:` 这类从云服务器元数据来的，说明有安全组，建议里写"安全组（入方向）"。

## 3. 状态和原因（`RelayStatus`）

卡片上的 `RelayView.status`，也是 `/relay/v1/check` 的回答。只有中转站程序报到过的才有；部署时种子文件里写的中转站（比如 hk-spare 上的 derper）没有。

```json
{
  "state": "unreachable",
  "checking": false,
  "alert": true,
  "usableAt": "2026-11-15T06:00:00Z",
  "problems": [
    {"code": "tcp_blocked", "level": "error", "port": 47445, "family": "ipv4", "addr": "203.0.113.9", "firewall": "ufw",
     "message": "从外网连不上 TCP 47445",
     "advice": "系统防火墙 ufw 没有放行：sudo ufw allow 47445/tcp && sudo ufw allow 47478/udp；云服务器的安全组也要放行 TCP 47445、UDP 47478。放行后不用重装，Deneb 每 10 分钟会重新检查，也可以点\"重新检查\""}
  ],
  "probes": [
    {"from": "control", "family": "ipv4", "addr": "203.0.113.9", "tcp": "timeout", "udp": "timeout"},
    {"from": "hk-spare", "family": "ipv4", "addr": "203.0.113.9", "tcp": "timeout", "udp": "timeout"}
  ],
  "devices": {"reached": ["d_…"], "failed": [{"device": "d_…", "reason": "timeout"}]},
  "checkedAt": "2026-11-15T06:10:02Z"
}
```

| 字段 | 意思 |
| --- | --- |
| `state` | 见下表 |
| `checking` | 有人要求的检查正在跑（安装时、主人点了"重新检查"、刚报到、搬了地址）。卡片写"正在检查"，"重新检查"变灰。每 10 分钟的检查在后台跑，不显示 |
| `alert` | 它装好以后曾经能用，现在连不上：客户端弹警示（组件文档里的 Alert），一直到能用为止 |
| `usableAt` | 最近一次变成能用的时间；从没能用过就没有 |
| `problems` | 原因，最要紧的在前；每条带一句话 `message` 和办法 `advice`，`install` 照着打印，卡片也可以按 `code` 和参数自己说 |
| `probes` | 各个位置最近一次看到的，一个地址一行 |
| `devices` | 主人在线的设备里，连上它的（`reached`）、试了连不上的（`failed`，带原因）。都没试过就没有这一项：比如还没拖进任何局域网。公共中转站没有 |
| `checkedAt` | 只在 `/relay/v1/check` 的回答里有：这次检查做完的时间。卡片上不放，免得每 10 分钟都变一次 |

### 3.1 状态

| `state` | 卡片 | 条件 |
| --- | --- | --- |
| `waiting` | 等待安装 | 给了命令还没报到；或者程序报了卸载（`problems` 里有 `uninstalled`） |
| `checking` | 检查中 | 报到了，第一次检查还没做完 |
| `ok` | 可以用 | TCP、UDP 都通，没有 warn、error |
| `partial` | 部分可用 | 有 warn：UDP 不通、只有 IPv6 通、主人有设备连不上、反复出错 |
| `unreachable` | 连不上 | 有 error：TCP 从外面连不上 |
| `offline` | 不在线 | 超过 3 分钟没同步；`RelayView.lastSeen` 是最后一次 |

一个位置连得上就算连得上：hk-155 连不上、hk-spare 连得上，算能用，各位置的结果在 `probes` 里。

### 3.2 原因（`problems[].code`）

| `code` | 级别 | 参数 | 说的话（例） |
| --- | --- | --- | --- |
| `no_public_ip` | error | `addr`、`family` | "这台机器的地址 10.0.0.5 不在公网上，设备从外面连不进来"；运营商大 NAT 时："路由器拿到的地址 100.64.3.4 是运营商的大 NAT，这台机器没有公网 IPv4" |
| `behind_nat` | error | `addr`（局域网地址）、`port` | "这台机器在路由器后面（局域网地址 192.168.1.5），从外网连不进来"；办法：把 TCP、UDP 转发到它，或者打开 UPnP |
| `tcp_blocked` | error | `port`、`family`、`addr`、`firewall` | "从外网连不上 TCP 47445"；办法按情况：系统防火墙没放行（给命令）、云服务器的安全组、都不清楚时一起说 |
| `pin_mismatch` | error | `port`、`family`、`addr` | "TCP 47445 上回答的不是这个中转站：证书对不上"；端口可能转到了别的机器，或者被别的程序占着 |
| `udp_blocked` | warn | `port` | "从外网连不上 UDP 47478"；能中转，帮不上直连 |
| `ipv6_only` | warn | `port`、`addr` | "只有 IPv6 连得上"；只有支持 IPv6 的设备能用 |
| `devices_failing` | warn | `devices`、`count`、`of` | "你的 1 台设备连不上它"；外网检查通，可能是线路问题（比如跨境），它们会改用别的中转站 |
| `crashing` | warn | `count` | "中转站反复出错：一小时里重启了 5 次" |
| `untested` | warn | `family`、`addr` | "没法从外网检查它的 IPv6 地址"（检查的位置都没有 IPv6） |
| `ipv6_blocked` | info | `port`、`addr` | "IPv6 地址 2001:db8::7 从外网连不上"；设备会用 IPv4 |
| `ports` | info | `derpPort`、`stunPort` | "用的端口是 TCP 47446、UDP 47478，不是默认的 47445、47478"（端口被占时换了一组） |
| `uninstalled` | info | — | "已经在服务器上卸载了"；要再用，重新安装 |

### 3.3 一个位置看到的（`probes[].tcp`、`.udp`）

`ok`（通，TCP 还要证书对得上）、`refused`（端口上没有程序在听）、`timeout`（被挡了）、`unreachable`（网络说到不了）、`tls`（有回应，但不是 TLS）、`pin`（TLS 证书不是它的）、`none`（中转站没开 STUN）、`noroute`（这个位置自己没有这种地址的路由，比如没有 IPv6，不算中转站的问题）、`skipped`（这个位置没查：不是公网地址，看门狗不查；也不算中转站的问题）、`error`。

`from` 是 `control`（控制服务器），或者第二个位置的名字（`hk-spare`）。

## 4. 卡片要用的（客户端线）

- `RelayView` 多了 `status`、`version`（程序的版本）、`install`（装法）。
- 新操作 `relay.check`，参数 `relay`：主人点"重新检查"（公共中转站是部署管理员）。它不改东西，回答的版本号不变；接着的版本里卡片先是 `checking: true`，再是结果。还没装好的中转站回 409。
- 警示：`status.alert` 是 true 时，弹警示，说 `problems[0].message`，动作是"去看"（打开卡片）。只有"能用过、现在连不上"才是 true；刚装上就连不上的，安装命令那边已经说了，卡片上照常显示原因，不弹警示。
- 公共中转站连不上，还会发运营提醒（键 `relay-unreachable/<编号>`），和"连不上了"（离线）的提醒一样。

## 5. 第二个位置：hk-spare 上的看门狗

### 5.1 为什么、怎么做

只从控制服务器查，控制服务器自己那条线路出了问题，就会把好好的中转站说成连不上。所以再找一个位置查一次。hk-spare 上本来就跑着看门狗，它会探 DERP 和 STUN，就让它兼做：

- 看门狗用长轮询向控制服务器要活：`POST /probe/v1/next`，带部署的探测密钥。
- 有中转站要查时，控制服务器把地址、端口、证书指纹交给它；它从 hk-spare 查完，`POST /probe/v1/done` 交回结果。
- 看门狗最近一分钟里来要过活，才算连着。没连着时只用控制服务器自己的结果，`probes` 里就只有 `control` 的。
- 看门狗自己也只查公网地址，谁派的活都一样：不是公网地址的，回 `skipped`，一个包也不发。控制服务器平时只派公网地址，但开发版会派本机地址，hk-spare 本机和它所在的网络还有别的服务。

探测本身是同一份代码 `control/probe`：控制服务器和看门狗都用它。

### 5.2 接口（`proto/probe.go`）

| 接口 | 请求 | 回答 |
| --- | --- | --- |
| `POST /probe/v1/next` | `{"name": "hk-spare"}`，`Authorization: Bearer <探测密钥>` | `{"jobs": [{"id", "targets": [{family, addr, derpPort, stunPort, pin, hostName}]}]}`，最多等 25 秒，没活就是空的 |
| `POST /probe/v1/done` | `{"id", "results": [{family, addr, tcp, udp}]}` | `{}` |

密钥不对回 401。结果里只认这次交出去的地址，`from` 由服务器按来要活的看门狗的名字填。

### 5.3 部署（主会话做，上线时）

1. 生成一个探测密钥：`openssl rand -hex 24`。
2. 写进 hk-155 的 `/opt/deneb-control/data/probe.json`：`{"secret": "<密钥>"}`，0600，属 nobody；或者给控制服务器设环境变量 `DENEB_PROBE_SECRET`。控制服务器启动时日志会说"by a prober with the probe secret"。
3. hk-spare 的看门狗配置 `/etc/deneb-watchdog/watch.json` 加一段：
   ```json
   "prober": {"control": "https://<控制服务器>:47443", "pin": "<控制服务器的指纹>", "secret": "<密钥>"}
   ```
   密钥也可以用环境变量 `DENEB_PROBE_SECRET`。看门狗日志会说"checks relays for … too"。
4. 两份都不进仓库。

## 6. 设备的实际结果（引擎线）

`proto.Report` 多了 `relayFailures`：

```json
"relayFailures": [{"relay": "r_…", "reason": "timeout", "since": "2026-11-15T06:00:00Z"},
                  {"relay": "r_…", "reason": "refused", "refused": "notListed"}]
```

- 列出设备列表上那些试过、连不上的中转站；连上了就从这里去掉，放进 `relays`。
- `reason`：`connect`（TCP 连不上）、`timeout`、`tls`（握手失败或者证书指纹不对）、`refused`（被拒，`refused` 填 `RelayDeny*` 的原因）。
- 服务器只留：存在的中转站、不在 `relays` 里的、原因认识的，每个中转站一条，最多 16 条。内容没变就不算改动。
- 汇总只看主人自己在线的设备，结果进卡片的 `devices` 和 `devices_failing`。

## 7. 安装命令

### 7.1 命令的样子

有下载站时（部署配置里设了，§7.3），`relay.join` 给的：

```sh
curl -fsSL https://deneb.app/relay | sh -s -- <加入码> 0.1.9:<这一版 SHA256SUMS 的 SHA-256>
wget -qO- https://deneb.app/relay | sh -s -- <加入码> 0.1.9:<…>
docker run -d --name deneb-relay … --join <加入码> --region auto
```

分别在 `RelayJoin` 的 `command`、`wgetCommand`、`dockerCommand` 里，`version`、`sha256` 也单独给。参数的写法照引擎线的引导脚本 `deploy/relay/install.sh`：加入码，再是"版本号:哈希"。

- 控制服务器还没读到下载站时，命令只带加入码：脚本自己去读 `latest`，只靠 HTTPS 保证没被改过，并且会说一句。
- 没设下载站（开发版）时，`command` 还是 Docker 的那条，和以前一样。

### 7.2 下载站的目录

```text
<下载站>/relay                                 引导脚本（deploy/relay/install.sh，发版时写进下载目录）
<下载站>/download/relay/latest                 一行：现在的版本号
<下载站>/download/relay/<版本>/SHA256SUMS      sha256sum 的格式
<下载站>/download/relay/<版本>/deneb-relay-linux-<amd64|arm64|armv7>
```

和引导脚本里的 `BASE`（下载站放中转站程序的目录）一致。

- 一个哈希保住所有架构：脚本先下 SHA256SUMS，核对它的哈希和命令里的一样，再按它核对程序。
- 版本号写在命令里，同一条命令 24 小时里都对得上，所以旧版本在站上至少留 24 小时。

### 7.3 控制服务器的配置

| 参数 | 意思 |
| --- | --- |
| `-relay-download https://<下载站>/download/relay/`（或环境变量 `DENEB_RELAY_DOWNLOAD`） | 下载站放中转站程序的目录。控制服务器每 10 分钟读一次 `latest` 和那一版的 `SHA256SUMS`，算出哈希放进命令。读不到时沿用上一次的 |
| `-relay-script https://<下载站>/relay` | 引导脚本的地址，默认是下载站域名下的 `/relay` |

地址都只放在部署配置里，不进仓库。

## 8. 其他改动

- **加入码**：有效期 1 小时改成 24 小时，用一次就作废（原来就是用一次）。
- **"不用这个中转站"**（`relay.delete`）、注销账号时删掉的中转站：密钥记 30 天，程序再来回 410（§1.2）。每天的清理删掉 30 天以前的。
- **卸载**（`/relay/v1/leave`）：中转站从所有列表里拿掉，卡片 `registered` 变成 false，状态是 `waiting`，带 `uninstalled`。主人可以"重新安装"（`relay.join` 带上这个中转站），装好就回来了，还在原来的局域网里；也可以删掉。
- **只查公网地址**：控制服务器不去连私网、回环、运营商 NAT 这些地址，免得被拿来探内网；这种中转站直接判 `no_public_ip`。开发版（`-dev`）照查，好在本机试。
- **同时最多 8 个检查**，每个最多 15 秒；同一个中转站 10 秒内只真查一次。

## 9. 数据库

第 13 个迁移：

- `relays` 加 `facts`（中转站报的上面那些，JSON）、`usable_at`（最近一次变成能用的时间）、`left_at`（报了卸载的时间）；
- `devices` 加 `relay_failures`（JSON）；
- 新表 `relay_tombstones`：删掉、卸载、被顶掉的中转站的密钥哈希，记 30 天。

上线前请拿生产备份的副本跑一遍迁移。

## 10. 测试

本机，`go test -p 1`：

- `control/probe`：`TestProbe`（真的 TLS 和 STUN：通、证书不对、不是 TLS、没人听、没回应、没开 STUN）、`TestPublic`。
- `control`：
  - `TestRelayChecks`：能用；路由器后面（转发到局域网地址、自动映射失败的原因）；ufw 没放行（给命令）；证书不对；UDP 不通；只有 IPv6；IPv6 不通只是提示；主人的设备连不上；反复出错；不在线；能用过再连不上时 `alert`。
  - `TestRelayNoPublicAddress`：私网地址不去查，判 `no_public_ip`；运营商大 NAT。
  - `TestRelaySecondPlace`：第二个位置连得上就算能用；密钥不对 401。
  - `TestRelaySameMachine`：同一个证书、同一个地址和端口不新建；别的机器、别人的照常新建；被顶掉的密钥回 410。
  - `TestRelayLeave`：卸载后 410、离开网络图、卡片等待安装；重新安装回来；删掉后 410，30 天后清理。
  - `TestRelayJoinLasts`：24 小时内能用，只能用一次，过期的说法。
  - `TestRelayFailuresReported`：设备上报的清理。
  - `TestRelayCheckOp`：主人点"重新检查"，卡片先"正在检查"再出结果；没装好的 409；`GET /relay/v1/check` 回上次的结果，不再查。
  - `TestRelayCheckWhilePublished`：一次检查查完、还没发布时来问的，要等卡片发布了才回答。
  - `TestPublicRelayUnreachableAlert`：公共中转站连不上发运营提醒。
  - `TestInstallCommand`：读下载站、命令带版本和哈希、wget 和 Docker 的、下载站坏了沿用上一次。
  - 原有的 `TestRelays`、`TestRelayJoin`、越权测试照旧通过：重新登记后旧密钥原来回 401，现在回 410。
- `cmd/deneb-watchdog`：
  - `TestProber`：看门狗兼做第二个位置，控制服务器连不上、它连得上，算能用。
  - `TestProberOnlyPublic`：本机、私网、运营商 NAT 的地址都回 `skipped`，不去探。
- `control` 的 `TestSeenSkipped`：`skipped` 不算查过，不会被当成"被挡了"。

## 11. 没做的

引擎线那一半（中转站程序的安装、状态、更新、卸载，上报 §2 的字段，收到 410 停下）和设备上报连不上的中转站（§6）都做好了，实现见 `relay-install.md` 第 9 节。

- **给主人发邮件**：连不上时只在卡片和警示里说（`status.alert`），公共中转站另给运营方提醒；没有发邮件。要发的话再加。
- **引导脚本和下载站**：脚本是引擎线写的（`deploy/relay/install.sh`）；放上下载站、每次发版传程序和 SHA256SUMS，归主会话。
- **卡片和警示的界面**：客户端线照 §3、§4 出原型。
- **上线**：控制服务器换版、写 `probe.json`、看门狗加 `prober`、设 `-relay-download`，都等用户点头，由主会话来做。
