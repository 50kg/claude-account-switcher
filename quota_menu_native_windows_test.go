package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type quotaMenuFixture struct {
	mu           sync.Mutex
	snapshot     snapshot
	input        quotaMenuInput
	actions      chan quotaMenuAction
	errors       chan string
	menu         *nativeQuotaMenu
	now          time.Time
	startup      bool
	failInterval bool
}

func newQuotaMenuFixture() *quotaMenuFixture {
	now := time.Date(2026, 11, 3, 14, 20, 0, 0, time.Local)
	allowance := 50.0
	f := &quotaMenuFixture{now: now, actions: make(chan quotaMenuAction, 16), errors: make(chan string, 16), input: quotaMenuInput{inside: true, ownForeground: true, knownForeground: true}}
	f.snapshot = snapshot{Active: "synthetic-riley", Threshold: 96}
	for i, a := range []struct {
		id, name, email, plan string
		session, week, model  float64
		reset                 time.Time
	}{
		{"synthetic-riley", "Riley Chen", "riley.chen@example.com", "Max", 67, 67, 100, time.Date(2026, 11, 3, 20, 0, 0, 0, time.Local)},
		{"synthetic-alex", "Alex Lin", "alex.lin@example.com", "Pro", 20, 34, 40, time.Date(2026, 11, 5, 20, 0, 0, 0, time.Local)},
		{"synthetic-jordan", "Jordan Wu", "jordan.wu@example.com", "Ultra", 85, 42, 76, time.Date(2026, 11, 6, 9, 0, 0, 0, time.Local)},
	} {
		at := now.Add(time.Duration([]int{40, 220, 130}[i]) * time.Minute)
		f.snapshot.Accounts = append(f.snapshot.Accounts, Account{UUID: a.id, Name: a.name, Email: a.email, Plan: a.plan, Usage: &Usage{Session: a.session, Weekly: a.week, SessionReset: at, WeeklyReset: a.reset, CheckedAt: now, ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: a.model, ResetsAt: a.reset, AllowancePercent: &allowance}}}})
	}
	m := newQuotaMenu(func() quotaMenuView {
		f.mu.Lock()
		defer f.mu.Unlock()
		return makeQuotaMenuView(f.snapshot, f.startup, f.now)
	}, func(a quotaMenuAction) error {
		f.mu.Lock()
		err := validateQuotaMenuAction(a, f.snapshot)
		if err == nil && a.Kind == "interval" && f.failInterval {
			err = errors.New("测试保存失败")
		}
		if err == nil {
			switch a.Kind {
			case "auto":
				f.snapshot.AutoSwitch = a.Enabled
			case "spend":
				f.snapshot.SpendWeekly = a.Enabled
			case "threshold":
				f.snapshot.Threshold = a.Threshold
			case "switch":
				f.snapshot.Active = a.Account
			case "autostart":
				f.startup = a.Enabled
			case "interval":
				err = saveRefreshMinutes(f.menu.preferencesPath, a.Minutes)
				if err == nil {
					f.snapshot.RefreshMinutes = a.Minutes
					f.snapshot.Status = refreshIntervalSavedText(a.Minutes)
				}
			case "sync":
				f.snapshot.Status = fmt.Sprintf("用量同步完成：已更新%d个账号", len(f.snapshot.Accounts))
			}
		}
		f.mu.Unlock()
		if err == nil {
			f.actions <- a
		}
		return err
	}, func(s string) { f.errors <- s }, "").(*nativeQuotaMenu)
	m.options = nativeQuotaMenuOptions{noActivate: true, input: func(uintptr) quotaMenuInput { f.mu.Lock(); defer f.mu.Unlock(); return f.input }}
	f.menu = m
	return f
}

func (f *quotaMenuFixture) fiveAccounts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot.Accounts[0].Usage.ModelWeekly = nil
	f.snapshot.Accounts[2].Usage.ModelWeekly = nil
	for i, name := range []string{"Casey Lee", "Taylor Chen"} {
		f.snapshot.Accounts = append(f.snapshot.Accounts, Account{UUID: fmt.Sprintf("synthetic-extra-%d", i), Name: name, Email: fmt.Sprintf("account.%d@example.com", i+4), Plan: "Max", Usage: &Usage{Session: 100, Weekly: 62, CheckedAt: f.now, SessionReset: f.now.Add(time.Hour), WeeklyReset: f.now.AddDate(0, 0, 2)}})
	}
}

func (f *quotaMenuFixture) expectAction(t *testing.T, kind string) quotaMenuAction {
	t.Helper()
	select {
	case a := <-f.actions:
		if a.Kind != kind {
			t.Fatalf("expected %s, got %+v", kind, a)
		}
		return a
	case <-time.After(5 * time.Second):
		t.Fatalf("synthetic %s action was not dispatched", kind)
		return quotaMenuAction{}
	}
}

func nativeMenuDo(t *testing.T, m *nativeQuotaMenu, fn func(*quotaMenuWindow)) {
	t.Helper()
	done := make(chan struct{})
	if err := m.invoke(func(w *quotaMenuWindow) { fn(w); close(done) }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("native menu UI thread did not respond")
	}
}

func nativeMenuWait(t *testing.T, m *nativeQuotaMenu, fn func(*quotaMenuWindow) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		nativeMenuDo(t, m, func(w *quotaMenuWindow) { ok = fn(w) })
		if ok {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("native menu state did not become ready")
}

func nativeMenuClick(t *testing.T, m *nativeQuotaMenu, x, y int) {
	t.Helper()
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		lp := uintptr(uint16(w.scale(x))) | uintptr(uint16(w.scale(y)))<<16
		procSendMessage.Call(w.hwnd, 0x201, 1, lp)
		procSendMessage.Call(w.hwnd, 0x202, 0, lp)
	})
}

func nativeMenuClickHit(t *testing.T, m *nativeQuotaMenu, match func(quotaMenuHit) bool) {
	t.Helper()
	var rect quotaRect
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		qmUpdateWindow.Call(w.hwnd)
		for _, h := range w.hits {
			if match(h) {
				rect = h.Rect
				break
			}
		}
	})
	if rect.W == 0 || rect.H == 0 {
		t.Fatal("requested menu control was not visible")
	}
	nativeMenuClick(t, m, rect.X+rect.W/2, rect.Y+rect.H/2)
}

func TestNativeQuotaMenuDoesNotStartOnHiddenUpdates(t *testing.T) {
	reads := 0
	m := newQuotaMenu(func() quotaMenuView { reads++; return quotaMenuView{} }, func(quotaMenuAction) error { return nil }, nil, "").(*nativeQuotaMenu)
	m.update()
	if reads != 0 || m.started || m.hwnd != 0 {
		t.Fatal("background update created a window or read account state")
	}
	m.close()
	if m.start() {
		t.Fatal("closed menu started a window")
	}
}

func TestQuotaMenuDismissalIgnoresOpeningClickAndUnknownForeground(t *testing.T) {
	now := time.Now()
	d := quotaMenuDismissal{}
	d.open(now, quotaMenuInput{buttons: 2})
	if d.hide(now.Add(100*time.Millisecond), quotaMenuInput{buttons: 2, ownForeground: true}) {
		t.Fatal("opening click dismissed the menu")
	}
	if d.hide(now.Add(time.Second), quotaMenuInput{knownForeground: false}) {
		t.Fatal("unknown foreground dismissed the menu")
	}
	if !d.hide(now.Add(2*time.Second), quotaMenuInput{knownForeground: true}) {
		t.Fatal("real focus transfer did not dismiss the menu")
	}
	d.open(now, quotaMenuInput{})
	if !d.hide(now.Add(time.Second), quotaMenuInput{buttons: 1, inside: false}) {
		t.Fatal("outside click was ignored")
	}
}

func TestNativeQuotaMenuSmoke(t *testing.T) {
	if os.Getenv("QUOTA_MENU_NATIVE_SMOKE") != "1" {
		t.Skip("set QUOTA_MENU_NATIVE_SMOKE=1 for isolated native tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := hidden(exec.CommandContext(ctx, exe, "-test.run=^TestNativeQuotaMenuChild$", "-test.v"))
	cmd.Env = append(os.Environ(), "QUOTA_MENU_NATIVE_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native smoke: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "NATIVE_QUOTA_MENU_VERIFIED") {
		t.Fatalf("no native completion marker: %s", output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestNativeQuotaMenuChild(t *testing.T) {
	if os.Getenv("QUOTA_MENU_NATIVE_CHILD") != "1" {
		t.Skip("isolated native helper")
	}
	useLanguage(t, "zh-CN")
	f := newQuotaMenuFixture()
	m := f.menu
	m.preferencesPath = filepath.Join(t.TempDir(), "ui.json")
	m.show()
	defer func() {
		m.close()
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
			t.Error("UI thread did not exit")
		}
	}()
	visible := user32.NewProc("IsWindowVisible")
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool {
		v, _, _ := visible.Call(w.hwnd)
		return v != 0 && len(w.hits) > 0 && len(w.view.Accounts) == 3
	})
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		auto, threshold, settings, ordering, toolbar := 0, 0, 0, 0, 0
		for _, h := range w.hits {
			if h.Action.Kind == "auto" {
				auto++
			}
			if h.Popup == "threshold" {
				threshold++
			}
			if h.Popup == "settings" {
				settings++
			}
			if h.Popup == "sort" {
				ordering++
				if h.Rect.Y+h.Rect.H > quotaMenuHeaderHeight {
					t.Error("account order control is outside the header")
				}
			}
			if h.Rect.Y >= w.bodyBottom+35 {
				toolbar++
			}
			if h.Popup == "more" {
				t.Error("removed More entry is still visible")
			}
		}
		if auto != 1 || threshold != 1 || settings != 1 || ordering != 1 || toolbar != 3 {
			t.Errorf("unexpected main menu controls: auto=%d threshold=%d settings=%d order=%d toolbar=%d", auto, threshold, settings, ordering, toolbar)
		}
	})
	dir := os.Getenv("QUOTA_MENU_EVIDENCE")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var imageErr error
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { imageErr = w.capture(filepath.Join(dir, "native-menu.png")) })
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "sort" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "sort" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		imageErr = w.capture(filepath.Join(dir, "native-header-sort.png"))
		if w.overlay.Rect.Y < quotaMenuHeaderHeight {
			t.Error("account order popup overlaps its header control")
		}
		procSendMessage.Call(w.hwnd, 0x100, 27, 0)
	})
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil })
	// Read-only body clicks never reach the account action callback.
	nativeMenuClick(t, m, 36, 120)
	if len(f.actions) != 0 || !m.shown.Load() {
		t.Fatal("reading quota triggered a switch or hid the menu")
	}
	// Account identities are read-only; all accounts stay expanded.
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		procSendMessage.Call(w.hwnd, 0x201, 1, uintptr(60)|uintptr(70)<<16)
		procSendMessage.Call(w.hwnd, 0x202, 0, uintptr(60)|uintptr(70)<<16)
		if w.overlay != nil {
			t.Error("identity opened an account action")
		}
	})
	// The real custom confirmation, including the safe keyboard default.
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "switch" && h.Action.Account == "synthetic-alex" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "confirm" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		imageErr = w.capture(filepath.Join(dir, "native-confirmation.png"))
		procSendMessage.Call(w.hwnd, 0x100, 13, 0)
	})
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil })
	if len(f.actions) != 0 || !m.shown.Load() {
		t.Fatal("default confirmation key did not cancel")
	}
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "switch" && h.Action.Account == "synthetic-alex" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "confirm" })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "switch" && h.Action.Confirmed })
	select {
	case a := <-f.actions:
		if a.Kind != "switch" || a.Account != "synthetic-alex" || !a.Confirmed {
			t.Fatalf("wrong explicit action: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirmed synthetic switch was not dispatched")
	}
	m.show()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && w.view.Active == "synthetic-alex" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { procSendMessage.Call(w.hwnd, 0x100, 27, 0) })
	if m.shown.Load() {
		t.Fatal("Escape did not dismiss the menu")
	}
	m.show()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { procSendMessage.Call(w.hwnd, 0x10, 0, 0) })
	if m.shown.Load() {
		t.Fatal("user window close did not hide menu")
	}
	m.show()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() })
	// A native outside-click signal only affects the synthetic menu window.
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { w.dismiss.open(time.Now().Add(-time.Second), quotaMenuInput{}) })
	f.mu.Lock()
	f.input = quotaMenuInput{buttons: 1, inside: false}
	f.mu.Unlock()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return !m.shown.Load() })
	f.mu.Lock()
	f.input = quotaMenuInput{inside: true, ownForeground: true, knownForeground: true}
	f.snapshot.Active = "synthetic-riley"
	f.mu.Unlock()
	m.show()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && w.view.Active == "synthetic-riley" })
	var toggleRect quotaRect
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		thresholdHit := w.hit(154, w.bodyBottom+20)
		if thresholdHit < 0 || w.hits[thresholdHit].Popup != "threshold" {
			t.Error("threshold text is not an independent settings target")
		}
		for _, h := range w.hits {
			if h.Action.Kind == "auto" {
				toggleRect = h.Rect
			}
		}
	})
	nativeMenuClick(t, m, toggleRect.X+toggleRect.W-15, toggleRect.Y+15)
	select {
	case a := <-f.actions:
		if a.Kind != "auto" || !a.Enabled {
			t.Fatalf("wrong toggle: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic auto toggle not dispatched")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && w.view.Auto })
	// Threshold selection is separate from Auto and uses the same custom UI.
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "threshold" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "threshold" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		imageErr = w.capture(filepath.Join(dir, "native-threshold.png"))
		procSendMessage.Call(w.hwnd, 0x100, 36, 0) // Home
		procSendMessage.Call(w.hwnd, 0x100, 40, 0) // Down
		procSendMessage.Call(w.hwnd, 0x100, 13, 0) // Enter: 94%
	})
	if a := f.expectAction(t, "threshold"); a.Threshold != 94 {
		t.Fatalf("wrong threshold: %+v", a)
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && w.view.Threshold == 94 && w.view.Auto })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		imageErr = w.capture(filepath.Join(dir, "native-settings.png"))
		counts := map[string]int{}
		for _, h := range w.hits {
			if h.Popup != "" && h.Popup != "interval" {
				t.Error("settings unexpectedly added a nested submenu")
			}
			counts[h.Action.Kind]++
		}
		if len(w.hits) != 8 {
			t.Errorf("settings has %d actions, want eight", len(w.hits))
		}
		for _, kind := range []string{"spend", "autostart", "import", "folder", "update", "sync"} {
			if counts[kind] != 1 {
				t.Errorf("settings action %s missing or repeated", kind)
			}
		}
	})
	verifyNativeInterval(t, f, dir)
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "spend" })
	if a := f.expectAction(t, "spend"); !a.Enabled {
		t.Fatal("spend setting did not enable")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && w.view.Spend })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "autostart" })
	if a := f.expectAction(t, "autostart"); !a.Enabled {
		t.Fatal("startup setting did not enable")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && w.view.Startup })
	for _, kind := range []string{"settings"} {
		nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == kind })
		nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == kind })
		nativeMenuDo(t, m, func(w *quotaMenuWindow) { procSendMessage.Call(w.hwnd, 0x100, 27, 0) })
		if !m.shown.Load() {
			t.Fatal("submenu Escape hid the whole menu")
		}
	}
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { imageErr = w.capture(filepath.Join(dir, "native-settings-enabled.png")) })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "import" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "confirm" })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Dismiss })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "add" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "confirm" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { procSendMessage.Call(w.hwnd, 0x100, 27, 0) })
	if len(f.actions) != 0 {
		t.Fatal("cancelled add/import dispatched an action")
	}
	// Tools are reached directly from Settings. The fixture only records the
	// intent; it never opens a real folder or makes a network request.
	for _, kind := range []string{"folder", "update"} {
		nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
		nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
		nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == kind })
		f.expectAction(t, kind)
		if m.shown.Load() {
			t.Fatal("tool command did not close menu")
		}
		m.show()
		nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && w.overlay == nil })
	}
	// An empty part of the parent closes only the overlay, never an account.
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil })
	nativeMenuClick(t, m, 10, 10)
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && m.shown.Load() })
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	// Refreshing while a confirmation is open must not retarget its intent.
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "switch" && h.Action.Account == "synthetic-alex" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "confirm" })
	f.mu.Lock()
	f.snapshot.Active = "synthetic-jordan"
	f.mu.Unlock()
	m.update()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.view.Active == "synthetic-jordan" })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Confirmed })
	select {
	case <-f.errors:
	case <-time.After(5 * time.Second):
		t.Fatal("stale confirmation was not rejected")
	}
	if len(f.actions) != 0 {
		t.Fatal("stale confirmation switched an account")
	}
	f.mu.Lock()
	f.snapshot.Active = "synthetic-riley"
	f.snapshot.Busy = true
	f.mu.Unlock()
	m.show()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && w.view.Busy })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		qmUpdateWindow.Call(w.hwnd)
		for _, h := range w.hits {
			if h.Action.Kind == "switch" || h.Action.Kind == "add" {
				t.Error("busy account action remained clickable")
			}
		}
	})
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		qmUpdateWindow.Call(w.hwnd)
		for _, h := range w.hits {
			if h.Action.Kind == "import" {
				t.Error("busy import remained clickable")
			}
		}
		procSendMessage.Call(w.hwnd, 0x100, 27, 0)
	})
	f.mu.Lock()
	f.snapshot.Busy = false
	f.mu.Unlock()
	f.mu.Lock()
	f.snapshot.Status = accountAddedNotice()
	f.mu.Unlock()
	m.update()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.view.Status == accountAddedNotice() })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { imageErr = w.capture(filepath.Join(dir, "native-account-added.png")) })
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	f.fiveAccounts()
	f.mu.Lock()
	f.snapshot.Status = ""
	f.mu.Unlock()
	m.update()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return len(w.view.Accounts) == 5 && w.view.Status == "" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		models := 0
		for _, a := range w.view.Accounts {
			if len(a.Quotas) == 3 {
				models++
			}
		}
		if models != 1 {
			t.Error("missing Fable data produced invented model rows")
		}
		imageErr = w.capture(filepath.Join(dir, "native-five-accounts.png"))
	})
	verifyNativeMenuResizing(t, f, dir)
	// Long identities, failed queries and a constrained 150% viewport remain readable.
	f.mu.Lock()
	f.snapshot.Accounts[0].Name = "A very long account name with & characters"
	f.snapshot.Accounts[0].Email = "long.account.identity.for.testing@example.com"
	f.snapshot.Accounts[0].Problem = usageLimitedMessage
	f.mu.Unlock()
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		w.menu.options.dpiOverride = 144
		w.reload(false)
		imageErr = w.capture(filepath.Join(dir, "native-menu-stress.png"))
	})
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		var r windows.Rect
		procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
		if int(r.Left) < w.work.X || int(r.Top) < w.work.Y || int(r.Right) > w.work.X+w.work.W || int(r.Bottom) > w.work.Y+w.work.H {
			t.Error("scaled window escaped its work area")
		}
		procSendMessage.Call(w.hwnd, 0x20A, uintptr(uint32(0xff880000)), 0)
	})
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.contentHeight > w.bodyBottom-w.bodyTop && w.scroll == 0 {
			t.Error("overflowing body did not scroll")
		}
	})
	// Many accounts must remain reachable without growing the menu or
	// dispatching account actions while scrolling.
	f.mu.Lock()
	for i := len(f.snapshot.Accounts); i < 60; i++ {
		f.snapshot.Accounts = append(f.snapshot.Accounts, Account{UUID: fmt.Sprintf("synthetic-many-%d", i), Name: fmt.Sprintf("Account %02d", i+1), Email: fmt.Sprintf("account.%02d@example.com", i+1), Plan: "Max", Usage: &Usage{Session: 30, Weekly: 45, CheckedAt: f.now, SessionReset: f.now.Add(time.Hour), WeeklyReset: f.now.AddDate(0, 0, 2)}})
	}
	f.mu.Unlock()
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		w.menu.options.dpiOverride = 96
		w.reload(false)
		procSendMessage.Call(w.hwnd, 0x100, 36, 0) // Home: first account
		imageErr = w.capture(filepath.Join(dir, "native-many-top.png"))
	})
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	var fixedTop, fixedBottom int
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		fixedTop, fixedBottom = w.bodyTop, w.bodyBottom
		if len(w.view.Accounts) != 60 || w.bodyBottom-w.bodyTop > quotaMenuMaxBodyHeight || w.maxScroll() == 0 || w.scroll != 0 {
			t.Error("large account list was truncated or did not have bounded scrolling")
		}
		// Four partial wheel deltas form one notch (precision touchpads).
		for i := 0; i < 4; i++ {
			procSendMessage.Call(w.hwnd, 0x20A, uintptr(uint32(0xffe20000)), 0)
		}
		if w.scroll != 60 {
			t.Error("partial wheel input did not accumulate")
		}
		procSendMessage.Call(w.hwnd, 0x100, 34, 0)
		if w.scroll <= 60 {
			t.Error("Page Down did not scroll the list")
		}
		procSendMessage.Call(w.hwnd, 0x100, 33, 0)
		if w.scroll != 60 {
			t.Error("Page Up did not return to the previous position")
		}
		procSendMessage.Call(w.hwnd, 0x100, 35, 0)
		if w.scroll != w.maxScroll() {
			t.Error("End did not reach the last account")
		}
		procSendMessage.Call(w.hwnd, 0x100, 36, 0)
	})
	var track, thumb quotaRect
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { track, thumb = w.scrollbar() })
	nativeMenuClick(t, m, track.X+track.W/2, track.Y+track.H-2)
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.scroll == 0 {
			t.Error("scrollbar track click did not page down")
		}
		procSendMessage.Call(w.hwnd, 0x100, 36, 0)
		track, thumb = w.scrollbar()
		x := track.X + track.W/2
		start := uintptr(uint16(w.scale(x))) | uintptr(uint16(w.scale(thumb.Y+thumb.H/2)))<<16
		end := uintptr(uint16(w.scale(x))) | uintptr(uint16(w.scale(track.Y+track.H+60)))<<16
		procSendMessage.Call(w.hwnd, 0x201, 1, start)
		procSendMessage.Call(w.hwnd, 0x200, 1, end)
		procSendMessage.Call(w.hwnd, 0x202, 0, end)
		if w.scroll != w.maxScroll() || w.scrollDragging {
			t.Error("dragging the scrollbar did not reach the end or release capture")
		}
		if w.bodyTop != fixedTop || w.bodyBottom != fixedBottom {
			t.Error("scrolling moved the header or footer")
		}
		imageErr = w.capture(filepath.Join(dir, "native-many-bottom.png"))
	})
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "switch" && h.Action.Account == "synthetic-many-59" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool {
		return w.overlay != nil && w.overlay.Confirm.Account == "synthetic-many-59"
	})
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		procSendMessage.Call(w.hwnd, 0x100, 27, 0)
		procSendMessage.Call(w.hwnd, 0x100, 36, 0)
		if w.scroll != 0 {
			t.Error("Home did not restore the top of the list")
		}
	})
	if len(f.actions) != 0 {
		t.Fatal("browsing the long list dispatched an account action")
	}
	// The combined settings popup also fits before the first account exists.
	f.mu.Lock()
	f.snapshot.Accounts = nil
	f.snapshot.Active = ""
	f.mu.Unlock()
	m.update()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return len(w.view.Accounts) == 0 })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		imageErr = w.capture(filepath.Join(dir, "native-settings-empty.png"))
		r := w.overlay.Rect
		if r.Y < 0 || r.Y+r.H > w.height || len(w.hits) != 7 {
			t.Error("combined settings clipped its actions in an empty account list")
		}
		procSendMessage.Call(w.hwnd, 0x100, 36, 0)
		procSendMessage.Call(w.hwnd, 0x100, 40, 0)
		procSendMessage.Call(w.hwnd, 0x100, 13, 0)
	})
	if a := f.expectAction(t, "autostart"); a.Enabled {
		t.Fatal("keyboard preference selection did not reach Start at login")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && !w.view.Startup })
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	if len(f.errors) != 0 {
		t.Fatal(<-f.errors)
	}
	m.close()
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
		t.Fatal("window close did not exit native thread")
	}
	t.Log("NATIVE_QUOTA_MENU_VERIFIED: first display, fields, expanded accounts, read-only clicks, custom menus, cancel/confirm, Escape, outside click, reopen, scaled bounds, scroll, shutdown; only synthetic actions")
}

// The interactive reviewer operates only this test host via window messages.
// It has no switcher instance, disk state, credential reader or usage client.
func TestNativeQuotaMenuReview(t *testing.T) {
	if os.Getenv("QUOTA_MENU_REVIEW") != "1" {
		t.Skip("isolated interactive review host")
	}
	useLanguage(t, "zh-CN")
	f := newQuotaMenuFixture()
	f.fiveAccounts()
	m := f.menu
	m.preferencesPath = filepath.Join(t.TempDir(), "ui.json")
	m.show()
	defer func() {
		m.close()
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
			t.Error("review UI thread did not exit")
		}
	}()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && len(w.hits) > 0 })
	if os.Getenv("QUOTA_MENU_REVIEW_SETTINGS") == "1" {
		nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
		nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	}
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("ISOLATED_NATIVE_REVIEW_READY")
	for scanner.Scan() {
		var req struct {
			Op, Path  string
			Text      string
			X, Y, Key int
			DX, DY    int
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			fmt.Println(err)
			continue
		}
		if req.Op == "close" {
			return
		}
		if req.Op == "open" {
			m.show()
		}
		if req.Op == "click" {
			nativeMenuDo(t, m, func(w *quotaMenuWindow) {
				lp := uintptr(uint16(w.scale(req.X))) | uintptr(uint16(w.scale(req.Y)))<<16
				qmPostMessage.Call(w.hwnd, 0x201, 1, lp)
				qmPostMessage.Call(w.hwnd, 0x202, 0, lp)
			})
		}
		if req.Op == "drag" || req.Op == "doubleclick" {
			nativeMenuDo(t, m, func(w *quotaMenuWindow) {
				start := uintptr(uint16(w.scale(req.X))) | uintptr(uint16(w.scale(req.Y)))<<16
				if req.Op == "doubleclick" {
					procSendMessage.Call(w.hwnd, 0x203, 0, start)
					return
				}
				move := uintptr(uint16(w.scale(req.X+req.DX))) | uintptr(uint16(w.scale(req.Y+req.DY)))<<16
				procSendMessage.Call(w.hwnd, 0x201, 1, start)
				procSendMessage.Call(w.hwnd, 0x200, 1, move)
				procSendMessage.Call(w.hwnd, 0x202, 0, move)
			})
		}
		if req.Op == "key" {
			m.mu.Lock()
			hwnd := m.hwnd
			m.mu.Unlock()
			qmPostMessage.Call(hwnd, 0x100, uintptr(req.Key), 0)
		}
		if req.Op == "type" {
			nativeMenuDo(t, m, func(w *quotaMenuWindow) {
				for _, ch := range req.Text {
					procSendMessage.Call(w.hwnd, 0x102, uintptr(ch), 0)
				}
			})
		}
		nativeMenuDo(t, m, func(w *quotaMenuWindow) {
			if req.Op == "capture" {
				if err := w.capture(req.Path); err != nil {
					fmt.Println(err)
				}
			}
			var popupItems []string
			overlay := ""
			if w.overlay != nil {
				overlay = w.overlay.Kind
				for _, choice := range w.choices(overlay) {
					popupItems = append(popupItems, choice.Label)
				}
			}
			output := struct {
				Visible         bool
				Width, Height   int
				View            quotaMenuView
				Hits            []quotaMenuHit
				Overlay         string
				PopupItems      []string
				PreferredHeight int
				Input, Error    string
			}{m.shown.Load(), w.width, w.height, w.view, w.hits, overlay, popupItems, w.preferredHeight, "", ""}
			if w.overlay != nil {
				output.Input = w.overlay.Input
				output.Error = w.overlay.Error
			}
			encoded, _ := json.Marshal(output)
			fmt.Println(string(encoded))
		})
	}
}
