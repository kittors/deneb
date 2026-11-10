# M5 · 客户端线（桌面客户端）方案和状态

> 负责目录：`cmd/deneb/`、`client/`。依据：实现计划 §3 M5"通知区托盘和窗口""AppIndicator 托盘和窗口"，DESIGN §7.2、§6.13、§8，MyGo 0.7.4 的文档。
> 最后更新：2026-11-09。

## 进度

| 项 | 状态 |
| --- | --- |
| 查清 MyGo 在 Windows、Linux 上能做什么 | ✅ 写进 DESIGN §7.2 的表 |
| 设计（DESIGN §7.2 窗口和托盘，§6.13 本地网络权限） | ✅ 写好，等用户看 |
| 演示里做出来、浅色深色录帧、逐帧自查 | ✅ 5 个场景，交付在 `~/Desktop/Deneb组网demo/M5-Windows和Linux/第1批-托盘和窗口/` |
| 写进客户端、到 Windows 测试机和 Linux 虚拟机上验 | 等用户看过设计 |

工作在副本 `/tmp/deneb-m5` 里做（M4 的副本再加 M5）；M3、M4 两批的补丁另存了一份在共享笔记的 `client-patches/` 里，免得 `/tmp` 被清掉。

## 查到的（MyGo 0.7.4）

- **窗口**：三个平台都用系统窗口、藏起标题栏（`TitleBarHidden`）。Windows 的三个按钮由 MyGo 照 Windows 11 画在右上角，高度是我们给的标题栏高度，指着最大化有贴边分屏；Linux 的是 GTK 自己的按钮，放在桌面设置的位置，在标题栏高度里居中。`c.TitleBar()` 告诉按钮占了多少（`Right`），界面据此在主区顶上留一条。圆角、阴影、拖边缩放都是系统的。
- **Windows 托盘**：通知区图标，`Tray.Dark` 告诉任务栏深浅（自己换黑星白星，模板图在 Windows 上不会被染色）；`OnClick` 加 `Tray.Bounds`，面板（`Flyout`）贴在图标正上方，Windows 11 给它圆角 8、细边和阴影，所以面板自己不画圆角和边。
- **Linux 托盘**：AppIndicator（`libayatana-appindicator3`），Ubuntu 默认有；单击出系统菜单，双击（GNOME 的扩展发 `activate`）才到 `OnClick`。不告诉图标在哪儿，Wayland 下程序也摆不了窗口，面板只能出现在屏幕正中，所以 Linux 用系统菜单。没有这个库时 `NewTray` 报错，那时关窗口就退出界面。

## 演示里做了什么

- `view.go`：`c.TitleBar().Right > 0`（按钮在右上角）时，主区顶上留一条按钮那么高的空白（能拖窗口），标题区和画布往下挪这一条；macOS 不变。
- `menubar.go`：Windows 上面板不画自己的圆角（系统画）。
- `client/model`：`Notice` 加 `Detail`（说明，可以分段）、`Settings`（系统设置的地址，有才出"去系统设置"按钮）；新问题码 `local-network` 的提示 `model.LocalNetwork`；`live.go` 把引擎报的 `local-network` 接成这条提示；演示里 `Simulate("local-network")` 让我的游戏主机绕远路（经香港中转），再按一次就好了。
- 录屏用的 `m5frames_test.go`：照系统的样子画 Windows 的三个按钮、Ubuntu 的三个圆按钮、Windows 11 的任务栏和通知区、Ubuntu 的顶栏和系统菜单；窗口在这些画面里是它自己每一帧的画（`ui.NewBitmap`），所以面板里暂停，窗口侧边栏的开关同时变。

## 录帧

`DENEB_M5=<目录> go test -run 'TestM5Frames$' ./cmd/deneb` 录 5 个场景（两种颜色，一秒 60 帧，加截图），`DENEB_M5_ONLY=win-tray` 只录一个；`/tmp/deneb-tools/review/deliver_m5.py <目录>` 交到桌面，顺手从托盘的截图里裁一张两倍大的特写。

## 要用户定的

见 DESIGN §7.2、§6.13：Linux 用系统菜单不做面板（推荐）；Windows 第一次关窗口出一句提示（推荐）；Windows 右键和单击一样出面板，不放"退出"（和菜单栏一致）；本地网络这一批不放"去系统设置"按钮（推荐，系统设置里还没有能打开的开关）。

## 没做的

- 真客户端里的事等用户看过设计再做：Windows 按任务栏深浅换星的颜色，第一次关窗口的提示，Linux 的系统菜单（`Tray.SetMenu`）和双击开窗口，没有 AppIndicator 时关窗口退出界面。
- 托盘图标还是三颗星，logo 定了再换。

## 21:02 重出

主会话检查后改了演示数据，五个场景全部重录、重交，旧图从桌面移走：
- Windows、Linux 上这台电脑是"kittors 的台式机"，用台式机图标，不再是 MacBook。
- 不再出现家里的设备当中转站（用户 11-09 定了挪到公测以后），Mia 的 MacBook 改为经"公司机房 · 我的服务器"中转（演示事件 `m5`）。
