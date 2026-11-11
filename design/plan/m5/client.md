# M5 · 客户端线（桌面客户端）方案和状态

> 负责目录：`cmd/deneb/`、`client/`。依据：实现计划 §3 M5"通知区托盘和窗口""AppIndicator 托盘和窗口"，DESIGN §7.2、§6.13、§8，MyGo 0.7.4 的文档。
> 最后更新：2026-11-11。

## 进度

| 项 | 状态 |
| --- | --- |
| 查清 MyGo 在 Windows、Linux 上能做什么 | ✅ 写进 DESIGN §7.2 的表 |
| 设计（DESIGN §7.2 窗口和托盘，§6.13 本地网络权限） | ✅ 用户 11-10 看过，按推荐定 |
| 演示里做出来、浅色深色录帧、逐帧自查 | ✅ 5 个场景，交付在 `~/Desktop/Deneb组网demo/M5-Windows和Linux/第1批-托盘和窗口/` |
| 写进客户端、到 Windows 测试机和 Linux 虚拟机上验 | ✅ 11-11，见 m5-acceptance.md 的 u1、u2；Linux 双击开窗口做不到（见"真机上查到的"） |

演示是在副本 `/tmp/deneb-m5` 里做的（M4 的副本再加 M5），补丁在共享笔记的 `client-patches/`；11-11 写进真客户端时以 main（43225f6，M3、M4 已经在里面）为准合上去。

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

## 用户 11-10 定的（都按推荐）

1. Linux 顶栏的星单击出系统菜单（`Tray.SetMenu`），双击开窗口。
2. Windows 第一次关窗口时提示一句 Deneb 还在通知区里。
3. Windows 托盘右键和左键一样出面板，面板里不放"退出"。
4. 本地网络权限的提示这一批不放"去系统设置"按钮。

## 写进客户端（11-11）

- **窗口**（`main.go`、`view.go`）：标题栏高度 Windows 32、Linux 40（`TitleBarHeight`），按钮在右上角时主区顶上留出这一条，标题区和画布往下挪（演示里就有）。按钮在右上角时，侧边栏顶上的空白照 Mac 的标题栏（`macTitleBar`，macOS 26 是 32）算，三个平台的侧边栏一样高（u2 量出来原来 Ubuntu 低 8 点）。窗口大小按内容算（`UseContentSize`）：Windows 原来把看不见的拖边算进 1180×740，内容只有 1167×734。
- **只开一个**（`main.go` 的 `oneWindow`）：Windows、Linux 上关窗口只是藏到托盘，再从开始菜单或应用列表打开 Deneb 时交给开着的那个，把窗口叫回来；`deneb://` 链接也交过去。演示模式不占。
- **托盘图标**（`systray.go`）：Windows 照任务栏深浅给近黑（#1b1b1b）或白的星，跟着系统切换；Windows、Linux 的星从菜单栏 18 点的图裁成 16 点（星本身的大小）。Linux 是白星。
- **Windows 面板**（`systray.go`）：单击、右键都开关面板；贴在星上方，底边离任务栏 12（`taskbarGap` 按工作区算星离任务栏边多远）；淡入时从低 8 的地方浮上来，收起时沉下去（`float`）；圆角、细边交给系统。
- **Windows 第一次关窗口的提示**（`traytip.go`）：和面板一个样子的小浮层，贴在星上方；星收在"^"里时（读注册表 `NotifyIconSettings` 的 `IsPromoted`）多一句"可以把它拖到任务栏上"，这时系统给的位置就是"^"；6 秒或点一下收起，点星也收起；只出一次，记在配置目录的 `closed-once`。
- **Linux 系统菜单**（`traymenu.go`）：总状态（灰）、要处理的事（最多 3 件，点了打开窗口、选中那件事，菜单里不直接同意）、这台电脑在的每个局域网一行带勾（点了暂停或恢复）、"打开 Deneb"。菜单跟着世界变，同样的几项原地改字和勾，项变了才整个换。菜单里点的事没有窗口的帧可用，`sendNow` 直接交给引擎、在主线程收结果，窗口藏着也照样生效。
- **没有托盘的 Linux**（`trayhost_linux.go`）：会话总线上没人提供 `org.kde.StatusNotifierWatcher`（原版 GNOME 没开 AppIndicator 扩展）就不放星，关窗口就退出界面。
- **本地网络权限**（§6.13）：演示里的胶囊和卡片原样接真数据，没有"去系统设置"。
- **测试**：`m5_test.go`（菜单的各项、点菜单暂停和打开窗口、任务栏间距、Windows 的星、提示只出一次和它的字、按钮那一条和侧边栏、本地网络提示），`client/live_m5_test.go`（引擎报 local-network 时的提示）。
- **截图用**：`DENEB_THEME=light|dark` 让窗口用指定的深浅，不改系统设置。

## 真机上查到的（11-11）

- **Linux 双击开窗口做不到**：Ubuntu 26.04 的 libayatana-appindicator3（0.5.94）没有激活信号，图标导出的接口里没有 `Activate`，GNOME 的扩展每次单击都直接出菜单，双击什么也不做。窗口从菜单的"打开 Deneb"打开。要支持得换 libayatana-appindicator-glib，MyGo 0.7.4 不支持。
- **Ubuntu 的窗口是直角**：MyGo 在 GTK 下没把窗口裁成主题的圆角，设计里写的 12 实际没有；系统阴影有。
- **字体是各系统自己的**：MyGo 用系统界面字体（苹方和 SF、微软雅黑和 Segoe UI、Noto Sans CJK 和 Ubuntu 字体），不是 DESIGN §8 说的内嵌字体，三个平台字形和行高不同（u2）。
- **Ubuntu 26.04 的按钮布局**写的是只有关闭（`appmenu:close`），MyGo 的窗口照样有三个圆按钮。
- **Windows 防火墙**：一个新路径的 denebd.exe 第一次监听时，系统自己建两条入站阻止规则并弹提示；正式安装包装进 Program Files 时也会这样，安装时要不要加规则归 M6。

## 没做的

- Linux 双击开窗口：系统做不到，11-11 设计按系统的做法改了（见最后一节）。
- 内嵌字体（DESIGN §8），要用户定。

## 21:02 重出

主会话检查后改了演示数据，五个场景全部重录、重交，旧图从桌面移走：
- Windows、Linux 上这台电脑是"kittors 的台式机"，用台式机图标，不再是 MacBook。
- 不再出现家里的设备当中转站（用户 11-09 定了挪到公测以后），Mia 的 MacBook 改为经"公司机房 · 我的服务器"中转（演示事件 `m5`）。

## 11-11 客户端线：按系统改两处，演示世界改回批过的样子

- **DESIGN §7.2**（主会话让改的）：
  - Linux 顶栏的星没有双击，写明这是系统的限制，窗口从菜单的"打开 Deneb"打开。
  - Ubuntu 上窗口是直角。查了 MyGo 0.7.4 的源码（`internal/linux/window.go` 的 `useCSD`、`decorate`，`surface.go`）：Wayland 上 GTK3 自己画阴影和细边，画面是一块铺满的 GtkGLArea，GTK3 不按主题的圆角裁它。画面只有透明窗口才带透明，可透明窗口 MyGo 不让 GTK 装饰，系统的阴影和外圈拖边就都没了。所以 Deneb 这边做不了，DESIGN 写明 Ubuntu 上是直角，§8 的平台差异也补了一句。
- **真客户端**：`systray.go`、`traymenu.go` 里说"双击开窗口"的注释改了。`tray.OnClick(m.openWindow)` 留着：托盘库能把点击交给程序的地方（GNOME 扩展的双击），照样开窗口。
- **演示 M5-04 重录**：单击马上出菜单（原来要等一个双击的间隔），窗口关着时从菜单里打开它；Ubuntu 的窗口画成直角。交在 `M5-Windows和Linux/第2批-Linux按系统改/`，等用户看过。
- **演示世界**：
  - **偏差**：M5 合进 main 时，`m5` 事件只把"公司机房"加进周末联机，没删掉 main 的 M4 世界里阿杰拖进来的"东京"。M5 副本里那台是 Mia 家里的 NAS，`m5` 删了它。结果周末联机有了两个中转站，"公司机房"被挤到画布顶上，标签被标题区挡住一半，和用户批过的 M5、M6 录像都不一样。
  - **改法**：现在 `m5` 把东京换成公司机房。
  - **比对**：五个场景重录后和批过的逐张比，win-window、win-tray、local-network 只差新的星。linux-window、linux-tray 还差侧边栏顶上的 8 点：写进客户端时有意改的，见"写进客户端"。

