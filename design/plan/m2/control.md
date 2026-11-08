# M2 · 控制服务器线（m2-control）状态

> 负责目录：`proto/`、`control/`、`cmd/deneb-control/`、`deploy/control/`。依据：实现计划 §3 M2，交互默认值按 §0.2"按 DESIGN 的建议做"。
> 最后更新：2026-11-08。M1 的接口和部署见 [`../m1/control.md`](../m1/control.md)。

## 进度

| 项 | 状态 |
| --- | --- |
| 待同意（新设备、新成员） | ✅ 本机做完、测完 |
| 登录到期、自动续期、"需要重新登录" | ✅ 本机做完、测完 |
| 设备改名、改种类、改 DNS 短名 | ✅（前两个 M1 已有，新增改短名） |
| 连不上的原因（服务器知道的几种） | ✅ 字段已给 |
| 暂停对照 M2 验收 | ✅ 服务器这边不用补；恢复时延的做法等引擎线定（见下） |
| proto 改动 | ✅ 已加，全部是新增字段和操作；方案已发引擎线、界面线，还没收到回复 |
| 部署到 hk-155 | ⏸ 没部署：验收还在用它，要先问主会话 |

`go test -race ./proto ./control` 全过；`go test ./...` 全过（25 个包，2832 项，2026-11-08）。

## 待同意

**默认值（11-08 定）**：用户对实施计划回过"都按推荐"，§0.2 推荐的就是默认要同意，所以新建局域网时"新设备要同意""新成员要同意"都默认开（M2 验收里"阿杰打开新设备要同意"按"默认已经开着"理解，主会话会改计划）。M1 时建的老局域网迁移后保持"设备关、成员开"，免得正在验收的局域网行为突然变化。

**规则**（DESIGN §12 第 6 条，计划 §0.2）：拥有者自己加的设备、亲手邀请的人直接进；其他成员加的设备、邀请的人，按局域网的两个开关等拥有者点一下。

- 两个开关在局域网上：`approveDevices`（新设备要同意）、`approveMembers`（新成员要同意），新建都默认开。M1 的局域网迁移后是"设备关、成员开"，所以老局域网的行为不变：M1 只有拥有者能邀请，拥有者邀请的人照旧直接进。
- **跟人一起进的那台电脑**：接受邀请时用的那台电脑跟着人走：人直接进它就直接进，人等同意它就一起等，拥有者同意这个人时它一起进来。之后成员再往局域网里加的设备，才按"新设备要同意"等拥有者。这样拥有者邀请朋友时，不用在对方加入后再点一次同意他的电脑。
- **M2 起成员也能邀请**（"把人的小球拖到局域网上就是邀请"），邀请可以指定给某个人（`lan.invite` 带 `person`），只有他能用，不用发链接。拥有者邀请的直接进；成员邀请的，`approveMembers` 开着就等拥有者。
- **待同意的设备、待同意成员的设备不在任何网络图里**：连不进来，也看不见别人。
- **谁看得见**：拥有者看得到所有待同意的成员和设备；设备主人看得到自己那台在等；别的成员什么都看不到。待同意的成员只看得到这个局域网的名字和拥有者（`LANView.pending`），里面有谁、有什么一概看不到。
- **操作**：`lan.setApproveDevices`、`lan.setApproveMembers`（拥有者）；`device.approve`、`device.reject`（拥有者）；`member.approve`、`member.reject`（拥有者，同意或拒绝时，他带进来、正在等的设备跟着一起）；已经加入的人用 `member.remove` 移出；等着的人自己可以 `lan.leave` 撤回，设备主人可以 `lan.removeDevice` 撤回。待同意的人不占位置。
- **收到的邀请**：`UserView.invitations` 是指定给我的、还没处理的邀请（`InvitePreview`：id、局域网、邀请人、到期时间，加 `needsApproval`：接受后还要等拥有者），界面直接弹"阿杰邀请你加入「周末联机」"。接受、拒绝用 `invite.accept`、`invite.decline` 带 `args.invitation`（邀请 id），和带链接的 `args.invite` 并列。"以后再说"不用调服务器，邀请会一直留着，直到被接受、拒绝或过期。
- **发出去还没回音的**：`LANView.invited [{person, invitedBy, expiresAt, invite}]`，界面画成虚线的人球"等他接受"；拥有者看全部，成员看自己发的。撤回用 `invite.revoke {invite}`。

## 登录到期和续期

- 设备登录 180 天有效（Tailscale 的默认）。设备每次换令牌（约一小时一次）时，以及长连接打开时、每次 keepalive 时，都自动续到"现在 + 180 天"，一天最多写一次库：挂几周不换令牌的长连接也不会过期，只有离开整整 180 天的设备才过期（引擎线的请求）。
- **过期的设备**：从所有网络图里拿掉（它自己的长连接收到 `loggedOut`，接口返回 `not_logged_in`，引擎回到登录流程）；在别人的视图里还在，`login.expired` 为真，界面写"登录过期"。浏览器里重新登录、服务器端批准，或者主人在任何一台自己的设备上点"续上"（`device.renew`），它就原样回来：同一台设备、同一个地址、原来的局域网和开关。续上时，那台设备正在等的 `login/wait` 会立刻返回已登录，它再调 `login` 也直接是已登录，不用浏览器（引擎线的请求）。
- **"需要重新登录"**：自己的设备 `DeviceView.login = {expiresAt, renewFrom, expired}`，别人的设备只给 `expired`（够解释"连不上：登录过期"，到期时间不外给）。过了 `renewFrom` 界面在设备块上提示，点一下发 `device.renew`，只对自己的设备有效。默认到期前 14 天提示；测试部署可以用 `-login-lifetime 30m` 之类缩短寿命，提示提前量自动按寿命的四分之一算，方便看到这几种状态。
- `LoginState.loginExpiresAt` 告诉引擎自己的登录什么时候到期。
- 服务器每 30 秒检查一次时钟：有设备登录到期、有邀请过期时，发一个新版本（网络图、视图跟着变），不会出现"画面上还连着、其实已经失效"。

## 连不上的原因（服务器知道的）

| 原因 | 字段 |
| --- | --- |
| 对方离线 | `Node.online`、新增 `Node.lastSeen`（`DeviceView` 里本来就有） |
| 登录过期 | `DeviceView.login.expired`（这时网络图里没有它） |
| 被限制 | `Peer.refusedByPeer`、`refusedByMe`；`DeviceView.refusesMe` |
| 待同意 | `LANDevice.pending` |
| 暂停了 | `LANDevice.paused`（对端暂停时它不在我的网络图里） |
| 路由器不让直连 | 引擎的 `PeerPath`，不归服务器 |

## 改名、改种类、改短名

- `device.rename {device, name}` 改给人看的名字（中文、空格都行）。服务器顺带从新名字生成 DNS 短名并去重（重名加 `-2`），正式名 `FQDN` 跟着变；纯中文的名字生成不出短名，就保留原来的。
- `device.setKind` 不变。`Node`、`DeviceView` 里有 `displayName`、`kind`。
- `device.setDNSName {device, name}` 手动设短名，留给命令行和"更多"：同一个主人的设备不能重名；手动设过之后，再改名也不动它（库表第 2 个迁移加了这个标记）。

## 暂停（对照 M2 验收"暂停周末联机 10 秒，工作室零影响，恢复后 0.5 秒内通"）

- 工作室零影响：测试 `TestPauseLeavesOtherLANAlone` 守住，Mac 暂停周末联机时，只在工作室里的构建服务器一帧都收不到；本机测，上报到周末联机里的 NAS 收到新网络图约 12 毫秒。
- **恢复不等服务器**（和引擎线定的，11-08）：暂停了的对端留在网络图里。`Peer.scopes` 含义不变（双方共有、对方没暂停的范围）；新加 `Peer.pausedScopes`（双方共有、但对方暂停了的范围）；两者合起来不空就留下，`scopes` 可以是空的。旧引擎看到 `scopes` 为空就跳过，和现在一样；M2 引擎把它留在 WireGuard 里，路径报 paused，隔离由暂停的那台自己做，恢复时本地一放开两边立刻就通。暂停的那台自己的网络图照旧。
- 这一条要改 `proto/`：Deneb Dev 窗口依赖它，一改就热重载，用户正在跑内核网卡那一轮，等主会话通知结束后再改。

## proto 改动（全部是新增，旧字段含义不变）

- `LANView`：`approveDevices`、`approveMembers`、`pending`、`invited`；`invites` 的可见范围从"只有拥有者"放宽到"拥有者看全部、成员看自己发的"。
- `LANDevice.pending`；`Member.state` 用上 `"pending"`。
- `UserView.invitations`；`InviteView.person`；`InvitePreview.needsApproval`；`OpArgs.invitation`。
- `DeviceView.login`（`LoginInfo`，别人的设备只有 `expired`）；`Node.lastSeen`；`LoginState.loginExpiresAt`。
- 操作：`lan.setApproveDevices`、`lan.setApproveMembers`、`device.approve`、`device.reject`、`member.approve`、`member.reject`、`device.renew`、`device.setDNSName`；`lan.invite` 加可选 `person`，成员也能用；`invite.accept`、`invite.decline` 认 `invitation`（邀请 id）；`invite.revoke` 发邀请的人也能用；`device.rename` 顺带改短名。
- 界面线 11-08 对字段的 7 条意见已按上面落实，界面线确认都可以：拥有者发的邀请（链接或拖人）直接进，成员发的在 `approveMembers` 开着时等拥有者。默认值已告诉界面线和引擎线（引擎线的假拓扑和真机脚本里，成员往别人局域网加设备的步骤要补一步同意，或者先关掉开关）。
- **对 M1 的界面有一处要注意**：一旦有人打开"新设备要同意"或成员发了邀请，视图里会出现 `pending` 的设备和成员。不认识这个字段的界面会把它们画成正常设备，所以界面线接 M2 时要一起认 `pending`。

## 库表

用 `PRAGMA user_version` 做迁移；M1 的表结构原样保留，之后的改动都是迁移。第 1 个迁移（M2）：`lans` 加两个开关、`lan_devices` 加 `state`、`invites` 加 `person`、`devices` 加 `expires_at`；第 2 个：`devices` 加 `name_pinned`（手动设过的短名）。M1 的老设备在第一次启动时从"现在"起算 180 天。`TestMigrateFromM1` 用 M1 的表结构造库再升级，确认数据不丢。

## 测试

- `TestNetMapIsolationRandom` 按 M2 规则重写判定：随机世界里加了待同意的设备和成员、过期的登录、指定给人的邀请和两个开关；逐人核对谁看得见待同意的条目、待同意成员只看得到名字、邀请只给被邀请的人、令牌不外漏。400 个拓扑里有 338 台待同意设备、110 个待同意成员、126 个指定邀请、154 个过期登录。
- `TestApproveDevices`：照 M2 验收走"阿杰打开新设备要同意 → 我拖构建服务器进周末联机 → 他点同意才连得进 → 拒绝就连不进"。
- `TestApproveMembers`：成员发的指定邀请、待同意成员看到的东西、拥有者同意和拒绝、拥有者亲手邀请的直接进、撤回申请、关掉开关后直接进。
- `TestLoginExpiry`、`TestRenewAndDNSName`、`TestInviteExpirySweep`、`TestMigrateFromM1`、`TestPauseLeavesOtherLANAlone`。

## 没做的

- 没部署到 hk-155：验收还在用它，部署前先问主会话。部署时库会自动迁移，证书指纹和地址段不变；会重启一次服务器（引擎几秒内重连，没用掉的登录链接作废）。
- `Peer.pausedScopes`：方案已和引擎线定好，等主会话通知可以动 `proto/` 时再加。
- `UserView.features`（和界面线定的，11-08）：服务器在视图里列出它支持的 M2 功能，`approve`（两个开关和待同意）、`invitePerson`（按人邀请、收到的邀请、撤回）、`rename`（改名、改种类）、`renew`（续上登录和 `DeviceView.login`）、`dnsName`（手动改短名）。M1 服务器不发这个字段，界面就不显示对应的新控件。同样等 `proto/` 解冻后和 `pausedScopes` 一起加。
- 人的 DNS 短名（"更多"里改主人的英文短名）还没做操作，等设计。
