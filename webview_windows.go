package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procSendMessage              = user32.NewProc("SendMessageW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo           = user32.NewProc("GetMonitorInfoW")
	procGetWindowLongPtr         = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr         = user32.NewProc("SetWindowLongPtrW")
)

const (
	gwlStyle                     = ^uintptr(15) // GWL_STYLE, -16
	wsMinimizeBox, wsMaximizeBox = 0x20000, 0x10000
)

// The page's width in CSS pixels; its height comes from the page itself.
const resumeWidth = 460

// showWebDialog shows page in a WebView2 window and returns what the page
// passed to resumeDone, or nil if the window was closed.
func showWebDialog(page string, chats int) ([]bool, error) {
	runtime.LockOSThread()
	dark := darkMode()
	// The window's color until the page paints, so it never flashes white.
	bg, argb := uint32(0xF5F9FA), "FFFAF9F5" // #faf9f5, COLORREF is BGR
	if dark {
		bg, argb = 0x242626, "FF262624" // #262624
	}
	os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", argb)
	home, _ := os.UserHomeDir()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:      filepath.Join(home, ".claude-account-switcher", "webview"),
		AutoFocus:     true,
		WindowOptions: webview2.WindowOptions{Title: "Continue chats?", Width: 1, Height: 1},
	})
	if w == nil {
		return nil, errors.New("WebView2 is not available")
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	scale := func(css int) int {
		dpi, _, _ := procGetDpiForWindow.Call(hwnd)
		if dpi == 0 {
			dpi = 96
		}
		return css * int(dpi) / 96
	}
	styleWindow(hwnd, dark, bg)
	resize := func(height int) {
		w.SetSize(scale(resumeWidth), scale(height), webview2.HintFixed)
		// Only the close button: a question has nothing to minimize.
		style, _, _ := procGetWindowLongPtr.Call(hwnd, gwlStyle)
		procSetWindowLongPtr.Call(hwnd, gwlStyle, style&^(wsMinimizeBox|wsMaximizeBox))
		centerWindow(hwnd)
	}
	resize(232 + min(chats, 5)*64)

	var keep []bool
	w.Bind("resumeFit", resize)
	w.Bind("resumeDone", func(answer []bool) {
		keep = answer
		w.Terminate()
	})
	w.SetHtml(page)
	w.Run()
	return keep, nil
}

// darkMode reports whether apps use the dark theme.
func darkMode() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}

// styleWindow paints the title bar in the page's color, gives the window the
// switcher's ring icon and keeps it above other windows, since it asks
// while the user may be busy elsewhere.
func styleWindow(hwnd uintptr, dark bool, bg uint32) {
	const (
		dwmwaDarkMode, dwmwaCaptionColor, dwmwaTextColor = 20, 35, 36
		wmSetIcon, hwndTopmost                           = 0x80, ^uintptr(0)
		swpNoSize, swpNoMove                             = 0x1, 0x2
	)
	on := uint32(0)
	text := uint32(0x1D1E1F)
	if dark {
		on, text = 1, 0xEFF4F5
	}
	h := windows.HWND(hwnd)
	_ = windows.DwmSetWindowAttribute(h, dwmwaDarkMode, unsafe.Pointer(&on), 4)
	_ = windows.DwmSetWindowAttribute(h, dwmwaCaptionColor, unsafe.Pointer(&bg), 4)
	_ = windows.DwmSetWindowAttribute(h, dwmwaTextColor, unsafe.Pointer(&text), 4)

	ico := encodeICO(drawIcon(42, 100))
	if icon, _, _ := procCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&ico[22])), uintptr(len(ico)-22), 1, 0x30000, 0, 0, 0); icon != 0 {
		procSendMessage.Call(hwnd, wmSetIcon, 0, icon)
		procSendMessage.Call(hwnd, wmSetIcon, 1, icon)
	}
	procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoSize|swpNoMove)
	procSetForegroundWindow.Call(hwnd)
}

// centerWindow puts the window in the middle of its monitor's work area.
func centerWindow(hwnd uintptr) {
	const monitorDefaultToNearest, swpNoSize, swpNoZOrder, swpFrameChanged = 2, 0x1, 0x4, 0x20
	var r windows.Rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	var mi struct {
		CbSize            uint32
		Monitor, WorkArea windows.Rect
		Flags             uint32
	}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if ok, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return
	}
	wa := mi.WorkArea
	x := wa.Left + (wa.Right-wa.Left-(r.Right-r.Left))/2
	y := wa.Top + (wa.Bottom-wa.Top-(r.Bottom-r.Top))/2
	procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpFrameChanged)
}
