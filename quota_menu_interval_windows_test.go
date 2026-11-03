package main

import (
	"path/filepath"
	"testing"
)

func nativeIntervalType(t *testing.T, m *nativeQuotaMenu, value string) {
	t.Helper()
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "interval-input" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		for _, ch := range value {
			procSendMessage.Call(w.hwnd, 0x102, uintptr(ch), 0)
		}
	})
}

func nativeOpenInterval(t *testing.T, m *nativeQuotaMenu) {
	t.Helper()
	var settings bool
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { settings = w.overlay != nil && w.overlay.Kind == "settings" })
	if !settings {
		nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
		nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	}
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "interval" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "interval" })
}

func verifyNativeInterval(t *testing.T, f *quotaMenuFixture, dir string) {
	t.Helper()
	m := f.menu
	nativeOpenInterval(t, m)
	nativeIntervalType(t, m, "7")
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if err := w.capture(filepath.Join(dir, "native-interval.png")); err != nil {
			t.Error(err)
		}
		procSendMessage.Call(w.hwnd, 0x100, 13, 0)
	})
	if a := f.expectAction(t, "interval"); a.Minutes != 7 {
		t.Fatalf("wrong custom interval: %+v", a)
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && w.view.RefreshMinutes == 7 })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.view.Status != refreshIntervalSavedText(7) {
			t.Error("saved interval feedback missing")
		}
		if err := w.capture(filepath.Join(dir, "native-interval-saved.png")); err != nil {
			t.Error(err)
		}
	})
	p, err := loadQuotaMenuPreferences(m.preferencesPath)
	if err != nil || p.RefreshMinutes != 7 {
		t.Fatal("custom interval was not saved")
	}
	nativeOpenInterval(t, m)
	nativeIntervalType(t, m, "0")
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "interval-save" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Error != "" })
	if len(f.actions) != 0 {
		t.Fatal("invalid interval dispatched")
	}
	nativeIntervalType(t, m, "1441")
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "interval-save" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Error != "" })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Dismiss })
	if f.snapshot.RefreshMinutes != 7 {
		t.Fatal("cancelled edit changed interval")
	}
	f.mu.Lock()
	f.failInterval = true
	f.mu.Unlock()
	nativeOpenInterval(t, m)
	nativeIntervalType(t, m, "11")
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "interval-save" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && !w.overlay.Saving && w.overlay.Error != "" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.overlay.Input != "11" || w.view.RefreshMinutes != 7 {
			t.Error("save failure lost draft or changed interval")
		}
		if err := w.capture(filepath.Join(dir, "native-interval-error.png")); err != nil {
			t.Error(err)
		}
	})
	f.mu.Lock()
	f.failInterval = false
	f.mu.Unlock()
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "interval-save" })
	if a := f.expectAction(t, "interval"); a.Minutes != 11 {
		t.Fatal("draft retry did not save")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && w.view.RefreshMinutes == 11 })
	nativeOpenInterval(t, m)
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "interval-smart" })
	if a := f.expectAction(t, "interval"); a.Minutes != 0 {
		t.Fatal("smart mode not restored")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil && w.view.RefreshMinutes == 0 })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.view.Status != refreshIntervalSavedText(0) {
			t.Error("smart-mode feedback missing")
		}
		if err := w.capture(filepath.Join(dir, "native-smart-saved.png")); err != nil {
			t.Error(err)
		}
	})
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "sync" })
	f.expectAction(t, "sync")
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && w.overlay == nil })
	f.mu.Lock()
	f.snapshot.Syncing = true
	f.mu.Unlock()
	m.update()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.view.Syncing })
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		qmUpdateWindow.Call(w.hwnd)
		for _, h := range w.hits {
			if h.Action.Kind == "sync" {
				t.Error("in-flight sync remained clickable")
			}
		}
		if err := w.capture(filepath.Join(dir, "native-syncing.png")); err != nil {
			t.Error(err)
		}
	})
	f.mu.Lock()
	f.snapshot.Syncing = false
	f.mu.Unlock()
	m.update()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return !w.view.Syncing })
}
