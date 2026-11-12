# 自动测试（GitHub Actions）

> 引擎线写，2026-11-10。文件：`.github/workflows/ci.yml`、`.github/actions/go/action.yml`、`.github/scripts/go-test.sh`。

## 什么时候跑、跑什么

每次推到 main、每个 PR 都跑，也可以在 Actions 页面手动跑。PR 有新的推送时，旧的那轮自动取消；推到 main 的每一轮都跑完。

几个任务同时跑，第一轮（没有缓存）估计十分钟左右：

| 任务 | 机器 | 做什么 |
| --- | --- | --- |
| Test (Linux) | ubuntu-latest | `go test` 除窗口（cmd/deneb）以外的全部包 |
| Test (macOS) | macos-latest | `go test ./...`，全部包，包括窗口和只在 macOS 上跑的网卡测试 |
| Test (Windows) | windows-latest | `go test` 七个包，见下面（11-12 加） |
| Vet and build (linux / darwin / windows) | ubuntu-latest | 每个系统一个任务，依次做下面几件 |

"Test (Windows)" 跑的七个包，都不要管理员权限，也不装服务：

- `engine/logfile`、`cmd/denebd`：日志开着也能滚动、文件开着也能改名。这两件只在 Windows 上才是真考验，测试机离线时就靠这里；
- `engine/filter`：过滤器，连同模糊测试的种子和回归输入；
- `engine/tun`：内核模式里不用网卡的部分，包括只读地读一遍 Network List Manager；
- `engine/dns`、`engine/bind`（UDP 绑到默认路由的网卡上）、`proto`。

还没放进来的，和原因：

- `engine/localapi/localclient`：测试在临时目录里开 Unix 套接字，Windows 上要的是命名管道；
- `engine`、`engine/magicsock`：进程内的网络测试看时机，没在 Windows 上跑过，先不放；
- `cmd/deneb`：窗口的测试按 Mac 的字体点坐标，只在 macOS 上跑；
- 控制服务器那几个包：归控制线。

"Vet and build" 每个系统依次做：

1. `go vet ./...`，正式版和测试版（`-tags denebtest`）各一遍；
2. 引擎、控制服务器、中转站、网页服务器、窗口，amd64、arm64 各编一份；
3. `deploy/check-release.sh` 查这些程序里没有测试版。再编一个测试版的引擎，确认这个脚本会拦下它；
4. `deploy/notices.sh` 确认每个依赖都有许可证文件；
5. 只在 linux 那个任务里加一步 `gofmt`。

## 约定

- **不要任何密钥，不连我们的服务器和测试机。** 连真机、真服务器的测试（`DENEB_ACCEPT`、`DENEB_REAL`、`DENEB_REMOTE`、`DENEB_PROD_COPY`、`DENEB_RESTORE_COPY` 等）不设变量就跳过，CI 里一个都不设。
- **全程不开 cgo**（`CGO_ENABLED=0`），和发布的构建一样。窗口经 purego 调系统，不需要 C 编译器。
- **窗口的测试只在 macOS 上跑**：有的测试按固定坐标点击，坐标是按 Mac 的字体排出来的。在 Linux 上 `TestLiveOldServerHidesM2` 点不到局域网的名字，每次都失败。
- **偶尔失败的测试会单独再跑一次**（`go-test.sh`）。引擎有几个测试看时机（丢包的线路、空闲计时），机器忙时偶尔失败（m2/engine.md「已知的限制」）。
  - 失败的测试单独再跑一次，过了就在这一轮的页面上留一条 "Flaky test" 警告，写明是哪个测试；
  - 两次都失败才算失败；
  - 下面几种情况不再跑：一个包没编过、在测试外 panic、超时，或者一次失败了三个以上的测试。
- **缓存**：模块缓存和编译缓存每个任务各存一份，按 go.mod、go.sum 和日期区分，每天存一次。
- **Windows 上保留原来的换行**：GitHub 的 Windows 机器取代码时默认把换行改成 CRLF，bash 脚本和模糊测试的语料（`testdata/fuzz`）会读坏。这个任务先设 `core.autocrlf false` 再取代码，只影响那台机器。脚本用 Git 带的 bash 跑，和别的系统一样。

## 本地先验

改了只在某个平台上的代码，先在本地验那个平台：

- Linux 的测试：在 Mac 上交叉编译测试程序，放进 `ubuntu:24.04` 容器里，以普通用户跑。不用另装 Go：

  ```
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/x.test ./engine/tun
  docker run --rm -u 1001 -v "$PWD:/src:ro" -v /tmp:/t ubuntu:24.04 sh -c 'cd /src/engine/tun && /t/x.test'
  ```

- 偶发失败的重试脚本，在本地照样用：`bash .github/scripts/go-test.sh ./engine/...`。

## 过程记录

### 11-10：写好，等主会话推上去跑第一轮

先在本地把 CI 要跑的都过了一遍：

- 交叉编译了所有有测试的包，在 Ubuntu 24.04 容器里以普通用户逐个包跑。用的是 HEAD，不是工作区：工作区里有别的线改到一半的控制服务器代码，那三个失败的测试在 HEAD 上是过的。
  - 引擎的包最慢，335 s；
  - 查出两件：
    - `engine/tun` 有两个测试只适用于 macOS（`TestPreflightRefusesCoveredBlock`、`TestUndoRecordRoundTrip`），在 Linux 上失败。已挪进 `tun_darwin_test.go`；
    - 窗口的 `TestLiveOldServerHidesM2` 在 Linux 上每次都失败，在本机每次都过，原因见上面「约定」。CI 里窗口只在 macOS 上测。要在 Linux 上也能跑，把第 413 行按坐标点击改成按文字点就行，这是客户端线的测试。客户端线的会话当时没在跑，请主会话转告。
- 在 HEAD 的干净导出上，用一个小脚本照 YAML 里的步骤，把三个系统的编译检查任务各跑了一遍。
  - 查出 `cmd/deneb-relay` 在 Windows 上编不过：它用 `syscall.Getrusage` 取 CPU 时间，Windows 没有这个调用。
  - 已把这段拆成 `cputime_unix.go`，Windows 上另写 `cputime_windows.go`，用 `GetProcessTimes`。
  - 改完以后，三个系统各 10 个程序（5 个程序 × amd64、arm64）都编出来了，各项检查都过。中转站的测试在本机和 Linux 容器里都过了。
- 本机模拟了 macOS 那个任务：3 个核、不开 cgo、用重试脚本跑全部测试。全过，没有需要重跑的，测试本身跑了 348 s，其中引擎 321 s。
- 两份 YAML 用 Ruby 解析过，字段照 GitHub 的写法逐条核过。这台 Mac 上没有 actionlint，没装。
