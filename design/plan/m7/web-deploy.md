# M7 · 网页后台正式上线

> 控制线，2026-11-12。用户给了域名，说"你上线吧"。方案、脚本和代码都做好了，本机测过，上线由主会话来做。
>
> 本文里的域名一律写成 `deneb.<域名>`，脚本和模板里写成 `deneb.example.com`。真的域名只放在 `deploy/web/site.hk155.env` 里，这个文件不进仓库。

## 0. 一句话

- 网页用一个子域名 `deneb.<域名>`。
- hk-155 上用户自己的 nginx 管 443：它的 `conf.d/` 里新加一个文件，把这个子域名转给本机的 deneb-web。
- 证书：用 certbot 的 Cloudflare DNS 验证，和机器上别的子域名一样，凭证用服务器上已有的那份。
- 控制服务器的 IP、端口、证书都不动，已经装好的客户端什么都不用改。
- 控制服务器加 `-web-url`：新的邀请链接打开网页版，旧链接跳过去。

## 1. 现在的样子

- **deneb-web**：只在主会话那台 Mac 上跑。
  - 它做三件事：发网页的文件；把 `/api/` 按指纹转给控制服务器；让 cookie 和同源检查按同源算。
  - 它只会明文 HTTP。
- **控制服务器**：在 hk-155 的 47443 端口，用自签证书。设备、中转站、正式版客户端都按"IP 加指纹"连它。
- **邀请链接**：是 `https://103.42.31.155:47443/i/<码>`。浏览器先报证书不受信任，打开以后没装客户端的人也接受不了。
- **hk-155**：80、443 由宿主机上的 nginx（systemd 服务）占着，它的 `conf.d/` 里一个站一个文件，是用户另一个项目的，一个都不动。certbot 已经装好，用户的几个子域名都是用 Cloudflare 的 DNS 验证拿的证书。

## 2. 域名和解析

- **一个子域名，只给网页**：网页的文件和 `/api/` 都在 `https://deneb.<域名>` 下。
- **控制服务器不换域名**。
  - 已经装好的客户端认的是 IP 和自签证书的指纹，控制服务器一换地址或者证书，它们就连不上了。
  - 网页上线用不着动它。
  - 以后做备机（[control-ops.md](control-ops.md) §5 第 1 条）再加 `control.<域名>`，指向同一台机器。客户端仍然按指纹认证书，不需要正式证书。到时候新版先试域名、不通再回 IP，旧版一直用 IP，要引擎线和客户端线一起改。
- **解析，用户在 Cloudflare 做**：
  - 这个域名有一条通配符记录，指向别的机器，所以 `deneb.<域名>` 现在解析不到 hk-155。
  - 要加一条记录：类型 A，名称 `deneb`，内容 `103.42.31.155`，代理状态"仅 DNS"（灰色云朵），TTL 自动。
  - 有了这条明确的记录，这个名字的所有类型都不再落到通配符上，也不会因为通配符有 AAAA 就解析到别处。
  - 证书走 DNS 验证，不等这条记录；可是浏览器要连得上，得先有它。
- **域名不进仓库**：
  - 脚本从 `deploy/web/site.hk155.env` 里读，这个文件被 `deploy/web/.gitignore` 挡在仓库外，写法见 `site.example.env`；
  - nginx 的站点文件只在 hk-155 上；
  - 提交说明里也不写。
- **但域名本身会公开**：邀请链接里有它，证书会被记进公开的证书透明日志。

## 3. 部署：deneb-web 单独一个容器

为什么不让控制服务器顺带发网页的文件：

- 网页线发新版，不用重启控制服务器，设备不会断一下。
- 控制服务器的 47443 和自签证书是给设备用的，浏览器要正式证书和 443，前面照样要 nginx。
- deneb-web 已经把同源、gzip、CSP、单页路由、按指纹转 `/api/` 都做好了。

`deploy/web/` 下的脚本，写法照 `deploy/control/`：

| 脚本 | 做什么 |
| --- | --- |
| `build.sh` | 把 deneb-web 编成 Linux 的静态程序；从 `web/app/src` 构建网页，在临时目录里构建，仓库里的 `web/app/dist` 不动 |
| `Dockerfile` | 空镜像，只有 deneb-web 和网页文件，以 nobody 运行 |
| `deploy-hk155.sh` | 构建、上传、起容器；上一版镜像留作 `deneb-web:prev`；最后在 hk-155 上自查：网页 200，`/api/` 经它到控制服务器得到 401 |
| `rollback-hk155.sh` | 换回上一版；带 `-remove` 是整个拿掉 |
| `cert-hk155.sh` | 用 certbot 拿证书（§4） |
| `nginx-hk155.sh` | `show` 在本机打出站点文件；`install` 放进 nginx 并平滑 reload；`remove` 拿出来（§4） |
| `check-local.sh` | 在本机把 nginx、deneb-web、控制服务器整条链跑一遍（§8） |

容器：

```
docker run -d --name deneb-web --network host --restart unless-stopped --read-only \
  --cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 128 \
  --log-opt max-size=10m --log-opt max-file=3 \
  deneb-web:v0 -static /app -listen 127.0.0.1:47447 \
  -control https://127.0.0.1:47443 -control-pin <控制服务器 data/tls/pin.txt 里的指纹>
```

- 只听 127.0.0.1:47447，外面连不到，只有 nginx 转得进来。
- 不开新的对外端口，不动防火墙，不动别的容器。
- deneb-web 不存任何东西；重新部署只会让浏览器的推送流断一下，马上自己连回来。

## 4. 证书和 nginx

### 证书

`cert-hk155.sh` 在 hk-155 上跑：

```
certbot certonly --dns-cloudflare --dns-cloudflare-credentials <CF_CREDENTIALS> \
  --dns-cloudflare-propagation-seconds 30 -d deneb.<域名> \
  --non-interactive --keep-until-expiring --deploy-hook 'nginx -t && systemctl reload nginx'
```

- 和这台机器上别的子域名一样，走 Cloudflare 的 DNS 验证，用服务器上已有的凭证文件。
  - 凭证文件的路径写在 `site.hk155.env` 里。
  - 脚本只把路径交给 certbot，不读、不打印、不拷它的内容。
  - 不需要新的令牌。
- 不用 80 端口验证：别的子域名都不是这么拿的；用 DNS 验证，nginx 在 80 上现有的配置一点都不用碰。
- 续期跟着现有的 certbot 定时任务走。每次续期以后，先 `nginx -t`，过了才 reload。

### nginx

`nginx-hk155.sh install` 只加一个文件 `/etc/nginx/conf.d/deneb.<域名>.conf`，由 `nginx.conf.template` 生成，要点：

- **监听地址**：和 nginx 现有站点一样，是 hk-155 的公网 IP 上的 80 和 443，所以 nginx 不新绑任何端口。
- **不写 http2**：http2 一开，同一个端口上的别的站点也会跟着变。
- **80**：只做一件事，跳到 https。
- **443**：
  - 用上面的证书，整站反向代理到 127.0.0.1:47447；
  - `proxy_buffering off`，推送流一帧一帧地到；超时 1 小时，推送流每 15 秒有一次心跳；
  - `X-Real-IP` 设成 nginx 看到的地址，覆盖浏览器自己带的；
  - `proxy_cookie_flags ~ secure`：deneb-web 为了本机明文调试会去掉 cookie 的 Secure，这里是 HTTPS，补回去。
- **日志**：`access_log off`，因为 `/i/<码>`、`/api/v1/invites/<码>` 的路径里就是邀请码；错误日志只记 crit 级，因为 error 级的每一行都带着请求的路径。

脚本的保护：

- 同名文件已经在、又不是这个脚本做的（第一行有标记）：什么都不做。
- 证书还没拿到：什么都不做。
- 先 `nginx -t`，过了才 `systemctl reload nginx`。reload 是平滑的，别的站点正在连着的不断。
- `nginx -t` 不过：文件放回原样，不 reload。
- `remove` 也一样：先拿出来，`nginx -t` 过了才 reload，不过就放回去。

## 5. 代码改动（做了，提交了）

**控制服务器**

- **`-web-url https://deneb.<域名>`**：
  - 邀请链接都改成网页上的 `/i/<码>`。链接是看的时候现算的，所以还没用的邀请也会马上换成新链接。
  - 控制服务器自己的 `GET /i/<码>` 改成 302 跳过去，带 `Referrer-Policy: no-referrer`。用 302 不用 301：回滚以后，浏览器不会记着旧的跳转。
  - 网页接口的同源检查，把这个地址算作受信的来源。新浏览器带 `Sec-Fetch-Site`，本来就过；不带这个头的旧浏览器（比如 iOS 16.4 以前的 Safari），控制服务器只能拿 Origin 去比 Host，而 deneb-web 转过来时 Host 换成了控制服务器的，比不上，以前会被挡掉。
  - `-public-url` 不动。设备登录链接、中转站加入码里的控制服务器地址、cookie 的 Secure，都还按它来。
- **`-trust-local-proxy`**：
  - 只有从本机（127.0.0.1、::1）来的请求，才拿 `X-Real-IP` 当它的来源地址，也就是 nginx 看到的、deneb-web 原样转过来的那个。
  - 从外面直连 47443 的请求，这个头写什么都不认。
  - 不加这一条的话，网页上所有人在控制服务器看来都是 127.0.0.1，按地址限验证码就成了大家共用一份额度，日志里也分不出谁是谁。
- **部署脚本**：`site.hk155.env` 里 `WEB_LIVE=yes` 时，`deploy/control/deploy-hk155.sh` 起控制服务器就带上面两个参数。
  - 回滚脚本也带上；回滚到的旧镜像要是不认 `-web-url`，就不带了，并说一声。
- **测试**：
  - `TestWebURL`：邀请链接、旧链接 302、旧浏览器的同源检查、会话记下的来源地址；
  - `TestLocalProxy`：只认本机来的、只认地址。

**deneb-web：不用改**

- nginx 设的 `X-Real-IP`，deneb-web 原样转给控制服务器：Go 的反向代理只去掉 X-Forwarded-* 这几个头。
- cookie 的 Secure 由 nginx 补回。

## 6. 邀请链接和客户端怎么过渡

- **新链接**（`https://deneb.<域名>/i/<码>`）：
  - 浏览器直接打开网页版，没有警告，没装客户端的人也能登录、接受；
  - 粘到客户端里也认：客户端只看路径里的 `/i/<码>`，不管主机是哪个。
- **旧链接**（`https://103.42.31.155:47443/i/<码>`）：
  - 照样能用：浏览器先报一次证书警告（和现在一样），继续以后跳到网页上的邀请页；
  - 邀请 7 天过期，上线一周后旧链接就都没了。
- **已经装好的客户端**：什么都不用改，也不用升级。卡片上显示的邀请链接是服务器给的，会自己换成新的。

## 7. 上线：主会话按顺序做

动 hk-155 的每一步，主会话都先问过用户。

0. **写站点设置**：在本机写 `deploy/web/site.hk155.env`，照 `site.example.env`。
   - `WEB_HOST=deneb.<域名>`
   - `WEB_PORT=47447`
   - `NGINX_IP=103.42.31.155`
   - `CF_CREDENTIALS=<那份凭证文件的路径>`
   - `WEB_LIVE=no`
1. **加 DNS 记录**：用户在 Cloudflare 加 §2 那条 A 记录。在本机用 `dig +short deneb.<域名> @1.1.1.1` 看到 103.42.31.155 再往下。绕开本机代理的假 IP，所以指定解析服务器。
2. **起 deneb-web**：`deploy/web/deploy-hk155.sh`。最后一行要看到网页 200、API 401。这时外面还连不到它。
3. **拿证书**：`deploy/web/cert-hk155.sh`。最后列出 `/etc/letsencrypt/live/deneb.<域名>/`。
4. **放进 nginx**：
   - `deploy/web/nginx-hk155.sh show`，看一眼要放进去的文件；
   - `deploy/web/nginx-hk155.sh install`，要看到 nginx -t 通过、"nginx reloaded"。
5. **从外面验**：
   - `curl -sI https://deneb.<域名>/` 得到 200，证书正常；
   - 浏览器打开，用测试账号登录，看得到局域网；
   - 打开一个邀请链接，能看到邀请；
   - 用户自己的别的站点照常打开。
6. **切邀请链接**：
   - `site.hk155.env` 改成 `WEB_LIVE=yes`，跑 `deploy/control/deploy-hk155.sh`。设备几秒内连回来。
   - 验：
     - `curl -sk -o /dev/null -w '%{redirect_url}' https://103.42.31.155:47443/i/x` 得到 `https://deneb.<域名>/i/x`；
     - 新邀请的链接是新域名的；
     - `deneb-control logs -grep 'web login'` 里，网页登录的地址不再是 127.0.0.1。
7. **以后**：
   - 看门狗加一项，探 `https://deneb.<域名>/`，证书还剩 14 天就提醒。域名写在服务器上的看门狗配置里。
   - 稳定一周后再定要不要加 HSTS。

## 8. 回滚

倒着来，哪一步出问题就从哪一步往回退：

- **邀请链接**：`WEB_LIVE=no`，再跑 `deploy/control/deploy-hk155.sh`，链接变回 IP 的。
  - 已经发出去的新链接要等网页恢复才能打开，最多影响 7 天里发出的邀请。
  - 控制服务器本身要回滚：`deploy/control/rollback-hk155.sh`。它会看旧镜像认不认 `-web-url`。
- **nginx**：`deploy/web/nginx-hk155.sh remove`，拿掉那个文件，`nginx -t` 通过后平滑 reload。别的文件从头到尾没动过。
- **deneb-web**：`deploy/web/rollback-hk155.sh` 换回上一版，或者带 `-remove` 整个拿掉。
- **证书**（可选）：`certbot delete --cert-name deneb.<域名>`。
- **DNS**（可选）：用户删掉那条 A 记录，这个名字回到通配符。

## 9. 本机测过的

- **控制服务器**：`TestWebURL`、`TestLocalProxy`；control、deneb-web、deneb-relay 的 `-race` 测试；`go vet`，也按 Linux 编译查过。
- **站点文件**：`nginx-hk155.sh show` 打出来的文件，在 nginx 1.27 和 stable 的镜像里 `nginx -t` 都通过。监听地址换成了容器里有的地址；在 hk-155 上，那个地址的端口已经被 nginx 自己占着，`nginx -t` 本来就容许这种情况。
- **镜像**：`build.sh` 和 `docker build` 都通过，镜像里的程序起得来。从源码构建出来的网页，和仓库里的 `web/app/dist` 一样。
- **整条链**（`check-local.sh`）：nginx（用生成的站点文件和自签证书）→ deneb-web → 控制服务器（`-web-url`、`-trust-local-proxy`）：
  - 网页 200；
  - 邀请页 200；
  - API 没登录时 401；
  - 不带 `Sec-Fetch-Site` 的登录 200；
  - cookie 带 Secure、HttpOnly、SameSite=Lax；
  - 登录后视图 200；
  - 推送流的第一帧马上到；
  - 控制服务器看到的是 nginx 看到的地址，不是浏览器自己编的 `X-Real-IP`，也不是 127.0.0.1；
  - 控制服务器自己的邀请链接 302 到网页上。
- **没测的**：真的证书和真的 DNS（要上线时才有），以及 hk-155 上真的 nginx。脚本已经用 `nginx -t` 把住了这一关。

## 10. 以后再说

- **设备登录链接**（`/login?c=…`，命令行登录时用）：还在控制服务器上，浏览器打开会报证书不受信任。要挪到网页上，网页线得加一个"同意这台设备登录"的页面。桌面客户端在窗口里登录，不走这个链接。
- **控制服务器换域名**：跟备机方案一起做（§2）。
- **HSTS、看门狗**：见 §7 第 7 步。
