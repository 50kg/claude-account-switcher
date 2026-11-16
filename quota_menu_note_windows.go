package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	qmSetFocus      = user32.NewProc("SetFocus")
	qmGetWindowText = user32.NewProc("GetWindowTextW")
	qmSetWindowText = user32.NewProc("SetWindowTextW")
	qmEnableWindow  = user32.NewProc("EnableWindow")
)

func (w *quotaMenuWindow) noteRect() quotaRect {
	width, height := min(440, w.width-24), 236
	return quotaRect{(w.width - width) / 2, max(8, (w.height-height)/2), width, height}
}

func (w *quotaMenuWindow) openNote() {
	o := w.overlay
	for _, accounts := range [][]quotaMenuAccount{w.view.Accounts, w.view.ArchivedAccounts} {
		for _, a := range accounts {
			if a.ID == o.Account {
				o.Input = a.Note
			}
		}
	}
	// A real Windows EDIT control supplies IME, selection, clipboard and undo.
	w.noteEdit, _, _ = qmCreateWindow.Call(0x200, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("EDIT"))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(o.Input))), 0x50010080, 0, 0, 1, 1, w.hwnd, 0, 0, 0)
	if w.noteEdit == 0 {
		o.Error = tr("Could not open the note editor", "无法打开备注输入框")
		w.invalidate()
		return
	}
	procSendMessage.Call(w.noteEdit, 0xc5, accountNoteMaxLength*2, 0) // EM_LIMITTEXT, UTF-16 units
	w.positionNoteEdit()
	procSendMessage.Call(w.noteEdit, 0xb1, 0, ^uintptr(0)) // EM_SETSEL: select existing note
	w.focus = -1
	if !w.menu.options.noActivate {
		qmSetFocus.Call(w.noteEdit)
	}
}

func (w *quotaMenuWindow) positionNoteEdit() {
	if w.noteEdit == 0 {
		return
	}
	r := w.noteRect()
	procSetWindowPos.Call(w.noteEdit, 0, uintptr(w.scale(r.X+18)), uintptr(w.scale(r.Y+91)), uintptr(w.scale(r.W-36)), uintptr(w.scale(34)), 0x14)
	procSendMessage.Call(w.noteEdit, 0x30, w.fonts[0], 1) // WM_SETFONT
}

func (w *quotaMenuWindow) closeNoteEdit() {
	if w.noteEdit != 0 {
		hwnd := w.noteEdit
		w.noteEdit = 0
		w.noteComposing = false
		qmDestroyWindow.Call(hwnd)
	}
}

func (w *quotaMenuWindow) noteValue() string {
	if w.noteEdit == 0 {
		return w.overlay.Input
	}
	var value [accountNoteMaxLength*2 + 1]uint16
	qmGetWindowText.Call(w.noteEdit, uintptr(unsafe.Pointer(&value[0])), uintptr(len(value)))
	return windows.UTF16ToString(value[:])
}

// Handle editor shortcuts before TranslateMessage; other messages go to EDIT,
// including all IME composition and ordinary text editing.
func (w *quotaMenuWindow) noteMessage(msg qmMessage) bool {
	if w.noteEdit == 0 || msg.Window != w.noteEdit {
		return false
	}
	if msg.Message == 0x10d { // WM_IME_STARTCOMPOSITION
		w.noteComposing = true
	}
	if msg.Message == 0x10e { // WM_IME_ENDCOMPOSITION
		w.noteComposing = false
	}
	if msg.Message != 0x100 || w.noteComposing {
		return false
	}
	switch msg.WParam {
	case 27:
		w.closeOverlay()
	case 13:
		w.submitNote()
	case 9:
		w.focus = 1 // Cancel; Shift+Tab starts at Save.
		if shift, _, _ := qmKeyState.Call(16); shift&0x8000 != 0 {
			w.focus = 2
		}
		qmSetFocus.Call(w.hwnd)
		w.invalidate()
	case 65:
		if ctrl, _, _ := qmKeyState.Call(17); ctrl&0x8000 != 0 {
			procSendMessage.Call(w.noteEdit, 0xb1, 0, ^uintptr(0))
		} else {
			return false
		}
	default:
		return false
	}
	return true
}

func (w *quotaMenuWindow) activateNote(a quotaMenuAction) bool {
	if a.Kind != "note-clear" && a.Kind != "note-save" {
		return false
	}
	o := w.overlay
	if o == nil || o.Kind != "note" || o.Saving || w.noteEdit == 0 {
		return true
	}
	if a.Kind == "note-clear" {
		qmSetWindowText.Call(w.noteEdit, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(""))))
		o.Error = ""
		if !w.menu.options.noActivate {
			qmSetFocus.Call(w.noteEdit)
		}
		w.focus = -1
		w.invalidate()
	} else {
		w.submitNote()
	}
	return true
}

func (w *quotaMenuWindow) submitNote() {
	o := w.overlay
	if o == nil || o.Kind != "note" || o.Saving || w.noteEdit == 0 {
		return
	}
	value := w.noteValue()
	if !validAccountNote(value) {
		o.Error = tr("Use a single line of up to 160 characters", "请输入不超过160个字符的单行备注")
		w.invalidate()
		return
	}
	o.Saving, o.Error = true, ""
	qmEnableWindow.Call(w.noteEdit, 0)
	w.invalidate()
	go func() {
		err := w.menu.onAction(quotaMenuAction{Kind: "account-note", Account: o.Account, Note: value})
		_ = w.menu.invoke(func(ui *quotaMenuWindow) {
			if ui.overlay != o {
				if err != nil && ui.menu.onError != nil {
					ui.menu.onError(err.Error())
				}
				return
			}
			o.Saving = false
			if err != nil {
				o.Error = err.Error()
				qmEnableWindow.Call(ui.noteEdit, 1)
				ui.invalidate()
				return
			}
			ui.closeOverlay()
			ui.reload(false)
		})
	}()
}

func (p *qmPainter) noteEditor(o *quotaMenuOverlay) {
	w := p.w
	r := w.noteRect()
	o.Rect = r
	p.round(r, 6, qmWhite, 0xcfd6e0)
	p.text(tr("Account note", "自定义备注"), quotaRect{r.X + 18, r.Y + 12, r.W - 36, 30}, 2, qmInk, false)
	for _, accounts := range [][]quotaMenuAccount{w.view.Accounts, w.view.ArchivedAccounts} {
		for _, a := range accounts {
			if a.ID == o.Account {
				p.text(a.Name+" · "+a.Email, quotaRect{r.X + 18, r.Y + 48, r.W - 36, 24}, 1, qmMuted, false)
			}
		}
	}
	field := quotaRect{r.X + 18, r.Y + 91, r.W - 36, 34}
	p.round(field, 3, qmWhite, 0xcfd6e0)
	// Also paint the value for isolated menu captures; EDIT covers it on screen.
	p.text(w.noteValue(), quotaRect{field.X + 5, field.Y, field.W - 10, field.H}, 0, qmInk, false)
	p.text(tr("Up to 160 characters. Leave blank to remove the note.", "最多160个字符，留空并保存即可删除备注。"), quotaRect{r.X + 18, r.Y + 135, r.W - 36, 24}, 1, qmMuted, false)
	message := o.Error
	if o.Saving {
		message = tr("Saving…", "正在保存…")
	}
	p.text(message, quotaRect{r.X + 18, r.Y + 161, r.W - 36, 23}, 1, qmRed, false)
	w.addFixedTooltip(quotaRect{r.X + 18, r.Y + 161, r.W - 36, 23}, message)
	for i, label := range []string{tr("Clear", "清空"), tr("Cancel", "取消"), tr("Save", "保存")} {
		button := quotaRect{r.X + 18 + i*(r.W-36)/3, r.Y + r.H - 44, (r.W - 48) / 3, 32}
		fill, ink := uint32(qmWhite), uint32(qmInk)
		if i == 2 {
			fill, ink = qmBlue, qmWhite
		}
		p.round(button, 4, fill, 0xcfd6e0)
		p.centeredText(label, button, 0, ink)
		hit := quotaMenuHit{Rect: button}
		if i == 1 {
			hit.Dismiss = true
		} else if i == 0 {
			hit.Action.Kind = "note-clear"
		} else {
			hit.Action.Kind = "note-save"
		}
		if !o.Saving {
			w.addHit(hit, false)
		}
	}
}
