# M1 · 控制服务器线（m1-control）状态

> 负责目录：`proto/`、`control/`、`cmd/deneb-control/`、`deploy/control/`。依据：实现计划 §2.2、§2.6、§3 M1。
> 最后更新：2026-11-08 09:05

> M2 的改动（待同意、登录到期、指定邀请等）见 [`../m2/control.md`](../m2/control.md)。

## 进度

| 项 | 状态 |
| --- | --- |
| `proto/` 设备接口、操作、用户视图 | ✅ 第一版定稿，引擎线、界面线已按它接 |
| 控制服务器 v0 | ✅ 计划 §3 M1 列的功能全部做完，`go test -race ./proto ./control` 全过 |
| 开发模式 `-dev` | ✅ 已交给引擎线 |
| 部署到 hk-155 | ✅ 在跑，远端冒烟测试通过（见"线上部署"） |

## 怎么连（给引擎、命令行和验收用）

| 项 | 值 |
| --- | --- |
| 地址 | `https://103.42.31.155:47443` |
| 证书指纹 | `sha256:a90009bbefa6cbcad0971350099e2bb54c4f9968a8cdf1401ab54c6861cf0dd4`（客户端内置这一串，用 `proto.PinnedTLSConfig`） |
| 这个部署的地址段 | IPv4 `10.48.0.0/16`，IPv6 `fdfc:dca5:8870::/48`，DNS `10.48.0.53`、`fdfc:dca5:8870::a30:35`，域名 `deneb.internal`（第一次启动时随机选的，之后不变） |
| 中转站 | `hk`：香港，hk-spare 上现成的 derper，`derp.example.com`（直接拨 `198.51.100.4`），DERP 33443，STUN 3478；所有局域网默认用它 |
| 测试账号 | `kittors@deneb.test`（kittors）、`jie@deneb.test`（阿杰）、`mia@deneb.test`、`leo@deneb.test`。验证码不进仓库：本机 `deploy/control/seed.hk155.json`（已忽略），服务器 `/opt/deneb-control/data/seed.json` |

**浏览器会报证书不受信任**：证书是自签的（计划 §0.1），引擎靠指纹认它，浏览器不认。打开登录链接、邀请链接时 Chrome 点"高级 → 继续前往"；Safari 点"显示详细信息 → 访问此网站"。以后有了域名再换正式证书，指纹方案不变。

**登录**：`deneb login` 打印的链接在任何一台电脑的浏览器里打开都行：填这台设备主人的邮箱 → 填验证码（页面写着"登录到阿杰（jie@deneb.test）"）→ "登录好了"，命令行自己接着走。链接 1 小时有效，用过就失效；过期页会说回到设备上用 `deneb login` 或 `deneb status` 拿新链接。每台设备都要填邮箱和验证码，浏览器不记住登过的账号：验收时一个浏览器要登两个人的设备，记住账号容易把阿杰的设备登到 kittors 名下。页面上写着发起登录的设备名、系统和 IP。服务器重启会让还没用的登录链接作废，引擎会马上拿到新的。

## proto 说明

包 `github.com/kittors/deneb/proto`，只依赖标准库。本机接口（§2.6.2）的类型在引擎线的 `engine/localapi/apitype`；用户视图由引擎原样转给界面，用 `proto.UserView` 解。

| 文件 | 内容 |
| --- | --- |
| `ids.go` | `PersonID`、`LANID`、`DeviceID`、`InviteID`、`RelayID`（都是不透明字符串）、`ScopeID`（`lan/<id>`，M3 起有 `share/<id>`） |
| `keys.go` | 机器密钥（Ed25519，签名换令牌）、节点密钥、探测密钥；文本格式和 Tailscale 一样（`nodekey:<hex>`、`discokey:<hex>`）；证书指纹 `sha256:<hex>`（公钥 SPKI 的 SHA-256）和 `PinnedTLSConfig(pin)` |
| `netmap.go` | `NetMap`（给引擎）：`Network`、`Node`、`Peer`、`Scope`（开放表、中转表、暂停）、`Service`、`PortRange`（`tcp/3389`）、`Relay`、设备种类、系统 |
| `view.go` | `UserView`（给界面）：人、局域网、成员、局域网里的设备和服务开关、设备、邀请、中转站、位置；`InvitePreview` |
| `ops.go` | 操作 `Op{id, op, args}`、`OpResult{version, result}`、错误 `ErrorBody{error:{code, message}}` 和错误码 |
| `device.go` | 设备接口的路径和请求、应答：认证、登录、长连接帧、上报、邀请预览 |
| `links.go` | 邀请链接 `https://<服务器>/i/<令牌>` 和 `deneb://join/<令牌>`，`ParseInvite` 都认；`MakeLabel`、`FQDN` |

**命名约定**（和 `apitype` 一致）：设备和人的 `Name` 是 DNS 短名（`nas`、`jie`），`DisplayName` 是给人看的（"阿杰的 NAS"、"阿杰"）；局域网和中转站只有 `Name`，就是显示名。正式名 `FQDN` = `设备.主人.deneb.internal`，由服务器算好下发。

**规则**

- **谁看得见谁**：设备 D 的网络图里，对端 P 出现，当且仅当两台同在至少一个局域网里、两个主人都是那个局域网的已加入成员、而且 P 没有暂停它。`Peer.Scopes` 只含这些共同范围。D 自己暂停的范围仍在 `NetMap.Scopes`（`Paused`）和对端的 `Scopes` 里，控制服务器连不上时也能立刻恢复。用户视图只含这个人加入了的局域网；别人的设备只列我也在的局域网、只列开给我的服务。
- **开放表**：`Scope.Open` 只含打开的服务。ping 不是服务，同范围的对端都能 ping，被拒绝的除外（引擎默认放行）。
- **谁能用**：按设备记。`RefusedByMe`：我的主人在我这台设备上不让对方的主人用；`RefusedByPeer`：对方不让我的主人用。
- **同一个版本号**：服务器每变一次版本号加一，长连接帧里的 `NetMap`、`UserView` 同号。操作的应答带生效的版本号，发操作的那台设备一定会收到这个版本的帧；内容没变的设备不推。
- **新建、加入局域网**：`lan.create`、`invite.accept` 从哪台设备发来，那台设备跟着加进去。
- **服务开关**：`service.set` 第一次出现的服务记进设备的服务目录；一个端口只属于一个服务。关掉、把设备撤出局域网都不删开关记录，撤销（再加回去）时原样恢复。
- **邀请**：一个链接一个人用，7 天有效；已经在局域网里的人再点，只把这台设备加进去，不占用链接。

**设备接口流程**（`proto/device.go` 开头有完整说明）

1. 认证：`POST /device/v1/auth/challenge` → 用机器密钥签 → `POST /device/v1/auth/token` 换 1 小时的令牌；之后带 `Authorization: Bearer`，401 就重新换。先证明持有机器密钥再登录，防止别人把你的机器公钥绑到他的账号上。挑战 1 分钟有效、只能用一次。
2. 登录：`POST /device/v1/login`，每次启动都发（服务器顺便更新节点密钥等）。没人登录时返回 `loginURL`（1 小时有效，期间再问还是同一条）；`POST /device/v1/login/wait` 长轮询，约 25 秒一轮。同一个人登出后再登录，设备、地址、局域网、开关原样回来；换一个人在这台机器上登录，旧设备删除，地址冷却 30 天。
3. 长连接：`GET /device/v1/stream?since=<版本>&userView=1`，NDJSON；连上先推整帧，之后有变化才推；15 秒 keepalive；登出时最后一帧 `loggedOut: true`。连上算在线，断开 10 秒内没重连才算离线。
4. 上报：`POST /device/v1/report {endpoints, homeRelay, paused}`，整份替换；有变化才加版本号。
5. 操作：`POST /device/v1/ops {id, op, args}` → `{version, result}`；拒绝是 4xx 加 `{error:{code, message}}`。同一个 `id` 10 分钟内重发，拿到第一次的应答，不会做两次。
6. `GET /device/v1/userview`（无界面节点按需拉）；`POST /device/v1/invite {invite}` → `InvitePreview`。

**操作**：M1 的全部（`lan.create/rename/delete`、`lan.invite`、`invite.accept/decline/revoke`、`lan.addDevice/removeDevice`、`service.set`、`device.refuse`），另外已经能用的：`device.rename`、`device.setKind`、`member.remove`、`lan.leave`。权限照计划 §2.2 的表；拒绝的话写明原因，例如"这是 kittors 的设备，只有 kittors 能移出"。看不见的局域网、设备一律答"找不到"，不泄露它存在。

## 控制服务器 v0

| 文件 | 内容 |
| --- | --- |
| `control/store.go` | SQLite 表结构（modernc.org/sqlite，纯 Go，单文件，WAL） |
| `control/world.go` | 整个库读进内存的只读快照；每次改动后整份重读再换上（M1 的规模约 1 毫秒） |
| `control/netmap.go` | 网络图、用户视图、位置的计算：安全关键代码 |
| `control/hub.go` | 长连接、在线状态、按内容变化推帧 |
| `control/deviceapi.go` | 设备接口：认证、登录、长连接、上报、邀请预览、`/dev/approve` |
| `control/ops.go` | 操作和权限 |
| `control/web.go` | 登录页、邀请落地页（纯 HTML，无脚本；表单防跨站提交） |
| `control/addr.go` | 地址段（`10.0.0.0/8` 里随机一个 `/16`，避开 10.0–10.47、10.96–10.111、10.128 以上和整数段；ULA `/48`），设备地址从 `.1.1` 起，删除后冷却 30 天 |
| `control/auth.go` | 挑战、令牌（都是 HMAC，重启不失效）；登录链接；验证码试错限制（15 分钟 5 次） |
| `control/seed.go` | 种子文件：测试账号和验证码、中转站、可选的固定地址段；每次启动按邮箱、id 合并 |
| `control/tlscert.go` | 自签证书：密钥只生成一次，证书 397 天、到期前 30 天用同一把密钥重签，指纹不变 |
| `control/backup.go` | 每天 `VACUUM INTO` 一份到 `<data>/backup/`，留 7 份 |
| `control/admin.go` | 服务器本机的管理套接字：`logins`、`approve`（见"线上部署"） |
| `cmd/deneb-control/` | 命令行参数，见下 |

**测试**（`go test -race ./proto ./control`）

- `TestNetMapIsolationRandom`：400 个随机拓扑（含待加入成员、主人没加入却挂在局域网里的设备、暂停、拒绝、多个中转站），逐台设备、逐个人对照规则重算，再在 JSON 里搜不该出现的局域网 id 和名字、设备、邮箱、邀请令牌。
- `TestM1Topology`：照计划 §3 M1 的验收拓扑走一遍设备接口：两人两个局域网、Mac 同时在两个里、邀请两头、隔离、拖进拖出和撤销、服务按局域网开放、谁能用、暂停、撤出、退出。
- 另有：操作幂等、长连接推送和在线状态、登出再登录、换人登录、节点密钥冲突、认证（错签名、重放、跨密钥）、重启后数据和版本、并发操作、登录页和邀请页（同一浏览器登第二台设备也要重新填邮箱）、登录日志、验证码试错、地址段、证书和指纹、种子变化。
- `TestRemoteSmoke`：对线上服务器跑（带环境变量才跑），固定指纹 + 网页验证码登录 + 建局域网 + 邀请 + 长连接；用 mia、leo 两个账号和固定的两把机器密钥，跑完删局域网、登出。

## 开发模式

```bash
go run ./cmd/deneb-control -dev
```

- 纯 HTTP，`127.0.0.1:47080`；数据在 `~/Library/Caches/deneb-control-dev`，`-dev-reset` 清空重来，`-data` 换目录。
- 四个测试账号（同上），网页上验证码都是 `000000`；中转站默认 hk-spare 的 derper。
- `POST /dev/approve {"login":"<登录链接或 c= 后的码>","email":"jie@deneb.test"}`：不经浏览器完成登录（只在 `-dev` 下有）。
- 容器里：`-dev -listen 0.0.0.0:47080 -public-url http://<主机>:47080 -relay 'id=local,name=本地,host=derper.deneb.test,ipv4=172.30.0.3,derp=443,stun=3478'`（`-relay` 可重复，替换默认中转站）。`-tls on` 可以在开发模式下测指纹。
- Linux 静态文件：`deploy/control/build.sh`（`ARCHES=arm64` 只编 arm64），镜像 `deploy/control/Dockerfile`（`FROM scratch`，不拉任何镜像；开发用时加 `--no-healthcheck`）。

## 线上部署（hk-155）

- **容器**：`deneb-control`，镜像 `deneb-control:v0`（`FROM scratch`，以 nobody 运行），`--network host`，只听 TCP 47443（部署前已确认空闲），`--cpus 1 --memory 256m --pids-limit 256`，`--restart unless-stopped`，日志 10 MB × 3。空闲时内存约 6 MB。
- **没动的**：宿主的防火墙、网卡、nginx、80/443 和已有的三个容器（clirelay-*）。宿主网络模式下 Docker 不加任何规则。
- **数据**：`/opt/deneb-control/data`（库、`tls/`、`backup/`、`seed.json`）。`tls/key.pem` 丢了指纹就变，所有客户端要换，**备份时一定带上 `tls/`**。
- **重新部署**：在本机执行 `deploy/control/deploy-hk155.sh`（编译、上传、重建容器；数据目录保留，指纹不变）。改测试账号：编辑 `deploy/control/seed.hk155.json` 再部署。
- **服务器本机的批准命令**（2026-11-08 已部署）：控制服务器在数据目录里开一个 Unix 套接字 `admin.sock`（权限 0600，只有容器里的 nobody 和宿主 root 能连，不是网络端口）。`ssh hk-155 docker exec deneb-control deneb-control logins` 列出等着登录的设备（登录码、主机名、系统、来源 IP、到期时间），先核对再批；`ssh hk-155 docker exec deneb-control deneb-control approve -code '<登录链接或 c= 后的码>' -email <种子里的测试账号>` 把它批给这个账号。只认种子里的测试账号，成功和拒绝都记日志。它能做的事，服务器 root 本来就能做（种子里有验证码）。浏览器登录那条路径留给验收第 1 条，由用户本人走。
  - 用户在主会话里选了服务器端批准：北京时间 11-08 08:22 主会话用它批了四台，构建服务器和这台 Mac（KittorsdeMacBook-Air）归 kittors，阿杰的 NAS 和台式机归阿杰（地址 10.48.1.3–.6）。08:55 我又部署了一次同样的代码，控制服务器重启了一下，各设备几秒内接回；那次替换把 08:22 那个容器的日志一起删了，所以批准那几行日志没留下。部署脚本现在会先把旧容器的日志存到 `/opt/deneb-control/logs/`（留 30 份）。
- **看日志**：`ssh hk-155 docker logs --tail 50 deneb-control`。登录的每一步都有一行：发出链接（设备名、系统、来源 IP、到期时间）、没开通的邮箱、验证码不对、试错太多被挡、打开了过期或用过的链接、谁把哪台设备登进了哪个账号。日志里只记登录码的前 4 位。**取备份**：`scp hk-155:/opt/deneb-control/data/backup/deneb-$(date +%Y%m%d).db .`。
- **验证**：本机（经 Clash）和 hk-spare（直连）访问 `/healthz` 都是 HTTP/2 200；`TestRemoteSmoke` 通过：

  ```bash
  DENEB_REMOTE=https://103.42.31.155:47443 DENEB_PIN=sha256:a90009bbefa6cbcad0971350099e2bb54c4f9968a8cdf1401ab54c6861cf0dd4 \
  DENEB_SEED=deploy/control/seed.hk155.json go test ./control -run TestRemoteSmoke -v
  ```

  冒烟测试在库里留下 mia、leo 名下两台已登出的设备（占 `10.48.1.1`、`10.48.1.2`），看不见、不影响别人；以后重跑还是这两台。

## 没做的、要注意的

- 位置只算不限（`Seats.Enforced=false`），满额画面在 M3。
- 没有发信：只有种子里的账号能登录。
- 邀请过期不会主动推新版本：拥有者的视图里过期的邀请要等下一次变化才消失（`ExpiresAt` 在里面，界面可以自己隐藏）。
- 长连接每次推整帧；设备多了再改成只推变化（计划 §2.2）。
- `/api/v1`（Web 用的用户接口）在 M3。中转站准入 `/relay/v1/admit` 在 M4。
- 控制服务器只有一台，没有高可用（M7）。

## 共用文件的改动

- `go.mod`、`go.sum`：加了 `modernc.org/sqlite v1.46.1`（连带 `modernc.org/libc` 等，只有 `control/` 用）。
- `.claude/launch.json`：加了一个预览配置 `deneb-control-dev`（开发模式，端口 47090，数据在 `/tmp/deneb-control-preview`，不碰默认的开发数据目录）。

## 别的线要知道的事

- proto 的改动会记在这里，并发消息告诉引擎线和界面线；只加字段，不改已有字段的含义。
- 默认开发数据目录 `~/Library/Caches/deneb-control-dev` 是大家共用的；要清空请用 `-dev-reset -data <自己的目录>`，别清别人的。
