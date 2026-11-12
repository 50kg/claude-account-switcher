package main

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	qmRegisterClass  = user32.NewProc("RegisterClassExW")
	qmCreateWindow   = user32.NewProc("CreateWindowExW")
	qmDestroyWindow  = user32.NewProc("DestroyWindow")
	qmDefWindowProc  = user32.NewProc("DefWindowProcW")
	qmGetMessage     = user32.NewProc("GetMessageW")
	qmTranslate      = user32.NewProc("TranslateMessage")
	qmDispatch       = user32.NewProc("DispatchMessageW")
	qmPostMessage    = user32.NewProc("PostMessageW")
	qmPostQuit       = user32.NewProc("PostQuitMessage")
	qmShowWindow     = user32.NewProc("ShowWindow")
	qmInvalidate     = user32.NewProc("InvalidateRect")
	qmUpdateWindow   = user32.NewProc("UpdateWindow")
	qmGetClientRect  = user32.NewProc("GetClientRect")
	qmGetCursorPos   = user32.NewProc("GetCursorPos")
	qmClientScreen   = user32.NewProc("ClientToScreen")
	qmScreenClient   = user32.NewProc("ScreenToClient")
	qmSetCursor      = user32.NewProc("SetCursor")
	qmForeground     = user32.NewProc("GetForegroundWindow")
	qmGetAncestor    = user32.NewProc("GetAncestor")
	qmKeyState       = user32.NewProc("GetAsyncKeyState")
	qmMonitorPoint   = user32.NewProc("MonitorFromPoint")
	qmGetMonitorDPI  = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
	qmLoadCursor     = user32.NewProc("LoadCursorW")
	qmSetCapture     = user32.NewProc("SetCapture")
	qmGetCapture     = user32.NewProc("GetCapture")
	qmReleaseCapture = user32.NewProc("ReleaseCapture")
	qmWindows        sync.Map
	qmClassOnce      sync.Once
	qmClassErr       error
	qmCallback       = windows.NewCallback(quotaMenuWindowProc)
)

const (
	qmShow = 0x8001 + iota
	qmRefresh
	qmInvoke
)

type qmPoint struct{ X, Y int32 }
type qmMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          qmPoint
	Private        uint32
}

type qmWindowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindExtra              int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}

type nativeQuotaMenu struct {
	view            func() quotaMenuView
	onAction        func(quotaMenuAction) error
	onError         func(string)
	mu              sync.Mutex
	hwnd            uintptr
	started         bool
	closing         bool
	ready           chan struct{}
	done            chan struct{}
	shown           atomic.Bool
	jobs            chan func(*quotaMenuWindow)
	options         nativeQuotaMenuOptions
	preferencesPath string
}

// Options are used by isolated tests; normal startup uses the zero value.
type nativeQuotaMenuOptions struct {
	noActivate  bool
	dpiOverride int
	input       func(uintptr) quotaMenuInput
}

type quotaMenuInput struct {
	buttons                                uint8
	inside, ownForeground, knownForeground bool
}

type quotaMenuDismissal struct {
	opened  time.Time
	buttons uint8
	owned   bool
}

func (d *quotaMenuDismissal) open(now time.Time, in quotaMenuInput) {
	d.opened, d.buttons, d.owned = now, in.buttons, false
}

func (d *quotaMenuDismissal) hide(now time.Time, in quotaMenuInput) bool {
	pressed := in.buttons &^ d.buttons
	d.buttons = in.buttons
	if in.ownForeground {
		d.owned = true
	}
	if now.Sub(d.opened) < 250*time.Millisecond {
		return false
	}
	return pressed != 0 && !in.inside || d.owned && in.knownForeground && !in.ownForeground
}

func newQuotaMenu(view func() quotaMenuView, action func(quotaMenuAction) error, onError func(string), preferencesPath string) quotaMenu {
	return &nativeQuotaMenu{view: view, onAction: action, onError: onError, jobs: make(chan func(*quotaMenuWindow), 16), preferencesPath: preferencesPath}
}

func (m *nativeQuotaMenu) start() bool {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return false
	}
	if !m.started {
		m.started = true
		m.ready, m.done = make(chan struct{}), make(chan struct{})
		go m.run()
	}
	ready := m.ready
	m.mu.Unlock()
	<-ready
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hwnd != 0 && !m.closing
}

func (m *nativeQuotaMenu) post(msg uint32) {
	m.mu.Lock()
	hwnd := m.hwnd
	m.mu.Unlock()
	if hwnd != 0 {
		qmPostMessage.Call(hwnd, uintptr(msg), 0, 0)
	}
}

func (m *nativeQuotaMenu) show() {
	if m.start() {
		m.post(qmShow)
	}
}

func (m *nativeQuotaMenu) update() {
	if m.shown.Load() {
		m.post(qmRefresh)
	}
}

func (m *nativeQuotaMenu) close() {
	m.mu.Lock()
	m.closing = true
	hwnd := m.hwnd
	m.mu.Unlock()
	if hwnd != 0 {
		qmPostMessage.Call(hwnd, 0x10, 0, 0) // WM_CLOSE, on the owning UI thread
	}
}

func (m *nativeQuotaMenu) run() {
	runtime.LockOSThread()
	// A locked goroutine's OS thread ends with it, including its message queue.
	defer close(m.done)
	qmClassOnce.Do(func() {
		var instance windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &instance)
		cursor, _, _ := qmLoadCursor.Call(0, 32512)
		wc := qmWindowClass{Proc: qmCallback, Instance: uintptr(instance), Cursor: cursor,
			ClassName: windows.StringToUTF16Ptr("ClaudeQuotaContextMenu"), Style: 0x20008} // CS_DROPSHADOW | CS_DBLCLKS
		wc.Size = uint32(unsafe.Sizeof(wc))
		if result, _, err := qmRegisterClass.Call(uintptr(unsafe.Pointer(&wc))); result == 0 {
			qmClassErr = fmt.Errorf("register quota menu: %w", err)
		}
	})
	var err error
	w := &quotaMenuWindow{menu: m, focus: -1, hover: -1, pressed: -1, dpi: 96}
	if height, loadErr := loadQuotaMenuHeight(m.preferencesPath); loadErr == nil {
		w.preferredHeight = height
	} else {
		w.preferenceError(loadErr)
	}
	if qmClassErr != nil {
		err = qmClassErr
	} else {
		var instance windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &instance)
		exStyle := uintptr(0x80) // WS_EX_TOOLWINDOW, never a taskbar application
		if m.options.noActivate {
			exStyle |= 0x08000000 // WS_EX_NOACTIVATE, isolated native tests only
		}
		w.hwnd, _, err = qmCreateWindow.Call(exStyle, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("ClaudeQuotaContextMenu"))),
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(tr("Account quotas", "账号与额度")))), 0x82000000, 0, 0, 520, 1, 0, 0, uintptr(instance), 0)
		if w.hwnd != 0 {
			err = nil
			qmWindows.Store(w.hwnd, w)
			corner := uint32(2)
			_ = windows.DwmSetWindowAttribute(windows.HWND(w.hwnd), 33, unsafe.Pointer(&corner), 4)
			w.makeFonts()
			w.createTooltip()
		}
	}
	m.mu.Lock()
	m.hwnd = w.hwnd
	closing := m.closing
	m.mu.Unlock()
	close(m.ready)
	if err != nil || w.hwnd == 0 {
		if m.onError != nil {
			m.onError(tr("Could not open quota menu: ", "额度菜单打开失败：") + fmt.Sprint(err))
		}
		return
	}
	defer func() {
		w.freeFonts()
		qmWindows.Delete(w.hwnd)
		m.mu.Lock()
		m.hwnd = 0
		m.mu.Unlock()
		m.shown.Store(false)
	}()
	if closing {
		qmDestroyWindow.Call(w.hwnd)
		return
	}
	var msg qmMessage
	for {
		r, _, _ := qmGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		qmTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		qmDispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

type quotaMenuHit struct {
	Rect    quotaRect
	Action  quotaMenuAction
	Popup   string
	Dismiss bool
}

type quotaMenuOverlay struct {
	Kind              string
	Account           string
	Anchor            quotaRect
	Rect              quotaRect
	Confirm           quotaMenuAction
	Input, Error      string
	SelectAll, Saving bool
}

type quotaMenuOrderDrag struct {
	Source, Slot int
	Start        qmPoint
	Active       bool
}

type quotaMenuChoice struct {
	Label             string
	Checked, Disabled bool
	Separator         bool
	Action            quotaMenuAction
	Popup             string
}

type quotaMenuWindow struct {
	menu                                                           *nativeQuotaMenu
	hwnd                                                           uintptr
	view                                                           quotaMenuView
	archivedView                                                   bool
	dpi, width, height, bodyTop, bodyBottom, contentHeight, scroll int
	anchor                                                         qmPoint
	work                                                           quotaRect
	fonts                                                          [4]uintptr
	hits                                                           []quotaMenuHit
	focus, hover, pressed                                          int
	scrollDragging                                                 bool
	scrollDragOffset, wheelRemainder                               int
	preferredHeight                                                int
	heightDrag                                                     *quotaMenuHeightDrag
	orderDrag                                                      *quotaMenuOrderDrag
	orderSaving                                                    bool
	overlay                                                        *quotaMenuOverlay
	dismiss                                                        quotaMenuDismissal
	tooltip                                                        uintptr
	tooltipText                                                    [][]uint16
}

func (w *quotaMenuWindow) scale(n int) int { return int(math.Round(float64(n*w.dpi) / 96)) }

func quotaMenuWindowProc(hwnd, msg, wp, lp uintptr) uintptr {
	value, exists := qmWindows.Load(hwnd)
	if !exists {
		r, _, _ := qmDefWindowProc.Call(hwnd, msg, wp, lp)
		return r
	}
	w := value.(*quotaMenuWindow)
	switch msg {
	case 0x06: // WM_ACTIVATE: remember genuine activation before the first timer tick
		if wp&0xffff != 0 {
			w.dismiss.owned = true
		}
	case qmShow:
		if w.menu.shown.Load() {
			w.hide()
		} else {
			w.reload(true)
		}
		return 0
	case qmRefresh:
		if w.menu.shown.Load() && w.heightDrag == nil {
			w.reload(false)
		}
		return 0
	case qmInvoke:
		select {
		case job := <-w.menu.jobs:
			job(w)
		default:
		}
		return 0
	case 0x0F: // WM_PAINT
		w.paintWindow()
		return 0
	case 0x14: // WM_ERASEBKGND: the buffered painter fills everything
		return 1
	case 0x113: // WM_TIMER
		if w.menu.shown.Load() && w.heightDrag == nil && w.dismiss.hide(time.Now(), w.input()) {
			w.hide()
		}
		return 0
	case 0x200: // WM_MOUSEMOVE
		if w.heightDrag != nil {
			w.dragHeight(qmPoint{X: int32(int16(lp & 0xffff)), Y: int32(int16(lp >> 16))})
			return 0
		}
		if w.scrollDragging {
			track, thumb := w.scrollbar()
			travel := track.H - thumb.H
			if travel > 0 {
				y := int(int16(lp>>16))*96/w.dpi - track.Y - w.scrollDragOffset
				w.setScroll(int(math.Round(float64(y*w.maxScroll()) / float64(travel))))
			}
			return 0
		}
		if w.orderDrag != nil {
			w.updateAccountOrderDrag(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi)
			return 0
		}
		w.hover = w.hit(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi)
		w.invalidate()
		return 0
	case 0x201: // WM_LBUTTONDOWN
		x, y := int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi
		if edge := w.heightEdge(x, y); edge != 0 {
			w.beginHeightDrag(edge, qmPoint{X: int32(int16(lp & 0xffff)), Y: int32(int16(lp >> 16))})
			return 0
		}
		if index := w.accountDragHandleAt(x, y); index >= 0 {
			w.beginAccountOrderDrag(index, qmPoint{X: int32(x), Y: int32(y)})
			return 0
		}
		if track, thumb := w.scrollbar(); w.overlay == nil && track.contains(x, y) {
			w.pressed = -1
			if thumb.contains(x, y) {
				w.scrollDragging, w.scrollDragOffset = true, y-thumb.Y
				qmSetCapture.Call(w.hwnd)
			} else {
				delta := max(40, w.bodyBottom-w.bodyTop-40)
				if y < thumb.Y {
					delta = -delta
				}
				w.setScroll(w.scroll + delta)
			}
			return 0
		}
		w.pressed = w.hit(x, y)
		return 0
	case 0x202: // WM_LBUTTONUP
		if w.heightDrag != nil {
			w.finishHeightDrag(false)
			return 0
		}
		if w.scrollDragging {
			w.stopScrollDrag()
			return 0
		}
		if w.orderDrag != nil {
			w.finishAccountOrderDrag(true)
			return 0
		}
		i := w.hit(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi)
		if i >= 0 && i == w.pressed {
			w.activate(w.hits[i])
		} else if w.overlay != nil && !w.overlay.Rect.contains(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi) {
			w.closeOverlay()
		}
		w.pressed = -1
		return 0
	case 0x20A: // WM_MOUSEWHEEL
		if w.overlay != nil || w.heightDrag != nil {
			return 0
		}
		w.wheelRemainder += int(int16(wp >> 16))
		steps := w.wheelRemainder / 120
		w.wheelRemainder %= 120
		w.setScroll(w.scroll - steps*60)
		return 0
	case 0x215: // WM_CAPTURECHANGED
		w.scrollDragging = false
		w.finishAccountOrderDrag(false)
		w.finishHeightDrag(true)
		return 0
	case 0x1F: // WM_CANCELMODE
		w.finishHeightDrag(true)
		w.stopScrollDrag()
		w.finishAccountOrderDrag(false)
		return 0
	case 0x203: // WM_LBUTTONDBLCLK
		if w.heightEdge(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi) != 0 {
			w.resetHeight()
		}
		return 0
	case 0x20: // WM_SETCURSOR
		var p qmPoint
		qmGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
		qmScreenClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
		cx, cy := int(p.X)*96/w.dpi, int(p.Y)*96/w.dpi
		if w.heightDrag != nil || w.heightEdge(cx, cy) != 0 {
			w.resizeCursor()
			return 1
		}
		if w.orderDrag != nil || w.accountDragHandleAt(cx, cy) >= 0 {
			cursor, _, _ := qmLoadCursor.Call(0, 32646) // IDC_SIZEALL
			qmSetCursor.Call(cursor)
			return 1
		}
	case 0x100: // WM_KEYDOWN
		if w.heightDrag != nil {
			if wp == 27 {
				w.finishHeightDrag(true)
			}
			return 0
		}
		if w.intervalKey(wp) {
			return 0
		}
		switch wp {
		case 27:
			if w.overlay != nil {
				w.closeOverlay()
			} else {
				w.hide()
			}
		case 9, 38, 40:
			if len(w.hits) > 0 {
				direction := 1
				shift, _, _ := qmKeyState.Call(16)
				if wp == 38 || wp == 9 && shift&0x8000 != 0 {
					direction = -1
				}
				w.focus = (w.focus + direction + len(w.hits)) % len(w.hits)
				w.invalidate()
			}
		case 13, 32:
			if w.focus >= 0 && w.focus < len(w.hits) {
				w.activate(w.hits[w.focus])
			}
		case 36, 35:
			if w.overlay == nil {
				target := 0
				if wp == 35 {
					target = w.maxScroll()
				}
				w.setScroll(target)
				return 0
			}
			w.focus = 0
			if wp == 35 {
				w.focus = len(w.hits) - 1
			}
			w.invalidate()
		case 33, 34:
			if w.overlay != nil {
				return 0
			}
			delta := w.bodyBottom - w.bodyTop - 40
			if wp == 33 {
				delta = -delta
			}
			w.setScroll(w.scroll + delta)
		}
		return 0
	case 0x102: // WM_CHAR
		if w.intervalChar(rune(wp)) {
			return 0
		}
	case 0x02E0: // WM_DPICHANGED
		w.finishHeightDrag(true)
		w.dpi = int(wp & 0xffff)
		w.makeFonts()
		w.reload(false)
		return 0
	case 0x10: // WM_CLOSE
		w.menu.mu.Lock()
		closing := w.menu.closing
		w.menu.mu.Unlock()
		if closing {
			w.finishHeightDrag(true)
			w.stopScrollDrag()
			w.finishAccountOrderDrag(false)
			qmDestroyWindow.Call(hwnd)
		} else {
			w.hide()
		}
		return 0
	case 0x02: // WM_DESTROY
		w.heightDrag = nil
		w.scrollDragging = false
		procKillTimer.Call(hwnd, 2)
		qmPostQuit.Call(0)
		return 0
	}
	r, _, _ := qmDefWindowProc.Call(hwnd, msg, wp, lp)
	return r
}

func (w *quotaMenuWindow) invalidate() { qmInvalidate.Call(w.hwnd, 0, 0) }

func (w *quotaMenuWindow) input() quotaMenuInput {
	if w.menu.options.input != nil {
		return w.menu.options.input(w.hwnd)
	}
	var p qmPoint
	var r windows.Rect
	qmGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	foreground, _, _ := qmForeground.Call()
	root, _, _ := qmGetAncestor.Call(foreground, 3)
	left, _, _ := qmKeyState.Call(1)
	right, _, _ := qmKeyState.Call(2)
	var buttons uint8
	if left&0x8000 != 0 {
		buttons |= 1
	}
	if right&0x8000 != 0 {
		buttons |= 2
	}
	return quotaMenuInput{buttons: buttons, inside: p.X >= r.Left && p.X < r.Right && p.Y >= r.Top && p.Y < r.Bottom,
		ownForeground: foreground == w.hwnd || root == w.hwnd, knownForeground: foreground != 0}
}

func (w *quotaMenuWindow) reload(show bool) {
	w.view = w.menu.view()
	if show {
		w.archivedView = false
	}
	if preferences, err := loadQuotaMenuPreferences(w.menu.preferencesPath); err == nil {
		w.view.SortMode = preferences.SortMode
		if w.view.SortMode == "" {
			w.view.SortMode = "manual"
		}
		w.view.Accounts = applyQuotaMenuPreferences(w.view.Accounts, preferences, time.Now())
	}
	if show {
		qmGetCursorPos.Call(uintptr(unsafe.Pointer(&w.anchor)))
		w.scroll, w.focus, w.hover, w.pressed = 0, -1, -1, -1
		w.wheelRemainder = 0
		w.overlay = nil
	}
	packed := uintptr(uint32(w.anchor.X)) | uintptr(uint32(w.anchor.Y))<<32
	monitor, _, _ := qmMonitorPoint.Call(packed, 2)
	info := struct {
		Size          uint32
		Monitor, Work windows.Rect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); r == 0 {
		info.Work = windows.Rect{Right: 1024, Bottom: 768}
	}
	var dx, dy uint32
	if qmGetMonitorDPI.Find() == nil {
		qmGetMonitorDPI.Call(monitor, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy)))
	}
	if dx == 0 {
		dx = 96
	}
	if w.menu.options.dpiOverride >= 96 && w.menu.options.dpiOverride <= 384 {
		dx = uint32(w.menu.options.dpiOverride)
	}
	if w.dpi != int(dx) {
		w.dpi = int(dx)
		w.makeFonts()
	}
	w.work = quotaRect{int(info.Work.Left), int(info.Work.Top), int(info.Work.Right - info.Work.Left), int(info.Work.Bottom - info.Work.Top)}
	w.contentHeight = 0
	for _, a := range w.visibleAccounts() {
		w.contentHeight += quotaAccountHeight(a)
	}
	if len(w.visibleAccounts()) == 0 {
		w.contentHeight = 92
	}
	w.bodyTop = quotaMenuHeaderHeight
	if w.view.Status != "" {
		w.bodyTop += 30
	}
	// The flat settings menu must also fit when no accounts have been added.
	height := max(w.bodyTop+min(w.contentHeight, quotaMenuMaxBodyHeight)+quotaMenuFooterHeight, quotaChoicesHeight(w.choices("settings"))+16)
	if w.preferredHeight > 0 {
		height = max(w.minimumHeight(), w.preferredHeight)
	}
	position := placeQuotaMenu(w.work, int(w.anchor.X), int(w.anchor.Y), w.scale(520), w.scale(height))
	w.width = position.W * 96 / w.dpi
	w.height = position.H * 96 / w.dpi
	w.bodyBottom = w.height - quotaMenuFooterHeight
	w.scroll = max(0, min(w.scroll, max(0, w.contentHeight-(w.bodyBottom-w.bodyTop))))
	order := ^uintptr(0) // HWND_TOPMOST
	if w.menu.options.noActivate {
		order = 1
	} // test window stays behind current applications
	procSetWindowPos.Call(w.hwnd, order, uintptr(position.X), uintptr(position.Y), uintptr(position.W), uintptr(position.H), 0x10)
	if show {
		w.dismiss.open(time.Now(), w.input())
		w.menu.shown.Store(true)
		procSetTimer.Call(w.hwnd, 2, 60, 0)
		if w.menu.options.noActivate {
			qmShowWindow.Call(w.hwnd, 4)
		} else {
			qmShowWindow.Call(w.hwnd, 5)
			procSetForegroundWindow.Call(w.hwnd)
		}
	}
	w.invalidate()
	// Synchronous first paint: no HTML, animation frame or child process can
	// keep a successfully created menu hidden.
	qmUpdateWindow.Call(w.hwnd)
}

func (w *quotaMenuWindow) visibleAccounts() []quotaMenuAccount {
	if w.archivedView {
		return w.view.ArchivedAccounts
	}
	return w.view.Accounts
}

func (w *quotaMenuWindow) hide() {
	w.finishHeightDrag(true)
	w.stopScrollDrag()
	w.finishAccountOrderDrag(false)
	procKillTimer.Call(w.hwnd, 2)
	qmShowWindow.Call(w.hwnd, 0)
	w.menu.shown.Store(false)
	w.focus, w.hover, w.pressed = -1, -1, -1
	w.overlay = nil
}

func (w *quotaMenuWindow) maxScroll() int {
	return max(0, w.contentHeight-(w.bodyBottom-w.bodyTop))
}

func (w *quotaMenuWindow) setScroll(target int) {
	w.scroll = max(0, min(target, w.maxScroll()))
	w.focus, w.hover, w.pressed = -1, -1, -1
	w.invalidate()
}

func (w *quotaMenuWindow) scrollbar() (track, thumb quotaRect) {
	if w.maxScroll() == 0 || w.bodyBottom-w.bodyTop < 16 {
		return
	}
	track = quotaRect{w.width - 12, w.bodyTop + 4, 9, w.bodyBottom - w.bodyTop - 8}
	height := min(track.H, max(28, track.H*(w.bodyBottom-w.bodyTop)/w.contentHeight))
	top := track.Y + w.scroll*(track.H-height)/w.maxScroll()
	thumb = quotaRect{track.X + 1, top, 7, height}
	return
}

func (w *quotaMenuWindow) stopScrollDrag() {
	if w.scrollDragging {
		w.scrollDragging = false
		if capture, _, _ := qmGetCapture.Call(); capture == w.hwnd {
			qmReleaseCapture.Call()
		}
	}
	w.pressed = -1
}

func (w *quotaMenuWindow) hit(x, y int) int {
	for i := len(w.hits) - 1; i >= 0; i-- {
		if w.hits[i].Rect.contains(x, y) {
			return i
		}
	}
	return -1
}

func (w *quotaMenuWindow) closeOverlay() {
	w.overlay = nil
	w.focus, w.hover, w.pressed = -1, -1, -1
	w.invalidate()
}

func (w *quotaMenuWindow) activate(hit quotaMenuHit) {
	if hit.Dismiss {
		w.closeOverlay()
		return
	}
	if hit.Popup != "" {
		w.overlay = &quotaMenuOverlay{Kind: hit.Popup, Account: hit.Action.Account, Anchor: hit.Rect}
		w.focus, w.hover, w.pressed = -1, -1, -1
		if hit.Popup == "interval" {
			w.openInterval()
		}
		w.invalidate()
		return
	}
	a := hit.Action
	if a.Kind == "archived-view" {
		w.archivedView = true
		w.closeOverlay()
		w.reload(false)
		return
	}
	if a.Kind == "accounts-view" {
		w.archivedView = false
		w.closeOverlay()
		w.reload(false)
		return
	}
	if w.activateInterval(a) {
		return
	}
	if a.Kind == "" {
		return
	}
	if (a.Kind == "switch" || a.Kind == "add" || a.Kind == "import" || a.Kind == "session-start") && !a.Confirmed {
		// Preserve the selected action until the user confirms. The
		// dispatcher revalidates it against the latest snapshot before acting.
		w.overlay = &quotaMenuOverlay{Kind: "confirm", Confirm: a}
		w.focus, w.hover, w.pressed = 0, -1, -1 // Cancel is the default.
		w.invalidate()
		return
	}
	keepOpen := a.Kind == "auto" || a.Kind == "spend" || a.Kind == "threshold" || a.Kind == "autostart" || a.Kind == "sync" || a.Kind == "sort" || a.Kind == "session-start"
	w.closeOverlay()
	if !keepOpen {
		w.hide()
	}
	go func() {
		if err := w.menu.onAction(a); err != nil && w.menu.onError != nil {
			w.menu.onError(err.Error())
		}
		if keepOpen {
			w.menu.update()
		}
	}()
}

func (w *quotaMenuWindow) choices(kind string) []quotaMenuChoice {
	switch kind {
	case "settings":
		syncLabel := tr("Sync usage now", "立即同步用量")
		if w.view.Syncing {
			syncLabel = tr("Syncing usage…", "正在同步用量…")
		}
		title, _ := updateMenuText(w.view.Update)
		if title == "" {
			title = tr("Check for updates", "检查更新")
		}
		choices := []quotaMenuChoice{
			{Label: tr("Use expiring weekly allowance", "优先使用即将重置的周额度"), Checked: w.view.Spend, Action: quotaMenuAction{Kind: "spend", Enabled: !w.view.Spend}},
			{Label: tr("Start at login", "开机启动"), Checked: w.view.Startup, Action: quotaMenuAction{Kind: "autostart", Enabled: !w.view.Startup}},
			{Label: refreshIntervalText(w.view.RefreshMinutes), Popup: "interval"},
		}
		choices = append(choices,
			quotaMenuChoice{Separator: true},
			quotaMenuChoice{Label: fmt.Sprintf(tr("Archived accounts (%d)", "已归档账号（%d）"), len(w.view.ArchivedAccounts)), Disabled: len(w.view.ArchivedAccounts) == 0, Action: quotaMenuAction{Kind: "archived-view"}},
			quotaMenuChoice{Label: tr("Import one account…", "导入单个账号…"), Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "account-import"}},
			quotaMenuChoice{Label: syncLabel, Disabled: w.view.Busy || len(w.view.Accounts)+len(w.view.ArchivedAccounts) == 0, Action: quotaMenuAction{Kind: "sync"}},
			quotaMenuChoice{Label: tr("Import chats from all accounts…", "导入所有账号的聊天…"), Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "import", Active: w.view.Active}},
			quotaMenuChoice{Label: tr("Open data folder", "打开数据文件夹"), Action: quotaMenuAction{Kind: "folder"}},
			quotaMenuChoice{Label: title, Action: quotaMenuAction{Kind: "update"}},
		)
		return choices
	case "sort":
		return []quotaMenuChoice{
			{Label: tr("Manual · drag to reorder", "手动排序（拖动调整）"), Checked: w.view.SortMode != "reset", Disabled: w.orderSaving, Action: quotaMenuAction{Kind: "sort", SortMode: "manual"}},
			{Label: tr("Soonest reset · current account first", "按最近重置时间（当前账号置顶）"), Checked: w.view.SortMode == "reset", Disabled: w.orderSaving, Action: quotaMenuAction{Kind: "sort", SortMode: "reset"}},
		}
	case "account-actions":
		name := tr("this account", "此账号")
		billingAction := tr("Verify subscription date on Claude…", "通过网页核验会员日期…")
		archived := false
		for _, accounts := range [][]quotaMenuAccount{w.view.Accounts, w.view.ArchivedAccounts} {
			for _, account := range accounts {
				if account.ID == w.overlay.Account {
					name, archived = account.Name, account.Archived
					if account.AppleSubscription {
						billingAction = tr("View or record Apple subscription date…", "查看或记录 Apple 订阅日期…")
					}
					break
				}
			}
		}
		choices := []quotaMenuChoice{
			{Label: tr("Start 5-hour window…", "启动 5 小时窗口…"), Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "session-start", Account: w.overlay.Account}},
			{Label: tr("Sign in to Claude Code for this account…", "为此账号登录 Claude Code…"), Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "session-login", Account: w.overlay.Account}},
			{Separator: true},
			{Label: billingAction, Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "billing-web", Account: w.overlay.Account}},
			{Label: fmt.Sprintf(tr("Export %s (unencrypted)…", "导出 %s（明文）…"), name), Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "account-export", Account: w.overlay.Account}},
		}
		if archived {
			choices = append(choices, quotaMenuChoice{Label: tr("Restore to account list", "恢复到账号列表"), Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "unarchive", Account: w.overlay.Account}})
		} else {
			choices = append(choices, quotaMenuChoice{Label: tr("Archive account", "归档账号"), Disabled: w.view.Busy, Action: quotaMenuAction{Kind: "archive", Account: w.overlay.Account}})
		}
		return choices
	case "threshold":
		var choices []quotaMenuChoice
		for _, value := range thresholds {
			choices = append(choices, quotaMenuChoice{Label: fmt.Sprintf(tr("Switch at %.0f%% used", "已用 %.0f%% 时切换"), value), Checked: w.view.Threshold == value, Action: quotaMenuAction{Kind: "threshold", Threshold: value}})
		}
		return choices
	}
	return nil
}

func quotaChoicesHeight(choices []quotaMenuChoice) int {
	height := 48
	for _, choice := range choices {
		if choice.Separator {
			height += 10
		} else {
			height += 34
		}
	}
	return height
}

// invoke is also used by isolated native tests, never by an external endpoint.
func (m *nativeQuotaMenu) invoke(fn func(*quotaMenuWindow)) error {
	m.mu.Lock()
	hwnd := m.hwnd
	m.mu.Unlock()
	if hwnd == 0 {
		return errors.New("quota window is not running")
	}
	select {
	case m.jobs <- fn:
		qmPostMessage.Call(hwnd, qmInvoke, 0, 0)
		return nil
	case <-m.done:
		return errors.New("quota window closed")
	}
}
