package main

import (
	"fmt"
	"log"
	"runtime"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procDialogBoxIndirectParam = user32.NewProc("DialogBoxIndirectParamW")
	procEndDialog              = user32.NewProc("EndDialog")
	procCheckDlgButton         = user32.NewProc("CheckDlgButton")
	procIsDlgButtonChecked     = user32.NewProc("IsDlgButtonChecked")
	procSetDlgItemText         = user32.NewProc("SetDlgItemTextW")
	procSetTimer               = user32.NewProc("SetTimer")
	procKillTimer              = user32.NewProc("KillTimer")
	resumeDialogProc           = windows.NewCallback(resumeDialog)
	// The state of the one resume dialog that can be open at a time.
	resumeShown, resumeLeft int
	resumeKeep              []bool
)

const (
	wmInitDialog = 0x0110
	wmCommand    = 0x0111
	wmTimer      = 0x0113
	idOK         = 1
	idCancel     = 2
	firstBox     = 100
	maxBoxes     = 12
)

// nativeResume asks which of the chats titled titles should go on and
// returns one answer per title. After wait without an answer, or if the
// dialog cannot open, all of them go on. Chats beyond the dialog's list go on.
func nativeResume(titles []string, wait time.Duration) []bool {
	done := make(chan []bool)
	go func() {
		// The dialog's window belongs to this thread; Go ends the thread
		// with the goroutine because it stays locked.
		runtime.LockOSThread()
		done <- showResumeDialog(titles, wait)
	}()
	return <-done
}

func showResumeDialog(titles []string, wait time.Duration) []bool {
	resumeKeep = make([]bool, len(titles))
	for i := range resumeKeep {
		resumeKeep[i] = true
	}
	resumeShown = min(len(titles), maxBoxes)
	resumeLeft = int(wait / time.Second)

	const (
		wsPopup, wsCaption, wsSysMenu   = 0x80000000, 0x00C00000, 0x00080000
		wsChild, wsVisible, wsTabStop   = 0x40000000, 0x10000000, 0x00010000
		dsSetFont, dsModalFrame         = 0x40, 0x80
		dsSetForeground, dsCenter       = 0x200, 0x800
		wsExTopmost                     = 0x8
		bsDefPushButton, bsAutoCheckBox = 0x1, 0x3
		ssNoPrefix                      = 0x80
		classButton, classStatic        = 0x80, 0x82
		width, margin, boxHeight, btnW  = 270, 7, 12, 66
		btnH                            = 14
	)
	var t dialogTemplate
	textHeight := 26
	boxesY := margin + textHeight
	listed := resumeShown
	if len(titles) > maxBoxes {
		listed++
	}
	buttonsY := boxesY + listed*boxHeight + 8
	height := buttonsY + btnH + margin
	t.header(wsPopup|wsCaption|wsSysMenu|dsSetFont|dsModalFrame|dsSetForeground|dsCenter, wsExTopmost,
		2+listed+1, width, height, "Continue chats?", 9, "Segoe UI")
	t.item(wsChild|wsVisible|ssNoPrefix, margin, margin, width-2*margin, textHeight, 0xFFFF, classStatic,
		"These chats were working when Claude closed for the switch. Claude continues the checked ones when it starts again.")
	for i := range resumeShown {
		t.item(wsChild|wsVisible|wsTabStop|bsAutoCheckBox, margin, boxesY+i*boxHeight, width-2*margin, boxHeight-2,
			uint16(firstBox+i), classButton, menuText(titles[i]))
	}
	if len(titles) > maxBoxes {
		t.item(wsChild|wsVisible|ssNoPrefix, margin, boxesY+resumeShown*boxHeight+2, width-2*margin, boxHeight-2, 0xFFFF, classStatic,
			fmt.Sprintf("and %d more, which continue too", len(titles)-maxBoxes))
	}
	t.item(wsChild|wsVisible|wsTabStop|bsDefPushButton, width-margin-2*btnW-4, buttonsY, btnW, btnH, idOK, classButton, continueLabel())
	t.item(wsChild|wsVisible|wsTabStop, width-margin-btnW, buttonsY, btnW, btnH, idCancel, classButton, "Don't continue")

	var inst windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &inst)
	tmpl := t.aligned()
	r, _, err := procDialogBoxIndirectParam.Call(uintptr(inst), uintptr(unsafe.Pointer(&tmpl[0])), 0, resumeDialogProc, 0)
	runtime.KeepAlive(tmpl)
	if int(r) <= 0 {
		log.Printf("resume dialog: %v", err)
		for i := range resumeKeep {
			resumeKeep[i] = true
		}
	}
	return resumeKeep
}

func resumeDialog(hwnd, msg, wparam, _ uintptr) uintptr {
	switch msg {
	case wmInitDialog:
		for i := range resumeShown {
			procCheckDlgButton.Call(hwnd, uintptr(firstBox+i), 1)
		}
		procSetTimer.Call(hwnd, 1, 1000, 0)
		return 1
	case wmTimer:
		resumeLeft--
		if resumeLeft <= 0 {
			endResumeDialog(hwnd, idOK)
		} else {
			setDialogText(hwnd, idOK, continueLabel())
		}
		return 1
	case wmCommand:
		id, code := wparam&0xFFFF, wparam>>16&0xFFFF
		switch {
		case id == idOK || id == idCancel:
			endResumeDialog(hwnd, id)
		case id >= firstBox && code == 0:
			// Someone is choosing, so the countdown stops.
			procKillTimer.Call(hwnd, 1)
			resumeLeft = 0
			setDialogText(hwnd, idOK, continueLabel())
		}
		return 1
	}
	return 0
}

// endResumeDialog closes the dialog. Escape and the close button count as
// Don't continue.
func endResumeDialog(hwnd, id uintptr) {
	procKillTimer.Call(hwnd, 1)
	for i := range resumeShown {
		checked, _, _ := procIsDlgButtonChecked.Call(hwnd, uintptr(firstBox+i))
		resumeKeep[i] = id == idOK && checked == 1
	}
	if id == idCancel {
		for i := range resumeKeep {
			resumeKeep[i] = false
		}
	}
	procEndDialog.Call(hwnd, id)
}

func continueLabel() string {
	if resumeLeft > 0 {
		return fmt.Sprintf("Continue (%d)", resumeLeft)
	}
	return "Continue"
}

func setDialogText(hwnd, id uintptr, s string) {
	p, _ := windows.UTF16PtrFromString(s)
	procSetDlgItemText.Call(hwnd, id, uintptr(unsafe.Pointer(p)))
}

// menuText shortens a chat title for a checkbox and keeps & from turning
// into a keyboard shortcut.
func menuText(title string) string {
	r := []rune(title)
	if len(r) > 64 {
		r = append(r[:63], '…')
	}
	out := make([]rune, 0, len(r))
	for _, c := range r {
		if c == '&' {
			out = append(out, c)
		}
		out = append(out, c)
	}
	return string(out)
}

// dialogTemplate builds an in-memory DLGTEMPLATE, so the dialog needs no
// resource file. Sizes are in dialog units, which follow the font.
type dialogTemplate struct{ w []uint16 }

func (t *dialogTemplate) words(v ...uint16) { t.w = append(t.w, v...) }
func (t *dialogTemplate) dword(v uint32)    { t.words(uint16(v), uint16(v>>16)) }

func (t *dialogTemplate) str(s string) {
	t.words(utf16.Encode([]rune(s))...)
	t.words(0)
}

func (t *dialogTemplate) header(style, exStyle uint32, items, cx, cy int, title string, points uint16, font string) {
	t.dword(style)
	t.dword(exStyle)
	t.words(uint16(items), 0, 0, uint16(cx), uint16(cy), 0, 0)
	t.str(title)
	t.words(points)
	t.str(font)
}

func (t *dialogTemplate) item(style uint32, x, y, cx, cy int, id, class uint16, text string) {
	if len(t.w)%2 == 1 {
		t.words(0) // each item starts on a DWORD boundary
	}
	t.dword(style)
	t.dword(0)
	t.words(uint16(x), uint16(y), uint16(cx), uint16(cy), id, 0xFFFF, class)
	t.str(text)
	t.words(0)
}

// aligned returns the template in DWORD-aligned memory.
func (t *dialogTemplate) aligned() []uint32 {
	buf := make([]uint32, (len(t.w)+1)/2)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[0])), len(buf)*2), t.w)
	return buf
}
