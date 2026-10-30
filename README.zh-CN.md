**中文版维护分支：** 请到 [50kg 的中文版发布页](https://github.com/50kg/claude-account-switcher/releases/latest)下载默认中文的 Windows 程序。发布包中的“下载中文版更新…”菜单会打开本 Fork 的下载页，供手动更新；不会自动安装上游英文程序，也不会在后台查询上游版本。更新前请先退出旧版切换器，再双击新版。下文自动更新和 winget 说明适用于标准上游构建。

<div align="center">

[English](README.md) | [简体中文](README.zh-CN.md)

<img src="assets/banner.svg" width="880" alt="Claude 账号切换器：一键切换 Claude 桌面端账号，无需反复接收邮箱验证码，并保留聊天入口。">

[![最新版本](https://img.shields.io/github/v/release/SnlperStripes/claude-account-switcher)](https://github.com/SnlperStripes/claude-account-switcher/releases/latest)
[![下载次数](https://img.shields.io/github/downloads/SnlperStripes/claude-account-switcher/total)](https://github.com/SnlperStripes/claude-account-switcher/releases)
[![测试](https://github.com/SnlperStripes/claude-account-switcher/actions/workflows/test.yml/badge.svg)](https://github.com/SnlperStripes/claude-account-switcher/actions/workflows/test.yml)
[![许可证：Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
![Windows | macOS](https://img.shields.io/badge/platform-Windows%20%7C%20macOS-lightgrey)

**一个 Claude 账号的额度用完了，就切到下一个继续工作。一键切换，无需反复接收邮箱验证码。**

这是一个为 Claude 桌面应用提供账号切换功能的系统托盘工具。它可以在约十秒内切换登录账号，让切换后的账号继续看到 Claude Code 聊天，并显示各账号最近查询到的用量。

**登录凭证保存在本机。** Token 保留 Claude 原有的加密形式，仅在查询账号信息和用量时发送给 `api.anthropic.com`。

[![下载 Windows 版](https://img.shields.io/badge/Windows-.exe-2ea44f?style=for-the-badge)](https://github.com/SnlperStripes/claude-account-switcher/releases/latest/download/claude-account-switcher-windows-amd64.exe)
[![下载 macOS 版](https://img.shields.io/badge/macOS-universal-555555?style=for-the-badge)](https://github.com/SnlperStripes/claude-account-switcher/releases/latest/download/claude-account-switcher-macos)

本项目为非官方工具，与 Anthropic 无隶属关系，也未获得其认可。

</div>

---

## 它解决什么问题

当你拥有两个 Claude 账号，而其中一个额度用完时，在桌面应用中手动切换通常需要：

1. **退出登录。** 原有登录状态可能因此失效，下次还要重新登录。
2. **输入另一个邮箱并等待验证码。** 如果使用隐私转发邮箱，还可能需要打开其他应用查找邮件。
3. **重新找到正在进行的工作。** Claude 按账号保存 Claude Code 聊天列表；聊天内容仍在本机磁盘上，但切换后侧边栏可能不再显示。
4. **猜测哪个账号还有剩余额度。** 应用通常只显示当前登录账号的用量。

这个工具把重复的账号切换步骤整合到系统托盘菜单中。

## 主要功能

**保留各账号的登录状态。** 切换离开当前账号时保存登录状态，切回来时恢复。每个账号首次添加时需要验证码，之后只要登录凭证仍有效，就无需反复验证。

**一键切换。** 右键点击托盘图标，选择目标账号。程序关闭 Claude、替换登录状态，再重新启动 Claude。其他应用设置保持原样。

**聊天和项目随账号切换。** Claude 启动前，切换器把其他账号的聊天列表条目导入目标账号，并同步项目分组。同步会参考删除标记，避免重新导入已删除的聊天。

**继续被切换中断的聊天。** 切换器会识别正在回复或运行工具的 Claude Code 聊天，在 Claude 再次启动前询问哪些聊天需要继续。勾选的聊天会在新账号下从中断处继续处理。

**同时查看所有账号的用量。** 菜单显示各账号的 5 小时用量、每周用量和周额度重置时间；5 小时用量接近阈值时也显示其重置时间。托盘圆环随当前账号的 5 小时用量填充：通常为绿色，达到 75% 时变橙色，达到切换阈值时变红色。

**可选的自动切换。** 默认关闭。开启后，当前账号的用量达到阈值时，程序自动切换到剩余空间更多的账号。默认阈值为 96%，也可选择 90%、94% 或 98%。另一个开关可优先使用即将重置但仍有大量剩余额度的账号。

## 原项目记录的实测结果

以下数据来自原项目首次在 Windows 11、Microsoft Store 版 Claude 上运行时的日志，仅供参考：

| 操作 | 耗时或结果 |
| --- | --- |
| 添加第二个账号，包含输入邮箱验证码 | 44 秒 |
| 识别新登录、导入缺少的聊天、重启 Claude | 9 秒 |
| 点击切换账号，到 Claude 再次启动 | 9 秒 |
| 真实 `config.json` 经过读取再写回 | 字节完全一致 |

## 安装

下载适合你系统的文件并运行即可，无需安装运行时。

- **Windows 10/11：**下载 [`claude-account-switcher-windows-amd64.exe`](https://github.com/SnlperStripes/claude-account-switcher/releases/latest/download/claude-account-switcher-windows-amd64.exe)。支持 Microsoft Store 版和直接下载安装的 Claude。也可以使用 winget 安装，之后通过 `claude-account-switcher` 启动：

  ```powershell
  winget install SnlperStripes.ClaudeAccountSwitcher
  ```

- **macOS：**下载 [`claude-account-switcher-macos`](https://github.com/SnlperStripes/claude-account-switcher/releases/latest/download/claude-account-switcher-macos)，包含 Apple Silicon 和 Intel 两种架构。首次运行可能询问是否允许读取钥匙串中的“Claude Safe Storage”，它是 Claude 加密登录凭证所用的密钥。原项目说明目前已通过 CI 构建，但仍缺少真实 Mac 的运行验证，欢迎反馈。
- **Linux：**本项目当前不提供可用的 Claude 桌面端账号切换支持。

启动后可在菜单中开启 **Start at login / 开机启动**。程序没有独立主窗口；Windows 用户请在任务栏右下角的托盘区域查找圆环图标，必要时展开隐藏图标。

上游下载的历史版本可能仍只有英文。中文界面和默认中文的可执行文件可按下方“界面语言”和“构建”部分生成。

发布文件没有代码签名，首次启动可能遇到系统提示：

- **Windows：**SmartScreen 可能显示“Windows 已保护你的电脑”，原项目提供的操作是点击“更多信息”，再选择“仍要运行”。
- **macOS：**下载文件可能尚无执行权限，并受到 Gatekeeper 限制。原项目提供的操作是在文件所在目录执行一次：

  ```bash
  chmod +x claude-account-switcher-macos && xattr -d com.apple.quarantine claude-account-switcher-macos
  ```

每个发布文件旁都有对应的 `.sha256` 文件，可用于校验下载内容。

## 首次使用

1. 先在 Claude 桌面端登录，再启动切换器。它通常会在一分钟内识别当前账号。
2. 点击 **Add account… / 添加账号…**。切换器保存当前账号的登录状态，重启 Claude 并显示登录页面。
3. 登录第二个账号。切换器识别成功后会再次重启 Claude，以导入其他账号的聊天列表。

之后，点击托盘菜单中的账号即可切换。

> [!IMPORTANT]
> **切换账号时，请使用切换器，不要在 Claude 中点击“退出登录”。** 退出登录可能让 Anthropic 撤销凭证，使切换器保存的副本失效。确实需要保存新的登录状态时再使用“添加账号…”。仅凭用量查询失败，不能断定当前 Claude 桌面会话已经退出。

## 界面语言

标准构建默认使用英文。通过启动参数可以选择语言：

```powershell
.\claude-account-switcher.exe -lang zh-CN
.\claude-account-switcher.exe -lang en
```

支持简体中文和英文。语言选择不会改变账号数据格式；默认中文构建仍可使用 `-lang en` 打开英文界面。若要双击后直接显示中文，请使用“构建”部分的中文构建命令。

## 自动切换

菜单中有两个开关，默认均关闭：

- **额度接近用尽时自动切换：**当前账号接近额度上限时，选择可用空间更多的账号。
- **优先使用即将重置的周额度：**选择周额度即将重置、但仍有较多剩余额度的账号。

切换会重启 Claude。正在生成的回复会暂停，切换器会询问哪些聊天需要在新账号下继续，详见“继续被中断的聊天”。

原版 README 对服务条款的说明是：拥有多个账号与绕过系统或保护措施是不同的事项。自动切换是否适合你的使用方式，需要结合适用条款自行判断，因此两个开关都默认关闭。本段为原项目说明的译述。

<details>
<summary><b>接近额度上限时，何时自动切换</b></summary>

必须同时满足以下条件：

| 条件 | 原因 |
| --- | --- |
| 当前账号的 5 小时或每周用量达到阈值 | 默认 96%，尽量在达到 100% 前切换 |
| 候选账号的两项用量都低于“阈值减 10 个百分点” | 避免切到同样接近上限的账号；默认要求低于 86% |
| 当前账号的数据在最近 5 分钟内查询过，候选账号在最近 15 分钟内查询过 | 避免根据过时数据决策 |
| 距离上次切换至少 10 分钟 | 避免频繁来回切换 |
| Claude 正在运行 | 自动切换不会主动打开已经关闭的 Claude |

候选账号按 `max(5 小时用量, 每周用量)` 比较，优先选择该值更低的账号。

</details>

<details>
<summary><b>优先使用即将重置的周额度，何时自动切换</b></summary>

周额度重置后，上一周期未使用的额度不会保留。开启此功能后，还需满足：

| 条件 | 原因 |
| --- | --- |
| 其他账号的周额度将在 24 小时内重置 | 这是即将失效的额度 |
| 该账号的周用量不超过 50% | 剩余额度较多，值得切换 |
| 该账号的 5 小时和周用量均低于“阈值减 10 个百分点” | 确保当前确实可用 |
| 当前账号本身不属于同样即将重置且用量较低的情况 | 当前账号的剩余额度同样有价值 |
| 最近 5 小时内没有手动切换 | 尊重用户自己的选择 |
| 最近 15 分钟内没有 Claude Code 聊天索引写入 | 此策略并不紧急，尽量等待空闲时再切换 |

前述数据新鲜度、冷却时间和 Claude 正在运行的要求同样适用。多个账号符合条件时，优先选择最早重置的账号。两个开关同时开启时，“接近额度上限”的切换策略优先。

切换到某个账号使用其即将重置的周额度后，程序会记住这个目标，直到本周额度重置；即使用量随后超过 50%，也会继续使用。若其 5 小时额度用完，普通自动切换可以暂时离开，等 5 小时窗口重置后再返回。手动切换会结束这种优先使用状态。

每次自动切换后，菜单顶部会在 12 小时内显示原因，例如“已于 03:12 自动切换，以使用此账号将在 18:00 重置的周额度”。

</details>

## 继续被中断的聊天

切换账号需要关闭 Claude，正在回复或运行工具的 Claude Code 聊天会因此暂停。Claude 再次启动前，切换器会列出这些聊天，显示标题和项目文件夹。

- 默认勾选全部聊天；点击“继续”后，由 Claude 在新账号下继续处理选中的聊天。
- 点击“不继续”、按 Escape 或关闭窗口，都不会继续这些聊天。
- 30 秒无人操作时，默认继续全部；操作复选框或全选按钮后，倒计时停止，等待你确认。
- Windows 使用系统 WebView2 显示选择窗口；无法使用时会回退到原生对话框。macOS 使用 WebKit，必要时回退到系统列表对话框。
- 窗口沿用托盘程序的语言，包括通过 `-lang` 显式选择的语言。

继续聊天使用 Claude 自己的重启恢复机制，切换器只设置或清除本地索引中的恢复标记，不修改聊天正文。

## 工作原理

切换器保存和恢复各账号的登录状态，在账号之间复制聊天列表与项目分组，并从 `api.anthropic.com` 查询用量。每次切换前会备份登录配置和 Cookie，其他 Claude 设置保持原样。

<details>
<summary><b>会访问哪些文件，如何处理</b></summary>

Claude 的数据目录因系统和安装方式而异：

| 平台 | 数据目录 |
| --- | --- |
| Windows，Microsoft Store 版 | 从 2.42304 起为 `%LOCALAPPDATA%\Claude-Data\claude_<id>`；旧版为 `%LOCALAPPDATA%\Packages\Claude_<id>\LocalCache\Roaming\Claude` |
| Windows，直接下载安装 | `%APPDATA%\Claude` |
| macOS | `~/Library/Application Support/Claude` |

| 数据 | 位置 | 切换器的处理方式 |
| --- | --- | --- |
| 登录 Token | `config.json` 中的 `oauth:tokenCacheV2`、`oauth:tokenCache`、`lastKnownAccountUuid` | 保存和恢复这三个字段，保留其他字段 |
| 网页登录会话 | `Network/Cookies`，部分版本位于数据目录根部 | Claude 关闭后保存和恢复文件及可能存在的 journal 文件 |
| 聊天列表 | `claude-code-sessions/<account>/<organization>/local_<id>.json` | 按真实会话 ID 去重，补充缺失条目；相同本地 ID 的旧副本可更新，不自动删除已有文件 |
| 已删除聊天标记 | 条目旁的 `deleted_<id>` | 读取标记，在能够关联到真实会话时也跳过其其他本地别名 |
| 项目分组 | `local-agent-mode-sessions/<account>/<organization>/spaces.json` | 添加缺失项目、更新较旧副本；跨组织复制时去掉来源组织的 claude.ai 项目关联，并参考已见项目记录 |
| 切换中断的聊天 | `local_<id>.json` 中的 `interruptedByQuitAt` 等字段 | 在目标账号的索引中设置恢复标记，并清除其他账号副本及未勾选聊天的标记 |
| 聊天正文 | `~/.claude/projects` | 读取最近 30 分钟内写入的聊天末尾，判断是否仍在处理任务；从不写入 |

**关闭 Claude。** Windows 上先向 Claude 窗口发送系统结束会话消息，让它正常停止正在处理的聊天并退出；超过 20 秒仍未退出时，再使用关闭请求和强制退出作为后备。普通关闭窗口通常只会把 Claude 隐藏到托盘。

每次切换前会备份 `config.json` 和 Cookie 存储，保留最近 10 份。修改登录文件失败时会尝试恢复备份，并在菜单顶部显示错误。这份备份不包含整个聊天和项目目录。

**用量查询。** 程序使用 Claude 自己的密钥，在内存中解密已保存的 Token 缓存：Windows 使用 DPAPI，macOS 使用钥匙串。随后请求账号信息和用量接口。当前账号通常每两分钟查询一次，接近上限、达到 85% 后每分钟查询一次；其他账号每十分钟查询一次。接口按账号限流，而 Claude 本身也会轮询，收到 `429` 后会按服务器要求等待；没有有效等待时间时逐步退避，最长 30 分钟。服务器明确要求的更长等待时间仍会遵守。凭证的实际有效期由服务端决定；本程序不会主动续期。

**用量状态与桌面登录分开判断。** 限流显示“用量查询受限”和剩余等待时间，重启切换器也不会绕过等待。`401` 显示“用量授权被拒绝”；本地用量令牌过期、缺少可用令牌、网络超时或其他查询错误均有各自提示，不再统一显示为“登录已失效”。这些结果本身不能证明 Claude 桌面会话已退出。查询失败时保留并标注“上次用量”，不会把它当成新查询结果来决定自动切换。旧版留下的模糊“失效”标记会先改为“用量授权待验证”，等待重新查询；已有账号按当前登录标识匹配刷新，不会重复添加。

**版本检查。** 程序每天向 GitHub 查询一次最新发布版本，不附带账号信息。发现新版本后，在菜单顶部提示；点击会下载文件、核对发布的 SHA-256 校验值、替换当前程序并重新启动。下载或安装失败时会打开下载页面。手动启动更新版本也可以：新版本会等待旧切换器完成正在进行的切换后接管。使用 winget 安装时可运行 `winget upgrade SnlperStripes.ClaudeAccountSwitcher`。若要关闭 GitHub 查询，请退出切换器，在 `state.json` 中加入 `"noUpdateCheck": true`，再启动程序。

**Store 版的路径重定向。** 从 Store 版 Claude 内启动的程序，例如 Claude Code 终端，会看到重定向后的 AppData 目录。切换器会检测这种情况，必要时通过 Explorer 再次启动自身，以访问真实文件，并避免随 Claude 退出。

</details>

## 你的数据

| 项目 | 说明 |
| --- | --- |
| 已保存的登录、状态、备份和日志 | 位于 `~/.claude-account-switcher`；Windows 为 `%USERPROFILE%\.claude-account-switcher`，可通过“打开数据文件夹”进入；`webview/` 为聊天恢复选择窗口的 WebView2 配置目录 |
| Token | 按 Claude 原有加密形式保存，不写成明文，只向 `api.anthropic.com` 发送 |
| 网络访问 | `api.anthropic.com` 用于账号信息和用量；`api.github.com` 用于每日版本查询，后者可关闭；点击更新时还会访问 `github.com` |
| 移除账号 | 退出切换器，删除 `accounts/` 下相应账号目录，以及 `state.json` 中的相应条目 |
| 卸载 | 关闭“开机启动”，退出程序，删除可执行文件和数据目录 |

## 命令行

```powershell
claude-account-switcher -status
```

查询并打印已保存账号的用量，然后退出。

`-lang zh-CN` 选择简体中文，`-lang en` 选择英文。

## 构建

需要 Go 1.26 或更新版本。

```bash
go vet ./...
go test ./...
go build -ldflags "-H=windowsgui -s -w" -o claude-account-switcher.exe .   # Windows，默认英文
CGO_ENABLED=1 go build -o claude-account-switcher .                        # macOS
```

生成双击后默认使用简体中文、通过本 Fork 发布页手动更新的 Windows 程序：

```powershell
go build -ldflags "-H=windowsgui -s -w -X main.defaultLanguage=zh-CN -X main.manualUpdateURL=https://github.com/50kg/claude-account-switcher/releases/latest" -o claude-account-switcher-zh-CN.exe .
```

标准库之外，使用 [`fyne.io/systray`](https://github.com/fyne-io/systray) 提供系统托盘能力；聊天恢复选择窗口在 Windows 上使用 [`jchv/go-webview2`](https://github.com/jchv/go-webview2)，macOS 上使用 [`webview/webview_go`](https://github.com/webview/webview_go)。Windows 还使用 `golang.org/x/sys`。

为 Windows exe 添加版本信息和应用清单时，可先执行上游构建流程中的资源生成命令，再执行上述编译命令：

```powershell
go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64 --product-version 0.3.6.0 --file-version 0.3.6.0
```

如需运行额外的真实聊天索引回归检查，可将 `SESSION_INDEX_SAMPLE` 设置为已去重的、离线的 `claude-code-sessions` 目录副本，再运行 `go test ./...`。测试只操作临时副本，不会修改提供的样本。请勿把真实账号数据或聊天索引提交到仓库。

## 常见问题

**Claude 桌面端能同时登录两个账号吗？**

这个工具每次让桌面端使用一个账号，保存各账号登录状态，并在需要时切换。原项目实测一次切换约十秒。

**如何不退出登录就切换账号？**

通过“添加账号…”分别登录一次，此后从托盘菜单选择账号即可。

**切换后，Claude Code 聊天会怎样？**

切换器把其他账号的聊天列表和项目分组导入目标账号，让已有本机会话继续显示在侧边栏中；聊天正文不被复制或删除。

**如何同时查看所有账号的用量？**

托盘菜单会显示各账号最近查询到的 5 小时和每周用量，以及重置时间。也可使用 `claude-account-switcher -status` 在终端查看。

**达到 Claude 用量限制后怎么办？**

选择仍有剩余额度的账号，或按需开启接近上限时自动切换。

**支持 Microsoft Store 版 Claude 吗？**

支持。程序会查找 Store 版实际数据目录，同时兼容直接下载安装的版本，目标系统为 Windows 10 和 11。

**支持 macOS 吗？**

CI 可构建 macOS 通用二进制文件，但原项目仍缺少真实 Mac 验证。欢迎提交 [macOS 测试报告](https://github.com/SnlperStripes/claude-account-switcher/issues/new?template=macos_report.yml)。

**会切换终端中的 Claude Code CLI 账号吗？**

不会。工具切换的是 Claude 桌面应用及其中的 Claude Code 聊天；终端中的 `claude` 命令使用独立登录状态。

**登录凭证会上传到其他地方吗？**

Token 按 Claude 原有加密形式存储，查询时只发送到 `api.anthropic.com`。另有每日一次、不附带账号信息的 GitHub 版本检查，可以关闭，详见“工作原理”。

**如何更新？**

发现新版本后，点击菜单中的提示即可下载、校验并重启为新版本；失败时会打开下载页面供手动更新。也可直接运行下载的新版本，由它接管正在运行的旧切换器。winget 用户可运行 `winget upgrade SnlperStripes.ClaudeAccountSwitcher`。账号和设置保存在独立数据目录中。

## 使用限制

本项目依赖 Claude 当前的本地文件存储方式，没有使用公开的账号切换接口。Claude 更新可能改变数据路径、配置结构或加密格式。程序遇到不能处理的格式时会报告错误，并在相应的登录修改流程中尝试恢复备份。

已有重复索引需要在 Claude 与切换器均退出后，先完整备份，再按真实会话 ID 检查和清理。不要仅凭标题判断重复，也不要删除共享的聊天正文文件。

## 参与贡献

欢迎提交问题反馈、建议和 Pull Request，详见 [CONTRIBUTING.md](CONTRIBUTING.md)。项目要求每个 PR 尽量聚焦一项改动，便于审阅。

如果你有 Mac，欢迎提供 [macOS 测试报告](https://github.com/SnlperStripes/claude-account-switcher/issues/new?template=macos_report.yml)，运行正常的反馈也有帮助。

发现安全问题时，请按 [SECURITY.md](SECURITY.md) 的说明私下报告。

如果工具帮你节省了时间，也欢迎给原项目点一个 Star。

## 许可证

项目使用 [Apache 2.0](LICENSE) 许可证。具体权利和义务以许可证原文为准。

**请保留作者署名。** 分发原版或修改版时，请包含 [`NOTICE`](NOTICE) 文件，保留作者及原项目链接，并说明所做修改。
