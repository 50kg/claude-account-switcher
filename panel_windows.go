package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	procPanelShowWindow       = user32.NewProc("ShowWindow")
	procPanelGetCursorPos     = user32.NewProc("GetCursorPos")
	procPanelMonitorFromPoint = user32.NewProc("MonitorFromPoint")
)

func preparePanelCommand(cmd *exec.Cmd) { hidden(cmd) }

// A native confirmed switch remains available even without WebView2.
func confirmAccountSwitch(account string) bool {
	title, err := windows.UTF16PtrFromString(tr("Switch account", "切换账号"))
	if err != nil {
		return false
	}
	text, err := windows.UTF16PtrFromString(fmt.Sprintf(tr("Switch to %s? Claude will restart and running replies may stop.", "切换到%s？这会重启Claude，正在运行的回复可能中断。"), account))
	if err != nil {
		return false
	}
	answer, err := windows.MessageBox(0, text, title, windows.MB_YESNO|windows.MB_ICONWARNING|windows.MB_DEFBUTTON2|windows.MB_TOPMOST)
	return err == nil && answer == 6 // IDYES; cancel/close/failure never switches.
}

func showQuotaPanel(initial panelMessage, scanner *bufio.Scanner, output io.Writer) error {
	runtime.LockOSThread()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	_ = os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "FFF8F9FB")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:      filepath.Join(home, ".claude-account-switcher", "quota-panel-webview"),
		AutoFocus:     true,
		WindowOptions: webview2.WindowOptions{Title: tr("Account quotas", "账号额度"), Width: 1, Height: 1},
	})
	if w == nil {
		return errors.New("WebView2 unavailable")
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	procPanelShowWindow.Call(hwnd, 0)
	style, _, _ := procGetWindowLongPtr.Call(hwnd, gwlStyle)
	procSetWindowLongPtr.Call(hwnd, gwlStyle, style&^(uintptr(0x00C00000)|0x40000|wsMinimizeBox|wsMaximizeBox))
	const exStyle = ^uintptr(19) // GWL_EXSTYLE, -20
	ex, _, _ := procGetWindowLongPtr.Call(hwnd, exStyle)
	procSetWindowLongPtr.Call(hwnd, exStyle, (ex|0x80)&^uintptr(0x40000)) // tool window, no taskbar button
	corner := uint32(2)
	_ = windows.DwmSetWindowAttribute(windows.HWND(hwnd), 33, unsafe.Pointer(&corner), 4)
	var anchor struct{ X, Y int32 }
	procPanelGetCursorPos.Call(uintptr(unsafe.Pointer(&anchor)))
	shown := false
	fitted := false
	height := 700
	var currentMonitor uintptr
	place := func() {
		packed := uint64(uint32(anchor.X)) | uint64(uint32(anchor.Y))<<32
		monitor, _, _ := procPanelMonitorFromPoint.Call(uintptr(packed), 2)
		var info struct {
			Size          uint32
			Monitor, Work windows.Rect
			Flags         uint32
		}
		info.Size = uint32(unsafe.Sizeof(info))
		if ok, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
			return
		}
		// Move onto the selected monitor before querying its per-window DPI.
		if monitor != currentMonitor {
			procSetWindowPos.Call(hwnd, 0, uintptr(info.Work.Left+8), uintptr(info.Work.Top+8), 0, 0, 0x1|0x4|0x10)
			currentMonitor = monitor
		}
		dpi, _, _ := procGetDpiForWindow.Call(hwnd)
		if dpi == 0 {
			dpi = 96
		}
		scale := func(value int) int32 { return int32(value * int(dpi) / 96) }
		margin := scale(12)
		width := min(scale(424), info.Work.Right-info.Work.Left-2*margin)
		actualHeight := min(scale(height), info.Work.Bottom-info.Work.Top-2*margin)
		x, y := info.Work.Right-width-margin, info.Work.Bottom-actualHeight-margin
		procSetWindowPos.Call(hwnd, ^uintptr(0), uintptr(x), uintptr(y), uintptr(width), uintptr(actualHeight), 0x10|0x20)
	}
	show := func() {
		if !fitted {
			return
		}
		procPanelGetCursorPos.Call(uintptr(unsafe.Pointer(&anchor)))
		place()
		shown = true
		procPanelShowWindow.Call(hwnd, 5)
		procSetForegroundWindow.Call(hwnd)
		w.Eval("window.panelShown && window.panelShown()")
	}
	hide := func() {
		shown = false
		procPanelShowWindow.Call(hwnd, 0)
	}
	fit := func(wanted int) {
		fitted = true
		height = max(240, min(wanted, 2000))
		place()
		if !shown {
			// Initial paint opens once. Later data updates must not reopen a
			// dismissed window, so only the page's first fit may show it.
			if initial.Show {
				initial.Show = false
				show()
			}
		}
	}
	place()
	return runPanelWebView(w, initial, scanner, output, fit, hide, show)
}
