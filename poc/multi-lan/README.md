# poc/multi-lan：一台设备同时在两个局域网

验证 Deneb 和 Tailscale 最根本的差异：一台设备同时在线于多个局域网。结论和实测数据见 [`design/research/multi-lan-tech.md`](../../design/research/multi-lan-tech.md)，这里只讲怎么跑。

这是一次性原型，不是产品代码。全部跑在用户态：不建网卡，不改路由、DNS、防火墙，也不需要管理员权限。每台机器上只有一个普通进程，占一个 UDP 端口，外加一个只听本机回环的控制接口。

## 测试拓扑

| 设备 | 跑在 | 局域网 | 开放的服务 |
| --- | --- | --- | --- |
| `mac.me` | 本机 Mac | 工作室 `studio`、周末联机 `weekend` | 工作室：ping、网页 :80、计时流 :7000、测速 :9000、:8080（给内核网卡模式下本机普通程序开的服务用）；周末联机：ping、:7000、:9000（不开 :80 和 :8080，用来测按局域网开放） |
| `box.lin` | hk-155 | 工作室 | ping、:80、:7000、:9000 |
| `box.jie` | Windows 测试机 | 周末联机 | 同上 |
| `nas.jie` | hk-spare | 周末联机 | 同上 |

- 中转站：hk-spare 上现成的 derper（`derp.example.com`，TCP 33443，STUN 3478）。原型只是它的普通客户端，没改它的任何配置。
- 两台 `box` 同名，用来测同名设备怎么区分。
- 地址：每台设备一个，从 `10.86.0.0/16` 依次分配（mac .1、box.lin .2、box.jie .3、nas.jie .4），在所有局域网里都是这一个。前几轮用户态测试用的是 `100.86.0.0/16`；后来查到 Tailscale 会对 100.64/10 段起反应，就换成了现在这一段（见文档 §3.3）。

## 需要什么

- 本机：Go 1.27 或更高，macOS（`-bind-if` 只在 macOS 上实现）。
- 能免密 ssh 到 `hk-155`、`hk-spare` 和 Windows 测试机；地址和登录参数在 `scripts/env.sh` 里。
- `topology.json` 里写着中转站和两台服务器的公网地址。换机器时改这两个文件。

## 跑起来

在本目录下执行。

1. 生成每台设备的配置。这一步扮演控制面：只给每台设备下发和它同在某个局域网的对端。密钥存在 `configs/keys/`，重复生成时不会变。

   ```bash
   go run . gen -topology topology.json -out configs
   ```

2. 编译并把二进制和各自的配置拷过去：hk-155、hk-spare 拷到 `/tmp/deneb-poc/`，Windows 拷到 `%USERPROFILE%\deneb-poc\`。Windows 那条线路慢，上传约 3 分钟。

   ```bash
   ./scripts/deploy.sh
   ```

3. 启动远端三个节点。这个脚本不会自己退出，因为 Windows 节点挂在这条 ssh 会话里，另开一个终端继续下面的步骤。

   ```bash
   ./scripts/start-remote.sh
   ```

4. 启动本机节点。本机开着 Clash TUN 时一定要带 `-bind-if en0`，否则打洞流量会被 Clash 转到代理节点上（见文档 §6.5）。

   ```bash
   ./scripts/start-mac.sh -bind-if en0
   ```

5. 看状态，试一试：

   ```bash
   ./scripts/api.sh mac /status
   ```

   ```bash
   curl --socks5-hostname 127.0.0.1:41080 http://box.lin.deneb.internal/
   ```

   ```bash
   curl --socks5-hostname 127.0.0.1:41080 http://box.jie.deneb.internal/
   ```

6. 跑测量脚本，原始结果存在 `results/raw/`：

   ```bash
   ./scripts/test-startup.sh bound -bind-if en0
   ```

   ```bash
   ./scripts/test-switch.sh box.lin 10 30 80
   ```

   ```bash
   ./scripts/test-pause.sh weekend 8 18 30
   ```

   - `test-startup.sh`：重启本机节点，从第一刻起 ping 三台设备，看"先经中转连上、再换直连"。
   - `test-switch.sh`：第 10 秒起丢弃本机和 box.lin 之间的所有 UDP（在原型里丢，不碰防火墙），第 30 秒恢复。期间 ping 一直在跑，还挂着一条 TCP 长连接。
   - `test-pause.sh`：第 8 秒暂停周末联机，第 18 秒恢复，看工作室是否受影响。

7. 停掉所有节点：

   ```bash
   ./scripts/stop.sh
   ```

   拷到远端的文件不会自动删。要删就到 hk-155、hk-spare 上执行 `rm -rf /tmp/deneb-poc`，在 Windows 上删 `%USERPROFILE%\deneb-poc`。

### 隔离攻击测试

借道攻击：让 box.lin 把 box.jie 的地址路由给 Mac，再试着访问。预期 ping 和网页都不通，Mac 日志里出现 `drop in transit-in`。

```bash
ssh hk-155 "pkill -f '^./deneb-poc-linux-amd64'; cd /tmp/deneb-poc; nohup ./deneb-poc-linux-amd64 node -config box.lin.json -attack-route mac.me=10.86.0.3/32 > node-attack.log 2>&1 < /dev/null &"
```

```bash
./scripts/api.sh lin '/ping?to=10.86.0.3&n=10&summary=1'
```

伪造来源：Mac 绕过自己的过滤，发出源地址冒充 box.lin、目的是 box.jie 的包。预期 Windows 节点日志出现 `disallowed source address`，这是 WireGuard 在接收方做的来源检查。

```bash
./scripts/api.sh mac -X POST '/inject?src=10.86.0.2&dst=10.86.0.3&dport=80'
```

### 内核网卡模式（只在本机 Mac 上，需要 sudo）

前面的步骤全在用户态，本机程序要经 SOCKS5 才能访问。这个模式改成正式产品在电脑上的做法：系统里多一张 Deneb 网卡，普通程序直接就能用。

它会改本机的三样东西，退出时全部撤掉：

- 一张 utun 网卡，地址 10.86.0.1；
- 一条 `10.86.0.0/16` 路由，指向这张网卡；
- 文件 `/etc/resolver/deneb.internal`，只影响以 `.deneb.internal` 结尾的名字。

Clash、Tailscale、系统 DNS 服务器、默认路由、pf 防火墙和 IP 转发都不动。地址段特意不用 100.64/10：Tailscale 一看到别的网卡用这一段，就会改自己的路由。也不用 198.18/15，那是 Clash 的地址段。

1. 先空跑检查。不需要 sudo，什么都不改，只确认这个地址段在本机没人用：

   ```bash
   ./bin/deneb-poc node -config configs/mac.me.json -tun -preflight
   ```

2. 启动。在一个保持打开的终端里执行，会要一次密码。按 Ctrl-C 结束，三样改动随即撤掉，并在 `/tmp/deneb-tun-before-*.txt` 和 `/tmp/deneb-tun-after-*.txt` 里留下改动前后的路由表和 DNS 配置，方便对比：

   ```bash
   sudo ./scripts/start-mac-tun.sh
   ```

3. 试一试。不设代理，直接访问：

   ```bash
   curl http://box.lin.deneb.internal/
   ```

   ```bash
   ping 10.86.0.3
   ```

4. 不想回到那个终端时，也可以用控制接口结束。效果和 Ctrl-C 一样：

   ```bash
   curl -X POST http://127.0.0.1:47777/shutdown
   ```

5. 只有节点被强行杀掉、来不及清理时，才需要执行：

   ```bash
   sudo ./scripts/cleanup-mac-tun.sh
   ```

`scripts/monitor-coexist.sh` 会在测试期间每 2 秒检查一次：经 Clash 能不能上外网、Tailscale 的路由有没有变，并挂一条经 Tailscale 的 SSH 心跳。结果写在 `results/raw/mon-*.log`。

### 排查"Deneb 是否拖慢 Clash"

这一组测试在用户态里跑，不改本机网络。远端节点和本机节点要按第 3、4 步先跑起来，Windows 上要先有一个 20MB 的测试文件 `deneb-poc\blob20m.bin`。

```bash
./scripts/repro-clash-stall.sh
```

```bash
python3 scripts/analyze-repro.py results/raw/repro-HHMMSS
```

脚本每秒探测一次经 Clash 访问 Google 和直连国内网站，同时通过 mihomo 的本地控制套接字只读接收警告级日志（只用 `GET /logs`）。然后把这几个操作各做两次，每次之后观察 60 秒，看有没有引起超时：

- 经 Deneb 拉 20MB；
- 不经 Deneb、用 scp 拉 20MB，作为对照；
- 暂停再恢复工作室。

结论见文档 §6.9。

### 过滤规则的单元测试

```bash
go test ./...
```

## 节点参数

| 参数 | 默认 | 作用 |
| --- | --- | --- |
| `-config` | 无 | `gen` 生成的设备配置 |
| `-api` | `127.0.0.1:47777` | 控制接口，只听本机 |
| `-socks` | 不开 | 给本机程序用的 SOCKS5，名字在原型内部解析 |
| `-bind-if` | 不绑 | 把对外的 UDP 和中转 TLS 连接绑到指定网卡，用来绕开代理软件的 TUN（只在 macOS 上实现） |
| `-heartbeat` / `-trust` | `3s` / `6.5s` | 直连心跳间隔；多久收不到回应就不再信任直连。默认值和 Tailscale 一样 |
| `-paused` | 无 | 启动时先暂停这些局域网 |
| `-tun` / `-preflight` | 关 | macOS，需要 root：改用一张真实的 utun，加一条整段路由；`-preflight` 只检查地址段在本机是否没人用，什么都不改 |
| `-attack-route`、`-insecure-no-filter` | 无 | 只给攻击测试用 |

## 控制接口

| 路径 | 作用 |
| --- | --- |
| `GET /status` | 局域网开关、每个对端的路径（直连还是中转、地址、延迟、收发计数）、过滤丢包计数 |
| `GET /ping?to=名字或地址&n=&interval=&timeout=` | 经隧道发 UDP 回显（端口 7），记录每个包发出时走的路径 |
| `GET /http?url=` | 经隧道取网页 |
| `GET /resolve?name=` | 按名字规则解析，有歧义时列出候选 |
| `GET /tick?to=&secs=` | 挂一条 TCP 长连接（对端每 100ms 发一行），统计停顿时长、有没有断线 |
| `GET /pull?to=&bytes=` | 测下载速度 |
| `POST /block?peer=&on=1\|0` | 模拟网络开始丢 UDP，以及恢复 |
| `POST /lan?id=&on=0\|1` | 暂停、恢复一个局域网（对应场景 3 里的开关） |
| `POST /inject?src=&dst=&dport=` | 伪造来源地址注入，只给测试用 |
| `POST /shutdown` | 让节点自己退出；内核网卡模式下先删路由和网卡 |
| `GET /events?since=毫秒时间戳` | 路径切换等事件，带时间 |

## 代码地图

| 文件 | 内容 |
| --- | --- |
| `magicsock.go` | 一个 UDP 端口加一条 DERP 连接服务所有局域网。负责探测（ping、pong、告知地址）、直连和中转之间的切换，以及给 wireguard-go 用的 `conn.Bind` |
| `filter.go` | 局域网隔离：只收发自己地址的包，按"共同的局域网"查开放的服务，记住连接以便放行回包 |
| `node.go` | 组装：一个 wireguard-go 设备、一个用户态网络栈、设备表的下发和局域网暂停 |
| `names.go` | 名字规则：`设备.主人.deneb.internal`；短名有歧义时不猜 |
| `config.go` | 配置格式；`gen` 子命令扮演控制面 |
| `tun_darwin.go`、`dns.go`、`overlay.go` | 内核网卡模式：建 utun 和路由，空跑检查；只回答 `deneb.internal` 的 DNS；让本机代码在两种模式下用同一套接口收发 |
| `services.go`、`socks.go`、`api.go` | 测试用的小服务、本机 SOCKS5、控制接口 |

## 和推荐方案的差别

原型做了简化：

- 内核网卡只做了 macOS，远端三台都跑在用户态。
- 没有真正的控制面服务器，配置是生成好的文件。
- 只有 IPv4。
- 只用一个中转站。
- 探测消息直接用 WireGuard 密钥加密；Tailscale 用的是单独的 disco 密钥。
- 心跳一直在跑；Tailscale 只在有流量时才心跳。

推荐方案本身见文档 §4。
