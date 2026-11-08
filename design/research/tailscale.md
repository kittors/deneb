# Tailscale 业务逻辑调研（给 Deneb 产品设计用）

> **调研日期**：2026-11-07。定价页、官方文档、变更日志都在这一天读取；本机已装的 macOS 客户端是 1.102.4。
> **用途**：让后续产品设计建立在对 Tailscale 业务逻辑的完整理解上，不是 Tailscale 使用教程。产品经理先读第 0 节和第 6 节，其余当手册查。
> **标记**：✅ 官方页面或文档能直接核对　🔎 社区或第三方的说法，只作旁证　⚠️ 未确认，或是我的推断。
> **出处写法**：正文里的 `[D4]`、`[G5]`、`[V2]` 可以点开，完整网址在第 8 节。D=官方文档和博客，G=GitHub issue，H=Hacker News，V=V2EX，T=第三方文章，L=本机检查。

---

## 0. 先看这一页

**一句话讲清 Tailscale**：云端有一台"协调服务器"，只管身份、密钥和访问规则，基本不碰流量；每台设备装客户端，设备之间用 WireGuard 点对点加密通信。能直连就直连，直连不了就借中继服务器转发。"谁能连谁"的规则由云端下发，在每台设备本地执行。[D1] [D3] [D28]

**最重要的发现**

1. **一台设备同一时间只能"活"在一个 tailnet 里。** 官方 2022-12 给的办法是"快速切换账号"（免重新登录），不是"同时在线"。要求同时连多个 tailnet 的 issue 2020-03 就提出了，到今天还是 open。[D46] [D30] [G1]
2. **分享单台设备的门槛偏高。** 接收方必须是自己 tailnet 的 Owner、Admin 或 IT admin；对方只能用完整域名访问；设备默认被"隔离"（不能主动连回去）；不能带子网、不能用 Taildrop；链接式共享至今标注 Beta。[D4] [D5] 社区里有人干脆把账号密码发给朋友[V1]，也有人因为共享设备不走自建中继，排查了好几天[V3]。
3. **访问控制默认"全开"，收紧要写 JSON。** 新建 tailnet 的默认策略允许所有设备互通，被邀请的人默认能访问整个 tailnet。[D8] [D5] 官方的可视化策略编辑器 2025-10-29 才正式发布，DERP、IPv4 开关这类网络策略项仍必须回 JSON。[D13] [D11]
4. **免费版 Personal：6 人，用户设备不限，仅限非商业用途**（2026-04-08 起从 3 人提到 6 人）。但 ACL 组只有 3 个；用自己域名的邮箱注册会被当作商用，先给 14 天企业试用，不选套餐新设备和新成员就激活不了。[D40] [D41]
5. **客户端入口多而分散，官方自己也承认。** macOS 菜单栏下拉就有账号、设备、出口节点、服务、设置、后台、更新、调试等十来项；官方说菜单栏放不下搜索、功能难发现，所以 2026-03 起默认再给一个窗口版。Windows 的类似界面只说"在做"。[L1] [D37] [D38]
6. **连不上时官方给的是命令行清单。** 官方 7 步排查要用 `tailscale status`、`tailscale ping` 等命令；macOS 客户端的设备详情里能看到是直连还是中继、有 Ping 按钮，但不解释为什么没直连，也不告诉你下一步。国内用户的痛点集中在：官方 28 个中继区域里没有中国大陆节点，打洞失败后走海外中继又慢，自建中继要域名和备案。[D43] [D44] [V5] [V9]
7. **登录有效期是普通人的暗坑。** 默认 180 天，过期后设备断线。macOS 客户端有到期弹窗、通知和 Renew Session…，但小白遇到"显示已连接却 ping 不通"时，社区回复的第一反应仍是可能过期了。[D16] [L1] [V4] [G11]

**对 Deneb 最有用的 5 条启示**（展开见第 6 节）

1. **把"账号"和"网络"拆开**：一个身份、多个局域网同时在线，界面上不再有"切换账号"。Tailscale 做不到的原因，在于所有 tailnet 共用同一段 100.64.0.0/10 地址和一张本机路由表，Deneb 要在地址分配上就避开这个坑。[T1]
2. **设备共享做到"拖一下就好"**：设备拖到人身上就是分享；对方不用当管理员，也不用先有自己的网络；默认只看到被分享的那台；一键收回。
3. **用"服务"代替 ACL、tag、group**：每台设备上开关"谁能用哪项服务"，默认从最小可见开始（Tailscale 默认全开）。JSON、tag、group、网段这些词不出现在界面上。
4. **连接状态要看得见、修得动**：只显示"直接连上 / 经中转"加一句话原因；缺中转站就拖一个进来；中转站对整个局域网（含共享设备）一致生效，避开 Tailscale 里"共享设备不走自建中继"的坑。
5. **把过期、审批这类"静默失效"变成画面上的待办**：新设备、新成员以"待同意"的虚线球出现；登录快到期的设备块上有小提示，一点就续上。不要让用户靠猜。

---

## 1. 概念和它们之间的关系

### 1.1 谁包含谁（文字关系图）

```
组织 / 账号（Owner 名下；套餐、账单、身份提供商 IdP 都挂在这一层）
└─ tailnet = 一张虚拟私有网络（名字形如 xxx.ts.net）          ← 最接近 Deneb 的「局域网」
   ├─ 用户 user（用 IdP 或 passkey 登录；角色：Owner / Admin / Member，付费版另有 4 种）
   │   ├─ 用户设备：手机、电脑（归某个人；套餐里不限数量）
   │   └─ 组 group：一批人的名字，只在访问规则里用（数量有上限）
   ├─ 带标签的资源：服务器、路由器、出口节点（归 tag，不归人；无人值守）
   │   └─ tag：由 tagOwners 规定谁能打；设备"要么有用户，要么有 tag"，不能两者兼有
   ├─ 给设备"开"的能力（先在设备上打开，再到后台批准）
   │   ├─ 出口节点 exit node            ├─ 子网路由器 subnet router
   │   ├─ 应用连接器 app connector       ├─ Serve / Funnel / Services
   │   └─ peer relay（自己的设备当中继）
   ├─ 访问策略 policy file：acls 或 grants + groups + tagOwners + autoApprovers + ssh + tests + nodeAttrs
   ├─ 设备管理设置：设备审批、用户审批、密钥有效期（1–180 天）
   ├─ DNS：MagicDNS、Nameservers、HTTPS 证书开关
   └─ 共享进来 / 共享出去的单台设备（node sharing）——对方不进你的 tailnet

连接层（任意两台设备之间，独立于上面的管理层）
   先经 DERP 牵线 → 同时打洞 → 成功就直连（UDP，WireGuard 加密）
   直连失败 → peer relay（如果有） → DERP 中继（只转发密文）
```

依据：[D2] [D3] [D7] [D10] [D14] [D28] [D40]

### 1.2 概念速查表

| 概念 | 白话解释 | 要点 | 出处 |
| --- | --- | --- | --- |
| tailnet | 只有受邀者能进的私有网络，设备在里面互相直通 | 第一次在任一设备登录就自动创建，并带一套默认访问策略。名字只能在系统给的几个里挑，自定义子域名是 Enterprise 才有 | [D2] [D21] [D40] |
| 组织 / 账号 | 套餐和账单的归属 | 公共邮箱（Gmail、Apple、个人 GitHub）注册 → 个人使用，自动进 Personal；自有域名注册 → 商用，自动开 14 天 Enterprise 试用 | [D40] |
| 用户 | 用 IdP（Google、Microsoft、GitHub、Apple 等）或 passkey 登录的人 | 基础角色 Owner、Admin、Member 全套餐可用；Network admin、IT admin、Billing admin、Auditor 仅 Standard 及以上。Member 进不了后台。每个组织必有且只有一个 Owner | [D7] [D6] |
| 设备（node） | 装了客户端的电脑、手机、服务器 | 分"用户设备"（归某人，不限数量）和"带标签资源"（归 tag，含 50 个，超出每个每月 $1）。每台分到一个 100.x.y.z 地址 | [D40] [D32] |
| 节点共享 | 把**单台**设备给别的 tailnet 里的某个人用，对方不进你的 tailnet | 链接式共享 Beta，声明式（管理员用策略在受信任的 tailnet 之间共享）Alpha，2026-10-06 刚出。所有套餐可用 | [D4] [D39] |
| 多个 tailnet / 快速切换 | 一台设备可保存多个账号，同一时间只激活一个 | 组织级"一个 IdP 下管多个 tailnet"是 Alpha（2025-10-29），也是需联系销售的付费附加项，面向 OEM | [D30] [D39] [D40] |
| ACL | 访问规则：谁能连哪台设备的哪个端口 | 默认拒绝、单向（允许 A→B 不等于 B→A）；规则在每台设备本地执行。新 tailnet 默认策略是"全允许" | [D8] |
| grants | ACL 的升级版：除网络层外还能表达应用层能力、路由过滤 | 官方建议新策略优先用 grants；两者可在同一份策略文件里并存；可视化编辑器会把 ACL 语法自动转成 grants | [D9] [D10] [D11] |
| groups | 一批用户的名字 | 写作 `group:xxx`，不能嵌套，可由 IdP（SCIM）同步。数量上限 Personal 3、Standard 10、Premium 300 | [D10] [D40] |
| tags | 给服务器类设备的"身份"，像服务账号 | 设备**不能同时**有用户和 tag；打 tag 会去掉用户身份；首次打 tag 并认证后默认关闭密钥过期；tagOwners 决定谁能打 | [D14] |
| 设备审批 | 新设备入网后要管理员点头 | 批准前不能收发流量；预批准的 auth key 可自动通过。所有套餐可用 | [D15] |
| 用户审批 | 被邀请或新登录的用户要管理员点头 | 开启后被邀请的人必须先被批准才能加入 | [D6] [D40] |
| 密钥过期 | 设备登录凭证的有效期 | 默认 180 天，可设 1–180 天；过期后与该设备的连接中断，需重新认证；管理员可对单台设备关闭过期，或"临时延长 30 分钟" | [D16] |
| exit node | 把某台设备当"出口"，别的设备所有上网流量经它出去 | 设备上开启 + 后台批准（或 autoApprovers 自动）+ 策略放行 `autogroup:internet`。所有套餐可用 | [D17] |
| subnet router | 一台设备当"网关"，让没装客户端的打印机、摄像头、NAS 也能被访问 | 通告路由 + 后台批准；其后的设备不计数；**不能随节点共享带出去**（想用就得邀请进 tailnet） | [D18] [D4] |
| app connector | 按"域名"而不是 IP 路由某些应用的流量 | 一台有公网 IP 的 Linux 设备当连接器；后台 Apps 页配置；所有套餐 | [D19] |
| MagicDNS | 给设备自动起名字，不用记 100.x | 2022-10-20 之后新建的 tailnet 默认开；共享进来的设备只能用完整域名 | [D20] [D4] |
| HTTPS 证书 | 给 tailnet 内的服务发 Let's Encrypt 证书 | 依赖 MagicDNS，后台 DNS 页开启；**设备名会写进公开的证书透明度日志**；90 天有效 | [D21] |
| Taildrop | 自己的设备之间互传文件 | 官方仍标 Alpha；只限同一个人的设备，不能发给别人的设备，tag 设备也不行；后台 General 里有 Send Files 开关；不受 ACL 限制 | [D22] |
| Tailscale SSH | 用 Tailscale 身份代替 SSH 密钥登录 | 服务端仅 Linux 和 macOS 开源版；策略里 `ssh` 规则有 accept 和 check（定期重新认证）；高级版（自定义 check 时长等）Premium 起 | [D23] [D40] |
| Serve / Funnel | Serve：把本机的网站开给 tailnet 里的人；Funnel：开给整个公网 | Funnel 标 Beta，只能用 443、8443、10000 端口，经 Tailscale 中继，带宽受限且不可配；同一端口不能同时 Serve 和 Funnel | [D24] [D25] |
| Services | 给一个后端服务一个固定名字和虚拟 IP，不绑具体机器 | 宿主必须是 tag 设备，目前只支持 TCP；HN 讨论里提到目前软上限约 10 个 🔎 | [D31] [H1] |
| DERP | Tailscale 在全球架设的中继服务器 | 既帮两台设备"牵线"（交换打洞信息），也兜底转发；转发的是 WireGuard 加密包，看不到内容；官方清单 28 个区域，无中国大陆 | [D26] [D44] |
| peer relay | 用自己 tailnet 里的设备当中继 | 2026-02 起 GA；要写一条 `tailscale.com/cap/relay` 的 grant；设备版本 ≥ 1.86；iOS、Android、Apple TV 不能当中继；只服务同一个 tailnet | [D27] [D47] |

### 1.3 谁影响谁

| 因素 | 影响谁 | 怎么影响 | 出处 |
| --- | --- | --- | --- |
| 设备审批（开启时） | 新设备 | 批准前不能收发流量；批准后立刻可用 | [D15] |
| 用户审批（开启时） | 被邀请的人 | 批准前不能加入 | [D6] |
| 密钥过期 | 用户设备 | 过期就断线，要重新登录；tag 设备默认不过期 | [D16] [D14] |
| tag | 设备的身份、过期、策略 | 打 tag 即失去用户身份，默认关闭过期；访问规则改按 tag 来写 | [D14] |
| ACL / grants | 谁能连谁的哪个端口；还决定出口节点能不能用（需 `autogroup:internet`）、SSH 能不能登、谁能用 peer relay | 默认拒绝、单向；规则由云端下发，在每台设备本地执行 | [D8] [D9] [D17] [D23] [D27] [D3] |
| 节点共享 | 对方看到什么、能做什么 | 遵守**双方**的 ACL；剥掉 tag、group、子网信息；默认隔离；接收方的策略看不到分享方的 tag | [D4] |
| exit node | 使用者的全部上网流量 | 子网路由和 app connector 处理的流量不走它；它的密钥一过期，使用者的上网流量会被挡住，直到停用它或换一个出口节点 | [D17] [L1] |
| subnet router | 没装客户端的设备 | 后台批准路由 + 策略放行，缺一不可；两者独立 | [D18] |
| MagicDNS | 设备名、共享设备的访问方式、HTTPS、Funnel | HTTPS 和 Funnel 明确依赖它；Serve 要先开 HTTPS | [D20] [D21] [D24] [D25] |
| HTTPS 证书 | 隐私 | 设备名公开在证书透明度日志里（只记执行过 `tailscale cert` 的设备） | [D21] |
| 自建 DERP | 共享设备、跨 tailnet | 官方明确不支持这两类；社区实测共享设备不走自建中继 | [D26] [V3] |
| 直连 / peer relay / DERP | 速度和延迟 | 直连最快，peer relay 次之，DERP 最慢；三者安全性相同 | [D28] |
| 套餐 | 组数、SSH、角色、日志等上限 | 见第 4 节 | [D40] |

### 1.4 连接是怎么建立的（直连优先，连不上走中继）

1. **先通后快**：每次连接一开始就经 DERP 中继，用户立即可用，不用等打洞结果。[D29] [D28]
2. **同时打洞**：双方交换候选地址，同时互发探测包；通常几秒内找到更快的路径，后台无感切换成直连。[D29]
3. **直连失败就找 peer relay**：前提是 tailnet 里有双方都够得着的 peer relay 设备。[D28] [D27]
4. **都不行就留在 DERP**；处于中继状态时，Tailscale 会定期重新尝试升级。[D28]
5. **什么时候只能中继**：UDP 被网络封掉；两端都在"硬 NAT"（每个目的地分配不同外部端口，常见于办公室路由器和云上 NAT 网关）。[D28] [D29]
6. **提高直连率的办法**：路由器开 NAT-PMP 或 UPnP；放行 UDP 41641；云上给公网 IP；实在不行部署 peer relay。[D28]
7. **怎么看当前走哪条路**：命令行 `tailscale status`（direct、relay、peer-relay）和 `tailscale ping`（via DERP、via peer-relay、direct）。连接类型文档只介绍了这两个命令的看法；macOS 客户端的界面文案里有 Direct connection、DERP-relayed connection、Peer-relayed connection 和 DERP 区域名，说明设备详情里能看到当前走哪条路 ⚠️（据文案推断，没截图核对）。[D28] [L1]
8. **有多可靠**：作者 2020 年估计打洞加 STUN 加中继能让超过九成时间直连，这是老文章的估算，不是现状统计。⚠️ [D29]
9. **中继的位置**：官方 DERP 清单 28 个区域（2026-11-07 读取），亚洲只有香港、东京、新加坡、孟买、迪拜，没有中国大陆节点。[D44]

---

## 2. 主要业务流程

> 每个流程写了谁能做、一步一步怎么做、容易卡住的地方。角色说明：Owner、Admin、IT admin、Network admin 都是后台角色，Member 进不了后台。[D7]

### 2.1 加设备

**谁能做**：任何用户；开了设备审批则需管理员批准。

1. 注册并登录：用 Google、Microsoft、Apple、GitHub 等身份提供商（或 passkey）。邮箱域名决定套餐：公共邮箱进 Personal；自有域名自动开 14 天 Enterprise 试用。第一次登录会自动创建 tailnet。[D32] [D40]
2. 在要加入的设备上装客户端，登录同一账号。浏览器里完成登录后，设备自动出现，分到 100.x.y.z 地址，设备名取自主机名（后台可改名）。[D32]
3. 想加更多设备：后台 Machines → Add device → 选系统 → 跳到下载页；设备端登录同一账号就算加入。[D33]
4. 开了设备审批：设备在后台显示 "Needs approval"，客户端显示等待管理员批准，批准前不能收发流量。[D15] [L1]
5. 无人值守的服务器、NAS：给设备打 tag，用 auth key 或二维码加入，不绑定个人；auth key 可选"预批准"。[D32] [D15] [D33]
6. 之后：默认每 180 天要重新登录一次，tag 设备除外。[D16]

**卡点**：自有域名邮箱会被当成商用试用，14 天后不选套餐就无法激活新设备，个人用户要自己去 Plans 页选 Personal。[D40]

### 2.2 邀请成员

**谁能做**：Owner、Admin、IT admin。[D6]

1. 后台 Users → Invite external users。
2. 二选一：**邮件**（填邮箱、选角色；同一批邮箱只能用同一个角色），或 **Copy invite link**（选角色 → Generate & copy invite link，自己把链接发给对方）。
3. 对方打开链接，用 IdP 账号或 passkey 登录，下载客户端。邀请单次有效，未使用 30 天过期。
4. 开了用户审批的话，管理员要再批准一次对方才能进。
5. 对方按你选的角色加入（默认 Member，进不了后台）；能连哪些设备由访问策略决定——**默认策略下能连 tailnet 里所有设备**。[D7] [D5] [D8]
6. 撤销：Users 页搜邮箱或用 `status:invited` 筛选 → 菜单 → Delete invite。已加入的成员怎么移除，邀请页没有写。⚠️

**卡点**：对方得有 IdP 账号；邀请页仍沿用"活跃用户计费"的说法，定价页已改成席位制，两处不一致。⚠️ [D6] [D40] 被邀请者默认能访问整个 tailnet 这一点很容易被忽视。[D5]

### 2.3 把设备分享给别人（节点共享）

**谁能做**：分享方是 Owner、Admin 或 IT admin；**接收方也必须是某个 tailnet 的 Owner、Admin 或 IT admin**。[D4]

1. 后台 Machines → 找到设备 → 菜单 → Share。
2. 选方式：**Share by email**（填邮箱，每人一个单次链接），或 **Copy invite link**（可勾 Reusable link，最多用 1000 次）。链接等于凭证；30 天不用就过期。
3. 对方打开链接，看到邀请（分享方的邮箱和头像）后接受。对方不必用同一个邮箱的账号。
4. 设备出现在对方的客户端和后台里（macOS 在菜单栏，Linux 用 `tailscale status`）。访问时**必须用完整域名** `设备名.tailnet名.ts.net`，不能用短名。
5. 默认被"隔离"：对方可以连进来，这台设备不会主动连对方；想双向，要互相分享一台设备。
6. 想限制对方只能访问某些端口：在分享方的访问策略里，以对方邮箱或 `autogroup:shared` 当来源写规则。
7. 撤销：Machines → 该设备 Share → Revoke invite；对方立刻不能用，要恢复得重新邀请、对方重新接受。

**限制**：不能带出子网路由；不能用 Taildrop；不能分享给 tag；自建 DERP 不支持共享；链接式共享仍是 Beta；官方没写共享是否占用对方的设备数或用户数。⚠️ [D4] [D5] [D26]

**对比"邀请用户"**：邀请会增加你的用户数，对方默认能访问整个 tailnet，可用 subnet router 和 Taildrop；分享不增加用户数，只给一台设备，适合设备数量固定的场景。[D5]

### 2.4 限制谁能访问哪台设备的哪个端口

**谁能做**：Owner、Admin、Network admin。[D7]

1. 后台 Access controls。默认打开 Visual editor 标签页，也可以切到 JSON editor 或 GitOps 模式。[D11]
2. 先想清楚"谁"：单个人用邮箱；一批人先在 Groups 里建组；服务器类设备先在策略里声明 tag（tagOwners），再到 Machines → Edit tags 给设备打上。[D10] [D14]
3. 在 General access rules 里加规则：来源（人、组、tag）→ 目标（设备或 tag，加端口，例如 tag:web 的 443）→ 保存。写 JSON 的话就是一条 grant：`src`、`dst`、`ip`。[D11] [D9]
4. 新 tailnet 的默认策略是"全允许"，要先把这条默认规则改掉或删掉，收紧的规则才起作用。⚠️ 官方博客配图里这条规则显示为 "All users and devices"。[D8] [D13]
5. 保存前可以用 Tests 写断言（例如 alice 能访问 tag:build 的 443、不能访问 22），断言失败会拒绝保存；也可以看 Preview rules。[D10] [D43]
6. 事后排查：目标设备上 `tailscale status` 里看不到对方，多半被策略挡了；接收端日志会记录每次连接是被允许还是被拒绝。[D43]

**卡点（对普通人）**：要理解来源、目标、tag、group、端口；tag 必须先在策略里声明才能打；Personal 只有 3 个组；网络类策略项（DERP、IPv4 开关等）只能在 JSON 里改。[D40] [D11]

### 2.5 切换 tailnet（切换账号）

1. 前提：这台设备上登录过多个账号，每个账号对应一个 tailnet。**同一时间只有一个账号处于活动状态**。[D30]
2. macOS：菜单栏图标 → 当前账号 → 点目标账号；v1.96.2 起，窗口的 Accounts 区域双击账号也能切。Windows：右键托盘图标 → 当前账号 → 点目标账号。iOS：右上角头像 → Settings → 点顶部账号 → 账号切换器；Android：右上角图标 → 点当前账号。命令行 `tailscale switch <邮箱或昵称>`。[D30] [D39]
3. 添加新账号：同一处点 Add account（macOS v1.60 起在 Settings → Accounts），到浏览器里登录。[D30]
4. 切换一般不用重新认证，除非这个账号从没在这台设备上用过，或这个账号的设备密钥已过期。[D30]
5. 切换那一刻，设备不再向原 tailnet 收发数据；两个 tailnet 互相不知道对方存在。官方没有明说"已有连接会断"，但按这个设计必然如此 ⚠️；第三方描述 SSH、远程桌面会中断 🔎。[D30] [T1] [T2]
6. 客户端的活动账号与后台登录的账号相互独立（后台可以开多个浏览器各登一个账号）。[D30]

### 2.6 用出口节点（exit node）

1. 选一台设备当出口：macOS 菜单 Exit Node → Run Exit Node；Windows Exit node → Run exit node；iOS、Android Exit Node → Run as exit node；Linux 用命令行 `tailscale set --advertise-exit-node` 并开 IP 转发。[D17]
2. 后台批准：Machines → 该设备菜单 → Edit route settings → Use as exit node（Admin 角色），或在策略里设 autoApprovers 自动批准。[D17]
3. 确认策略放行了 `autogroup:internet`（默认全允许时自带，自定义后要补一条）；只把出口设备当 dst 只能连上它自己，不能拿它上网。[D17]
4. 在要用的设备上选出口：macOS 菜单栏 Exit Nodes；Windows 托盘 Use exit node；iOS 屏幕顶部；Android Exit Node 区域。**每台设备各选一次**。可勾 Allow local network access，保留访问打印机等本地设备；也可用"推荐出口节点"，按延迟、性能、位置自动选。[D17] [D37]
5. 验证：查"我的 IP"应是出口设备的公网地址。停用：选 None。[D17]
6. 可以分享给 tailnet 外的人：分享设备时勾 Allow use as an exit node。[D4]
7. 性能：Linux 内核转发最好；macOS、Windows、Android 只有用户态转发，较慢，还要防止休眠。[D17]

### 2.7 连不上时怎么排查

官方"连不上其他 tailnet 设备"页的 7 步（2025-11-14 验证）：[D43]

1. 两台设备都更新到最新客户端（`tailscale version`，或后台 Machines 页看版本）。
2. 两台设备上都运行 `tailscale status`：对方不在列表里，多半是访问策略挡住了。
3. 运行 `tailscale ping 对方`，看是直连、`via DERP(...)` 还是 `via peer-relay(...)`；开头几个包走 DERP 是正常的。[D28]
4. 检查两端本机防火墙和网络防火墙。
5. 检查访问策略（ACL、grants、SSH）；可以用 Access controls 里的 Preview rules，也可以看接收端的 Tailscale 日志。
6. 确认两端都已认证、密钥没过期。
7. 用了出口节点或子网路由器的话，确认它在线、策略放行。

补充：

- 一直走 DERP，或 ping 提示没建立直连：多半是 UDP 被封，或两端都是硬 NAT。路由器开 NAT-PMP 或 UPnP，放行 UDP 41641，云上给公网 IP，有条件时部署 peer relay。[D28]
- 其他"没连上"的常见原因：设备在等审批，用户在等审批，试用期结束未选套餐（新设备无法激活）。[D15] [D6] [D40]
- 共享设备连不上：必须用完整域名；检查**双方**的 ACL；别用自建 DERP。[D4] [V3]
- 客户端里的线索：macOS 菜单会出现 System Configuration Issue Found…、Login Required、Waiting for Admin Approval… 等提示；窗口版有 Dock 红点和侧栏底部错误区；按住 Option 点图标有 Debug 菜单（含 Bug report）。[L1] [D37] [D35]
- 我的判断 ⚠️：官方清单面向会用命令行的人；macOS 客户端能显示连接方式和提供 Ping，但从界面文案看没有"为什么没直连"和"下一步怎么办"，这正是 Deneb "一句话原因加一个动作"可以做得更好的地方。[L1]

---

## 3. 管理后台和各平台客户端

### 3.1 管理后台有什么

2026-07-23 起后台网址迁到 console.tailscale.com，登录仍在 login.tailscale.com。[D39] 我没有登录后台，下面的页面结构由文档和变更日志拼出来，左侧入口的排列顺序未确认 ⚠️。

| 页面 | 主要内容 | 出处 |
| --- | --- | --- |
| Machines | 设备列表和 Add device 按钮。设备行菜单：Edit tags、Edit route settings（批准子网路由、出口节点）、Share、Disable key expiry、Approve（带 Needs approval 徽标的设备）、改名、删除。可用 `property:exit-node` 之类过滤；2026-04 起能看设备姿态 | [D33] [D15] [D14] [D17] [D4] [D16] [D39] |
| Apps | 配置 app connector：Add an app → Name、Target（预设应用或 Custom 域名）、Connectors（选 tag） | [D19] |
| Services | Advertise → Define a Service；宿主在 Service hosts 里 Approve | [D31] |
| Users | 邀请（Invite external users：邮件或 Copy invite link）、改角色、批准用户、Delete invite（用 `status:invited` 筛选） | [D6] [D7] |
| Access controls | 三种模式：Visual editor、JSON editor、GitOps。Visual editor 分区：General access rules、Tailscale SSH、Groups、Tags、IP sets、Hosts、Node attributes、Device posture、Auto approvers、Tests；另有 Preview rules 标签；Funnel 可一键写入策略 | [D11] [D25] [D43] |
| Logs | 配置审计日志（所有套餐）；网络流日志（Premium 起）。页面名称来自搜索摘要 ⚠️ | [D7] [D40] |
| DNS | MagicDNS 开关、Nameservers、tailnet 名称、HTTPS Certificates | [D20] [D21] |
| Settings | General（Send Files 即 Taildrop 开关）、Device management（设备审批、密钥有效期 1–180 天）、Keys（auth key，含 Pre-approved）、Trust credentials（取代原 OAuth clients，2025-10-30）、Billing（含席位计算器，2026-04-22）、Domains（2026-05-19） | [D22] [D15] [D16] [D39] |

### 3.2 哪些操作只能在后台做

| 事情 | 需要的角色 | 出处 |
| --- | --- | --- |
| 编辑访问策略（ACL、grants、groups、tagOwners） | Owner、Admin、Network admin | [D7] |
| 批准新设备（设备审批） | Owner、Admin、IT admin | [D15] |
| 批准子网路由、出口节点（或写 autoApprovers 免批准） | Owner、Admin、Network admin（IT admin 不能批准子网路由） | [D17] [D18] [D7] |
| 给设备打 tag、改 tag | Owner、Admin、Network admin（或 tagOwners 里的人） | [D14] |
| 邀请、移除用户，改角色，批准用户 | Owner、Admin、IT admin | [D6] [D7] |
| 分享设备、撤销分享、接受别人的分享 | Owner、Admin、IT admin | [D4] |
| 生成 auth key（含预批准） | Owner、Admin、Network admin、IT admin | [D15] [D7] |
| 设置密钥有效期，给单台设备关闭过期或临时延长 30 分钟 | Owner、Admin、IT admin（按"管理设备"权限推断 ⚠️） | [D16] [D7] |
| MagicDNS、Nameservers、HTTPS 证书开关 | Owner、Admin、Network admin | [D7] [D20] [D21] |
| 开关 Taildrop | Owner、Admin、IT admin | [D7] [D22] |
| 启用 Funnel（往策略里加节点属性） | Owner、Admin、Network admin | [D25] |
| 套餐与账单 | Owner、Admin、Billing admin | [D7] [D40] |

**客户端上做的事**（后台做不了，也不用）：连接和断开、选出口节点、切换账号、发 Taildrop 文件、ping 设备、复制地址、登录和退出、Windows 的无人值守模式。**Member 角色整个进不了后台**，所以普通成员想"分享我的设备"，得先找管理员。[D7]

### 3.3 macOS：菜单栏加窗口

2025-09-26 窗口版开始 beta，2026-03-18（v1.96.2）起默认开启；菜单栏版继续保留，二者并行。官方给的理由：菜单栏下拉没法用形状颜色传达信息，没地方放搜索，Taildrop、ping 这些功能不好被发现，还有刘海屏会把图标挡住。[D37] [D38] [D39]

**菜单栏下拉**（按本机 1.102.4 的界面文案还原，层级和顺序是推断 ⚠️）[L1]

```
├─ 状态：Connected / Not Connected（含连接、断开）
│        异常时变成：Login Required、Waiting for Admin Approval…、Waiting for Admin Signing…、
│        Waiting for Network…、System Configuration Issue Found…
├─ Accounts ▸ 账号列表（打勾=当前）、Account Settings…、Add Account…、Log Out
├─ Network Devices ▸ 设备列表（点设备=复制地址或打开窗口，可在 Options 里关掉打开窗口）
├─ Exit Nodes ▸ 可用出口节点 / Use this device as an exit node / Allow Local Network Access
├─ Services ▸
├─ Open Tailscale（打开窗口版）
├─ Admin Console（只有管理员看得到）
├─ Settings…（部分文案是 Preferences…）
├─ Update Available… / Security Update Available…
├─ About Tailscale
└─ Quit / Quit completely / Keep running in the menu bar
按住 Option 点图标 → Debug ▸ Bug report、Reauthenticate、Reset All Settings、Flush DNS Cache、
                      Show Onboarding、System Extension、Reset…                    [D35]
```

**设置里的开关**：开机启动、允许传入连接、使用 Tailscale DNS、使用 Tailscale 子网、用出口节点时允许访问本地网络、Tailnet Lock、命令行集成、隐藏 Dock 图标、更新通道。[L1] [D39]

**窗口版**：可搜索的设备列表，选中后右侧是 IP、状态和操作（复制 MagicDNS 名或 IP、ping、把文件拖到投放区发 Taildrop）；Exit Nodes 页可搜索，并按延迟、性能和位置推荐一个；Services 页；Pinned 页（设备、出口节点、服务可置顶，v1.104.1）；mini player 精简模式（底部显示当前 tailnet，可切出口节点和账号）；严重错误时 Dock 图标出红点，侧栏底部显示错误，点虫子图标进调试窗口；首次使用有引导，可选"App + 菜单栏"或"只要菜单栏"。据界面文案，设备详情里还有连接方式（Direct connection、DERP-relayed connection、Peer-relayed connection）、DERP 区域、Ping 按钮和密钥到期时间 ⚠️（没截图核对）；会话快到期或已到期时有弹窗和通知，菜单里变成 Login Required 或 Renew Session…。[D37] [D38] [D39] [L1]

### 3.4 Windows：系统托盘

没有主窗口；右键托盘图标出菜单。下面由文档片段拼成，没有装机核对 ⚠️。

```
├─ 当前账号 ▸ 账号列表（当前账号标 (current)）、Add another account、Log out        [D30]
├─ Network Devices ▸                                                                 [D34]
├─ Exit node ▸ Use exit node / Run exit node / Allow local network access            [D17]
│              （tailnet 里没有可用出口节点时，Use exit node 不显示）
├─ Preferences ▸ Run unattended（注销、重启后仍保持连接）等                           [D36]
├─ Admin console                                                                     [D34]
├─ Update Tailscale（有新版本时）                                                    [D34]
└─ Exit
按住 Ctrl 点托盘图标 → 调试菜单：Debug report、Use Notepad for URLs、Start Onboarding Test  [D35]
```

Windows 特有的"无人值守模式"：开启后用户注销或设备重启，Tailscale 仍保持在线；开启者的 Windows 账户独占配置权，别的用户不能改，除非先关闭。[D36] 官方 2025-09 说在做类似 macOS 的窗口界面，截至 2026-11-05 的变更日志里没有找到 ⚠️。[D37] [D39]

### 3.5 iOS

官方文档对 iOS 主界面几乎没有描述，下面由文档和变更日志拼成 ⚠️：

- 首次打开：Get Started → 接受安装 VPN 配置 → 允许推送通知（用于提醒重新认证）→ Log in，用 IdP 登录。[D45]
- 账号：右上角头像 → Settings → 点顶部账号 → 账号切换器（Add another account、Log out）。[D30]
- 设备列表、顶部选 Exit Node、可以 Run as Exit Node；设备可置顶；iPad 和宽屏 iPhone 横屏有分栏布局（v1.102.1）；小组件可直接切出口节点。[D17] [D39]
- Taildrop：系统分享菜单里选 Tailscale；v1.104.1 起分享菜单里也能切账号。[D22] [D39]
- 入口容易丢：2025-09 一次更新后 Settings 入口消失，31 条评论，改不了自定义服务器、切不了账号。[G13]
- 限制：iOS 不能当 peer relay；MDM 可以强制保持连接或隐藏菜单项。[D27] [D34]

### 3.6 三端对照

| 动作 | macOS | Windows | iOS | 只能在后台？ |
| --- | --- | --- | --- | --- |
| 连接 / 断开 | 菜单栏顶部 | 托盘菜单 | 主界面顶部开关 | 否 |
| 切换账号 | 菜单栏 → 当前账号；窗口 Accounts 双击 | 托盘 → 当前账号 | 头像 → Settings → 顶部账号 | 否 |
| 看设备列表 | Network Devices；窗口 Devices | Network Devices | 主界面设备列表 | 全量和详情在后台 Machines |
| 选出口节点 | Exit Nodes | Use exit node | 屏幕顶部 | 否 |
| 当出口节点 | Run Exit Node | Run exit node | Run as Exit Node | **批准在后台** |
| 发文件（Taildrop） | 右键 → 共享；窗口拖放 | 右键 → Send with Tailscale | 分享菜单 | **开关在后台** |
| 打开后台 | Admin Console（管理员可见） | Admin console | 未确认 ⚠️ | — |
| 调试 | 按住 Option 点图标 | 按住 Ctrl 点托盘图标 | 未确认 ⚠️ | — |
| 到期提醒 | 通知；菜单变 Login Required | 通知 | 推送通知（安装时授权） | 有效期设置在后台 |

---

## 4. 免费版和付费版的限制

**查询日期：2026-11-07。** 来源是官方定价页 [D40]；我把页面 HTML 下载下来，逐格解码了对比表里的勾选图标。页面没有写"更新于"字样，只有页脚的 2026 年版权。最近一次改版是 2026-04-08 的 Pricing v4。[D41]

### 4.1 四个套餐

| | Personal（免费） | Standard | Premium | Enterprise |
| --- | --- | --- | --- | --- |
| 价格 | $0，永久免费，**仅限非商业** | $8 / 用户 / 月 | $18 / 用户 / 月 | 定制，联系销售 |
| 用户数 | **最多 6 人** | 不限 | 不限 | 定制 |
| 用户设备（电脑、手机） | 不限 | 不限 | 不限 | 不限（Enterprise 卡片另写"自定义设备数量和限制"，与对比表不一致 ⚠️） |
| 带标签资源（出口节点、子网路由器等） | 含 50 个，超出 $1 / 个 / 月 | 同左 | 同左 | 定制 |
| 临时资源（CI、K8s pod） | 1,000 分钟 / 月 | 1,000 分钟 / 月 | 10,000 分钟 / 月 | 定制 |
| ACL 组数量 | 3 | 10 | 300 | 定制 |

### 4.2 功能对照（✓ 有，– 没有）

| 功能 | Personal | Standard | Premium | Enterprise |
| --- | --- | --- | --- | --- |
| 点对点加密、子网路由器和出口节点、分流隧道和 MagicDNS、ACL、peer relays、Funnel、K8s | ✓ | ✓ | ✓ | ✓ |
| 基础 Tailscale SSH | ✓ | 表中写 "Up to 5 hosts" ⚠️ | ✓ | ✓ |
| 高级 Tailscale SSH（自定义 check 时长、localpart 用户名匹配） | – | – | ✓ | ✓ |
| SSO（任意 IdP）、用户审批、设备审批、自定义认证周期、控制台会话计时、基础角色、用户管理 API、MDM 部署 | ✓ | ✓ | ✓ | ✓ |
| 高级用户角色（Network admin、IT admin、Billing admin、Auditor） | – | ✓ | ✓ | ✓ |
| SCIM 用户和组同步 | – | ✓ | ✓ | ✓ |
| 设备姿态集成（MDM、EDR、XDR）、姿态地理位置、MDM 配置 | – | ✓ | ✓ | ✓ |
| 即时访问（JIT）、区域路由 | – | – | ✓ | ✓ |
| Webhooks、配置审计日志 | ✓ | ✓ | ✓ | ✓ |
| 网络流日志、日志流 | – | – | ✓ | ✓ |
| 付费支持 SLA 包 | – | ✓ | ✓ | ✓ |
| 自定义 MSA、发票付款 | – | – | – | ✓ |
| 自定义 tailnet 子域名 | – | – | – | ✓ |

**附加项**：Mullvad $5 / 每 5 台设备 / 月；额外带标签资源 $1 / 个 / 月；多个 tailnet（页面称适合 OEM，在同一组织和 IdP 下做测试、开发隔离）、更多临时资源、支持包都要联系销售；平台扩展（PAM、CI/CD、工作负载、边缘与 IoT）联系销售。[D40]

### 4.3 免费版人数的变化

| 时间 | 免费版 | 出处 |
| --- | --- | --- |
| 2023-04-18 之前 | 1 个用户；设备上限 20 台（后一条来自 V2EX 转述 🔎） | [D42] [V10] |
| 2023-04-18（Pricing v3） | 3 个用户；官方说每用户可用节点翻倍，HN 转述官方邮件为 100 台设备 🔎 | [D42] [H2] [V10] |
| 2024-02-21 | 免费版改称 Personal；新注册且用自有域名的，默认当商用并给试用 | [H6] |
| 2024-10 | 另有 Personal Plus（6 人，约 $5 / 月，价格据 HN 帖标题 🔎） | [H7] [D40] |
| 2026-04-08（Pricing v4） | Personal 提到 6 人，Personal Plus 退役；Starter 改名 Standard；商业版从按活跃用户计费改成按席位计费；老付费客户至少 12 个月内保留原价 | [D41] [D40] |

### 4.4 计费规则里值得注意的点

- **按席位计费**：用户加入时占一个席位，离开时让出；席位可复用；被邀请但还没登录的用户不占席位；SCIM 预配的用户首次登录前也不占。不管有没有人坐，你买的席位都收费；减席位不能低于已占用数。[D40]
- **用户设备不限**；带标签资源和临时资源另算。临时设备在线超过 4 小时，不再扣分钟池，改按带标签资源计。[D40]
- **额度目前没有强制执行**：ACL 组、多 tailnet、带标签资源、临时资源的硬上限和超额收费"暂未执行"，页面称未来几个月逐步引入，没给日期。[D40]
- **个人 / 商用的判定**：用公共邮箱域名（Gmail、Apple、个人 GitHub）建的 tailnet 算个人；用自有域名建的算商用，自动开 14 天 Enterprise 试用。个人用户用自有域名注册后，要手动去 Plans 页选 Personal。[D40]
- **试用结束没选套餐**：已有用户和设备照常用，但新用户和新设备不能激活，待审批的不能批，不能建新 auth key，部分设备和 tailnet 设置不能改。[D40]
- **页面里有两处对不上** ⚠️：（1）基础 SSH 一行，Personal 是勾，Standard 栏写 "Up to 5 hosts"，疑似排版错误；（2）邀请文档仍讲"活跃用户计费"，定价页已是席位制。以定价页为准。[D40] [D6]

---

## 5. 普通人用起来的痛点

**样本说明**：来源是 GitHub issue（官方论坛 forum.tailscale.com 上有 2023-07 的停用公告，具体状态未确认，官方侧的反馈主要在 GitHub）、Hacker News、V2EX 和几篇博客。**Reddit 在我的工具里读不了**（搜索工具屏蔽、内置浏览器拒绝、脚本访问返回 403），所以没有收录 Reddit 原帖，只有一条 HN 评论转述了 Reddit 上的话。这是定性样本，不是统计。下面的话尽量转述，不照抄原文。

| 痛点 | 证据强度 | 一句话 |
| --- | --- | --- |
| 切换账号会掉线 | 强：官方文档 + 开了 6 年的 issue + 多篇第三方 | 设计上同一时间只活一个 tailnet，断开是必然的 |
| 共享设备的流程 | 强：文档 + 多个 issue + V2EX | 双方都要管理员、只能用完整域名、默认隔离、带不出子网、与自建中继冲突 |
| 用 JSON 写 ACL | 强：issue 提出三年多后官方才补可视化编辑器 | 网络类项目至今仍要回 JSON |
| 菜单复杂 | 中：官方自述 + 若干 issue，直接用这个词吐槽的帖子不多 | 入口多、难发现、会被刘海挡住 |
| 免费人数限制 | 中：官方变更清楚，公开抱怨多集中在"想在公司里用" | 1 → 3 → 6 人；真正卡人的是商用付费跳变 |
| 登录过期、设备重复 | 中：官方后来补了提示，但小白仍分不清原因 | 到期后断线，更新后多出同名设备 |
| 国内连接质量 | 强：V2EX 大量 | 无大陆中继，打洞失败走海外，自建中继门槛高 |

### 5.1 切换账号会掉线

- **官方设计就是单活动**：一台设备同一时间只能登录一个活动账号，不能同时在多个 tailnet 上收发数据；tailnet 之间互相不知道对方存在。[D30]
- **官方 2022-12 的方案是"快速切换"**：博客说这是回应用户嫌来回切账号麻烦，切换后几秒内连上；没提同时在线，也没给计划。[D46]
- **"同时连多个"的需求开了 6 年**：[GitHub #183](https://github.com/tailscale/tailscale/issues/183)（2020-03 提出，62 条评论，至今 open）。官方当年答复说计划支持（内部叫 personas），但没有时间表。评论里有人试过同时跑两个 tailscaled 的变通办法，结果两边都不稳定，只好撤掉家里的网络；有人在向两家大公司推荐 Tailscale 时，发现自己已经在用公司网络，没法再接别人的。2022-09 官方在 issue 顶部加注：#713 只做快速切换，#183 保留给同时在线。[G1] [G2]
- **用户嫌切换耗时**：#713 里有人说，在个人和公司账号之间来回切非常费时间。[G2]
- **根因是地址和路由，不是 UI**：2026-06 一篇博客的作者想让同一台 MacBook 全天同时接工作网和家庭实验室网，官方应用做不到：工作网一开，家里的就没了，MagicDNS 也只解析当前那个 tailnet。他的解释是两个 tailnet 的地址都来自 100.64.0.0/10，本机只有一张路由表。他的办法是另起一个用户态 tailscaled 加本地代理。[T1]
- **断连的具体表现**：官方没写"已有连接会断"，但按设计必然如此 ⚠️。第三方切换工具 TailMux 的营销页称 SSH、远程桌面、已挂载的共享、长传输会中断 🔎——这是厂商说法，没有测试数据。[T2]
- **拿别家对比**：HN 上有人说 ZeroTier 的桌面端可以同时接入多个网络，Tailscale 要切换账号。🔎 [H3] [H4]
- **切换出过错**：#13649（2024-10）切换账号后 `tailscale ping` 通、普通 ping 和 curl 超时，后续评论说升级自建的 Headscale 后好了，可能与自建控制端有关 ⚠️。[G3] #7768（2023-04，已关）重新认证其中一个账号，另一个账号的 tag 被清空、过期时间被重置，官方称在安全公告 ts-2023-005 里修复。[G4]
- **用户自己的变通**：V2EX 2026-05 有人建议把朋友拉进自己的免费组织，理由是现在各平台的切换账号已经做得不错。[V2]

### 5.2 共享设备的流程

- **接收方也得是管理员角色**：文档要求接收方是自己 tailnet 的 Owner、Admin 或 IT admin。[D4] GitHub #1865（2021-05）：想把服务器共享给承包商公司里的所有人，含非管理员，做不到；另有人想把自己的个人设备共享给公司账号下的设备，只能在公司电脑上再登录个人 tailnet 并手动收紧 ACL。[G5]
- **默认隔离让人困惑**：#10350（2023-11）兄弟俩各有 tailnet，互相共享服务器做备份，结果共享来的服务器连不回去；官方答复说，这更像是想让一台设备同时在两个 tailnet 里，想支持但短期不会有。[G6]
- **带不出子网**：#1390（2021-02 起，27 条评论，仍 open）。官方当年说出于安全故意不支持，设想以后做成双重确认；2022 年有公司用户说这是他们在全公司推广的障碍。[G7]
- **只有被分享的设备时菜单是空的**：#4731（2022-05，已关）用户的 Network Devices 子菜单什么都没有，被告知该菜单只显示自己的设备。[G8]
- **官方自己列出的短板**：分享不能用 Taildrop，不能访问子网路由器后的设备，也不能自己再加新设备。[D5]
- **和自建中继冲突**：V2EX 2026-05 有人排查了好几天才发现：自己用了自建中继，朋友共享来的设备不会走它，又打不通洞，所以怎么都连不上；另一个帖子里的人为此只能二选一：让朋友也配同样的中继（会耗自己的流量，还会一传十），或者改用 frp、Headscale。官方文档确实写了自建 DERP 不支持共享。[V3] [V2] [D26]
- **"邀请别人"这一步本身就卡人**：V2EX 2023-05 有人不知道怎么邀请，直接把自己的账号密码给对方，自己也觉得不对；回复里有人教用 auth key，有人说对方已在用 Tailscale 就用 Share。[V1]
- **亲友的安装门槛**：HN 2026-10，有人给几十位亲友提供 Jellyfin 服务，要求每人再装一个 Tailscale 并登录太难，最后改成了公开网址。[H5]

### 5.3 用 JSON 写 ACL

- **用户要界面，官方拖了三年多**：GitHub #4055（2022-03 提出）：希望基础访问控制有界面，不必写 JSON，指出给运维人员增加了一门格式要学；2025-03 有人提到竞品 NetBird 主打易用的配置界面，建议提高优先级；官方 2026-03 回复说 2025 年已发布可视化策略编辑器，关闭 issue。[G9]
- **官方 2025-08 的 beta 博客**：承认有人更想用网页表单而不是直接改 HuJSON，连主要用 HuJSON 的人有时也需要语法帮助。[D12]
- **官方 2025-10 的 GA 博客**：引用客户的话，以前非技术人员要靠开发者解释 JSON 配置，现在能直接看。[D13]
- **仍有缺口**：DERP、IPv4 开关这类网络策略项只能在 JSON 里改；Personal 只有 3 个组。[D11] [D40]
- **JSON 编辑本身也烦**：编辑器遇到没声明的 tag 只报"未知错误"[G15]；有人要标准 JSON 兼容的注释[G16]、要 JSON schema[G17]。
- **大改动怕出事**：2026-01 有 HN 评论说大 ACL 文件重构花了很多功夫，还是有人丢了访问，觉得官方的 ACL 测试不足以放心改。[H8]
- **tag 的"主人消失"令人意外**：#5812（2022-10）给设备加 tag，原来的所有者就没了，去掉最后一个 tag 才回来；官方答复这是设计本意，tag 本来就是让设备脱离个人。[G10]
- **端口号要懂**：HN 2026-06 有人想把一个域名分享给朋友，让对方不带端口访问，需要在 ACL 里给对方开 443 和 80。[H9]

### 5.4 菜单复杂

- **官方自己的说法**：菜单栏下拉没法用形状颜色传达信息，没地方放搜索，Taildrop、ping 这些功能不好被发现；刘海屏还会把图标挡在后面，macOS 没有溢出区，用户既看不见也点不到。[D37] [D38]
- **本机实测的条目**：1.102.4 的菜单栏下拉有账号、设备、出口节点、服务、打开窗口、后台、设置、更新、关于、退出、调试十来项，设置里还有开机启动、传入连接、DNS、子网、本地网络、Tailnet Lock、命令行集成等开关（详见 3.3）。[L1]
- **想打开却打不开**：GitHub #14022（2024-11，13 条评论，已关）用户在菜单栏图标被挡住时用 Spotlight 想打开 Tailscale，只得到一条没用的提示；官方回复在做窗口版。[G12]
- **菜单和后台说法不一致**：#4731 见上。[G8]
- **入口脆弱**：GitHub #17124（2025-09，31 条评论，已关）iOS 一次更新后右上角的设置入口消失，改不了自定义服务器，也切不了账号。[G13]
- **企业要自己裁剪**：官方提供至少 18 项"隐藏菜单或界面元素"的系统策略（隐藏 Network Devices、Exit Node 选择器、Preferences、调试菜单、更新提示等）。[D34]
- **我的判断 ⚠️**：公开渠道里直接用"菜单复杂"吐槽的原帖不多，更常见的是找不到入口、图标被挡、功能不容易被发现，所以这一条的证据主要来自官方自述，不是用户量化数据。

### 5.5 免费人数限制

- **官方的变化很清楚**：1 人（2023-04 前）→ 3 人（2023-04-18）→ 6 人（2026-04-08）。官方 FAQ 明说新 Personal 是 6 人，上一版是 3 人。[D42] [D40] [D41]
- **公开反馈整体偏正面**：V2EX 2023-04 的帖子对 100 台设备、还能自建中继这两点评价很高。[V10] HN 2026-04 有人说免费版提到 6 人，正好够家里用。[H1]
- **抱怨集中在"想在公司里用"**：同一条 HN 评论接着说，他们 8 人的小团队月费会从约 $6–12 涨到 $64，可能要放弃 Tailscale；另一位回复说自己公司也差不多。[H1] 2023 年那次改版，也有 5 人团队算账后发现新方案少了用户和组级别的 ACL 等功能。[H2]
- **带标签资源缩水**：HN 2026-04 有人指出带标签设备从 100 降到 50，很多"基础设施用户"受影响；官方人员回应"联系销售"只是暂时的，自助购买在路线图上；现在定价页已可自助加购，每个 $1 / 月。[H1] [D40]
- **免费版里被限制的不是人数而是"分组"**：Personal 只有 3 个 ACL 组，大家庭或朋友圈想分几个小圈子就不够用。[D40]
- **自有域名的陷阱**：用自己域名的邮箱注册，会被当商用，14 天后不选套餐新设备和成员就激活不了，个人用户得手动选 Personal。[D40] [H6]
- **用户的变通**：V2EX 上有人提到免费版刚涨到 6 人一个组织，建议把朋友拉进组织，而不是去折腾设备共享。[V2]

### 5.6 登录过期和设备重复

- **过期时的体验曾经很差**：GitHub #4103（2022-03，22 条评论，仍 open）：公司策略要求 24 小时过期，登录成功后立刻提示一天后过期、请重新认证，菜单里却没有重新认证的入口；Windows 用户希望界面直接显示具体到期时间。官方回复最短是 3 天，更短要联系支持。[G11] 后来官方补了：本机 1.102.4 的文案里有会话快到期和已到期的弹窗与通知、菜单里的 Login Required 和 Renew Session…，设备详情里有到期时间；这个 issue 是否因此解决，我没法确认 ⚠️。[L1]
- **出口节点过期会挡住上网**：文案写明所选出口节点的密钥过期后，使用者的上网流量会被挡住，直到停用或换一个。[L1]
- **小白不知道是过期**：V2EX 2024-12，有人自称纯小白，手机显示已连接但 ping 不通，回复里第一条建议是：可能过期了，重新登录试试。[V4]
- **更新后多出同名设备**：GitHub #17760（2025-11，47 条评论，已关）macOS 每次更新都要求重新登录，并在后台多出一个同名设备（device、device-1），越攒越乱；有人回退版本，有人用临时命令绕过。[G14]
- **官方默认**：180 天过期，可设 1–180 天；管理员可临时延长 30 分钟让设备有机会重登。[D16]

### 5.7 国内连接质量和中继（对 Deneb 最相关的一组）

- **没有大陆中继**：官方 28 个区域，亚洲只有香港、东京、新加坡、孟买、迪拜。[D44] V2EX 2021 的高回复帖就在问有没有国内公开 DERP，说默认国内延迟过大。[V7]
- **打洞经常失败**：V2EX 2025-07（76 条回复）同小区同运营商也常常直连失败，晚高峰走 DERP，想让设备能手动开端口；2024-01 有人 NAS 有公网 IPv6，延迟仍稳定在 500ms 左右，说明没打通。[V5] [V6]
- **走中继很慢**：2024-04 有人说走 DERP 比 frp 直连慢了五六倍。[V8]
- **自建中继门槛高**：国内服务器搭 DERP 需要域名和 https，要备案，否则会被按 SNI 拦截。[V9]
- **和代理软件打架**：开着 v2rayN 的 TUN 模式就打不了洞、只能走中继（2024-08）；Stash 或 Surge 开着，Tailscale 显示已连接却访问不了其他机器（2022-07）；iOS 上要在 Clash 和 Tailscale 之间来回切才能又上外网又回家（2023-06）。[V11] [V12] [V13] 2026-07 Surge 6 加入原生 Tailscale 支持，帖子里的评论称这下不用在两个软件之间来回切了，侧面说明需求真实存在。[V14]

---

## 6. 对 Deneb 的启示

> 判断标准：服务于"设备共享模式"和"虚拟局域网模式"，且普通人不填表单。界面用语沿用 `DESIGN.md` 第 2 节（局域网、设备、共享给我、直接连上、经中转、中转站、服务）。拖拽规则沿用该文件 6.4 的表。下面凡是写"建议"的，都只是给主会话的参考，我没有改动 `DESIGN.md` 和 demo。

### 6.1 逐个功能：保留 / 简化 / 不做

| Tailscale 功能 | 判断 | 一句理由 | 普通人怎么用（拖 / 点，不填表单） |
| --- | --- | --- | --- |
| tailnet（虚拟网络） | **保留**，改叫「局域网」 | 这是产品的核心单位，但不能再等于"账号" | 拖「我的设备」进局域网区域或侧边栏那一行 = 加入；拖出区域 = 撤出；新建局域网点「+」，只起个名字 |
| 多个 tailnet、切换账号 | **不做"切换"，改成「同时在线」** | Tailscale 同一时间只能活一个，6 年没解决，也是你的第三个痛点 [G1] [T1] | 同一台设备再拖进第二个局域网就同时在线；侧边栏点哪一行只是"看哪个"，不会断开另一个 |
| 用户和 7 种角色 | **简化**为「拥有者 / 成员」 | 7 种角色里 4 种要付费才有，普通人读不懂 [D7] | 拥有者头像带标记，成员没有角色下拉；"谁能移出别人的设备"只有一条规则：只有设备主人能（`DESIGN.md` 6.4 已有） |
| 邀请成员 | **保留**，简化 | Tailscale 要选角色、选邮件或链接、对方得有 IdP 账号；社区有人直接发账号密码 [V1] | 把「人」的小球拖到局域网上 = 邀请；或点标题区「邀请朋友」直接得到链接或二维码；对方接受前随时可收回 |
| 设备（node） | **保留** | 基本单位；Tailscale 已经不限用户设备数 [D40] | 点设备块看一句话状态；点名字就地改名 |
| 节点共享 | **保留**，做成「设备共享模式」主入口，并大幅减负 | Tailscale 接收方要管理员、只能用完整域名、默认隔离、带不出子网 [D4] [D5] | 拖设备到某个人身上 = 分享给他；对方不用先建网络；设备出现在他的「共享给我」；收回 = 一次点击，带"撤销" |
| 共享的默认隔离 | **保留**思路，换说法 | 默认单向是对的，但用户不知道为什么连不回去 [G6] | 设备卡片一句话：「只有他能连你，反过来不行」；想互通，把对方的设备也拖给我 |
| ACL、grants | **简化**成「服务」加「谁能用」 | 默认全开加手写 JSON 是第二个痛点，官方三年多后才补可视化编辑器 [G9] [D13] | 设备卡片上一排服务图标（远程桌面、文件夹、网站、命令行）；每个图标下是"谁能用"的人球，点人球开关；把人拖到服务图标上 = 允许；关掉一项服务 = 所有人都不能用（对应"某些端口不让别人用"） |
| 默认策略 | **反过来**：默认最小可见 | Tailscale 默认被邀请者能连整个网络 [D5] [D8] | 新成员只看见分给他的设备；拥有者想放开，点一下「大家都能用」 |
| groups | **后置**，做成「小组」圈 | Tailscale 把组数做成付费限制（3 / 10 / 300），还得先写策略 [D40] | 把人拖进一个圈 = 小组；把设备或服务拖到圈上 = 整组可用；MVP 可不做 |
| tags | **不做**（界面不出现） | 概念反直觉：打了 tag，设备就没有主人了 [G10] [D14] | 对应的需求是「公共设备」：设备拖进局域网的公共区 = 不属于个人，系统自动处理过期等 |
| 设备审批、用户审批 | **简化**成「待同意」 | 开启后新设备停在"等待审批"，没人点头就一直连不上，管理员要去后台才处理得了 [D15] | 新设备、新成员以虚线球出现在拥有者画布上，点一下 = 同意，拖到托盘 = 拒绝；拥有者亲手邀请的人自动同意 |
| 密钥过期（默认 180 天） | **不让用户感知**：自动续期，快到期才提示 | 到期就断线，小白分不清原因；官方已补通知，但仍要用户自己去续 [V4] [G11] [L1] | 设备块上出现小提示「需要重新登录」，单击 = 续上；"每 N 天确认一次"放进「更多」 |
| exit node | **后置**（MVP 不做） | 不在三个核心场景里；非 Linux 平台只有用户态转发，性能差 [D17] | 将来：设备卡片点「让大家从这里上网」，对方设备点「走这里上网」 |
| subnet router | **不做**（MVP） | 要开 IP 转发、后台批准、改路由，普通人不会懂，共享也带不出去 [D18] [D4] | 将来可做成一个「把这台当网关」开关 |
| app connector | **不做** | 面向企业访问 SaaS 的固定出口场景 [D19] | — |
| MagicDNS | **保留**，做成隐形 | 用名字找设备是好体验；Tailscale 里共享设备却要完整域名 [D4] | 点设备「复制地址」；共享给我的设备同样直接用名字 |
| HTTPS 证书 | **不做**（内置默认） | 设备名会进公开的证书透明度日志，普通人不该做这个决定 [D21] | 「网站」类服务自动用 https，不让用户选 |
| Taildrop | **保留**，后置 | 官方至今 Alpha，只限自己的设备 [D22]；但"拖文件到设备"最符合拖拽哲学，macOS 窗口版已有投放区 [D37] | 拖文件到设备块或人的小球 = 发送，对方点「收下」 |
| Tailscale SSH | **不做** | 面向工程师 [D23] | 「服务」里的「命令行」图标开关即可 |
| Serve | **并入**「服务」 | 就是"把我这台上的网站开给局域网里的人" [D24] | 同"ACL、grants"一行 |
| Funnel（暴露到公网） | **不做** | 暴露风险大，官方仍标 Beta，带宽受限且不可配 [D25] | — |
| Services（固定名字加虚拟 IP） | **不做** | 面向运维，要 tag 宿主 [D31] | — |
| DERP、peer relay | **保留**并产品化为「中转站」 | 国内用户痛点最集中，"缺中转站就拖一个进来"方向是对的 [V5] [V7] [V9] | 连不上时提示可以拖一个中转站过来；拖到两台设备之间 = 经它中转；拖回托盘 = 移除；中转站对整个局域网（含共享设备）一致生效 |
| NAT 穿透、直连优先 | **保留**，作为底层 | 先中继立即可用、后台打洞、成功无感切换，这套成熟 [D29] | 界面只说「直接连上 / 经中转」加「很快 / 有点慢」，原因收在卡片一句话里 |
| 连不上时的排查 | **保留意图**，换成一句话原因加一个动作 | 官方 7 步要敲命令 [D43] | 设备卡片上点「为什么连不上」，按顺序查：对方在线吗、登录过期了吗、被限制了吗、能直连吗；给一句话原因和一个可点的动作（拖中转站、续期、联系对方） |
| Tailnet Lock、设备姿态、SCIM、SSO、日志、API、Terraform、GitOps | **不做**（MVP） | 面向企业合规，不是普通人场景 [D40] | — |
| 管理后台 | **简化**为 5 件事 | 官方后台有 8 个入口，普通人只会用到设备、成员、谁能用、中转站、套餐 [D33] | 沿用 `DESIGN.md` 场景 4 的列表后台：设备块拖到另一个格子 = 换位置；成员行拖动排序；成员拖到设备上 = 分享 |

### 6.2 最重要的 5 条启示（展开）

**1. 把账号和网络拆开，做「一个身份、多个局域网同时在线」**

- Tailscale 的做法：账号绑定网络，登录一个账号就进入一个 tailnet；设备一个时刻只在一个 tailnet 里。所有 tailnet 的地址都来自同一段 100.64.0.0/10，本机只有一张路由表，所以同时连两个会冲突。官方 2020 年就说想做，2022 年只做了"快速切换"，2025-10 只在管理侧做了"一个 IdP 下多个 tailnet"（Alpha），客户端同时在线仍然没有。[T1] [G1] [D46] [D30] [D39]
- Deneb 怎么做（建议）：身份是"一个人"，登录一次；局域网是人和设备的集合，一个人可以属于多个；一台设备可以同时加入多个局域网（`DESIGN.md` 6.4 已规定"再拖一次"）。地址上，每个局域网用自己的名字解析，不依赖全网唯一的虚拟 IP；若必须有虚拟 IP，要给"设备 × 局域网"分配互不冲突的地址，避免单张路由表冲突。
- 要注意 ⚠️：这是技术路线选择，需要工程评估和验证，别先把交互定死。

**2. 设备共享做到"拖一下就好"**

- Tailscale 的做法：接收方得是管理员，只能用完整域名，默认隔离，带不出子网，也不能 Taildrop；链接式共享仍是 Beta。社区里有人为这一步直接发账号密码，有人排查几天才发现共享设备不走自建中继。[D4] [D5] [V1] [V3]
- Deneb 怎么做（建议）：对方不用管理员、不用先建网络、不用记域名；分享的范围就是这台设备上"开了的服务"；收回是一次点击加"撤销"。分享设备和中转站设置要一起生效，共享设备走和分享方同一批中转站。

**3. 用「服务」代替 ACL、tag、group**

- Tailscale 的做法：默认全开；收紧要在 JSON 或可视化编辑器里选来源、目标、端口；tag 和 group 是另外两层概念；组数、角色还和付费挂钩。[D8] [D11] [D40]
- Deneb 怎么做（建议）：默认最小可见；设备卡片上直接开关"谁能用哪项服务"；界面上只有两种角色；端口号只在「更多」里出现。底层仍需要一套"默认拒绝、单向、设备本地执行"的规则引擎，这套 Tailscale 做得对，值得照抄，只是不暴露给用户。[D3] [D8]

**4. 连接状态要看得见、修得动，中转站要对全局一致**

- Tailscale 的做法：先中继立即可用、后台打洞、无感升级，很好；macOS 客户端能显示直连还是中继，但不解释为什么没直连、下一步怎么办，深入排查要靠命令行；国内没有中继节点，自建中继要域名和备案；共享设备不走自建中继。[D29] [D43] [D44] [L1] [V9] [V3]
- Deneb 怎么做（建议）：只展示「直接连上 / 经中转」加一句话原因；缺中转站就拖一个进来；中转站的作用范围是整个局域网，含共享设备。Tailscale 的 peer relay 需要显式授权、只服务同一个 tailnet，且手机不能当中继，这三点可以直接借鉴到"拖进来"的规则里。[D27]
- 国内要点 ⚠️：公共中转站要有大陆可用的节点，并考虑用户自带中转站时免域名备案的方案；这是工程和合规问题，需要另行评估。

**5. 把"静默失效"变成画面上的待办**

- Tailscale 的做法：登录 180 天过期、审批没通过，都会变成"设备在，但连不上"。macOS 客户端有"等待审批"和到期的提示，但分散在弹窗、通知、菜单小字和后台里，小白仍然会遇到显示已连接却连不上。[D16] [D15] [L1] [V4]
- Deneb 怎么做（建议）：状态都画在设备块上：待同意（虚线球）、需要重新登录（小提示，单击续上）、对方离线、被限制。每个状态一句话加一个可点的动作，不用靠命令行。

### 6.3 值得照抄 / 不要照抄

| 值得照抄 | 不要照抄 |
| --- | --- |
| 控制面只管身份、密钥和规则，不碰流量；规则在设备本地执行 [D3] | 默认全开，被邀请者能连整个网络 [D5] |
| 先中继立即可用，后台打洞，成功无感切换 [D29] | 账号 = 网络，设备同一时间只能在一个网络里 [D30] |
| 访问规则默认拒绝、单向 [D8] | 7 种角色，其中 4 种要付费 [D7] |
| 共享时剥掉 tag、group、子网信息，并遵守双方策略，保护分享方隐私 [D4] | 把组数当付费限制（Personal 3 组）[D40] |
| 受邀但没登录的用户不占名额；席位可复用 [D40] | 登录过期只靠弹窗、通知和菜单小字提醒 [D16] [L1] |
| peer relay 要显式授权，别人的设备不会被悄悄当中继 [D27] | 共享要求接收方是管理员，且必须用完整域名 [D4] |
| 保存策略前先跑断言（Tests），防止误删权限 [D10] | 自建中继与共享设备互相冲突 [D26] |

### 6.4 与 `DESIGN.md` 现有设计的对照（只建议，不改文件）

**已经对上 Tailscale 痛点的**：直接连上 / 经中转两态加一句话状态卡片（6.5）；拖中转站到连不上的两台设备之间（6.4）；设备拖到人身上 = 分享，带"已收回 · 撤销"（6.4、6.5）；人拖到局域网 = 邀请，接受前可收回（6.4）；别人的设备只有主人能移出（6.4）；一台设备再拖一次进第二个局域网（6.4）。

**Tailscale 里让人踩坑、demo 目前没覆盖的点**（建议主会话评估要不要补）：

1. 「待同意」状态：新设备、新成员加入后等拥有者点头。
2. 「登录到期」状态：快到期的提示和一键续上。
3. 连不上的原因句：对方离线 / 登录过期 / 被限制 / 路由器不让直连，每种配一个动作。
4. 共享的范围怎么画：分享的是哪几项服务，单向还是双向。
5. 名额满了怎么表现：免费 6 人满额时「+」怎么变、提示什么。
6. 中转站对共享设备也生效的画法。
7. 与 Clash、Surge、v2rayN 的 TUN 模式共存的提示（国内用户常见，见 5.7）。
8. 多个局域网里出现同名设备时怎么区分。

### 6.5 需要产品经理拍板的 3 件事

1. **免费额度的计量单位和边界。** Tailscale 按"人"计（免费 6 人），设备不限，受邀没登录不占名额。这套计法可以借鉴；要避开的是它的两个"隐形收费点"：把分组数量和高级角色放进付费，让"免费 6 人"实际可用性打折；以及按邮箱域名自动判定"商用"并强制试用。[D40] [H6]
2. **多个局域网同时在线的技术路线。** 这是 Deneb 和 Tailscale 最根本的差异，也是你第三个痛点的解法，建议先做技术验证，再定交互细节。[T1]
3. **中转站的成本和信任模型。** 公共中转站谁出钱、大陆有没有节点、用户自带中转站的流量算谁的、共享设备走谁的中转站。Tailscale 在这几处留下了用户最多的抱怨。[V2] [V3] [V9]

---

## 7. 未确认的事项和调研局限

1. **Reddit 没有收录。** 搜索工具屏蔽 reddit.com，内置浏览器拒绝打开，脚本访问返回 403。只有 [H1] 里一位 HN 评论者转述了他在 Reddit 上的发言。需要 Reddit 证据的话，得有人手动看 r/Tailscale。
2. **官方论坛状态未确认。** forum.tailscale.com 上有 2023-07 的"停用"公告，公告正文被截断，是关闭、只读还是迁移不清楚；页面上最近的主题到 2024-10。⚠️
3. **管理后台没有登录核对。** 页面名称和层级来自文档与变更日志；Logs 页名称、左侧入口顺序未确认。⚠️
4. **客户端只核对了 macOS。** macOS 菜单来自本机 1.102.4 安装包的界面字符串（只读检查，没有截图，层级和顺序是推断）；Windows 托盘和 iOS 主界面是文档片段拼接，没有装机核对；iOS 有没有后台入口未确认；Windows 的窗口版界面没找到。⚠️
5. **切换账号时已有连接是否中断**：官方没写，是从设计推出来的，加上第三方描述。⚠️
6. **共享是否占用接收方的设备或用户名额**：官方文档没写。⚠️
7. **定价页自身的不一致**：基础 SSH 一行疑似排版错误；Enterprise 的设备数描述与对比表不一致；邀请文档的"活跃用户计费"措辞与席位制不一致；页面没有"更新于"日期。⚠️
8. **免费版历史里的个别数字来自转述**：2023 年前的设备上限 20 台来自 V2EX，2023 年的 100 台来自 HN 转述官方邮件，官方 v3 博客只写"翻倍"；Personal Plus 的 $5 来自 HN 帖标题。🔎
9. **DERP 区域数**：28 个是 2026-11-07 读取官方 JSON 得到的，"亚洲只有香港、东京、新加坡、孟买、迪拜"是按区域名整理的。
10. **抓取方式的局限**：我用的网页抓取工具会先让小模型摘要页面，可能有遗漏或误差。关键数字（定价页、DERP 清单、本机字符串）已回到原始数据核对；其余以文档为准，但没有逐字复核。文档没写的，我都标成了未确认，没有补。
11. **社区样本偏技术用户**：GitHub、HN、V2EX 的检索是关键词搜索（V2EX 用的是第三方 sov2ex 接口，可能漏检），样本量小；真正的普通人很少发帖，他们的声音缺席。
12. **时效**：Tailscale 更新很快（变更日志到 2026-11-05 仍有条目）。本报告只反映 2026-11-07 读到的状态。
13. **Tailscale 内部路线图**（比如何时支持客户端同时多 tailnet）不在公开资料里，未知。

---

## 8. 出处

正文里的 `[D4]` 这类标记可以直接点开；下面是完整清单。读取日期除特别说明外都是 2026-11-07。

### 官方文档和博客（D）

- **D1** · 概览：What is Tailscale：<https://tailscale.com/docs/concepts/what-is-tailscale>
- **D2** · 什么是 tailnet：<https://tailscale.com/docs/concepts/tailnet>
- **D3** · How Tailscale works（2020-03-20，架构：控制面、数据面、密钥、ACL 本地执行）：<https://tailscale.com/blog/how-tailscale-works>
- **D4** · 节点共享：Share your machines with other users：<https://tailscale.com/docs/features/sharing>
- **D5** · 邀请用户 vs 分享设备：<https://tailscale.com/docs/reference/inviting-vs-sharing>
- **D6** · 邀请任何用户加入 tailnet：<https://tailscale.com/docs/features/sharing/how-to/invite-any-user>
- **D7** · 用户角色 User roles：<https://tailscale.com/docs/reference/user-roles>
- **D8** · 访问控制 ACL：<https://tailscale.com/docs/features/access-control/acls>
- **D9** · Grants：<https://tailscale.com/docs/features/access-control/grants>
- **D10** · 策略文件语法（groups、autogroups、tagOwners、autoApprovers、tests）：<https://tailscale.com/docs/reference/syntax/policy-file>
- **D11** · 可视化策略编辑器文档（2026-03-13 验证）：<https://tailscale.com/docs/features/visual-editor>
- **D12** · 可视化策略编辑器 beta 博客（2025-08-05）：<https://tailscale.com/blog/visual-editor-beta>
- **D13** · 可视化策略编辑器 GA 博客（2025-10-29）：<https://tailscale.com/blog/visual-editor-ga>
- **D14** · Tags：<https://tailscale.com/docs/features/tags>
- **D15** · 设备审批 Device approval：<https://tailscale.com/docs/features/access-control/device-management/device-approval>
- **D16** · 密钥过期 Key expiry：<https://tailscale.com/docs/features/access-control/key-expiry>
- **D17** · 出口节点 Exit nodes：<https://tailscale.com/docs/features/exit-nodes>
- **D18** · 子网路由器 Subnet routers：<https://tailscale.com/docs/features/subnet-routers>
- **D19** · 应用连接器 App connectors；配置页 https://tailscale.com/docs/features/app-connectors/how-to/setup：<https://tailscale.com/docs/features/app-connectors>
- **D20** · MagicDNS：<https://tailscale.com/docs/features/magicdns>
- **D21** · HTTPS 证书：<https://tailscale.com/docs/how-to/set-up-https-certificates>
- **D22** · Taildrop（2026-01-05 验证）：<https://tailscale.com/docs/features/taildrop>
- **D23** · Tailscale SSH：<https://tailscale.com/docs/features/tailscale-ssh>
- **D24** · Tailscale Serve：<https://tailscale.com/docs/features/tailscale-serve>
- **D25** · Tailscale Funnel：<https://tailscale.com/docs/features/tailscale-funnel>
- **D26** · DERP 服务器（2026-01-21 验证）：<https://tailscale.com/docs/reference/derp-servers>
- **D27** · Peer Relays 文档（2026-02-04 验证）：<https://tailscale.com/docs/features/peer-relay>
- **D28** · 连接类型：直连、peer relay、DERP（排查与提高直连率）：<https://tailscale.com/docs/reference/connection-types>
- **D29** · How NAT traversal works（2020）：<https://tailscale.com/blog/how-nat-traversal-works>
- **D30** · 快速用户切换文档：<https://tailscale.com/docs/features/client/fast-user-switching>
- **D31** · Tailscale Services（2026-10-07 验证）：<https://tailscale.com/docs/features/tailscale-services>
- **D32** · 快速开始 Quickstart：<https://tailscale.com/docs/how-to/quickstart>
- **D33** · 添加设备 Add a device：<https://tailscale.com/docs/features/access-control/device-management/how-to/set-up>
- **D34** · 用系统策略定制客户端（可反推各平台菜单项）：<https://tailscale.com/docs/features/tailscale-system-policies>
- **D35** · 调试菜单 Debug menu：<https://tailscale.com/docs/reference/debug-menu>
- **D36** · Windows 无人值守模式 Run unattended：<https://tailscale.com/docs/how-to/run-unattended>
- **D37** · macOS 窗口版 UI 进入 beta 博客（2025-09-26）：<https://tailscale.com/blog/windowed-macos-ui-beta>
- **D38** · macOS 窗口版为何出现（刘海屏）博客（2026-03-27）：<https://tailscale.com/blog/macos-notch-escape>
- **D39** · 变更日志 Changelog（2026-11-07 读取）：<https://tailscale.com/changelog>
- **D40** · 定价页含 FAQ（查询日期 2026-11-07）：<https://tailscale.com/pricing>
- **D41** · Pricing v4 博客（2026-04-08）：<https://tailscale.com/blog/pricing-v4>
- **D42** · Pricing v3 博客（2023-04-18）：<https://tailscale.com/blog/pricing-v3>
- **D43** · 排查：设备连通性（2025-11-14 验证）；子页「连不上其他 tailnet 设备」https://tailscale.com/docs/reference/troubleshooting/connectivity/connect-device-failure：<https://tailscale.com/docs/reference/troubleshooting/connectivity>
- **D44** · 官方 DERP 区域清单 JSON（2026-11-07 读取，28 个区域）：<https://controlplane.tailscale.com/derpmap/default>
- **D45** · iOS 安装页；同系列 macOS https://tailscale.com/docs/install/mac ，Windows https://tailscale.com/docs/install/windows：<https://tailscale.com/docs/install/ios>
- **D46** · 快速用户切换博客（2022-12-05）：<https://tailscale.com/blog/fast-user-switching>
- **D47** · Peer Relays GA 博客（2026-02）：<https://tailscale.com/blog/peer-relays-ga>

### GitHub issue（G，仓库 tailscale/tailscale）

- **G1** · #183 同时连接多个 tailnet（2020-03，open）：<https://github.com/tailscale/tailscale/issues/183>
- **G2** · #713 快速切换账号（2020-08，已关）：<https://github.com/tailscale/tailscale/issues/713>
- **G3** · #13649 切换账号后超时（2024-10，open）：<https://github.com/tailscale/tailscale/issues/13649>
- **G4** · #7768 多账号下重新认证串扰（2023-04，已关）：<https://github.com/tailscale/tailscale/issues/7768>
- **G5** · #1865 向多个域名和非管理员共享（2021-05，open）：<https://github.com/tailscale/tailscale/issues/1865>
- **G6** · #10350 取消共享设备的隔离（2023-11，open）：<https://github.com/tailscale/tailscale/issues/10350>
- **G7** · #1390 共享子网（2021-02，open）：<https://github.com/tailscale/tailscale/issues/1390>
- **G8** · #4731 只有外部设备时 Network Devices 菜单为空（2022-05，已关）：<https://github.com/tailscale/tailscale/issues/4731>
- **G9** · #4055 给基础用户一个 ACL 界面（2022-03 提出，2026-03 关闭）：<https://github.com/tailscale/tailscale/issues/4055>
- **G10** · #5812 在后台区分用户设备和 tag 设备（2022-10，open）：<https://github.com/tailscale/tailscale/issues/5812>
- **G11** · #4103 24 小时密钥过期下的重新认证体验（2022-03，open）：<https://github.com/tailscale/tailscale/issues/4103>
- **G12** · #14022 刘海遮挡时也要能打开 Tailscale（2024-11，已关）：<https://github.com/tailscale/tailscale/issues/14022>
- **G13** · #17124 iOS 应用缺少设置菜单（2025-09，已关）：<https://github.com/tailscale/tailscale/issues/17124>
- **G14** · #17760 macOS 更新后要求重新登录并生成重复设备（2025-11，已关）：<https://github.com/tailscale/tailscale/issues/17760>
- **G15** · #5551 ACL 编辑器遇到未定义 tag 只报未知错误（2022-09，已关）：<https://github.com/tailscale/tailscale/issues/5551>
- **G16** · #12361 策略文件支持符合 JSON 的注释（2024-06，open）：<https://github.com/tailscale/tailscale/issues/12361>
- **G17** · #10794 ACL 的 JSON schema（2024-01，open）：<https://github.com/tailscale/tailscale/issues/10794>

### Hacker News（H）

- **H1** · Tailscale's Pricing v4 讨论串（2026-04-08）：<https://news.ycombinator.com/item?id=47691281>
- **H2** · Changes to Tailscale Pricing and Plans 讨论串（2023-04-18）：<https://news.ycombinator.com/item?id=35615848>
- **H3** · 评论：ZeroTier 桌面端可同时接入多个网络（2024-04-17）：<https://news.ycombinator.com/item?id=40062160>
- **H4** · 评论：ZeroTier 支持多网络、Tailscale 只有一个（2021-06-13）：<https://news.ycombinator.com/item?id=27491133>
- **H5** · 评论：给亲友装 Tailscale 门槛太高，改用公开网址（2026-10-20）：<https://news.ycombinator.com/item?id=50163389>
- **H6** · 评论：Free 改名 Personal，自有域名被视为商用（2024-02-21）：<https://news.ycombinator.com/item?id=39458797>
- **H7** · Tailscale Personal Plus（Family Plan $5/月）帖（2024-10-04）：<https://news.ycombinator.com/item?id=41744108>
- **H8** · 评论：ACL 重构后有人丢访问，测试不足以放心改（2026-01-06）：<https://news.ycombinator.com/item?id=46511160>
- **H9** · 评论：让分享的域名不带端口，要在 ACL 开 443 和 80（2026-06-08）：<https://news.ycombinator.com/item?id=48454184>

### V2EX（V）

- **V1** · tailscale 如何邀请别人加入自己的网络（2023-05-28）：<https://www.v2ex.com/t/943556>
- **V2** · Tailscale 中，如何在分享节点的同时使用 DERP / peer-relay 加速国内访问？（2026-05-06）：<https://www.v2ex.com/t/1210371>
- **V3** · 分享一个 tailscale peer relay 相比自建 derp 的优势（2026-05-12）：<https://www.v2ex.com/t/1212280>
- **V4** · tailscale 显示连接，但无法 ping 通（2024-12-23）：<https://www.v2ex.com/t/1099509>
- **V5** · tailscale 为什么经常打洞失败？（2025-07-28，76 条回复）：<https://www.v2ex.com/t/1148160>
- **V6** · tailscale 如何提高打洞成功率？（2024-01-17）：<https://www.v2ex.com/t/1009378>
- **V7** · Tailscale 有国内公开的 DERP 中继机器吗？（2021-11-18）：<https://www.v2ex.com/t/816264>
- **V8** · tailscale derp 速度慢（2024-04-13）：<https://www.v2ex.com/t/1032199>
- **V9** · 国内服务器确实不便搭建 tailscale derp（2024-10-30）：<https://www.v2ex.com/t/1084815>
- **V10** · TailScale 免费套餐内容限制调整（2023-04-19）：<https://www.v2ex.com/t/933632>
- **V11** · 请教个 tailscale 和科学共用无法打洞的问题（2024-08-08）：<https://www.v2ex.com/t/1063480>
- **V12** · macos 下的 stash 和 tailscale 不能一起使用？（2022-07-27）：<https://www.v2ex.com/t/869099>
- **V13** · tailscale 使用问题：Tailscale 与 Clash 来回切换（2023-06-06）：<https://www.v2ex.com/t/946231>
- **V14** · [慢迅] Surge 6 支持 tailscale 了（2026-07-22）：<https://www.v2ex.com/t/1229110>

### 第三方文章（T）

- **T1** · Connecting to Two Tailnets from a Single Device（Raj Rajhans，2026-06-15）：<https://rajrajhans.com/2026/06/connecting-to-two-tailnets-from-a-single-device/>
- **T2** · TailMux vs fast user switching（TailMux 厂商营销页，2026-06-26，只作旁证）：<https://tailmux.app/guides/tailmux-vs-fast-user-switching>

### 本机检查（L）

- **L1** · 本机 Tailscale.app 1.102.4 的界面字符串，只读检查，未改动任何文件：<file:///Applications/Tailscale.app/Contents/Resources/Base.lproj/Localizable.strings>

<!-- 以下是正文标记 [D4] 这类写法对应的链接定义，渲染时不显示 -->

[D1]: https://tailscale.com/docs/concepts/what-is-tailscale
[D2]: https://tailscale.com/docs/concepts/tailnet
[D3]: https://tailscale.com/blog/how-tailscale-works
[D4]: https://tailscale.com/docs/features/sharing
[D5]: https://tailscale.com/docs/reference/inviting-vs-sharing
[D6]: https://tailscale.com/docs/features/sharing/how-to/invite-any-user
[D7]: https://tailscale.com/docs/reference/user-roles
[D8]: https://tailscale.com/docs/features/access-control/acls
[D9]: https://tailscale.com/docs/features/access-control/grants
[D10]: https://tailscale.com/docs/reference/syntax/policy-file
[D11]: https://tailscale.com/docs/features/visual-editor
[D12]: https://tailscale.com/blog/visual-editor-beta
[D13]: https://tailscale.com/blog/visual-editor-ga
[D14]: https://tailscale.com/docs/features/tags
[D15]: https://tailscale.com/docs/features/access-control/device-management/device-approval
[D16]: https://tailscale.com/docs/features/access-control/key-expiry
[D17]: https://tailscale.com/docs/features/exit-nodes
[D18]: https://tailscale.com/docs/features/subnet-routers
[D19]: https://tailscale.com/docs/features/app-connectors
[D20]: https://tailscale.com/docs/features/magicdns
[D21]: https://tailscale.com/docs/how-to/set-up-https-certificates
[D22]: https://tailscale.com/docs/features/taildrop
[D23]: https://tailscale.com/docs/features/tailscale-ssh
[D24]: https://tailscale.com/docs/features/tailscale-serve
[D25]: https://tailscale.com/docs/features/tailscale-funnel
[D26]: https://tailscale.com/docs/reference/derp-servers
[D27]: https://tailscale.com/docs/features/peer-relay
[D28]: https://tailscale.com/docs/reference/connection-types
[D29]: https://tailscale.com/blog/how-nat-traversal-works
[D30]: https://tailscale.com/docs/features/client/fast-user-switching
[D31]: https://tailscale.com/docs/features/tailscale-services
[D32]: https://tailscale.com/docs/how-to/quickstart
[D33]: https://tailscale.com/docs/features/access-control/device-management/how-to/set-up
[D34]: https://tailscale.com/docs/features/tailscale-system-policies
[D35]: https://tailscale.com/docs/reference/debug-menu
[D36]: https://tailscale.com/docs/how-to/run-unattended
[D37]: https://tailscale.com/blog/windowed-macos-ui-beta
[D38]: https://tailscale.com/blog/macos-notch-escape
[D39]: https://tailscale.com/changelog
[D40]: https://tailscale.com/pricing
[D41]: https://tailscale.com/blog/pricing-v4
[D42]: https://tailscale.com/blog/pricing-v3
[D43]: https://tailscale.com/docs/reference/troubleshooting/connectivity
[D44]: https://controlplane.tailscale.com/derpmap/default
[D45]: https://tailscale.com/docs/install/ios
[D46]: https://tailscale.com/blog/fast-user-switching
[D47]: https://tailscale.com/blog/peer-relays-ga
[G1]: https://github.com/tailscale/tailscale/issues/183
[G2]: https://github.com/tailscale/tailscale/issues/713
[G3]: https://github.com/tailscale/tailscale/issues/13649
[G4]: https://github.com/tailscale/tailscale/issues/7768
[G5]: https://github.com/tailscale/tailscale/issues/1865
[G6]: https://github.com/tailscale/tailscale/issues/10350
[G7]: https://github.com/tailscale/tailscale/issues/1390
[G8]: https://github.com/tailscale/tailscale/issues/4731
[G9]: https://github.com/tailscale/tailscale/issues/4055
[G10]: https://github.com/tailscale/tailscale/issues/5812
[G11]: https://github.com/tailscale/tailscale/issues/4103
[G12]: https://github.com/tailscale/tailscale/issues/14022
[G13]: https://github.com/tailscale/tailscale/issues/17124
[G14]: https://github.com/tailscale/tailscale/issues/17760
[G15]: https://github.com/tailscale/tailscale/issues/5551
[G16]: https://github.com/tailscale/tailscale/issues/12361
[G17]: https://github.com/tailscale/tailscale/issues/10794
[H1]: https://news.ycombinator.com/item?id=47691281
[H2]: https://news.ycombinator.com/item?id=35615848
[H3]: https://news.ycombinator.com/item?id=40062160
[H4]: https://news.ycombinator.com/item?id=27491133
[H5]: https://news.ycombinator.com/item?id=50163389
[H6]: https://news.ycombinator.com/item?id=39458797
[H7]: https://news.ycombinator.com/item?id=41744108
[H8]: https://news.ycombinator.com/item?id=46511160
[H9]: https://news.ycombinator.com/item?id=48454184
[V1]: https://www.v2ex.com/t/943556
[V2]: https://www.v2ex.com/t/1210371
[V3]: https://www.v2ex.com/t/1212280
[V4]: https://www.v2ex.com/t/1099509
[V5]: https://www.v2ex.com/t/1148160
[V6]: https://www.v2ex.com/t/1009378
[V7]: https://www.v2ex.com/t/816264
[V8]: https://www.v2ex.com/t/1032199
[V9]: https://www.v2ex.com/t/1084815
[V10]: https://www.v2ex.com/t/933632
[V11]: https://www.v2ex.com/t/1063480
[V12]: https://www.v2ex.com/t/869099
[V13]: https://www.v2ex.com/t/946231
[V14]: https://www.v2ex.com/t/1229110
[T1]: https://rajrajhans.com/2026/06/connecting-to-two-tailnets-from-a-single-device/
[T2]: https://tailmux.app/guides/tailmux-vs-fast-user-switching
[L1]: file:///Applications/Tailscale.app/Contents/Resources/Base.lproj/Localizable.strings

