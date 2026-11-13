# M7 第 3 批 · 运营后台：服务器这一半（控制线）

> 控制线，2026-11-13。照 [ops-console.md](ops-console.md) 第 2 节的接口清单和第 3 节用户的决定做的。字段名照客户端线副本里 `cmd/deneb-web/mock_ops.go` 的 `Ops*` 结构体。本机测过，没上线。网页照这份接线。和假服务器不一样的地方，在每节最后标"跟假服务器不同"。

## 0. 总的

- **地址**：都在 `/api/v1/operator/` 下，登录方式和网页一样，靠会话 cookie。
- **谁能用**：
  - 没登录：401；
  - 登录了但不是部署管理员：一律 403，`message` 是"只有部署管理员能看"。
- **"做"的接口**：`POST /api/v1/operator/act`，和网页别的 POST 一样，只收 JSON，有同源检查。
- **不给的东西**：设备报上来的地址和端口、令牌、验证码、局域网的成员和服务一律不给。局域网只给个数，例外只有 `people/{id}` 的 `owned`。
- **运营记录**：每个改动写一条，同时写一行服务器日志：`operator <邮箱>: <那句话> [<act>]`。

## 1. 看

### `GET overview`

`OpsOverview`：

- **`alerts`**：现在的报警，新的在前。
  - `level`：连不上、备份写不出来是 `bad`，别的是 `warn`。
  - 公共中转站的报警带 `relay`（中转站的名字）。
- **`recent`**：最近 7 天报过、已经好了的，带 `resolved`。
- **`server`**：
  - `started`：这次启动的时间。
  - `version`：编这个服务器的提交号，比如 `153a93d`；编的时候有没提交的改动，后面加 `-dirty`。**跟假服务器不同**：不是 `0.7.3`。
  - `memory`；`memoryLimit` 是容器的上限，不知道时是 0。
  - `diskFree`：数据目录所在的磁盘剩的比例，0 到 1。
  - `backup`、`backups`、`backupAt`：最新一份备份写的时间、一共几份，以及最新那份是几点写的（UTC+8，"08:12"）。**跟假服务器不同**：备份不是在固定的钟点写，而是每天过了 UTC 0 点第一次检查时写，所以写最新那份的钟点。
- **`relays`**：公共中转站。
  - `count`、`online`、`names`；
  - `full`：满了的；
  - `busy`：没满，但同时在用的设备到了容量的四分之三，或者 CPU 到了"满"那条线（90%）的四分之三。
- **`counts`**：
  - `people`、`newThisWeek`（7 天内开通的）；
  - `devices`（所有设备行，登出的、过期的也算）、`online`；
  - `lans`、`lanPeople`（每个局域网平均几个已加入的人，一位小数）；
  - `browsers`：在浏览器里登录着的人数，不是会话数。
- **跟假服务器不同**：`recent` 在服务器的内存里，服务器重启以后是空的。

### `GET backups`

`[]OpsBackup`，新的在前：`name`、`size`、`written`。没有备份时是 `[]`。

### `GET people?q=&filter=&offset=&limit=`

`{"total", "people": []OpsPerson}`。

- `q`：在名字或邮箱里找，不分大小写。
- `filter`：`new`（7 天内开通的）、`online`（有设备在线）、`operators`、`test`（种子文件里的测试账号）；别的值回 400。
- `limit` 默认 30，最多 100。
- **跟假服务器不同**，先后是：开通得早的在前，同时开通的按邮箱；`filter=new` 时新的在前。
- `lastSeen`：他的设备里最近的那次在线；没有设备时不出现。

### `GET people/{id}`

`OpsPersonDetail`；没有这个人，404"没有这个人了"。

- `deviceList`：按名字排。
- `seats`：
  - `used`；
  - `limit`：现在管用的上限，0 是不限；
  - `own`：是不是单独调过；
  - `default`：部署的默认，0 是不限。
- `browsers`：在几个浏览器里登录着（没过期的会话），`newest` 是最近那次登录的时间。
- `lans`：`count` 是加入了的局域网个数，`owned` 是自己建的个数。
- `owned`：自己建的、里面还有别人的局域网，按建的先后排。
  - `id`、`name`、`others`（除他以外已加入的人数）、`first`（最早加入的那个人的显示名，注销时会转给他）。
  - 这是后台唯一出现局域网名字和成员名字的地方。
- `relays`：他自己的中转站几台（"我的服务器""家里的设备"，不算公共的）。

### `GET devices?q=&state=&offset=&limit=`

`{"total", "all", "online", "devices": []OpsDevice}`。

- `q`：在设备名、主人的名字和邮箱里找。
- `state`：`online`、`offline`、`loggedOut`、`expired`；别的值回 400。
- `all`、`online`：全部设备的数，不受筛选影响。
- `address` 是 Deneb 分的 IPv4。
- **跟假服务器不同**，先后是：按主人的邮箱，再按设备名。

### `GET logins`

`{"logins": []OpsLogin, "accounts": []OpsWho}`。

- `logins`：等着批准的登录，早的在前。`id` 是后台自己的编号，**不是**登录码，登录码不出服务器。
- `accounts`：能批准进去的测试账号。`name` 放的是邮箱，和假服务器一样。

### `GET records?before=&limit=`

`[]OpsRecord`，新的在前。

- `limit` 默认 200，最多 1000。
- `before` 是 RFC 3339 的时间，要这之前的，用来往前翻。
- `by`：做事的部署管理员。服务器上用管理命令做的，`by.id` 是空的，`by.name` 是"服务器上的命令"。
- `text`：那句话，不带做事的人，网页在前面加 `by.name`，比如"替 Leo（leo@example.com）注销了账号，「家里」转给了阿杰"。
- 记录留 1 年，每天的清理删掉更早的。

### `GET log?kind=&after=&before=`

`{"lines": []OpsLine, "more", "last", "note"}`。今天和昨天的日志（按 UTC 分天）。

- **`seq`**：只拿来比大小、当游标，别拿它算数。它由日期和这一行在文件里的位置拼成，一天过去、文件变长都不会变。
- **不带 `after`、`before`**：最后 200 行；`more` 说还能不能往前翻；`last` 是最新一行的 `seq`。
- **`before=<seq>`**：这之前的 200 行，旧的在前；`more` 说还有没有更早的。
  - **跟假服务器不同**：往前翻有没有到头，要看 `more`，别看回来了几行。网页现在用 `lines.length <= 200` 判断，要改。
- **`after=<seq>`**：这之后的全部，最多 2000 行。`last` 跟着往后走，筛了种类、一行也没回来时也一样。
  - 还没写完的半行不给，下一次再给整行。
- **2000 行**：不管怎么翻，最多给到最新的 2000 行，按筛过种类以后算。更早的到服务器上用 `deneb-control logs` 看。
- **`kind`**：`login`、`device`、`op`、`alert`、`error`、`other`。照每行的开头和里面的字分：
  - `alert …` 是报警；
  - 带 error、failed、panic、did not go out 的是出错；
  - `login`、`web login`、`mail`、`sign-up` 开头的是登录；
  - `device`、`publish` 开头的是设备；
  - `op`、`operator`、`admin:`、`account` 开头的是操作；
  - 剩下的是其他。
- **遮掉的**：
  - 带端口的地址（`203.0.113.7:41641`、`[2001:db8::7]:41641`）换成"‹地址›"；
  - 链接 `#` 后面的令牌换成"#‹…›"；
  - "code 123456"、"code … is 123456"里的 6 位数换成"‹码›"；
  - 32 位以上的一长串字母数字（令牌、钥匙、指纹）换成"‹…›"。
  - 不带端口的来源地址照留，隐私说明里写了日志里有它。
- **`note`**：服务器的日志不在文件里时（开发模式，或者 `-log-dir off`）给一句话，`lines` 是空的。本机要试日志，起服务器时带 `-log-dir <目录>`。

## 2. 做：`POST act`

请求 `{"act": "…", "args": {"…": "…"}}`，值都是字符串。

| act | args | 成功的回答 | 记录里的那句话 |
| --- | --- | --- | --- |
| `seats.set` | `person`、`limit` | `OpsPersonDetail` | 把 Mia 的位置上限调成了 8；恢复成了部署的默认（6 个）；改成了不限 |
| `sessions.revoke` | `person` | `OpsPersonDetail` | 让 Mia 在 2 个浏览器里都退出了（一个也没有时不记） |
| `operator.set` | `person`、`on`（`true` / `false`） | `OpsPersonDetail` | 把 Mia 设成了部署管理员；不让 Mia 管这个部署了 |
| `device.relogin` | `device` | `OpsDevice`（`state` 变成 `expired`） | 让 Leo 的「笔记本」重新登录 |
| `device.delete` | `device` | `{}` | 删了阿杰的「旧 NAS」 |
| `account.delete` | `person`、`confirm`、`lan:<编号>` | `{}` | 替 Leo（leo@…）注销了账号，「家里」转给了阿杰，删了「x」 |
| `login.approve` | `login`（`logins` 里的 `id`）、`email` | `{}` | 批准了「ThinkPad」登进 mia@deneb.test |

**规矩和拒绝**（`message` 都可以原样显示）：

- **没有这个人、这台设备**：404，"没有这个人了""没有这台设备了"。
- **`seats.set`**：
  - `limit` 是 1 到 10000，`0` 恢复部署的默认，`-1` 不限；别的回 400。
  - 正好等于部署默认的数，也算恢复默认（`own` 变成 false），所以"撤销"时把原来的数传回来就行。
- **`operator.set`**：不能撤自己，409"不能不让自己管：请另一位部署管理员来做"。
- **`device.relogin`**：
  - 已经登出的设备：409。
  - 已经过期的：照常回 200，什么都不变，也不记。
- **`account.delete`**：
  - `confirm` 是他的邮箱，去掉首尾空格、不分大小写；对不上回 400"邮箱对不上，再看一眼"，什么都不做。
  - 不能注销自己：409"自己的账号在账号卡片里注销"。
  - 种子文件里写着的账号：403，原因和自己注销一样。
  - `lan:<局域网编号>` 是 `transfer`（转给最早加入的人）或 `delete`（整个删掉），值不对回 400。`owned` 里的局域网每个都要选：漏了的，回 409，说出是哪几个。
  - 规则和自己注销（`account.delete` 操作）一样：设备、局域网里的位置、分享、邀请、他自己的中转站、浏览器里的登录一起没了。
  - 注销成功后，服务器在后台给本人发信"应你的要求，你的 Deneb 账号注销了"，样子和验证码邮件一样，不写是谁操作的。发信失败不回滚，只记一行日志。
- **`login.approve`**：
  - 那台设备已经不等了：404"这台设备已经不等了"；
  - `email` 不是测试账号：403。
- **不认识的 `act`**：400。

**公共中转站那一页**原有的操作也写记录，由部署管理员对公共中转站做的才记：

- 踢人"不让 Mia 用公共中转站「香港」"；撤销"又让 Mia 用公共中转站「香港」了"。是两条，不是设计稿里合成一句的"后来撤销了"。
- 调份额"把公共中转站「香港」的份额调成了 3"，调容量，改名。
- "为加一台公共中转站「新加坡」生成了安装命令"，作废安装命令。

**服务器上的管理命令**也写记录，`by` 是"服务器上的命令"：`seats`、`sessions -revoke`、`operators`、`device-delete`、`account-delete`（也给本人发信）、`approve`。

## 3. 数据库

加了一张表 `ops_records`，是第 11 个迁移：时间、谁（编号、名字、邮箱）、做了什么、对谁、那句话、结果。上线前请拿生产备份的副本跑一遍迁移。

## 4. 测试

本机，`go test -p 1`：

- `TestConsoleOnlyOperators`：所有地址和操作，非部署管理员 403，没登录 401，什么都不变。
- `TestConsoleViews`：人（筛选、找、分页、卡片里的设备、位置、局域网、`owned`）、设备（状态、找）、等着的登录（编号不是登录码）、概况。
- `TestConsoleActs`：七种操作各做一遍，连同拒绝的情况，以及记录的先后、文字、做事的人、日志行；替人注销后发信。
- `TestConsoleRelayRecords`：公共中转站上的踢人、撤销、调份额、加一台写进记录，自己的中转站不写。
- `TestConsoleLog`：遮掉的东西、分类、往前翻到 2000 行为止、`after` 只给新的、半行不给、没有日志文件时的 `note`。
- `TestOpsRecordsKeepAYear`：清理删掉一年以前的记录，别的不动。
- `TestDeletedMail`：注销通知信的样子，不写是谁做的。

## 5. 没做的

- 服务器上的公共中转站管理命令（`relay-add`、`relay-kick`、`relay-set` 这些）不写记录，只有网页那一页上的操作写。
- `recent`（好了的报警）在内存里，重启就没了。要留得久，得把报警存进数据库，这次没做。
