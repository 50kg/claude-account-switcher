<div align="center">

[简体中文](README.md) | **English**

<img src="assets/mark.svg" width="96" alt="">

# Claude Account Switcher

[![Latest release](https://img.shields.io/github/v/release/50kg/claude-account-switcher)](https://github.com/50kg/claude-account-switcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/50kg/claude-account-switcher/total)](https://github.com/50kg/claude-account-switcher/releases)
[![Tests](https://github.com/50kg/claude-account-switcher/actions/workflows/test.yml/badge.svg)](https://github.com/50kg/claude-account-switcher/actions/workflows/test.yml)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
![Windows 10/11](https://img.shields.io/badge/platform-Windows%2010%20%7C%2011-lightgrey)

**Hit your limit on one Claude account, keep working on the next one. One click, no email code.**

[![Download for Windows](https://img.shields.io/badge/Download_for_Windows-.exe-2ea44f?style=for-the-badge)](https://github.com/50kg/claude-account-switcher/releases/latest/download/claude-account-switcher-windows-amd64.exe)

Unofficial. Not affiliated with or endorsed by Anthropic.

</div>

Claude Account Switcher is a tray tool for the Claude desktop app on Windows. It swaps the signed-in account in about ten seconds, carries your Claude Code chat list over to the new account, and shows the usage, reset times and subscription dates of every account in one menu.

## Features

- **Keeps every sign-in.** Each account's sign-in is saved when you switch away and put back when you switch to it. You type an email code once per account, and not again until Anthropic ends that sign-in.
- **All quotas in one menu.** Either mouse button on the tray icon opens the same native menu: for every account the 5-hour quota, the total week and, when the usage response has them, separate model weekly caps such as Fable, each with used percentage, bar and reset time. Bars turn orange from 75% and red at 100%; stale or failed readings are grey and labelled as last known. Plan badges follow Claude's displayed plan name (Pro, Max, Ultra).
- **Confirm before switching.** Browsing the menu never switches; the **Switch** button next to an account asks before it restarts Claude.
- **Chats and projects come along.** Before Claude starts, the target account's Claude Code sidebar follows the list of the account you just left, projects included.
- **Picks up interrupted chats.** Chats that were mid-reply or running a tool when you switched are listed in a small window, and Claude continues the checked ones on the new account.
- **Subscription dates.** Shows when each account's paid access ends, or its next renewal. Apple (App Store) subscriptions can be recorded by hand.
- **Optional auto-switch.** Moves to the account with the most room left when the active one is nearly full; a second toggle spends weekly allowance that is about to reset. Both are off by default.
- **Start a 5-hour window without switching.** Uses the official Claude Code to send one short Haiku prompt with the target account; the current desktop account stays as it is.
- **Notes, archive, permanent delete.** A custom note (up to 160 characters) replaces the account's name in its heading; accounts you rarely use can be archived; archived accounts can be deleted for good.
- **Move one account to another PC.** Export an account as a `.cas-account` file and import it on the other computer.
- **English and Simplified Chinese.** One exe; switch in **Settings → 语言 / Language**.

## Download and install

Requires Windows 10 or 11 and the Claude desktop app (Microsoft Store version or direct download).

1. Download `claude-account-switcher-windows-amd64.exe` from [Releases](https://github.com/50kg/claude-account-switcher/releases/latest), put it in any folder and run it. No installer, no runtime.
2. There is no main window. The icon is in the notification area at the bottom right of the taskbar (possibly under hidden icons).
3. Turn on **Start at login** in **Settings**.

The exe is not code-signed, so SmartScreen may say "Windows protected your PC" on the first start: click **More info**, then **Run anyway**. Every release file has a `.sha256` next to it; compare it with `Get-FileHash`.

**Language.** On first run the language follows the Windows display language (Chinese gives Simplified Chinese, anything else English). Upgrades from releases before 1.0, which were Chinese builds, stay in Simplified Chinese. Change it any time in **Settings → 语言 / Language**; the choice is saved in `ui.json`.

**Updating.** The switcher checks for a new release once a day and says so at the top of the menu; clicking the entry in Settings downloads it, verifies the published SHA-256, replaces the file and restarts on the new version; if that fails, the releases page opens. **Settings → Check for updates** checks immediately. You can also download the new exe and start it by hand: it asks the running older one to quit. Accounts and settings live in the data folder and are kept.

## Usage

### First run

1. Sign in to Claude, then start the switcher. It picks up that account within a minute.
2. Click **Add account** at the bottom of the menu and confirm. Claude restarts on its sign-in screen; your first account is kept.
3. Sign in with the second account. The switcher registers it and shows a notice; it does not import chats on its own. To merge chats from all accounts, choose **Settings → Import chats from all accounts…** (restarts Claude).

From then on, use the **Switch** button next to an account.

> [!IMPORTANT]
> **Never use Sign out in Claude to change accounts.** Signing out can end that sign-in on Anthropic's side, and the switcher's saved copy may stop working. Use **Add account** when you need to save a new sign-in. A failed usage query alone does not mean the desktop app has signed out.

### Menu overview

| Where | What |
| --- | --- |
| Top | **Account order**: manual (drag the dotted handle beside an account) or soonest reset with the current account first. Drag the top or bottom edge to resize; double-click an edge to reset |
| Account row | Name or note, plan, email, every quota with its reset time; **Switch** button; **⋯** account actions |
| Account **⋯** | Edit account note… · Start 5-hour window… · Sign in to Claude Code for this account… · Refresh subscription date… (View or record Apple subscription date… for Apple subscriptions) · Export (unencrypted)… · Archive account. Archived accounts also have Restore to account list and Permanently delete account… |
| Bottom | Auto-switch toggle and threshold; Add account · Settings · Quit |
| Settings | Use expiring weekly allowance · Start at login · Auto-sync interval · 语言 / Language · Archived accounts · Import one account… · Sync usage now · Import chats from all accounts… · Open data folder · Check for updates |

Switching, adding an account and importing chats restart Claude, so each asks first, with Cancel as the default. Escape, another tray click or a click outside closes the menu.

**Start 5-hour window** needs the [native Claude Code for Windows](https://code.claude.com/docs/en/setup) and a one-time official sign-in through that account's **⋯ → Sign in to Claude Code for this account…**. The account and organization are verified first; if a window is already active, its reset time is shown.

### Auto-switch

Both toggles are off by default:

- **Auto-switch** (bottom of the menu): when the active account's 5-hour or weekly usage reaches the threshold (96% by default, or 90, 94, 98%), Claude moves to the account with the most room left. The target must be at least 10 points below the threshold on both counters; the active account must have been checked in the last 5 minutes and the target in the last 15; the last switch must be more than 10 minutes ago; Claude must be running.
- **Use expiring weekly allowance** (Settings): moves to an account whose weekly limit resets within 24 hours with 50% or less used, so that allowance is not lost. It does not act within 5 hours of a switch made by hand or while a Claude Code chat was active in the last 15 minutes.

Archived accounts are never auto-switch targets. After an automatic switch, the top of the menu says why for 12 hours. Anthropic's terms do not forbid having more than one account, but they do forbid "bypassing any of our systems or protective measures"; whether auto-switch fits how you use your accounts is your call.

### Chats interrupted by a switch

A switch has to quit Claude, so Claude Code chats that are writing a reply or running a tool stop. Before Claude starts again, the switcher lists them, all checked: **Continue** lets Claude carry on with the checked ones on the new account; **Don't continue**, Escape or closing the window leaves them where they stopped; with no answer, all of them go on after 30 seconds, and touching a checkbox stops the countdown.

## How it works

- **Sign-in swap.** Claude keeps its sign-in in `config.json` (the entries `oauth:tokenCacheV2`, `oauth:tokenCache` and `lastKnownAccountUuid`) and in the cookie store `Network\Cookies` inside its data folder: `%LOCALAPPDATA%\Claude-Data\claude_<id>` for the Store app (formerly `%LOCALAPPDATA%\Packages\Claude_<id>\LocalCache\Roaming\Claude`), `%APPDATA%\Claude` for the direct download. To switch, the switcher tells Claude that Windows is ending the session, the way an installer does (and ends it only if it is still running after 20 seconds), saves those two parts for the old account, puts back the new account's, and starts Claude. Every other setting is left alone. `config.json` and the cookie store are backed up before each switch and the last 10 backups are kept; if any step fails, the backup goes back and the error shows at the top of the menu.
- **Chats and sidebar.** Claude files the Claude Code chat list per account and organization under `claude-code-sessions` and projects in `local-agent-mode-sessions\…\spaces.json`. On a switch the target list is matched to the account you left by conversation ID: missing chats are added, matching archived ones restored and extra ones archived (not deleted), and deletion markers carried along; projects are added, never deleted. The conversations themselves, in `~\.claude\projects`, are shared by all accounts; the switcher only reads the end of those written in the last 30 minutes to see which chats are mid-reply, and never writes them. Continuing a chat uses Claude's own quit mark, `interruptedByQuitAt`.
- **Usage.** The saved token cache is decrypted in memory with Claude's own DPAPI key, and `api.anthropic.com` is asked for each account's profile and usage, the same endpoints Claude and Claude Code use. By default (smart mode) the active account is checked every 2 minutes, every minute from 85%, the others every 10; **Settings → Auto-sync** sets one interval of 1–1440 minutes for all accounts, saved in `ui.json`. **Sync usage now** refreshes every account, archived ones included, and skips any still waiting to retry. After a `429` the switcher waits as long as the server asks (or backs off up to 30 minutes) and keeps showing the last numbers. A failed query does not mean the desktop app signed out, and the switcher never refreshes or rewrites Claude's credentials.
- **Subscription dates.** Every 6 hours each account's saved Claude desktop web session is used to read them from `api.anthropic.com` (claude.ai/api puts a Cloudflare browser challenge in front of non-browser clients). **⋯ → Refresh subscription date…** checks at once and opens a claude.ai verification window only if the direct check cannot complete. Dates are accepted only after the account and organization match; a failed query is told apart from a missing date, and an older date is marked as last known. While Claude runs it locks the current account's cookie store; an account never switched away from, which has no saved sign-in yet, is read from the backup the switcher made at its last restart of Claude. Without even a backup, one switch or chat import makes it readable. Apple (App Store) subscriptions have no date in Claude's billing: **⋯ → View or record Apple subscription date…** opens Apple's subscription page so you can check it and record the date by hand. Manual dates never roll forward.
- **Archived accounts.** They are not queried automatically (no usage polling, no subscription-date checks) but **Sync usage now** still refreshes them; restoring an account resumes the automatic queries. The account Claude is signed in with is still queried even when archived. They can still be switched to, exported, noted and permanently deleted.
- **The Store app quirk.** Programs started from inside the Store version of Claude, a Claude Code terminal for example, see a redirected AppData folder. The switcher notices and starts itself again through Explorer, so it sees the real files and survives Claude quitting.

## Data and privacy

| | |
| --- | --- |
| Data folder | `%USERPROFILE%\.claude-account-switcher` (**Settings → Open data folder**): `state.json` (accounts and state), `ui.json` (language, menu height, sync interval, order), `accounts\` (each account's saved sign-in and cookies), `backups\` (backups taken before switches), `session-code\` (each account's own Claude Code sign-in), `webview\` (WebView2 profile of the dialog windows), `switcher.log` (log) |
| Tokens | Stay encrypted as Claude stores them, are only sent to `api.anthropic.com` and are never logged |
| Web session cookie | Only sent to `api.anthropic.com` (direct subscription check), or to `claude.ai` in the verification window you open |
| Network | `api.anthropic.com` for profiles, usage and subscription dates. `claude.ai` only in the optional subscription verification window. `api.github.com` once a day for the latest version number, with nothing about you or your accounts. `github.com` only when you click to update. **Start 5-hour window** runs the official Claude Code you installed |
| Turn off update checks | Quit the switcher, add `"noUpdateCheck": true` to `state.json`, start it again |
| `.cas-account` export | The one plaintext exception: the file is unencrypted and contains that account's sign-in and cookies (no chats), so anyone with it can use the account. Import only adds a saved account and leaves Claude's current sign-in alone. Close Claude yourself before exporting the account in use. Keep the file private and delete it after import |
| Delete an account | Archive it, then **⋯ → Permanently delete account…** and confirm. Removes its saved sign-in, cookies, usage and billing records, its Claude Code sign-in and its backups; switch away first if it is active. Chats and exported files stay; this cannot be undone |
| Uninstall | Turn off **Start at login**, quit, delete the exe and the data folder |

## Command line

```powershell
.\claude-account-switcher-windows-amd64.exe -status
```

Prints every saved account with its usage and exits.

## Build from source

Windows and Go 1.26 or newer. No CGO.

```powershell
.\build.ps1              # vet, test, Windows resources with go-winres, build
.\build.ps1 -SkipTests   # build only
```

This writes `dist\claude-account-switcher-windows-amd64.exe`, its `.sha256` and the third-party license notices `THIRD_PARTY_NOTICES.txt`. A plain build also works (it does not regenerate the version info and manifest):

```powershell
go build -ldflags "-H=windowsgui -s -w" -o claude-account-switcher.exe .
```

Dependencies beyond the standard library: [`fyne.io/systray`](https://github.com/fyne-io/systray) (tray icon), [`jchv/go-webview2`](https://github.com/jchv/go-webview2) (dialog windows), the `pkg/edge` package of [`wailsapp/go-webview2`](https://github.com/wailsapp/go-webview2) (subscription verification window), [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) (pure Go, cookie store) and `golang.org/x/sys`. Pushing a `v*` tag makes GitHub Actions build the exe and publish it with its `.sha256`.

## Caveats

The switcher relies on how the Claude desktop app stores its files today, not on a published interface, and an app update can change that. When it does, the switcher stops, restores its backup and says so in the menu. It does not guess. It switches the desktop app, including the Claude Code chats inside it, not the sign-in of the `claude` command in a terminal.

## Contributing

Bug reports and pull requests are welcome, see [CONTRIBUTING.md](CONTRIBUTING.md). Found a security problem? Report it privately, see [SECURITY.md](SECURITY.md).

## Credits and license

Derived from [SnlperStripes/claude-account-switcher](https://github.com/SnlperStripes/claude-account-switcher), with extensive changes and new features. Thanks to the original author.

Licensed under [Apache 2.0](LICENSE). If you pass it on, changed or not, include the [`NOTICE`](NOTICE) file and say what you changed.
