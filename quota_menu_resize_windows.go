package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type quotaMenuHeightDrag struct {
	Rect                         windows.Rect
	StartY, Height, Scroll, Edge int
}

func (w *quotaMenuWindow) heightEdge(x, y int) int {
	if w.overlay != nil || x < 0 || x >= w.width || y < 0 || y >= w.height {
		return 0
	}
	if y < 7 {
		return -1
	}
	if y >= w.height-6 {
		return 1
	}
	return 0
}

func (w *quotaMenuWindow) resizeCursor() {
	cursor, _, _ := qmLoadCursor.Call(0, 32645) // IDC_SIZENS
	qmSetCursor.Call(cursor)
}

func (w *quotaMenuWindow) beginHeightDrag(edge int, p qmPoint) {
	var r windows.Rect
	procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	qmClientScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	w.heightDrag = &quotaMenuHeightDrag{Rect: r, StartY: int(p.Y), Height: w.height, Scroll: w.scroll, Edge: edge}
	w.focus, w.hover, w.pressed = -1, -1, -1
	qmSetCapture.Call(w.hwnd)
	w.resizeCursor()
}

func (w *quotaMenuWindow) dragHeight(p qmPoint) {
	d := w.heightDrag
	if d == nil {
		return
	}
	qmClientScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	height := int(d.Rect.Bottom-d.Rect.Top) + d.Edge*(int(p.Y)-d.StartY)
	available := w.work.Y + w.work.H - 8 - int(d.Rect.Top)
	if d.Edge < 0 {
		available = int(d.Rect.Bottom) - w.work.Y - 8
	}
	available = min(available, w.scale(8192))
	height = max(min(w.scale(quotaMenuMinHeight), available), min(height, available))
	r := d.Rect
	if d.Edge < 0 {
		r.Top = r.Bottom - int32(height)
	} else {
		r.Bottom = r.Top + int32(height)
	}
	w.placeResized(r)
}

func (w *quotaMenuWindow) placeResized(r windows.Rect) {
	w.height = int(r.Bottom-r.Top) * 96 / w.dpi
	w.bodyBottom = w.height - quotaMenuFooterHeight
	w.scroll = min(w.scroll, w.maxScroll())
	w.focus, w.hover, w.pressed = -1, -1, -1
	// Keep the new bottom as the anchor on later cached-data refreshes.
	w.anchor = qmPoint{X: r.Right - 20, Y: min(r.Bottom+8, int32(w.work.Y+w.work.H-1))}
	procSetWindowPos.Call(w.hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x14) // no activation or z-order change
	w.invalidate()
	qmUpdateWindow.Call(w.hwnd)
}

func (w *quotaMenuWindow) finishHeightDrag(cancel bool) {
	d := w.heightDrag
	if d == nil {
		return
	}
	w.heightDrag = nil // ReleaseCapture can synchronously send WM_CAPTURECHANGED.
	if capture, _, _ := qmGetCapture.Call(); capture == w.hwnd {
		qmReleaseCapture.Call()
	}
	if cancel {
		w.scroll = d.Scroll
		w.placeResized(d.Rect)
		return
	}
	if w.height != d.Height {
		w.preferredHeight = max(quotaMenuMinHeight, w.height)
		w.persistHeight()
	}
	// A usage refresh deferred during dragging may now safely update the view.
	w.reload(false)
}

func (w *quotaMenuWindow) resetHeight() {
	w.finishHeightDrag(true)
	w.preferredHeight = 0
	w.persistHeight()
	w.reload(false)
}

func (w *quotaMenuWindow) persistHeight() {
	if err := saveQuotaMenuHeight(w.menu.preferencesPath, w.preferredHeight); err != nil {
		w.preferenceError(err)
	}
}

func (w *quotaMenuWindow) preferenceError(err error) {
	if w.menu.onError != nil {
		w.menu.onError(tr("Menu height preference: ", "菜单高度设置：") + err.Error())
	}
}
