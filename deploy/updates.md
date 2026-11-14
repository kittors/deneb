# Deneb 在线更新发布

客户端从下载站的 `https://<网站>/download/` 读取平台清单（打包时由 `DENEB_UPDATE_FEED` 填进程序，见 `deploy/package.sh`；不填时读 `deploy/web/site.hkspare.env` 里的 `WEB_HOST`，两样都没有就从 GitHub Releases 读。网站地址不进仓库），使用 `mygo.json` 中的 Ed25519 公钥验证更新包。GitHub Releases 用于发行归档；只发布 GitHub Release 不会刷新客户端的 HTTPS 更新源。

当前平台为 `darwin-arm64` 和 `windows-amd64`。Linux `.deb` 使用包管理更新。macOS 包为 ad hoc 签名，尚未 Apple 公证；macOS/Windows 的系统引擎若缺少可校验的发行者签名，重启客户端后可能再次要求管理员授权安装引擎，取消时会明确提示引擎未更新。

1. 提升 `cmd/deneb/mygo.json` 的版本，并在 `CHANGELOG.md` 添加同版本记录。
2. 从受保护的密钥存储为构建子进程提供 `MYGO_UPDATER_PRIVATE_KEY`。不得把私钥提交、输出或加入安装包。
3. macOS 主机执行 `sh deploy/release.sh -n macos windows`，产物位于 `deploy/out/<version>/`。CI 现有 Linux runner 只构建 Windows/Linux；macOS 仍在 macOS 主机单独构建。需要 GitHub 发行归档时去掉 `-n`，会创建草稿供人工发布。
4. 校验产物及清单签名。复制清单，将 `url` 改为下载源同目录下的对应归档 URL；不改变归档、签名、大小或版本。若有 `deltas`，其 URL 也必须指向同一 HTTPS 下载目录。签名覆盖归档 SHA-256，改 URL 不会取消签名保护。
5. 先上传归档到 `/srv/deneb-web/download/`，核对远端 SHA-256，再上传临时清单并用同目录 `mv` 原子替换 `update-darwin-arm64.json`、`update-windows-amd64.json`。保留历史归档，避免正在下载的客户端中断。不需要重启 Nginx 或其他服务。
6. 从公网 HTTPS 重新下载清单和归档，核对大小、SHA-256、Ed25519 签名；使用旧版真实客户端验证下载、进度、就绪、稍后、重启与网络身份保留后记录验收结果。

本地验收可用 `DENEB_UPDATE_SOURCE=<目录>`，目录包含平台清单和其归档；同样必须通过签名验证。验收后取消该环境变量，确保最终客户端使用 HTTPS 源。旧 0.1.0 包未内置本轮集成更新入口，需要先安装 0.1.4，后续版本才可从窗口内更新。
