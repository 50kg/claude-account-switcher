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
	qmRegisterClass = user32.NewProc("RegisterClassExW")
	qmCreateWindow  = user32.NewProc("CreateWindowExW")
	qmDestroyWindow = user32.NewProc("DestroyWindow")
	qmDefWindowProc = user32.NewProc("DefWindowProcW")
	qmGetMessage    = user32.NewProc("GetMessageW")
	qmTranslate     = user32.NewProc("TranslateMessage")
	qmDispatch      = user32.NewProc("DispatchMessageW")
	qmPostMessage   = user32.NewProc("PostMessageW")
	qmPostQuit      = user32.NewProc("PostQuitMessage")
	qmShowWindow    = user32.NewProc("ShowWindow")
	qmInvalidate    = user32.NewProc("InvalidateRect")
	qmUpdateWindow  = user32.NewProc("UpdateWindow")
	qmGetClientRect = user32.NewProc("GetClientRect")
	qmGetCursorPos  = user32.NewProc("GetCursorPos")
	qmClientScreen  = user32.NewProc("ClientToScreen")
	qmForeground    = user32.NewProc("GetForegroundWindow")
	qmGetAncestor   = user32.NewProc("GetAncestor")
	qmKeyState      = user32.NewProc("GetAsyncKeyState")
	qmMonitorPoint  = user32.NewProc("MonitorFromPoint")
	qmGetMonitorDPI = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
	qmLoadCursor    = user32.NewProc("LoadCursorW")
	qmCreatePopup   = user32.NewProc("CreatePopupMenu")
	qmAppendMenu    = user32.NewProc("AppendMenuW")
	qmTrackPopup    = user32.NewProc("TrackPopupMenuEx")
	qmDestroyMenu   = user32.NewProc("DestroyMenu")
	qmWindows       sync.Map
	qmClassOnce     sync.Once
	qmClassErr      error
	qmCallback      = windows.NewCallback(quotaMenuWindowProc)
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
	view     func() quotaMenuView
	onAction func(quotaMenuAction) error
	onError  func(string)
	mu       sync.Mutex
	hwnd     uintptr
	started  bool
	closing  bool
	ready    chan struct{}
	done     chan struct{}
	shown    atomic.Bool
	jobs     chan func(*quotaMenuWindow)
	options  nativeQuotaMenuOptions
}

// Options are used by isolated tests; normal startup uses the zero value.
type nativeQuotaMenuOptions struct {
	noActivate  bool
	dpiOverride int
	input       func(uintptr) quotaMenuInput
	confirm     func(quotaMenuAction) bool
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

func newQuotaMenu(view func() quotaMenuView, action func(quotaMenuAction) error, onError func(string)) quotaMenu {
	return &nativeQuotaMenu{view: view, onAction: action, onError: onError, jobs: make(chan func(*quotaMenuWindow), 16)}
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
			ClassName: windows.StringToUTF16Ptr("ClaudeQuotaContextMenu"), Style: 0x20000} // CS_DROPSHADOW
		wc.Size = uint32(unsafe.Sizeof(wc))
		if result, _, err := qmRegisterClass.Call(uintptr(unsafe.Pointer(&wc))); result == 0 {
			qmClassErr = fmt.Errorf("register quota menu: %w", err)
		}
	})
	var err error
	w := &quotaMenuWindow{menu: m, collapsed: map[string]bool{}, focus: -1, hover: -1, pressed: -1, dpi: 96}
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
	Rect   quotaRect
	Action quotaMenuAction
	Fold   string
	Popup  string
}

type quotaMenuWindow struct {
	menu                                                           *nativeQuotaMenu
	hwnd                                                           uintptr
	view                                                           quotaMenuView
	dpi, width, height, bodyTop, bodyBottom, contentHeight, scroll int
	anchor                                                         qmPoint
	work                                                           quotaRect
	fonts                                                          [4]uintptr
	collapsed                                                      map[string]bool
	hits                                                           []quotaMenuHit
	focus, hover, pressed                                          int
	modal                                                          bool
	nativePopup                                                    uintptr
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
		if w.menu.shown.Load() && !w.modal {
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
		if w.menu.shown.Load() && !w.modal && w.dismiss.hide(time.Now(), w.input()) {
			w.hide()
		}
		return 0
	case 0x200: // WM_MOUSEMOVE
		w.hover = w.hit(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi)
		w.invalidate()
		return 0
	case 0x201: // WM_LBUTTONDOWN
		w.pressed = w.hit(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi)
		return 0
	case 0x202: // WM_LBUTTONUP
		i := w.hit(int(int16(lp&0xffff))*96/w.dpi, int(int16(lp>>16))*96/w.dpi)
		if i >= 0 && i == w.pressed {
			w.activate(w.hits[i])
		}
		w.pressed = -1
		return 0
	case 0x20A: // WM_MOUSEWHEEL
		w.scroll = max(0, min(w.scroll-int(int16(wp>>16))/120*60, max(0, w.contentHeight-(w.bodyBottom-w.bodyTop))))
		w.focus, w.hover = -1, -1
		w.invalidate()
		return 0
	case 0x100: // WM_KEYDOWN
		switch wp {
		case 27:
			w.hide()
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
		case 33, 34:
			delta := w.bodyBottom - w.bodyTop - 40
			if wp == 33 {
				delta = -delta
			}
			w.scroll = max(0, min(w.scroll+delta, max(0, w.contentHeight-(w.bodyBottom-w.bodyTop))))
			w.invalidate()
		}
		return 0
	case 0x02E0: // WM_DPICHANGED
		w.dpi = int(wp & 0xffff)
		w.makeFonts()
		w.reload(false)
		return 0
	case 0x10: // WM_CLOSE
		w.menu.mu.Lock()
		closing := w.menu.closing
		w.menu.mu.Unlock()
		if closing {
			qmDestroyWindow.Call(hwnd)
		} else {
			w.hide()
		}
		return 0
	case 0x02: // WM_DESTROY
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
		qmGetCursorPos.Call(uintptr(unsafe.Pointer(&w.anchor)))
		w.scroll, w.focus, w.hover, w.pressed = 0, -1, -1, -1
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
	for _, a := range w.view.Accounts {
		w.contentHeight += quotaAccountHeight(a, w.collapsed[a.ID])
	}
	if len(w.view.Accounts) == 0 {
		w.contentHeight = 92
	}
	w.bodyTop = 58
	if w.view.Status != "" {
		w.bodyTop += 30
	}
	position := placeQuotaMenu(w.work, int(w.anchor.X), int(w.anchor.Y), w.scale(520), w.scale(w.bodyTop+w.contentHeight+88))
	w.width = position.W * 96 / w.dpi
	w.height = position.H * 96 / w.dpi
	w.bodyBottom = w.height - 88
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

func (w *quotaMenuWindow) hide() {
	procKillTimer.Call(w.hwnd, 2)
	qmShowWindow.Call(w.hwnd, 0)
	w.menu.shown.Store(false)
	w.focus, w.hover, w.pressed = -1, -1, -1
}

func (w *quotaMenuWindow) hit(x, y int) int {
	for i := len(w.hits) - 1; i >= 0; i-- {
		if w.hits[i].Rect.contains(x, y) {
			return i
		}
	}
	return -1
}

func (w *quotaMenuWindow) activate(hit quotaMenuHit) {
	if hit.Fold != "" {
		w.collapsed[hit.Fold] = !w.collapsed[hit.Fold]
		w.focus, w.hover = -1, -1
		w.reload(false)
		return
	}
	if hit.Popup != "" {
		w.popup(hit.Popup, hit.Rect)
		return
	}
	a := hit.Action
	if a.Kind == "" {
		return
	}
	if a.Kind == "switch" || a.Kind == "add" || a.Kind == "import" {
		w.modal = true
		confirmed := false
		if w.menu.options.confirm != nil {
			confirmed = w.menu.options.confirm(a)
		} else {
			text := tr("This restarts Claude. Continue?", "此操作会关闭并重新启动 Claude。是否继续？")
			if a.Kind == "switch" {
				for _, account := range w.view.Accounts {
					if account.ID == a.Account {
						text = account.Name + "\n" + account.Email + "\n\n" + text
						break
					}
				}
			} else if a.Kind == "add" {
				text = tr("Add an account", "添加账号") + "\n\n" + text
			} else {
				text = tr("Import chats", "导入聊天") + "\n\n" + text
			}
			answer, _ := windows.MessageBox(windows.HWND(w.hwnd), windows.StringToUTF16Ptr(text), windows.StringToUTF16Ptr(tr("Confirm restart", "确认重启Claude")), windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2)
			confirmed = answer == 6
		}
		w.modal = false
		w.dismiss.open(time.Now(), w.input())
		if !confirmed {
			return
		}
		a.Confirmed = true
	}
	keepOpen := a.Kind == "auto" || a.Kind == "spend" || a.Kind == "threshold" || a.Kind == "autostart"
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

func (w *quotaMenuWindow) popup(kind string, anchor quotaRect) {
	menu, _, _ := qmCreatePopup.Call()
	if menu == 0 {
		return
	}
	defer qmDestroyMenu.Call(menu)
	actions := map[uintptr]quotaMenuAction{}
	next := uintptr(100)
	add := func(label string, checked bool, action quotaMenuAction) {
		next++
		flags := uintptr(0)
		if checked {
			flags |= 8
		}
		if w.view.Busy && (action.Kind == "import" || action.Kind == "add") {
			flags |= 3
		}
		qmAppendMenu.Call(menu, flags, next, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(label))))
		actions[next] = action
	}
	if kind == "settings" || kind == "threshold" {
		if kind == "settings" {
			add(tr("Auto-switch when nearly full", "额度接近用尽时自动切换"), w.view.Auto, quotaMenuAction{Kind: "auto", Enabled: !w.view.Auto})
		}
		for _, value := range thresholds {
			add(fmt.Sprintf(tr("Switch at %.0f%% used", "已用%.0f%%时切换"), value), w.view.Threshold == value, quotaMenuAction{Kind: "threshold", Threshold: value})
		}
		if kind == "settings" {
			qmAppendMenu.Call(menu, 0x800, 0, 0)
			add(tr("Use expiring weekly allowance", "优先使用即将重置的周额度"), w.view.Spend, quotaMenuAction{Kind: "spend", Enabled: !w.view.Spend})
			add(tr("Start at login", "开机启动"), w.view.Startup, quotaMenuAction{Kind: "autostart", Enabled: !w.view.Startup})
		}
	} else {
		add(tr("Import chats from all accounts…", "导入所有账号的聊天…"), false, quotaMenuAction{Kind: "import", Active: w.view.Active})
		add(tr("Open data folder", "打开数据文件夹"), false, quotaMenuAction{Kind: "folder"})
		title, _ := updateMenuText(w.view.Update)
		if title == "" {
			title = tr("Check for updates", "检查更新")
		}
		add(title, false, quotaMenuAction{Kind: "update"})
	}
	p := qmPoint{X: int32(w.scale(anchor.X)), Y: int32(w.scale(anchor.Y + anchor.H))}
	qmClientScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	w.modal = true
	w.nativePopup = menu
	selected, _, _ := qmTrackPopup.Call(menu, 0x100|0x2, uintptr(p.X), uintptr(p.Y), w.hwnd, 0)
	w.nativePopup = 0
	w.modal = false
	input := w.input()
	w.dismiss.open(time.Now(), input)
	if selected == 0 && !input.inside && input.knownForeground && !input.ownForeground {
		w.hide()
		return
	}
	if action, ok := actions[selected]; ok {
		w.activate(quotaMenuHit{Action: action})
	}
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
