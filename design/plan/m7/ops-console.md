# M7 第 3 批：网页版运营后台

> 客户端线，2026-11-13。用户 11-13 定了"要做"（起因："我以为这是管理后台呢"）。设计在 DESIGN §14.15，交付在桌面 `Deneb组网demo/M7-公测/第3批-运营后台/`。**用户 11-13 看过，6 件都按推荐定了**（第 3 节）。服务器这一半归控制线，下面第 2 节是网页需要的接口；控制线做好以后，客户端线接线，网页代码那时一起进主线。

## 1. 网页这一半（演示已经做好，还没进主线）

- 页面：`web/app/src/console.ts`、`console.css`；侧边栏"部署"一组五行（`shell.ts`）；路由 `/ops`、`/ops/people`、`/ops/devices`、`/ops/log`，原来的运营页挪到 `/ops/relays`，标题改成"公共中转站"（`router.ts`、`pages.ts`、`operate.ts`）。
- 演示用假服务器 `cmd/deneb-web/mock_ops.go`：自己造的一个部署（132 个人、339 台设备、报警、备份、今天的日志），每次都一样；kittors 是部署管理员。
- 录屏：`rec-console.ts`（M7-31 到 M7-35）。

## 2. 要请控制线加的接口（提议）

只给部署管理员（`operatorView` 那个身份），别人一律 403；每个改动写一条运营记录。网页上不出现设备报上来的地址和端口、令牌、验证码，也不出现局域网里的成员和服务（隐私说明第 9 节）。

### 看

| 接口 | 回答 |
| --- | --- |
| `GET /api/v1/operator/overview` | 现在的报警、最近 7 天好了的报警；服务器（启动时间、版本、内存和上限、磁盘剩多少、最新备份的时间、份数、每天几点）；公共中转站（几台、几台在线、名字、满的、忙的）；数字（人、本周新来、设备、在线、局域网、平均几个人、在浏览器里登录着的人） |
| `GET /api/v1/operator/backups` | 备份：名字、大小、写的时间（`deneb-control backups`） |
| `GET /api/v1/operator/people?q=&filter=&offset=&limit=` | 人：编号、名字、头像字、邮箱、开通时间、最后在线、在不在线、几台设备、几台在线、是不是部署管理员、是不是测试账号。`filter`：`new`、`online`、`operators`、`test` |
| `GET /api/v1/operator/people/{id}` | 上面那些，加：设备列表、位置（用了几个、上限、是不是单独调过、部署默认）、浏览器（几个、最近一次）、局域网（几个、自己建了几个）、自己建的还有别人的局域网（名字、还有几个人、最早加入的那个人，注销时问转给谁）、自己的中转站几台 |
| `GET /api/v1/operator/devices?q=&state=&offset=&limit=` | 设备：编号、名字、种类、系统、引擎版本、状态（`online`、`offline`、`loggedOut`、`expired`）、最后在线、登录到期、Deneb 地址、主人；外加一共几台、几台在线 |
| `GET /api/v1/operator/logins` | 等着批准的登录（测试账号用的）：编号、电脑名、系统、什么时候；外加能登进去的测试账号 |
| `GET /api/v1/operator/records` | 运营记录，新的在前：时间、谁、一句话 |
| `GET /api/v1/operator/log?kind=&after=&before=` | 今天和昨天的日志：序号、时间、种类（`login`、`device`、`op`、`alert`、`error`、`other`）、原文；能按 `kind` 筛。不带 `after`、`before` 时给最后 200 行；带 `before` 往前翻，一次 200 行，一共最多 2000 行，`more` 说还能不能再往前；网页每 2 秒带 `after` 来要新的 |

### 做：`POST /api/v1/operator/act`，`{act, args}`

| act | args | 和命令行的对应 |
| --- | --- | --- |
| `seats.set` | `person`、`limit`（0 恢复部署的默认） | `deneb-control seats` |
| `sessions.revoke` | `person` | `deneb-control sessions -revoke` |
| `operator.set` | `person`、`on` | `deneb-control operators -add / -remove`；不能撤自己 |
| `device.relogin` | `device` | `deneb-control expire`（马上到期） |
| `device.delete` | `device` | `deneb-control device-delete` |
| `account.delete` | `person`、`confirm`（他的邮箱，服务器核对，对不上不做）、`lan:<编号>`（`transfer` 或 `delete`） | `deneb-control account-delete`，规则和自己注销一样；不能注销自己。注销以后给本人发一封信："应你的要求，你的 Deneb 账号注销了"，信里不写是谁操作的 |
| `login.approve` | `login`、`email`（测试账号） | `deneb-control approve` |

公共中转站那一页原有的操作（踢人、调份额、加一台）也要各写一条运营记录。

### 运营记录

- 一条：时间、谁（部署管理员）、对谁、做了什么、结果。网页上显示成一句话（"kittors 替 Leo（leo.chen@example.com）注销了账号"）。
- 也写进服务器日志一行（`operator …: …`）。
- 留 1 年；只记做了什么，不记打开过谁的卡片。

## 3. 用户 11-13 定了：都按推荐

1. **放在哪**：就在网页里，部署管理员侧边栏的"部署"一组，路径 `/ops/…`，登录方式和网页一样。
2. **再确认**：注销账号要输他的邮箱，服务器也核对 `confirm`；删设备、设和撤部署管理员要再点一下；其余不确认，调位置上限能撤销。
3. **日志**：接口给今天和昨天的；用 `before` 往前翻，最多 2000 行；能按种类筛。
4. **局域网**：只给个数。替人注销账号的卡片为了问"转给谁"，会给出他建的、还有别人的局域网的名字和最早加入的那个人的名字（`people/{id}` 的 `owned`）。这是运营后台唯一出现局域网名字和成员名字的地方。
5. **运营记录**：留 1 年，只记做了什么，不记打开过谁的卡片。
6. **替人注销账号以后**：给本人发一封信："应你的要求，你的 Deneb 账号注销了"，信里不写是谁操作的（上面 `account.delete` 那一行）。

## 4. 没做的

- 手机上的样子：这一批只做了电脑上的。
- 真服务器上没跑：接口还没有，演示连的是假服务器。
- 隐私说明第 9 节：要跟着写网页运营后台上能看到什么，包括第 3 节第 4 条那一句（注销卡片上会出现局域网和最早加入的人的名字），以及运营记录留 1 年。归主会话。
- 网页代码里还要跟着改的（接线时做）：记录页那行"运营记录留 1 年"去掉"等你定"；日志往前翻到 2000 行为止。
