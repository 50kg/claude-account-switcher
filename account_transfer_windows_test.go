//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestAccountTransferFileDialogLayout(t *testing.T) {
	var ofn transferOpenFilename
	wantSize := uintptr(88)
	wantOffsets := []uintptr{28, 32, 36, 40, 44, 52, 60, 76}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantSize = 152
		wantOffsets = []uintptr{48, 56, 64, 72, 80, 96, 104, 136}
	}
	if got := unsafe.Sizeof(ofn); got != wantSize {
		t.Fatalf("OPENFILENAMEW size = %d, Windows SDK requires %d", got, wantSize)
	}
	gotOffsets := []uintptr{
		unsafe.Offsetof(ofn.File), unsafe.Offsetof(ofn.MaxFile),
		unsafe.Offsetof(ofn.FileTitle), unsafe.Offsetof(ofn.MaxFileTitle),
		unsafe.Offsetof(ofn.InitialDir), unsafe.Offsetof(ofn.Flags),
		unsafe.Offsetof(ofn.DefaultExtension), unsafe.Offsetof(ofn.Reserved),
	}
	for i, want := range wantOffsets {
		if gotOffsets[i] != want {
			t.Errorf("OPENFILENAMEW field %d offset = %d, Windows SDK requires %d", i, gotOffsets[i], want)
		}
	}
}

// Explicit opt-in: opens only isolated common dialogs, uses fake files in
// t.TempDir, and never starts the switcher or reads Claude/account data.
func TestAccountTransferNativeFileDialogs(t *testing.T) {
	if os.Getenv("ACCOUNT_TRANSFER_NATIVE_SMOKE") != "1" {
		t.Skip("set ACCOUNT_TRANSFER_NATIVE_SMOKE=1 for isolated native dialogs")
	}
	dir := filepath.Join(t.TempDir(), "中文路径")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	longPath := make([]uint16, 32768)
	if n, err := windows.GetLongPathName(windows.StringToUTF16Ptr(dir), &longPath[0], uint32(len(longPath))); err != nil || n == 0 || n >= uint32(len(longPath)) {
		t.Fatalf("normalize fixture directory: %v", err)
	} else {
		dir = windows.UTF16ToString(longPath[:n])
	}
	openPath := filepath.Join(dir, "synthetic@example.com.cas-account")
	if err := os.WriteFile(openPath, []byte("synthetic file picker fixture; no credentials"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		save   bool
		cancel bool
		path   string
	}{
		{"save-selected", true, false, filepath.Join(dir, "export@example.com.cas-account")},
		{"save-cancelled", true, true, filepath.Join(dir, "已取消.cas-account")},
		{"open-selected", false, false, openPath},
		{"open-cancelled", false, true, openPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getParent := user32.NewProc("GetParent")
			post := user32.NewProc("PostMessageW")
			send := user32.NewProc("SendMessageW")
			setTimer := user32.NewProc("SetTimer")
			killTimer := user32.NewProc("KillTimer")
			initialized := make(chan uintptr, 1)
			done := make(chan struct{})
			watchdogDone := make(chan struct{})
			var timedOut atomic.Bool
			wait := 8 * time.Second
			t.Logf("fixture path: %s", tc.path)
			go func() {
				defer close(watchdogDone)
				select {
				case parent := <-initialized:
					select {
					case <-time.After(wait):
						timedOut.Store(true)
						post.Call(parent, 0x111, 2, 0) // Cancel only this test dialog.
					case <-done:
					}
				case <-done:
				}
			}()
			var sawInit, acted bool
			hook := windows.NewCallback(func(hwnd, message, wp, lp uintptr) uintptr {
				parent, _, _ := getParent.Call(hwnd)
				if message == 0x110 { // WM_INITDIALOG
					sawInit = true
					initialized <- parent
					setTimer.Call(hwnd, 10, 250, 0)
				}
				if message == 0x113 && wp == 10 && !acted { // WM_TIMER, after initialization
					killTimer.Call(hwnd, 10)
					acted = true
					command := uintptr(1) // IDOK
					if tc.cancel {
						command = 2 // IDCANCEL
					} else {
						// Simulate entering the selected fixture's full path in the
						// filename control, independent of Explorer's recent folder.
						text := windows.StringToUTF16(tc.path)
						send.Call(parent, 0x468, 0x480, uintptr(unsafe.Pointer(&text[0]))) // CDM_SETCONTROLTEXT / edt1
					}
					post.Call(parent, 0x111, command, 0)
				}
				return 0
			})
			path, err := transferFileDialogWithHook(tc.save, tc.path, hook)
			close(done)
			<-watchdogDone
			if err != nil {
				t.Fatal(err)
			}
			if !sawInit || !acted || timedOut.Load() {
				t.Fatalf("native dialog was not usable: initialized=%t, acted=%t, timeout=%t", sawInit, acted, timedOut.Load())
			}
			if tc.cancel {
				if path != "" {
					t.Fatalf("cancel returned a path: %q", path)
				}
			} else if !strings.EqualFold(path, tc.path) {
				t.Fatalf("selected path = %q, want %q", path, tc.path)
			}
			if tc.save {
				if _, err := os.Stat(tc.path); !os.IsNotExist(err) {
					t.Fatalf("file picker unexpectedly wrote an export: %v", err)
				}
			}
		})
	}
	if cwd, err := os.Getwd(); err != nil || cwd != beforeCWD {
		t.Fatalf("file picker changed the working directory: %q, %v", cwd, err)
	}
}
