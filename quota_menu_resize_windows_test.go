package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func nativeMenuDragHeight(t *testing.T, m *nativeQuotaMenu, edge, delta int, cancel bool) {
	t.Helper()
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		x, y := w.width/2, 3
		if edge > 0 {
			y = w.height - 3
		}
		start := uintptr(uint16(w.scale(x))) | uintptr(uint16(w.scale(y)))<<16
		move := uintptr(uint16(w.scale(x))) | uintptr(uint16(w.scale(y+delta)))<<16
		procSendMessage.Call(w.hwnd, 0x201, 1, start)
		if w.heightDrag == nil {
			t.Error("resize edge did not begin dragging")
		}
		procSendMessage.Call(w.hwnd, 0x200, 1, move)
		if cancel {
			procSendMessage.Call(w.hwnd, 0x100, 27, 0)
		} else {
			procSendMessage.Call(w.hwnd, 0x202, 0, move)
		}
		if w.heightDrag != nil {
			t.Error("resize did not release capture")
		}
	})
}

func verifyNativeMenuResizing(t *testing.T, f *quotaMenuFixture, dir string) {
	t.Helper()
	m := f.menu
	var original, small int
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		w.anchor = qmPoint{X: int32(w.work.X + w.work.W - 1), Y: int32(w.work.Y + w.work.H - 1)}
		w.reload(false)
		original = w.height
	})
	nativeMenuDragHeight(t, m, -1, 160, false)
	nativeMenuDo(t, m, func(w *quotaMenuWindow) { small = w.height })
	if small >= original || small < quotaMenuMinHeight {
		t.Fatalf("height was not reduced safely: %d -> %d", original, small)
	}
	if saved, err := loadQuotaMenuHeight(m.preferencesPath); err != nil || saved != small {
		t.Fatalf("resize did not persist height: %d, %v", saved, err)
	}
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		w.reload(false)
		if w.height != small {
			t.Error("background update overwrote resized height")
		}
		if err := w.capture(filepath.Join(dir, "native-height-small.png")); err != nil {
			t.Error(err)
		}
		w.hide()
	})
	m.show()
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && w.height == small })
	// Escape cancels both the visual change and its persistence.
	nativeMenuDragHeight(t, m, -1, 45, true)
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.height != small || !m.shown.Load() {
			t.Error("Escape did not cancel resizing in place")
		}
	})
	if saved, _ := loadQuotaMenuHeight(m.preferencesPath); saved != small {
		t.Fatal("cancelled resize was persisted")
	}
	// Another native instance loads the same UI file, using only fake accounts.
	f2 := newQuotaMenuFixture()
	f2.menu.preferencesPath = m.preferencesPath
	f2.menu.show()
	nativeMenuWait(t, f2.menu, func(w *quotaMenuWindow) bool { return w.preferredHeight == small && w.height == small })
	nativeMenuDo(t, f2.menu, func(w *quotaMenuWindow) {
		start := uintptr(uint16(w.scale(w.width/2))) | uintptr(uint16(w.scale(3)))<<16
		procSendMessage.Call(w.hwnd, 0x201, 1, start)
	})
	f2.menu.close()
	select {
	case <-f2.menu.done:
	case <-time.After(5 * time.Second):
		t.Fatal("second native instance did not close")
	}
	// Top-edge expansion is clamped to the display's work area.
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		// Reopening uses the real pointer's position. Anchor this isolated test
		// near the work-area bottom so the top edge has room to expand.
		w.anchor = qmPoint{X: int32(w.work.X + w.work.W - 1), Y: int32(w.work.Y + w.work.H - 1)}
		w.reload(false)
	})
	nativeMenuDragHeight(t, m, -1, -8000, false)
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		var r windows.Rect
		procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
		if int(r.Top) < w.work.Y+8 || int(r.Bottom) > w.work.Y+w.work.H-8 || w.height <= small {
			t.Errorf("expanded height did not respect work-area limits: rect=%+v work=%+v height=%d previous=%d", r, w.work, w.height, small)
		}
		if err := w.capture(filepath.Join(dir, "native-height-expanded.png")); err != nil {
			t.Error(err)
		}
	})
	// The bottom edge works too and shrinking keeps footer actions accessible.
	nativeMenuDragHeight(t, m, 1, -120, false)
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.bodyBottom != w.height-quotaMenuFooterHeight {
			t.Error("resizing detached the fixed footer")
		}
	})
	savedHeight, err := loadQuotaMenuHeight(m.preferencesPath)
	if err != nil {
		t.Fatal(err)
	}
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		w.menu.options.dpiOverride = 144
		w.reload(false)
		var r windows.Rect
		procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
		if int(r.Top) < w.work.Y || int(r.Bottom) > w.work.Y+w.work.H || w.preferredHeight != savedHeight {
			t.Error("high-DPI clamping lost the preference or escaped the work area")
		}
		if err := w.capture(filepath.Join(dir, "native-height-150.png")); err != nil {
			t.Error(err)
		}
		w.menu.options.dpiOverride = 96
		w.reload(false)
	})
	if saved, _ := loadQuotaMenuHeight(m.preferencesPath); saved != savedHeight {
		t.Fatal("display clamping overwrote the stored height")
	}
	// Losing capture cancels an unfinished resize without saving it.
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		start := uintptr(uint16(w.scale(w.width/2))) | uintptr(uint16(w.scale(3)))<<16
		move := uintptr(uint16(w.scale(w.width/2))) | uintptr(uint16(w.scale(23)))<<16
		procSendMessage.Call(w.hwnd, 0x201, 1, start)
		procSendMessage.Call(w.hwnd, 0x200, 1, move)
		procSendMessage.Call(w.hwnd, 0x215, 0, 0)
		if w.heightDrag != nil || w.height != savedHeight {
			t.Error("capture loss did not cancel resize")
		}
	})
	// A failed preference save reports an error without closing the menu.
	goodPath := m.preferencesPath
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.preferencesPath = filepath.Join(blocked, "ui.json")
	nativeMenuDragHeight(t, m, -1, 30, false)
	select {
	case <-f.errors:
	default:
		t.Fatal("preference write failure was not reported")
	}
	if !m.shown.Load() {
		t.Fatal("preference write failure closed the menu")
	}
	m.preferencesPath = goodPath
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		procSendMessage.Call(w.hwnd, 0x203, 0, uintptr(uint16(w.scale(w.width/2)))|uintptr(uint16(w.scale(3)))<<16)
		if w.preferredHeight != 0 {
			t.Error("double-click did not reset height")
		}
	})
	if saved, err := loadQuotaMenuHeight(goodPath); err != nil || saved != 0 {
		t.Fatalf("default height reset was not persisted: %d, %v", saved, err)
	}
	if len(f.actions) != 0 {
		t.Fatal("resizing triggered an account action")
	}
}
