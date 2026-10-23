package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"fyne.io/systray"
)

const maxAccounts = 8

var thresholds = []float64{90, 94, 96, 98}

type tray struct {
	s          *switcher
	status     *systray.MenuItem
	update     *systray.MenuItem
	accounts   [maxAccounts]*systray.MenuItem
	auto       *systray.MenuItem
	spend      *systray.MenuItem
	thresholds []*systray.MenuItem
	add        *systray.MenuItem
	importNow  *systray.MenuItem
	autostart  *systray.MenuItem
	lastIcon   string
	updates    chan struct{}
}

func (t *tray) onReady() {
	systray.SetTooltip(tr("Claude Account Switcher", "Claude 账号切换器"))
	t.setIcon(-1, defaultThreshold)

	t.status = systray.AddMenuItem("", "")
	t.status.Disable()
	t.status.Hide()
	t.update = systray.AddMenuItem("", tr("Open the download page", "打开下载页面"))
	t.update.Hide()
	for i := range t.accounts {
		t.accounts[i] = systray.AddMenuItemCheckbox("", tr("Switch the Claude app to this account", "将 Claude 切换到此账号"), false)
		t.accounts[i].Hide()
	}
	systray.AddSeparator()
	t.auto = systray.AddMenuItemCheckbox(tr("Auto-switch when nearly full", "额度接近用尽时自动切换"), tr("Switch to the account with the most room left", "自动切换到剩余额度最多的账号"), false)
	limit := systray.AddMenuItem(tr("Auto-switch at", "自动切换阈值"), "")
	for _, v := range thresholds {
		t.thresholds = append(t.thresholds, limit.AddSubMenuItemCheckbox(fmt.Sprintf("%.0f%%", v), "", false))
	}
	t.spend = systray.AddMenuItemCheckbox(tr("Auto-switch to use up expiring weekly", "优先使用即将重置的周额度"), tr("Switch to an account whose weekly limit resets within a day with half or more of it unused", "自动切换到周额度将在一天内重置、且剩余至少一半周额度的账号"), false)
	t.add = systray.AddMenuItem(tr("Add account…", "添加账号…"), tr("Restart Claude on its sign-in screen, your current account is kept", "保留当前账号的登录状态，重启 Claude 并打开登录页面"))
	t.importNow = systray.AddMenuItem(tr("Import chats from all accounts", "导入所有账号的聊天"), tr("Restart Claude so every chat shows up in this account", "重启 Claude，让所有账号的聊天都显示在当前账号中"))
	systray.AddSeparator()
	// Rewriting an enabled entry keeps its path current and adds the Task
	// Manager record that entries from before v0.1.2 are missing.
	if autostartEnabled() {
		if err := setAutostart(true); err != nil {
			log.Printf("refresh start at login: %v", err)
		}
	}
	t.autostart = systray.AddMenuItemCheckbox(tr("Start at login", "开机启动"), "", autostartEnabled())
	folder := systray.AddMenuItem(tr("Open data folder", "打开数据文件夹"), "")
	quit := systray.AddMenuItem(tr("Quit", "退出"), "")

	for i, item := range t.accounts {
		go func() {
			for range item.ClickedCh {
				snap := t.s.snapshot()
				if i < len(snap.Accounts) && snap.Accounts[i].UUID != snap.Active {
					go t.s.run(tr("Switch", "切换账号"), func() error { return t.s.switchTo(snap.Accounts[i].UUID, byHand) })
				}
				t.s.onChange()
			}
		}()
	}
	for i, item := range t.thresholds {
		go func() {
			for range item.ClickedCh {
				t.s.setThreshold(thresholds[i])
			}
		}()
	}
	go func() {
		for {
			select {
			case <-t.auto.ClickedCh:
				t.s.setAuto(!t.auto.Checked())
			case <-t.spend.ClickedCh:
				t.s.setSpendWeekly(!t.spend.Checked())
			case <-t.add.ClickedCh:
				go t.s.run(tr("Add account", "添加账号"), t.s.addAccount)
			case <-t.importNow.ClickedCh:
				go t.s.run(tr("Import", "导入聊天"), t.s.importChats)
			case <-t.autostart.ClickedCh:
				if err := setAutostart(!t.autostart.Checked()); err != nil {
					t.s.setStatus(tr("Start at login: ", "设置开机启动失败：") + err.Error())
				}
				setChecked(t.autostart, autostartEnabled())
			case <-t.update.ClickedCh:
				shellOpen(repoURL + "/releases/latest")
			case <-folder.ClickedCh:
				shellOpen(t.s.dir)
			case <-quit.ClickedCh:
				systray.Quit()
			}
		}
	}()

	// Menu updates arrive from worker goroutines; one goroutine applies them.
	t.s.onChange = func() {
		select {
		case t.updates <- struct{}{}:
		default:
		}
	}
	go func() {
		for range t.updates {
			t.render()
		}
	}()
	t.s.onChange()
	go t.loop()
	go t.updateLoop()
}

// loop runs refresh every few seconds while a new sign-in is expected,
// otherwise every minute; refresh decides which accounts are due.
func (t *tray) loop() {
	var last time.Time
	for ; ; time.Sleep(5 * time.Second) {
		if !t.s.adding() && time.Since(last) < hotEvery {
			continue
		}
		last = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		t.s.refresh(ctx)
		cancel()
	}
}

// updateLoop looks for a new release a minute after start, then every hour;
// checkUpdate decides whether a day has passed since the last answer.
func (t *tray) updateLoop() {
	time.Sleep(time.Minute)
	for {
		t.s.checkUpdate(context.Background())
		time.Sleep(time.Hour)
	}
}

func (t *tray) render() {
	snap := t.s.snapshot()
	now := time.Now()

	switch {
	case snap.Status != "":
		t.status.SetTitle(snap.Status)
		t.status.Show()
	case snap.Note != "":
		t.status.SetTitle(snap.Note)
		t.status.Show()
	default:
		t.status.Hide()
	}
	if snap.Update != "" {
		t.update.SetTitle(fmt.Sprintf(tr("Version %s is out, click to download", "发现新版本 %s，点击下载"), snap.Update))
		t.update.Show()
	} else {
		t.update.Hide()
	}
	if len(snap.Accounts) == 0 && snap.Status == "" {
		t.status.SetTitle(tr("Open Claude and sign in", "请打开 Claude 并登录账号"))
		t.status.Show()
	}

	activePct := -1.0
	tip := tr("Claude Account Switcher", "Claude 账号切换器")
	for i, item := range t.accounts {
		if i >= len(snap.Accounts) {
			item.Hide()
			continue
		}
		a := snap.Accounts[i]
		detail := usageText(a, snap.Threshold, now)
		item.SetTitle(a.label() + menuTab + detail)
		setChecked(item, a.UUID == snap.Active)
		if snap.Busy {
			item.Disable()
		} else {
			item.Enable()
		}
		item.Show()
		if a.UUID == snap.Active {
			tip = a.label() + "\n" + detail
			if a.Usage != nil {
				activePct = a.Usage.current(now).Session
			}
		}
	}
	systray.SetTooltip(tip)
	t.setIcon(activePct, snap.Threshold)

	setChecked(t.auto, snap.AutoSwitch)
	setChecked(t.spend, snap.SpendWeekly)
	for i, item := range t.thresholds {
		setChecked(item, thresholds[i] == snap.Threshold)
	}
	for _, item := range []*systray.MenuItem{t.add, t.importNow} {
		if snap.Busy {
			item.Disable()
		} else {
			item.Enable()
		}
	}
}

// menuTab right-aligns the usage on Windows, where a tab in a menu title
// starts a right-aligned column.
var menuTab = map[bool]string{true: "\t", false: "    "}[runtime.GOOS == "windows"]

func usageText(a Account, threshold float64, now time.Time) string {
	if a.Problem != "" {
		// Accept state saved by both the original and the earlier localized build.
		if a.Problem == signedOutMessage || a.Problem == "登录已失效，请重新添加账号" {
			return tr(signedOutMessage, "登录已失效，请重新添加账号")
		}
		return a.Problem
	}
	if a.Usage == nil {
		return tr("usage unknown", "用量未知")
	}
	u := a.Usage.current(now)
	parts := []string{fmt.Sprintf(tr("5h %.0f%%", "5小时 %.0f%%"), u.Session), fmt.Sprintf(tr("week %.0f%%", "每周 %.0f%%"), u.Weekly)}
	if u.Session >= threshold && !u.SessionReset.IsZero() {
		parts[0] += tr(" until ", "，重置于 ") + resetText(u.SessionReset, now)
	}
	// The weekly reset is always shown: it decides how much of the week is left to use.
	if !u.WeeklyReset.IsZero() && u.WeeklyReset.After(now) {
		prefix := tr(", resets ", "，重置于 ")
		if u.Weekly >= threshold {
			prefix = tr(" until ", "，重置于 ")
		}
		parts[1] += prefix + resetText(u.WeeklyReset, now)
	}
	return strings.Join(parts, " · ")
}

func resetText(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	weekday := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[at.Weekday()]
	switch {
	case at.YearDay() == now.YearDay() && at.Year() == now.Year():
		return at.Format("15:04")
	case at.Sub(now) < 6*24*time.Hour:
		return tr(at.Format("Mon 15:04"), weekday+" "+at.Format("15:04"))
	}
	// A weekday alone would read as today for a reset a week out.
	return tr(at.Format("Mon 2 Jan 15:04"), at.Format("1月2日")+" "+weekday+" "+at.Format("15:04"))
}

func (t *tray) setIcon(pct, threshold float64) {
	key := fmt.Sprintf("%.0f/%.0f", pct, threshold)
	if key == t.lastIcon {
		return
	}
	t.lastIcon = key
	systray.SetIcon(trayIcon(pct, threshold))
}

func setChecked(item *systray.MenuItem, on bool) {
	if on != item.Checked() {
		if on {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}
