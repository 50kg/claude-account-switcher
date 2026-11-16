package main

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Reuse the isolated bilingual archived-account host; no real sign-in is read.
func verifyNativeAccountNotes(t *testing.T, f *quotaMenuFixture) {
	t.Helper()
	m := f.menu
	open := func() {
		nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "account-actions" && h.Action.Account == "synthetic-alex" })
		nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "note" })
		nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.noteEdit != 0 && w.overlay != nil && w.overlay.Kind == "note" })
	}
	value := "工作账号 / Project A 🚀"
	typeNote := func() {
		nativeMenuDo(t, m, func(w *quotaMenuWindow) {
			procSendMessage.Call(w.noteEdit, 0xb1, 0, ^uintptr(0))
			procSendMessage.Call(w.noteEdit, 0xc2, 1, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(value)))) // EM_REPLACESEL
			if w.noteValue() != value {
				t.Error("native Unicode editing lost characters")
			}
		})
	}
	open()
	typeNote()
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		w.noteMessage(qmMessage{Window: w.noteEdit, Message: 0x10d})
		if w.noteMessage(qmMessage{Window: w.noteEdit, Message: 0x100, WParam: 13}) {
			t.Error("IME confirmation submitted the note")
		}
		w.noteMessage(qmMessage{Window: w.noteEdit, Message: 0x10e})
		w.noteMessage(qmMessage{Window: w.noteEdit, Message: 0x100, WParam: 27})
		if w.overlay != nil || w.noteEdit != 0 {
			t.Error("Escape did not close the note editor")
		}
	})
	if len(f.actions) != 0 {
		t.Fatal("cancel saved a note")
	}
	open()
	typeNote()
	f.mu.Lock()
	f.failNote = true
	f.mu.Unlock()
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "note-save" })
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && !w.overlay.Saving && w.overlay.Error != "" })
	nativeMenuDo(t, m, func(w *quotaMenuWindow) {
		if w.noteValue() != value {
			t.Error("save failure discarded input")
		}
	})
	f.mu.Lock()
	f.failNote = false
	f.mu.Unlock()
	if dir := os.Getenv("QUOTA_MENU_DELETE_EVIDENCE"); dir != "" {
		nativeMenuDo(t, m, func(w *quotaMenuWindow) {
			w.overlay.Error = ""
			if err := w.capture(filepath.Join(dir, language+"-note-editor.png")); err != nil {
				t.Error(err)
			}
		})
	}
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "note-save" })
	if a := f.expectAction(t, "account-note"); a.Account != "synthetic-alex" || a.Note != value {
		t.Fatal("note was saved on the wrong account")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool {
		return w.overlay == nil && w.noteEdit == 0 && w.view.ArchivedAccounts[1].Note == value
	})
	if dir := os.Getenv("QUOTA_MENU_DELETE_EVIDENCE"); dir != "" {
		nativeMenuDo(t, m, func(w *quotaMenuWindow) {
			if err := w.capture(filepath.Join(dir, language+"-note-saved.png")); err != nil {
				t.Error(err)
			}
		})
	}
	open()
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "note-clear" })
	if len(f.actions) != 0 {
		t.Fatal("clearing input saved without confirmation")
	}
	nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "note-save" })
	if a := f.expectAction(t, "account-note"); a.Note != "" {
		t.Fatal("clear did not save an empty note")
	}
	nativeMenuWait(t, m, func(w *quotaMenuWindow) bool {
		return w.overlay == nil && w.noteEdit == 0 && w.view.ArchivedAccounts[1].Note == ""
	})
}
