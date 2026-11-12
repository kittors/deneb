# M7 · 网页后台正式上线（放在 hk-spare）

> 控制线，2026-11-12。用户 11-12 定了"给域名，正式上"，又定了网页后台放 hk-spare，原话是"就放 hk-spare"。方案、脚本、代码都做好了，本机测过，上线由主会话来做。
>
> 本文里的域名写成 `deneb.<域名>`，脚本和模板里写成 `deneb.example.com`；hk-spare 的地址一律不写。真值只放在 `deploy/web/site.hkspare.env` 里，这个文件不进仓库。
>
> 原先写的是放 hk-155。hk-155 上什么都没放过（主会话只读核对过），那几个脚本改名成了 `*-hkspare.sh`。

## 0. 一句话

- **网页后台**：用一个子域名 `deneb.<域名>`，放在 hk-spare。
  - hk-spare 上用户自己的 nginx 管 443，它的 `conf.d/` 里新加一个文件，把这个子域名转给同一台机器上的 deneb-web。
  - deneb-web 跨机器连 hk-155 上的控制服务器，走公网 TCP，TLS 按指纹认，和设备一样。
- **证书**：certbot 用 Cloudflare 做 DNS 验证，和 hk-spare 上别的子域名一样，凭证用机器上已有的那份。
- **控制服务器**：IP、端口、证书都不动，已经装好的客户端什么都不用改。
- **邀请链接**：控制服务器加 `-web-url`，新的邀请链接打开网页版，旧链接跳过去。
- **来源地址**：控制服务器加 `-trust-proxy <hk-spare 的公网地址>`，只信从 hk-spare 来的请求带的来源地址。

## 1. 放在 hk-spare 以后是什么样

```
浏览器 ──https──▶ hk-spare：nginx（443，正式证书）
                     └─http─▶ deneb-web（127.0.0.1:47447，网页的文件；/api/ 往下转）
                                 └─https，认指纹，公网 TCP─▶ hk-155：控制服务器（47443）
设备、客户端、中转站 ──────────────────────────────────────▶ hk-155：控制服务器（照旧）
```

- **为什么放 hk-spare**：用户定的。
- **hk-spare 上用户自己的东西，一个都不碰**：
  - clirelay 的数据库（patroni、etcd、redis）；
  - derper（旧中转站 `hk`）、deneb-relay、deneb-node；
  - nginx 的 `conf.d/` 里现有的几个文件。
- **只多出三样东西**：一个 deneb-web 容器（只听本机端口）、一张证书、一个 nginx 站点文件。
- **两台机器之间**：hk-spare 到 hk-155 的 47443 走的是 TCP。11-11 看门狗互探时，两台之间的 HTTPS 是通的；挡掉的只是 UDP（[next-round.md](../next-round.md) 11-11 那一轮）。

## 2. 哪边挂了会怎样

**网页这边挂了**：deneb-web、hk-spare 的 nginx，或者整台 hk-spare。

- 网页后台打不开，新的邀请链接在浏览器里也打不开。
- 装了客户端的人，把链接粘进客户端照样能接受：客户端只看路径里的 `/i/<码>`，接受走的是 hk-155 上的控制服务器。
- 设备、客户端、组网都照常：它们直接连 hk-155，不经过 hk-spare。
- 整台 hk-spare 挂了的话，它上面的旧中转站 `hk` 也跟着挂，设备改走 hk-155 上的公共中转站（[control-ops.md](control-ops.md) §5）。
- 要是一时修不好：把 `WEB_LIVE` 改回 `no`，重新部署控制服务器。邀请链接就回到控制服务器自己的落地页，不再指向打不开的网页。

**控制服务器挂了**：hk-155。

- 网页的页面还打得开，因为网页的文件在 hk-spare 上；但登录、看局域网、做任何操作都会报"连不上控制服务器"。推送流断开，控制服务器回来后浏览器自己重连。
- 设备之间照常通，详见 [control-ops.md](control-ops.md) §5；不能新登录、不能做改动。
- hk-155 上的公共中转站跟着挂，设备改走 hk-spare 上的 `hk`。

**两台之间不通**（hk-spare 连不上 hk-155 的 47443）：网页和"控制服务器挂了"一样报错，设备和客户端不受影响。

**两台都挂了**：网页和控制服务器都用不了，已经直连上的设备之间照常通。

## 3. 域名和解析

- **一个子域名，只给网页**：网页的文件和 `/api/` 都在 `https://deneb.<域名>` 下。
- **解析用户已经加好**：A 记录，名称 `deneb`，指向 hk-spare，仅 DNS。有了这条明确的记录，这个名字的任何类型都不再落到域名的通配符上。
- **控制服务器不换域名**：
  - 已经装好的客户端认的是 IP 和自签证书的指纹，一换就连不上了；网页上线也用不着动它。
  - 以后做备机（[control-ops.md](control-ops.md) §5 第 1 条）再加 `control.<域名>`。
- **域名和 hk-spare 的地址都不进仓库**：
  - 脚本从 `deploy/web/site.hkspare.env` 里读，这个文件被 `deploy/web/.gitignore` 挡在仓库外，写法见 `site.example.env`；
  - nginx 的站点文件只在 hk-spare 上；
  - 提交说明里也不写。
- **但域名本身会公开**：邀请链接里有它，证书会被记进公开的证书透明日志。

## 4. 部署：deneb-web 单独一个容器，在 hk-spare

`deploy/web/` 下的脚本：

| 脚本 | 做什么 |
| --- | --- |
| `build.sh` | 把 deneb-web 编成 Linux 的静态程序；从 `web/app/src` 构建网页，在临时目录里构建，仓库里的 `web/app/dist` 不动 |
| `Dockerfile` | 空镜像，只有 deneb-web 和网页文件，以 nobody 运行 |
| `deploy-hkspare.sh` | 构建、上传、起容器，上一版镜像留作 `deneb-web:prev`；最后在 hk-spare 上自查：网页 200，`/api/` 经它跨机器到控制服务器得到 401 |
| `rollback-hkspare.sh` | 换回上一版；带 `-remove` 是整个拿掉 |
| `cert-hkspare.sh` | 用 certbot 拿证书（§5） |
| `nginx-hkspare.sh` | `show` 在本机打出站点文件；`install` 放进 nginx 并平滑 reload；`remove` 拿出来（§5） |
| `check-local.sh` | 在本机把 nginx、deneb-web、控制服务器整条链跑一遍（§10） |

容器：

```
docker run -d --name deneb-web --network host --restart unless-stopped --read-only \
  --cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 128 \
  --log-opt max-size=10m --log-opt max-file=3 \
  deneb-web:v0 -static /app -listen 127.0.0.1:47447 \
  -control https://103.42.31.155:47443 -control-pin <hk-155 上 data/tls/pin.txt 里的指纹>
```

- 只听 127.0.0.1，外面连不到，只有 nginx 转得进来。
- 端口默认 47447，部署脚本会先查它有没有被占用；占了就停，换一个写进 `site.hkspare.env`。
- 指纹：`site.hkspare.env` 里没写，部署脚本就到 hk-155 上读 `pin.txt`（只读）。
- 不开新的对外端口，不动防火墙，不动别的容器。
- deneb-web 不存任何东西；重新部署只会让浏览器的推送流断一下，马上自己连回来。

## 5. 证书和 nginx（hk-spare）

### 证书

`cert-hkspare.sh` 在 hk-spare 上跑：

```
certbot certonly --dns-cloudflare --dns-cloudflare-credentials <CF_CREDENTIALS> \
  --dns-cloudflare-propagation-seconds 30 -d deneb.<域名> \
  --non-interactive --keep-until-expiring --deploy-hook 'nginx -t && systemctl reload nginx'
```

- 和 hk-spare 上别的子域名一样，走 Cloudflare 的 DNS 验证，用机器上已有的凭证文件。
  - 凭证文件的路径写在 `site.hkspare.env` 里。
  - 脚本只把路径交给 certbot，不读、不打印、不拷它的内容。
- 续期跟着机器上现有的 certbot 定时任务走。每次续期以后，先 `nginx -t`，过了才 reload。

### nginx：只加一个文件

`nginx-hkspare.sh install` 只往 `/etc/nginx/conf.d/` 里加一个文件，由 `nginx.conf.template` 生成。现有的 `00-map.conf`、`90-clirelay-body-size.conf`、`relay.<域名>.conf` 一个都不动。

**文件名是 `zz-deneb.<域名>.conf`，不是 `deneb.<域名>.conf`**：

- 同一个地址和端口上，nginx 把最先读到的 server 当默认站点，除非哪个 server 写了 `default_server`。没对上域名的请求、直接用 IP 访问的请求，都归默认站点。
- `conf.d/` 按文件名顺序读，`deneb.` 排在 `relay.` 前面。要是 relay 那个文件没写 `default_server`，新站点就会把 relay 的默认站点抢过来。
- 所以加了 `zz-`，排在现有文件后面。
- 安装脚本还会查一遍，有下面的情况就停，不往下做：
  - `conf.d/` 里有别的站点文件排在它后面；
  - `sites-enabled/` 里有站点在监听、却没写 `default_server`（`sites-enabled` 在 `conf.d` 之后读）。

  主会话看过、确认没问题以后，可以设 `NGINX_ORDER_CHECKED=yes` 跳过这一步。

**站点文件的要点**：

- **listen**：照 relay 那个站点的写法抄，写在 `site.hkspare.env` 的 `NGINX_LISTEN_80` 和 `NGINX_LISTEN_443` 里，可以有多条。
  - 写法要和别的站点一样。只写 `443` 的站点和写了"IP:443"的站点混在一起时，带 IP 的那个会把这个地址整个占走。
  - 自己不写 http2：同一个端口上，http2 是所有站点一起开、一起关的。
- **80**：只做一件事，跳到 https。
- **443**：
  - 用上面的证书，整站反向代理到 127.0.0.1:47447；
  - `proxy_buffering off`，推送流一帧一帧地到；超时 1 小时，推送流每 15 秒有一次心跳；
  - `X-Real-IP` 设成 nginx 看到的地址，覆盖浏览器自己带的；
  - `proxy_cookie_flags ~ secure` 把 cookie 的 Secure 补回去（要 nginx 1.19.3 以上；更老的版本，模板注释里写了替代的写法）。
- **日志**：`access_log off`，因为 `/i/<码>`、`/api/v1/invites/<码>` 的路径里就是邀请码；错误日志只记 crit 级，因为 error 级的每一行都带着请求的路径。

**脚本的别的保护**：

- 同名文件已经在、又不是这个脚本做的（第一行有标记）：不动。
- 证书还没拿到：不做。
- 先 `nginx -t`，过了才 `systemctl reload nginx`。reload 是平滑的，别的站点正在连着的不断。
- `nginx -t` 不过：文件放回原样，不 reload。
- `remove` 也一样：先拿出来，`nginx -t` 过了才 reload，不过就放回去。

## 6. 代码改动

**控制服务器**

- **`-web-url https://deneb.<域名>`**（上一版已经做了）：
  - 邀请链接都改成网页上的 `/i/<码>`。链接是看的时候现算的，还没用的邀请也会马上换成新链接。
  - 控制服务器自己的 `GET /i/<码>` 改成 302 跳过去，带 `Referrer-Policy: no-referrer`。
  - 网页接口的同源检查把这个地址算作受信的来源，不带 `Sec-Fetch-Site` 的旧浏览器（比如 iOS 16.4 以前的 Safari）也能用。
  - `-public-url` 不动：设备登录链接、中转站加入码里的控制服务器地址、cookie 的 Secure 都还按它来。
- **`-trust-proxy <地址>`**（这一版，取代上一版的 `-trust-local-proxy`）：
  - 从列出的地址来的请求，拿 `X-Real-IP` 当来源地址，也就是 hk-spare 上 nginx 看到的、deneb-web 原样转过来的那个。可以写几个，逗号分开，也可以写网段。
  - 从别处来的请求，这个头写什么都不认。
  - deneb-web 不在本机，所以只信本机已经不够；要信的是 hk-spare 的公网地址。
  - hk-spare 上用户自己的别的程序，理论上也能带这个头，但它们是用户自己的；网上的别人做不到。
  - 不加这一条的话，网页上所有人在控制服务器看来都是 hk-spare，按地址限验证码就成了大家共用一份额度，日志里也分不出谁是谁。
- **部署脚本**：`site.hkspare.env` 里 `WEB_LIVE=yes` 时，`deploy/control/deploy-hk155.sh` 起控制服务器就带上 `-web-url` 和 `-trust-proxy`。
  - 回滚脚本也带上；回滚到的旧镜像要是不认 `-web-url`，就不带了，并说一声。
- **测试**：
  - `TestWebURL`：邀请链接、旧链接 302、旧浏览器的同源检查、会话记下的来源地址；
  - `TestTrustProxies`：只认列出的地址，本机没列出也不认；只认地址。

**deneb-web：不用改**

- nginx 设的 `X-Real-IP`，deneb-web 原样转给控制服务器：Go 的反向代理只去掉 X-Forwarded-* 这几个头。
- cookie 的 Secure 由 nginx 补回。

## 7. 邀请链接和客户端怎么过渡

- **新链接**（`https://deneb.<域名>/i/<码>`）：
  - 浏览器直接打开网页版，没有警告，没装客户端的人也能登录、接受；
  - 粘到客户端里也认。
- **旧链接**（`https://103.42.31.155:47443/i/<码>`）：
  - 浏览器先报一次证书警告（和现在一样），继续以后跳到网页上的邀请页；
  - 邀请 7 天过期，上线一周后旧链接就都没了。
- **已经装好的客户端**：什么都不用改，也不用升级。卡片上的邀请链接是服务器给的，会自己换成新的。
- **什么时候生效**：要等 hk-155 上的控制服务器换成带 `-web-url` 的这一版（连同 455c2ff 等）。这一步由主会话上线时做（§8 第 6 步）。

## 8. 上线：主会话按顺序做

动 hk-spare、hk-155 的每一步，主会话都照常先问过用户。

0. **写站点设置**：在本机写 `deploy/web/site.hkspare.env`，照 `site.example.env`。
   - `WEB_HOST=deneb.<域名>`
   - `WEB_PORT=47447`
   - `CONTROL_URL` 照例子；`CONTROL_PIN` 留空，到 hk-155 上读。
   - `NGINX_LISTEN_80` 和 `NGINX_LISTEN_443`：照抄 relay 站点的 listen 行，在 hk-spare 上只读查 `grep -h listen /etc/nginx/conf.d/relay.<域名>.conf`。
   - `NGINX_CONF` 留空，就是 `zz-deneb.<域名>.conf`。
   - `CF_CREDENTIALS`：那份凭证文件的路径。
   - `WEB_PROXY_IP`：hk-155 看到的、从 hk-spare 来的地址。第 2 步以后，可以在 hk-155 上只读查 `ss -tn '( sport = :47443 )'` 核对。
   - `WEB_LIVE=no`
1. **核对 DNS**：用户已经加好。在本机用 `dig +short deneb.<域名> @1.1.1.1` 看到 hk-spare 的地址。绕开本机代理的假 IP，所以指定解析服务器。
2. **起 deneb-web**：`deploy/web/deploy-hkspare.sh`。最后要看到网页 200、API 401，401 说明已经跨机器连到了控制服务器。这时外面还连不到它。
3. **拿证书**：`deploy/web/cert-hkspare.sh`。最后列出 `/etc/letsencrypt/live/deneb.<域名>/`。
4. **放进 nginx**：
   - `deploy/web/nginx-hkspare.sh show`，看一眼要放进去的文件；
   - `deploy/web/nginx-hkspare.sh install`，要看到 nginx -t 通过、"nginx reloaded"。
5. **从外面验**：
   - `curl -sI https://deneb.<域名>/` 得到 200，证书正常；
   - 浏览器打开，用测试账号登录，看得到局域网；打开一个邀请链接，能看到邀请；
   - relay 的站点照常：`curl -sI https://relay.<域名>/` 和上线前一样。
6. **切邀请链接**：
   - `WEB_LIVE=yes`，跑 `deploy/control/deploy-hk155.sh`。这一版同时带上 455c2ff 等改动，设备几秒内连回来。
   - 验：
     - `curl -sk -o /dev/null -w '%{redirect_url}' https://103.42.31.155:47443/i/x` 得到 `https://deneb.<域名>/i/x`；
     - 新邀请的链接是新域名的；
     - `deneb-control logs -grep 'web login'` 里，网页登录的地址是浏览器的，不是 hk-spare 的。
7. **以后**：
   - 看门狗加一项，探 `https://deneb.<域名>/`，证书还剩 14 天就提醒。域名写在服务器上的看门狗配置里。
   - 稳定一周后再定要不要加 HSTS。

## 9. 回滚

倒着来，哪一步出问题就从哪一步往回退：

- **邀请链接**：`WEB_LIVE=no`，再跑 `deploy/control/deploy-hk155.sh`，链接变回控制服务器的。
  - 已经发出去的新链接，要等网页恢复才能在浏览器里打开；粘进客户端照样能用。
  - 控制服务器本身要回滚：`deploy/control/rollback-hk155.sh`，它会看旧镜像认不认 `-web-url`。
- **nginx**：`deploy/web/nginx-hkspare.sh remove`，拿掉那一个文件，`nginx -t` 通过后平滑 reload。别的文件从头到尾没动过。
- **deneb-web**：`deploy/web/rollback-hkspare.sh` 换回上一版，或者带 `-remove` 整个拿掉。
- **证书**（可选）：在 hk-spare 上 `certbot delete --cert-name deneb.<域名>`。
- **DNS**（可选）：用户删掉那条 A 记录，这个名字回到通配符。

## 10. 本机测过的

- **控制服务器**：`TestWebURL`、`TestTrustProxies`；control、deneb-web、deneb-relay 的 `-race` 测试；`go vet`，也按 Linux 编译查过。
- **站点文件**：`nginx-hkspare.sh show` 打出来的文件（一条 listen 和多条 listen 都试了），在 nginx 1.27 和 stable 的镜像里 `nginx -t` 都通过。
- **安装和拿掉的保护**：在 nginx 镜像里把装和拆的那段原样跑了一遍（`systemctl` 换成了只打印的替身），11 种情况都对：
  1. 正常装上；
  2. 再装一次，换掉自己的文件；
  3. 文件名排在 relay 前面：拒绝；
  4. `sites-enabled` 里有站点没写 `default_server`：拒绝；
  5. 同上，主会话看过以后放行：装上；
  6. 写了 `default_server`、另有一行注释掉的 listen：不拦；
  7. 同名文件不是我们做的：不碰；
  8. `nginx -t` 不过：放回原样，不 reload；
  9. 没有证书：不做；
  10. 拿掉；
  11. 再拿一次：说没有，什么都不做。
- **镜像**：`build.sh` 和 `docker build` 都通过，镜像里的程序起得来。从源码构建出来的网页，和仓库里的 `web/app/dist` 一样。
- **整条链**（`check-local.sh`）：nginx（生成的站点文件、自签证书）→ deneb-web → 控制服务器（`-web-url`、`-trust-proxy 127.0.0.1`）：
  - 网页 200；
  - 邀请页 200；
  - API 没登录时 401；
  - 不带 `Sec-Fetch-Site` 的登录 200；
  - cookie 带 Secure、HttpOnly、SameSite=Lax；
  - 登录后视图 200；
  - 推送流的第一帧马上到；
  - 控制服务器看到的是 nginx 看到的地址，不是浏览器自己编的 `X-Real-IP`；
  - 控制服务器自己的邀请链接 302 到网页上。
- **没测的**：两台机器之间真的公网连接、真的证书和 DNS、hk-spare 上真的 nginx。上线步骤里都有对应的检查。

## 11. 以后再说

- **设备登录链接**（`/login?c=…`，命令行登录时用）：还在控制服务器上，浏览器打开会报证书不受信任。要挪到网页上，网页线得加一个"同意这台设备登录"的页面。
- **控制服务器换域名**：跟备机方案一起做（§3）。
- **HSTS、看门狗**：见 §8 第 7 步。

## 12. 上线实况（11-12 23:0x，主会话）

照 §8 做完。用户点头："你上线吧""就放 hk-spare""可以"（分流表那一行）。

- **0–3 步**：站点设置写在本机 `deploy/web/site.hkspare.env`（不进仓库）。22:21 起 deneb-web，网页 200、API 401；22:23 拿到证书，到 2027-02-10。签证书前看过 hk-spare 上 certbot 的四个全局钩子：同步 derp、hk2、hk3 的三个只认自己的证书，这次都直接退出；只有重载 nginx 那个跑了一次，配置没变。
- **方案没覆盖的一处：hk-spare 的 443 是 stream 按 SNI 分流的。**
  - `/etc/nginx/streams-enabled/sni-mux.conf` 的 map 里，relay、code 两个名字分到 `127.0.0.1:8444`，其余分到 `127.0.0.1:10443`（那里没有服务在听）。
  - 新站点照抄 relay 的 listen（`<hk-spare>:80`、`127.0.0.1:8444 ssl http2`），还得在 map 里 default 前面加一行 `deneb.<域名>  127.0.0.1:8444;`。用户点头后 23:02 加的，原文件备份在同一目录的 `sni-mux.conf.bak.20261112_230255`（不以 .conf 结尾，nginx 不读）。
  - 回滚：删掉这一行（或放回备份），`nginx -t`，平滑 reload。
- **因此拿不到浏览器的真实地址。**
  - stream 转给 8444 时，http 层看到的 `$remote_addr` 是 127.0.0.1，X-Real-IP 也就是 127.0.0.1。控制服务器信任 hk-spare 来的 X-Real-IP（`-trust-proxy`），所以网页请求的地址都记成 127.0.0.1：日志里看不出是谁，按地址限流对所有网页用户合在一起算。
  - 查过控制服务器没有按 127.0.0.1 给的特权：只有邮件、报警看目标地址是不是本机。
  - 要拿到真实地址，得在 stream 层开 proxy_protocol，8444 上 relay、code 的监听也要跟着改，要用户定（waiting-for-user.md）。
- **`nginx-hkspare.sh` 修了一处**：ssh 把参数交给远端 shell 再解析一遍，标记里的空格和 # 让第 4 个参数成了注释，install 一开头就停了（什么都没改，留下的临时文件已删）。install、remove 两处都加了单引号。
- **和上线无关的两件**：
  - 23:00 前后 hk-155 那边断了约一分钟：hk-spare 的测试节点 15:00:25–15:01:07 UTC 连不上控制服务器，15:01:35 恢复；主会话那台 Mac 连 hk-155 的 ssh 随后被拒了几分钟。
  - hk-spare 上 relay、code 返回 502：它们的后端 `127.0.0.1:8318` 没在跑，error.log 里 11-11 00:44 起就有。
- **5 步**：从 hk-spare 走 443 验，网页 200（证书通过）、API 401、邀请页 200、http 301 到 https；relay、code 和改之前一样。主会话那台 Mac 走平时的网络打开也是 200。
- **6 步**：控制服务器从 e85b7ff 的干净检出编。部署前在 hk-155 上用当天的一致备份跑 `TestProdCopy`，通过，副本删了；没有数据库迁移。23:06 换版，端口不在听 0.51 s，证书指纹没变，带上了 `-web-url` 和 `-trust-proxy`。旧链接 `https://103.42.31.155:47443/i/x` 302 到 `https://deneb.<域名>/i/x`；网页的 API 经控制服务器 401。
- **设备**：换版前 4 台在线。用户的 Mac、构建服务器、NAS 都连回来了；一台外部用户的笔记本没马上回来，它这两小时本来就时断时续（13:23 下、13:50 上、14:08 下、14:09 上、14:25 下、14:51 上，UTC）。
