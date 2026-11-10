# 下一轮上线：等用户在场时一起做

## 11-09 21:1x 起已上线（用户点头："现在做"）

- **控制服务器**（主会话做）：
  - 21:1x 在 hk-155 上用线上数据的一致副本跑 `TestProdCopy`，数据没离开服务器，测完删了：迁到第 10 版，每个范围的中转站是 `[r_5uppwwtduq hk]`，设备都照常；
  - 21:17 部署，停了 0.73 s；新种子换上，五台设备 2 s 内都常驻公共的香港；
  - 21:18 换签名密钥：停服务、删掉旧的 `secret`、重启，服务器自己生成新的，停了 0.36 s；五台设备 1 s 内都自己重新认证；数据目录的文件都还归 nobody；
  - `deneb-control operators -add kittors@deneb.test`，kittors 是部署管理员。
- **引擎**：引擎线还在改工作区，所以把引擎用到的代码拷到 `/tmp/nr-tree` 冻结（21:30:14），在拷贝上跑全套测试，全过以后编 `m2-11092136`。
  - 21:37 换了构建服务器、NAS、台式机（`deploy-nodes.sh swap`，`previous` 可回滚）；测试 Mac 单独换，旧版留作 `~/deneb/denebd.m2-11090126`。测试 Mac 中间停了约 1 分钟：主会话用变量重起，测试 Mac 的 zsh 不拆分参数，起失败了，随后按原参数补起。
  - 回归：五台都连着、都登录，从用户的 Mac ping 四台都通。
  - **用户的 Mac 没换**，还是 m2-11090126。换它要先在内核模式重跑一遍第 5 步，因为引擎已经不是 07:59 那一版了。
- **真机验**：
  - M4 r1 的设备那一半、r2：通过（m4-acceptance.md）；
  - M3 收回分享当场断开：通过（m3-acceptance.md）；
  - M2 空闲后第一个包：通过。6 组第一个回应 4–76 ms，两边都两路发、收端去重；跟 NAS 有关的两组后面的包卡了 0.7–0.9 s，交引擎线（m2-acceptance.md）。
- **补换两台**（22:27）：测试 Mac 和台式机换成 m2-11092227（工作区 22:20:58 的冻结拷贝 `/tmp/nr-tree2`，全套测试过），带引擎线的中转站修正，用来复测 r2，复测通过。构建服务器和 NAS 还是 m2-11092136。测试 Mac 的旧版留作 `denebd.m2-11092136`。
- **收尾**（21:57）：五台都连着、都登录、都常驻公共的香港；hk-155、hk-spare 上的容器都健康。

2026-11-09 凌晨做完、本地测过的几样，都要动线上才能生效。两条线的自动模式都把线上改动拦下了，要用户本人点头，所以攒成一轮，用户在的时候一次做完。整轮大约 40 分钟，用户要做的只有点头（要输密码的那一步 11-09 07:59 已提前做完）。

## 上什么

1. **控制服务器**（hk-155，一次部署，停顿约半秒）：
   - 新代码，带两个数据库迁移（9、10）。部署前照例拿 hk-155 的数据备份跑一遍 `TestProdCopy`，这次不能打印设置表：
     - 设备能力 `Caps`（迁移 9）："握手两路发"要靠它把对端支不支持告诉设备（[m4/control.md](m4/control.md) §18，本机测过）；
     - 部署管理员和运营页的接口（迁移 10，§19，本机测过）。部署后用 `deneb-control operators -add` 把 kittors 设成部署管理员，网页上的"运营"页等用户看过 M4-13 的设计再接；
   - 新的默认中转站顺序：Deneb 的公共中转站"香港"排到兜底第一个，旧的 `hk` 留在后面当最后一道兜底。新种子已经用线上数据的副本试过，在 hk-155 的 `data/seed.json.new`（[m4/control.md](m4/control.md) §17）；
   - 换签名密钥：控制线 11-09 凌晨导出线上数据副本时，不小心把它打印进了自己的会话记录。做法是删掉设置里的 `secret`，重启时服务器自己生成新的。设备令牌失效以后，引擎拿到 401 会丢掉旧令牌、重新认证（`engine/controlclient/client.go` 的 `call` 和 `Stream`）。网页上登录着的人要不要重新登录，部署前由控制线核实。
2. **引擎**，从工作区编一版，带：
   - 收回分享、关掉服务、不让某人用、暂停时，已经开着的 TCP 连接两头当场断开（RST），平时每对包只多约 10 ns（[m2/engine.md](m2/engine.md)）；
   - 长时间空闲后，握手两路都发：只在对端声明支持时这样做，收端去重；老版本对端照旧只走一条路。

## 顺序

1. 控制服务器：备份 → 停 → 换 → 起，量停顿。前后给五台设备各拍一次快照，逐项比对。回滚用 `deploy/control/rollback-hk155.sh`。
2. 4 台测试设备换新引擎：构建服务器、阿杰的 NAS、阿杰的台式机、测试 Mac。用 `test/topo/deploy-nodes.sh`，测试 Mac 单独换。换完回归：全直连，心跳延迟和换版前相当。不行就 `deploy-nodes.sh previous`。
3. 用户的 Mac 换新引擎。只换 Deneb 的引擎，不影响网络和 Clash。
4. 真机验：
   - M4 [r1 的设备那一半和 r2](m4-acceptance.md)：
     - 挡掉 UDP，改经公共的中转；
     - 把"我的服务器"拖进来，改走它；
     - 停掉它，10 s 内改走公共的，不出现"断开"；
     - 拿掉，能撤销。
   - M3：开着长连接时收回分享，两头当场断开。
   - M2：空闲 10 分钟以后的第一个包；`debug paths` 里两台的 `hsBoth` 都是 true。
5. ~~M2 第 7 条的 Clash 共存~~：11-09 07:59 已提前做完，37 项全过，见 [m2-acceptance.md](m2-acceptance.md)。用的就是当前代码编的这一版引擎；上线前引擎代码要是又改了，再照原样跑一次：重编 `test/topo/.accept/kernel-denebd`，先开 `test/topo/monitor-coexist.sh`，再 `SUDO_ASKPASS=$PWD/test/topo/askpass.sh test/topo/kernel-round.sh`。

## 用户要定的

- 点头上这一轮；
- ~~输一次密码（第 5 步）~~：11-09 07:59 已输过，这一步做完了；
- 看设计：M3 三批，M4 第 1 批（客户端线正在做）。看过满意了，桌面端代码再进仓库；
- 要不要提交。现在什么都没提交。

## 再下一轮：控制线 11-10 攒的（都在工作区、本机测过，等用户点头）

- **控制服务器**：一次部署，没有新的数据库迁移。
  - M7 安全审查的修复（[m7/control-security.md](m7/control-security.md)）；
  - 每天的备份不再带签名密钥（[m7/control-ops.md](m7/control-ops.md) §1）；
  - 新操作 `device.delete` 和管理命令 `devices`、`device-delete`、`sessions`、`backups`。
- **部署后**：
  1. 在 hk-155 上跑一次恢复演练：`HOST=hk-155 deploy/control/restore-drill.sh`。会在服务器 `/tmp` 放一分钟文件。
  2. **待删**：mia 名下的 `deneb-u26`（客户端线 Linux 虚拟机里测完留下的，已登出），主会话来删：
     - `docker exec deneb-control deneb-control devices -person mia@deneb.test`，找出它的 id；
     - `docker exec deneb-control deneb-control device-delete -device <id>`。
