package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestNativeArchivedDeletionSmoke(t *testing.T) {
	if os.Getenv("QUOTA_MENU_NATIVE_SMOKE") != "1" {
		t.Skip("explicit isolated native smoke only")
	}
	if os.Getenv("QUOTA_MENU_DELETE_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := hidden(exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeArchivedDeletionSmoke$", "-test.v"))
		cmd.Env = append(os.Environ(), "QUOTA_MENU_DELETE_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native deletion smoke: %v\n%s", err, out)
		}
		return
	}
	for _, lang := range []string{"zh-CN", "en"} {
		t.Run(lang, func(t *testing.T) {
			useLanguage(t, lang)
			f := newQuotaMenuFixture()
			f.snapshot.Accounts[0].Archived = true // Current account must be protected.
			f.snapshot.Accounts[1].Archived = true
			m := f.menu
			m.show()
			defer func() {
				m.close()
				select {
				case <-m.done:
				case <-time.After(5 * time.Second):
					t.Error("native window did not close")
				}
			}()
			nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return m.shown.Load() && len(w.hits) > 0 })
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "settings" })
			nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "settings" })
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "archived-view" })
			nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.archivedView && len(w.visibleAccounts()) == 2 })
			verifyNativeAccountNotes(t, f)
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool {
				return h.Popup == "account-actions" && h.Action.Account == "synthetic-riley"
			})
			nativeMenuDo(t, m, func(w *quotaMenuWindow) {
				if !slices.ContainsFunc(w.choices("account-actions"), func(c quotaMenuChoice) bool { return c.Action.Kind == "account-delete" && c.Disabled }) {
					t.Error("current archived account has an enabled delete action")
				}
				w.closeOverlay()
			})
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "account-actions" && h.Action.Account == "synthetic-alex" })
			capture := func(name string) {
				if dir := os.Getenv("QUOTA_MENU_DELETE_EVIDENCE"); dir != "" {
					nativeMenuDo(t, m, func(w *quotaMenuWindow) {
						if err := w.capture(filepath.Join(dir, lang+"-"+name+".png")); err != nil {
							t.Error(err)
						}
					})
				}
			}
			capture("archived-actions")
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "account-delete" })
			nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "confirm" })
			capture("delete-confirmation")
			if len(f.actions) != 0 {
				t.Fatal("deletion dispatched before confirmation")
			}
			// Enter defaults to Cancel and returns without invoking deletion.
			nativeMenuDo(t, m, func(w *quotaMenuWindow) { procSendMessage.Call(w.hwnd, 0x100, 13, 0) })
			nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay == nil })
			if len(f.actions) != 0 {
				t.Fatal("cancel invoked deletion")
			}
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Popup == "account-actions" && h.Action.Account == "synthetic-alex" })
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "account-delete" })
			nativeMenuWait(t, m, func(w *quotaMenuWindow) bool { return w.overlay != nil && w.overlay.Kind == "confirm" })
			nativeMenuClickHit(t, m, func(h quotaMenuHit) bool { return h.Action.Kind == "account-delete" && h.Action.Confirmed })
			a := f.expectAction(t, "account-delete")
			if a.Account != "synthetic-alex" || !a.Confirmed {
				t.Fatalf("wrong deletion action: %+v", a)
			}
			nativeMenuWait(t, m, func(w *quotaMenuWindow) bool {
				return w.archivedView && m.shown.Load() && w.overlay == nil && len(w.view.ArchivedAccounts) == 1 && w.view.ArchivedAccounts[0].Current
			})
		})
	}
}
