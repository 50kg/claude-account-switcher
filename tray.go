package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/systray"
)

// The auto-switch thresholds the menu offers.
var thresholds = []float64{90, 94, 96, 98}

// tray owns the notification icon. Every command lives in the native quota
// menu, which opens on either mouse button.
type tray struct {
	s        *switcher
	lastIcon string
	updates  chan struct{}
	menu     quotaMenu
}

func (t *tray) onReady() {
	systray.SetTooltip(tr("Claude Account Switcher", "Claude 账号切换器"))
	t.setIcon(-1, defaultThreshold)
	// Rewrite the login entry so it follows this exe after a move or update.
	if autostartEnabled() {
		if err := setAutostart(true); err != nil {
			log.Printf("refresh start at login: %v", err)
		}
	}
	t.menu = newQuotaMenu(func() quotaMenuView {
		return makeQuotaMenuView(t.s.snapshot(), autostartEnabled(), time.Now())
	}, t.handleQuotaMenuAction, t.s.setStatus, t.uiPath())
	show := func() { go t.menu.show() }
	systray.SetOnTapped(show)
	systray.SetOnSecondaryTapped(show)

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

func (t *tray) uiPath() string { return filepath.Join(t.s.dir, "ui.json") }

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
		go t.s.refreshBilling()
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

// render updates the tooltip, the ring icon and an open menu.
func (t *tray) render() {
	snap := t.s.snapshot()
	now := time.Now()
	tip := tr("Claude Account Switcher", "Claude 账号切换器")
	pct := -1.0
	for _, a := range snap.Accounts {
		if a.UUID == snap.Active {
			tip = accountTooltip(a, now)
			if a.Usage != nil {
				pct = a.Usage.current(now).Session
			}
			break
		}
	}
	systray.SetTooltip(tip)
	t.setIcon(pct, snap.Threshold)
	t.menu.update()
}

func (t *tray) handleQuotaMenuAction(action quotaMenuAction) error {
	if err := validateQuotaMenuAction(action, t.s.snapshot()); err != nil {
		return err
	}
	switch action.Kind {
	case "switch":
		t.s.run(tr("Switch", "切换账号"), func() error { return t.s.switchTo(action.Account, byHand) })
	case "add":
		t.s.run(tr("Add account", "添加账号"), t.s.addAccount)
	case "import":
		t.s.run(tr("Import", "导入聊天"), t.s.importChats)
	case "auto":
		t.s.setAuto(action.Enabled)
	case "spend":
		t.s.setSpendWeekly(action.Enabled)
	case "threshold":
		t.s.setThreshold(action.Threshold)
	case "interval":
		return t.s.setRefreshMinutes(action.Minutes)
	case "sort":
		return saveQuotaMenuSortMode(t.uiPath(), action.SortMode)
	case "order":
		return saveQuotaMenuAccountOrder(t.uiPath(), action.AccountOrder)
	case "language":
		if err := saveLanguagePreference(t.uiPath(), action.Language); err != nil {
			return err
		}
		_ = setLanguage(action.Language)
		t.s.onChange()
	case "account-export":
		return handleAccountExport(t.s, action.Account)
	case "account-note":
		return t.s.setAccountNote(action.Account, action.Note)
	case "archive":
		return t.s.setAccountArchived(action.Account, true)
	case "unarchive":
		return t.s.setAccountArchived(action.Account, false)
	case "account-delete":
		return t.s.deleteArchivedAccount(action.Account)
	case "billing-web":
		return verifyBillingWeb(t.s, action.Account)
	case "session-login", "session-start":
		return handleSessionWindowAction(t.s, action.Account, action.Kind == "session-login")
	case "account-import":
		return handleAccountImport(t.s)
	case "sync":
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return t.s.syncUsageNow(ctx)
	case "autostart":
		if err := setAutostart(action.Enabled); err != nil {
			return err
		}
		t.s.onChange()
	case "folder":
		shellOpen(t.s.dir)
	case "update":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		t.s.requestUpdate(ctx)
	case "quit":
		systray.Quit()
	}
	return nil
}

// usageText is the one-line usage summary -status prints.
func usageText(a Account, threshold float64, now time.Time) string {
	problem := usageProblemText(a, now)
	if a.Usage == nil {
		if problem != "" {
			return problem
		}
		return tr("usage unknown", "用量未知")
	}
	u := a.Usage.current(now)
	parts := []string{fmt.Sprintf(tr("5h %.0f%%", "5小时 %.0f%%"), u.Session), fmt.Sprintf(tr("week %.0f%%", "每周 %.0f%%"), u.Weekly)}
	if len(u.ModelWeekly) > 0 {
		parts[1] = fmt.Sprintf(tr("total week %.0f%%", "总周 %.0f%%"), u.Weekly)
	}
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
	for _, model := range u.ModelWeekly {
		parts = append(parts, modelWeeklyText(model, freshModelUsage(a, now), now))
	}
	text := strings.Join(parts, " · ")
	if problem != "" {
		return problem + tr(" · last known: ", " · 上次用量：") + text
	}
	return text
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
