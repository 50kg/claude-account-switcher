# Contributing

Thanks for wanting to help. Bug reports, ideas and pull requests are all welcome.

The most useful report right now: **breakage after a Claude update.** The switcher depends on how Claude stores its files. When an update changes that, a [bug report](https://github.com/50kg/claude-account-switcher/issues/new?template=bug_report.yml) with the Claude version and a log excerpt is the fastest way to a fix.

## Build and test

Windows 10/11, Go 1.26 or newer. No CGO.

```powershell
go vet ./...
go test ./...
.\build.ps1   # vet, test, Windows resources (go-winres), dist\claude-account-switcher-windows-amd64.exe
# or a plain build:
go build -ldflags "-H=windowsgui -s -w" -o claude-account-switcher.exe .
```

Every pull request runs vet and tests on Windows. A release is a `v*` tag that matches `const version` in `main.go`; GitHub Actions builds it with `build.ps1`.

`go test ./...` never opens the UI. The native menu smoke test runs only when you ask for it: set `QUOTA_MENU_NATIVE_SMOKE=1` and run `go test -run '^TestNativeQuotaMenuSmoke$' -count=3 -v .`. It uses synthetic accounts in an isolated window and never starts the switcher or loads real credentials; `QUOTA_MENU_EVIDENCE` optionally names a folder for screenshots of the test menu. For an offline session-index regression check, set `SESSION_INDEX_SAMPLE` to a deduplicated copy of `claude-code-sessions`; the test works on a temporary copy. Never commit real account data or session indexes.

## Where things are

| Files | What they do |
| --- | --- |
| `main.go`, `instance.go` | Start-up, flags, log, single instance and hand-over to a newer version |
| `tray.go`, `quota_menu*.go`, `icon.go` | The tray icon and the native custom menu |
| `switcher.go` | Accounts, `state.json`, the switch itself, auto-switch |
| `profiles.go`, `desktop.go` | Saving and restoring a sign-in, backups, reading Claude's data folder |
| `sessions.go`, `sidebar.go`, `spaces.go` | Syncing the Claude Code chat list and projects between accounts |
| `resume.go`, `resume.html`, `dialog_windows.go`, `webview_windows.go` | Asking which interrupted chats continue |
| `usage*.go`, `model_usage.go` | Profile and usage requests to `api.anthropic.com`, polling, backoff |
| `billing*.go`, `apple_date_windows.go` | Subscription dates and the verification window |
| `account_*.go`, `session_window_windows.go` | Notes, archive, delete, `.cas-account` export/import, the 5-hour window |
| `update.go`, `locale.go` | The daily release check and self-update, English/Chinese strings |
| `crypto_windows.go`, `platform_windows.go` | DPAPI, finding, quitting and starting Claude, start at login |

## Ground rules

These keep the tool safe to run on someone's real sign-ins:

- **Tokens stay encrypted in app-managed storage** and are never logged or sent anywhere except `api.anthropic.com`. The one opt-in exception is the user-created `.cas-account` file: deliberately unencrypted, only the selected account, and a clear warning every time.
- **Touch only what you must.** The switcher changes three `config.json` entries and the cookie store. Everything else in Claude's data stays as it is.
- **Fail loudly, restore the backup.** If Claude's files do not look as expected, stop, put the backup back and show the error in the menu. Do not guess.
- **Every user-visible string goes through `tr("English", "中文")`**, so both languages stay complete.
- **Keep dependencies deliberate.** The current set is `fyne.io/systray`, `jchv/go-webview2`, `wailsapp/go-webview2` (`pkg/edge`), `modernc.org/sqlite` (pure Go) and `golang.org/x/sys`. Talk about any new one in an issue first.

## Pull requests

- One change per pull request. Small is easier to review.
- Say what you tested on: Windows 10 or 11, Microsoft Store Claude or direct download.
- Commit messages in plain English, imperative, like the existing history ("Poll usage less often and back off after 429").
- By contributing you agree that your work is licensed under [Apache 2.0](LICENSE), like the rest of the project.
