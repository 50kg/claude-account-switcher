<div align="center">

**简体中文** | [English](README.en.md)

<img src="assets/mark.svg" width="96" alt="">

# Claude 账号切换器

[![最新版本](https://img.shields.io/github/v/release/50kg/claude-account-switcher)](https://github.com/50kg/claude-account-switcher/releases/latest)
[![下载次数](https://img.shields.io/github/downloads/50kg/claude-account-switcher/total)](https://github.com/50kg/claude-account-switcher/releases)
[![测试](https://github.com/50kg/claude-account-switcher/actions/workflows/test.yml/badge.svg)](https://github.com/50kg/claude-account-switcher/actions/workflows/test.yml)
[![许可证：Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
![Windows 10/11](https://img.shields.io/badge/platform-Windows%2010%20%7C%2011-lightgrey)

**一个 Claude 账号的额度用完了，就切到下一个继续工作。一键切换，不用反复收邮箱验证码。**

[![下载 Windows 版](https://img.shields.io/badge/Windows-.exe-2ea44f?style=for-the-badge)](https://github.com/50kg/claude-account-switcher/releases/latest/download/claude-account-switcher-windows-amd64.exe)

非官方工具，与 Anthropic 无隶属关系，也未获其认可。

</div>

Claude 账号切换器是 Windows 上 Claude 桌面应用的托盘工具：约十秒切换登录账号，把 Claude Code 聊天列表带到新账号，并在一个菜单里显示所有账号的用量、重置时间和会员日期。

## 功能

- **保留每个账号的登录。** 切走时保存，切回时恢复。每个账号只在首次添加时输一次验证码，之后只要 Anthropic 没有结束这次登录，就不用再输。
- **一个菜单看全部额度。** 左键或右键点击托盘图标，打开同一个原生菜单：每个账号的 5 小时额度、本周总额度，以及 Fable 等模型的独立周额度（接口提供时），显示已用百分比、进度条和重置时间。75% 起为橙色、100% 起为红色；过期或查询失败的数据灰显，并标为"上次用量"。套餐标签按 Claude 显示的名称区分 Pro、Max、Ultra。
- **先确认再切换。** 查看菜单不会切换账号；点击账号右侧的"切换账号"并确认重启 Claude 后才执行。
- **聊天和项目随账号走。** Claude 启动前，目标账号的 Claude Code 侧栏会对齐刚切出账号的列表，项目分组一并同步。
- **继续被切换打断的聊天。** 切换时正在回复或运行工具的聊天会列在一个小窗口里，勾选的由 Claude 在新账号下接着做。
- **会员日期。** 显示每个账号的会员到期日或下次续费日；Apple（App Store）订阅可以手动记录。
- **可选的自动切换。** 当前账号接近额度上限时切到余量最多的账号；另一个开关优先用掉即将重置的周额度。两者默认关闭。
- **不切换也能开启 5 小时窗口。** 借助官方 Claude Code，用目标账号发送一条 Haiku 短消息，当前桌面账号照常使用。
- **备注、归档、彻底删除。** 自定义备注（最多 160 字）显示在账号标题位置；不常用的账号可以归档；已归档账号可以彻底删除。
- **单账号迁移。** 把一个账号导出为 `.cas-account` 文件，在另一台电脑上导入。
- **中英双语。** 同一个 exe，在"设置 → 语言 / Language"中切换。

## 下载与安装

需要 Windows 10 或 11，以及 Claude 桌面版（Microsoft Store 版或官网直接下载版均可）。

1. 从 [Releases](https://github.com/50kg/claude-account-switcher/releases/latest) 下载 `claude-account-switcher-windows-amd64.exe`，放到任意文件夹直接运行。无需安装，也无需运行时。
2. 程序没有主窗口，图标在任务栏右下角的托盘区（可能在隐藏图标里）。
3. 在"设置"中打开"开机启动"。

exe 没有代码签名，首次运行时 SmartScreen 可能提示"Windows 已保护你的电脑"：点击"更多信息"，再点"仍要运行"。每个发布文件旁都有对应的 `.sha256`，可以用 `Get-FileHash` 核对。

**语言。** 首次运行时跟随 Windows 显示语言（中文显示为简体中文，其他为 English）。从 1.0 之前的版本升级时保持简体中文。之后可在"设置 → 语言 / Language"中切换，选择保存在 `ui.json`。

**升级。** 切换器每天检查一次新版本，有新版时菜单顶部会提示，在"设置"中点击即可更新：自动下载、核对发布的 SHA-256、替换文件并以新版本重启；失败时打开发布页。"设置 → 检查更新"可以立即检查。也可以手动下载新 exe 直接启动，它会让正在运行的旧版退出。账号和设置都在数据文件夹里，升级不受影响。

## 使用

### 首次使用

1. 先在 Claude 中登录，再启动切换器。它会在一分钟内识别当前账号。
2. 点击菜单底部的"添加账号"并确认。Claude 会重启到登录页，原账号的登录已经保存。
3. 登录第二个账号。切换器登记后会提示，但不会自动导入聊天；需要合并各账号的聊天时，选择"设置 → 导入所有账号的聊天…"（会重启 Claude）。

之后点击账号右侧的"切换账号"即可。

> [!IMPORTANT]
> **不要用 Claude 里的"退出登录"来换账号。** 退出登录可能让 Anthropic 结束这次登录，切换器保存的副本也会随之失效。需要保存新的登录时，请用"添加账号"。用量查询失败本身并不说明桌面端已经退出登录。

### 菜单一览

| 位置 | 内容 |
| --- | --- |
| 顶部 | "账号排序"：手动排序（拖动账号左侧的点阵）或按最近重置时间（当前账号置顶）。拖动菜单上下边缘可调整高度，双击边缘恢复默认 |
| 账号行 | 名称或备注、套餐、邮箱、各项额度和重置时间；"切换账号"按钮；"⋯"账号操作 |
| 账号"⋯" | 编辑自定义备注… · 启动 5 小时窗口… · 为此账号登录 Claude Code… · 刷新会员日期…（Apple 订阅为"查看或记录 Apple 订阅日期…"）· 导出（明文）… · 归档账号。已归档账号另有"恢复到账号列表"和"彻底删除账号…" |
| 底部 | 自动切换开关和阈值；添加账号 · 设置 · 退出 |
| 设置 | 优先使用即将重置的周额度 · 开机启动 · 自动同步间隔 · 语言 / Language · 已归档账号 · 导入单个账号… · 立即同步用量 · 导入所有账号的聊天… · 打开数据文件夹 · 检查更新 |

切换、添加账号和导入聊天都会重启 Claude，执行前都要确认，确认框默认选中"取消"。按 Esc、再次点击托盘图标或点击菜单外部可收起菜单。

"启动 5 小时窗口"需要先安装 [Windows 原生 Claude Code](https://code.claude.com/docs/en/setup)，并在该账号的"⋯ → 为此账号登录 Claude Code…"中完成一次官方登录。启动前会核对账号和组织；已有窗口时直接显示重置时间。

### 自动切换

两个开关默认都关闭：

- **自动切换**（菜单底部）：当前账号的 5 小时或本周用量达到阈值（默认 96%，可选 90%、94%、98%）时，切到余量最多的账号。条件是：目标账号两项用量都比阈值低至少 10 个百分点；当前账号 5 分钟内、目标账号 15 分钟内查询过；距上次切换超过 10 分钟；Claude 正在运行。
- **优先使用即将重置的周额度**（设置）：某个账号的周额度将在 24 小时内重置、且已用不超过 50% 时，切过去把它用掉。最近 5 小时内手动切换过，或最近 15 分钟内有 Claude Code 聊天活动时不会触发。

已归档账号不会成为自动切换的目标。每次自动切换后，菜单顶部会在 12 小时内说明原因。Anthropic 的条款不禁止拥有多个账号，但禁止"绕过任何系统或保护措施"；自动切换是否适合你的用法，请自行判断。

### 被切换打断的聊天

切换需要关闭 Claude，正在回复或运行工具的 Claude Code 聊天会停下。Claude 重新启动前，切换器会列出这些聊天（默认全选）：点"继续"，Claude 会在新账号下自行接着处理勾选的聊天；点"不继续"、按 Esc 或关闭窗口则保持原样；30 秒无人操作则全部继续，动过复选框后倒计时停止。

## 工作原理

- **登录切换。** Claude 把登录保存在数据目录的 `config.json`（`oauth:tokenCacheV2`、`oauth:tokenCache`、`lastKnownAccountUuid` 三项）和 Cookie 库 `Network\Cookies` 中。数据目录：Store 版为 `%LOCALAPPDATA%\Claude-Data\claude_<id>`（旧版为 `%LOCALAPPDATA%\Packages\Claude_<id>\LocalCache\Roaming\Claude`），直接下载版为 `%APPDATA%\Claude`。切换时，切换器像 Windows 注销那样通知 Claude 退出（20 秒后仍未退出才强制结束），保存旧账号的这两部分、放回新账号的，再启动 Claude。其他设置一概不动。每次切换前备份 `config.json` 和 Cookie，保留最近 10 份；任何一步失败都会还原备份，并在菜单顶部报错。
- **聊天与项目。** Claude 按账号和组织在 `claude-code-sessions` 中保存 Claude Code 聊天列表，在 `local-agent-mode-sessions\…\spaces.json` 中保存项目。切换时，目标账号的列表按会话 ID 对齐刚切出的账号：补上缺少的、恢复应显示的、把多出来的归档（不删除），删除标记也一并带过去；项目只增不删。聊天正文在 `~\.claude\projects` 中由所有账号共享，切换器只读取最近 30 分钟写入的聊天末尾，判断哪些聊天正在进行，从不写入。继续聊天用的是 Claude 自己的退出标记 `interruptedByQuitAt`。
- **用量。** 已保存的 Token 用 Claude 自己的 DPAPI 密钥在内存中解密，再向 `api.anthropic.com` 查询账号资料和用量（与 Claude、Claude Code 使用的接口相同）。默认智能同步：当前账号每 2 分钟一次，达到 85% 后每分钟一次，其他账号每 10 分钟一次；"设置 → 自动同步"可改为所有账号统一 1–1440 分钟，保存在 `ui.json`。"立即同步用量"刷新所有账号（包括已归档账号），并跳过仍在等待重试的账号。收到 `429` 时按服务器要求等待（没有给出时逐步退避，最长 30 分钟），期间显示上次数据。查询失败不代表桌面端已退出登录，切换器也不会刷新或改写 Claude 的凭证。
- **会员日期。** 每 6 小时用各账号保存的 Claude 桌面网页会话从 `api.anthropic.com` 读取一次（claude.ai/api 对非浏览器客户端有 Cloudflare 浏览器验证）。"⋯ → 刷新会员日期…"立即查询，只有直接查询无法完成时才打开 claude.ai 核验窗口。账号和组织核对一致后才采用日期；查询失败和账单未提供日期会分开提示，旧日期标为"上次查询"。Claude 运行时会锁定当前账号的 Cookie 库；从未切走过、还没有保存登录的账号，改用切换器上次重启 Claude 时自动做的备份。连备份都没有时，切换账号或导入聊天一次后即可读取。Apple（App Store）订阅在 Claude 的账单里没有日期：在"⋯ → 查看或记录 Apple 订阅日期…"中打开 Apple 订阅页核对后手动记录，手动记录不会自动顺延。
- **已归档账号。** 不再自动查询用量和会员日期，但"立即同步用量"仍会刷新它们；恢复后继续自动查询。Claude 当前登录的账号即使已归档也照常查询。已归档账号仍可切换、导出、编辑备注和彻底删除。
- **Store 版路径重定向。** 从 Store 版 Claude 内部启动的程序（例如 Claude Code 终端）看到的是重定向后的 AppData。切换器会识别这种情况，通过资源管理器重新启动自己，以便访问真实文件，并且不随 Claude 一起退出。

## 数据与隐私

| 项目 | 说明 |
| --- | --- |
| 数据文件夹 | `%USERPROFILE%\.claude-account-switcher`（"设置 → 打开数据文件夹"）：`state.json`（账号和状态）、`ui.json`（语言、菜单高度、同步间隔、排序）、`accounts\`（各账号保存的登录和 Cookie）、`backups\`（切换前的备份）、`session-code\`（各账号专用的 Claude Code 登录）、`webview\`（对话窗口的 WebView2 配置）、`switcher.log`（日志） |
| Token | 保持 Claude 原有的加密形式，只发送到 `api.anthropic.com`，不写入日志 |
| 网页会话 Cookie | 只发送到 `api.anthropic.com`（直接查询会员日期），或在你打开的核验窗口中发送到 `claude.ai` |
| 联网 | `api.anthropic.com`：账号资料、用量、会员日期。`claude.ai`：仅在可选的会员日期核验窗口中。`api.github.com`：每天一次查询最新版本号，不附带账号信息。`github.com`：仅在你点击更新时下载。"启动 5 小时窗口"调用你安装的官方 Claude Code |
| 关闭更新检查 | 退出切换器，在 `state.json` 中加入 `"noUpdateCheck": true`，再启动 |
| `.cas-account` 导出 | 唯一的明文例外：文件不加密，包含该账号的登录和 Cookie（不含聊天），拿到文件的人就能使用该账号。导入只新增账号档案，不改变 Claude 当前登录。导出正在使用的账号前请先手动关闭 Claude。请私密保存，导入后删除 |
| 删除账号 | 先归档，再选"⋯ → 彻底删除账号…"并确认。会删除保存的登录、Cookie、用量和账单记录、该账号的 Claude Code 登录和相关备份；当前账号要先切走。聊天和已导出的文件保留，删除无法撤销 |
| 卸载 | 关闭"开机启动"，退出，删除 exe 和数据文件夹 |

## 命令行

```powershell
.\claude-account-switcher-windows-amd64.exe -status
```

打印所有已保存账号及其用量，然后退出。

## 从源码构建

需要 Windows 和 Go 1.26 或更新版本，无需 CGO。

```powershell
.\build.ps1              # vet、测试，用 go-winres 生成 Windows 资源，再构建
.\build.ps1 -SkipTests   # 只构建
```

输出 `dist\claude-account-switcher-windows-amd64.exe`，以及对应的 `.sha256` 和第三方许可证声明 `THIRD_PARTY_NOTICES.txt`。也可以直接构建（不会重新生成版本信息和清单）：

```powershell
go build -ldflags "-H=windowsgui -s -w" -o claude-account-switcher.exe .
```

标准库之外的依赖：[`fyne.io/systray`](https://github.com/fyne-io/systray)（托盘图标）、[`jchv/go-webview2`](https://github.com/jchv/go-webview2)（对话窗口）、[`wailsapp/go-webview2`](https://github.com/wailsapp/go-webview2) 的 `pkg/edge`（会员日期核验窗口）、[`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite)（纯 Go，读写 Cookie 库）、`golang.org/x/sys`。推送 `v*` 标签后，GitHub Actions 构建 exe 并连同 `.sha256` 一起发布。

## 已知限制

切换器依赖 Claude 桌面端当前的本地文件格式，而不是公开接口，Claude 更新可能改变它。遇到无法识别的格式时，切换器会停止、还原备份并在菜单中报错，不会猜测。它只切换桌面应用（包括其中的 Claude Code 聊天），不切换终端里 `claude` 命令的登录。

## 参与贡献

欢迎提交问题和 Pull Request，见 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请按 [SECURITY.md](SECURITY.md) 私下报告。

## 致谢与许可证

本项目源自 [SnlperStripes/claude-account-switcher](https://github.com/SnlperStripes/claude-account-switcher)，在此基础上做了大量修改并新增了许多功能，感谢原作者。

以 [Apache 2.0](LICENSE) 许可证发布。再分发（无论是否修改）时请附上 [`NOTICE`](NOTICE) 文件，并说明所做的修改。
