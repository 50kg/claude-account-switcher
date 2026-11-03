package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAccountRegistrationOnlyNotifiesWithoutRestartOrImport(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		for _, mode := range []string{"new-account", "initial-discovery", "known-account-login"} {
			t.Run(lang+"/"+mode, func(t *testing.T) {
				useLanguage(t, lang)
				now := time.Now()
				s := newSwitcher(t.TempDir())
				s.active = "synthetic-new"
				s.st.Accounts = []*Account{{UUID: "synthetic-new", Usage: &Usage{Session: 100, Weekly: 50, CheckedAt: now}}, {UUID: "synthetic-other", Usage: &Usage{Session: 10, Weekly: 10, CheckedAt: now}}}
				s.st.AutoSwitch = true
				s.st.Threshold = 96
				if mode != "initial-discovery" {
					s.addingAt = now.Add(-time.Minute)
					s.status = tr("Sign in with the other account in Claude", "请在 Claude 中登录另一个账号")
				}
				changed := 0
				s.onChange = func() { changed++ }
				label, action := s.afterUsageCheck(mode != "known-account-login", true, now.Add(-time.Hour), now)
				if label != "" || action != nil {
					t.Fatalf("registration scheduled a restart: %q", label)
				}
				if !s.addingAt.IsZero() || changed != 1 || s.snapshot().Status != accountAddedNotice() {
					t.Fatalf("registration did not publish manual-import notice: %+v", s.snapshot())
				}
				if !strings.Contains(s.status, tr("Import chats from all accounts", "导入所有账号的聊天")) {
					t.Fatal("notice lost the menu command")
				}
				if runtime.GOOS == "windows" && !strings.Contains(s.status, tr("Settings →", "设置 →")) {
					t.Fatal("notice does not point to the combined Settings menu")
				}
				view := makeQuotaMenuView(s.snapshot(), false, now)
				if view.Status != accountAddedNotice() {
					t.Fatal("native menu cannot display completion notice")
				}
				if s.busy.Load() || !s.st.AutoSwitch || len(s.st.Accounts) != 2 {
					t.Fatal("registration changed unrelated state")
				}
				entries, err := os.ReadDir(s.dir)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.Name() != "state.json" {
						t.Fatalf("registration wrote unexpected data: %s", entry.Name())
					}
				}
				if len(newSwitcher(s.dir).st.Accounts) != 2 {
					t.Fatal("account registration was not saved")
				}
				// The completion poll is quiet; the user's existing auto policy
				// remains in force on a later ordinary poll.
				label, action = s.afterUsageCheck(false, true, now.Add(-time.Hour), now.Add(time.Minute))
				if label == "" || action == nil {
					t.Fatal("ordinary auto-switch policy was disabled")
				}
			})
		}
	}
}

func TestAccountRegistrationWaitsForKnownIdentityAndTimesOut(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Now()
	s := newSwitcher(t.TempDir())
	s.addingAt = now.Add(-time.Minute)
	s.status = "请在 Claude 中登录另一个账号"
	for _, active := range []string{"", "unverified-new"} {
		s.active = active
		_, action := s.afterUsageCheck(false, true, time.Time{}, now)
		if action != nil || s.addingAt.IsZero() || s.status == accountAddedNotice() {
			t.Fatal("unfinished login was marked added")
		}
	}
	s.afterUsageCheck(false, true, time.Time{}, now.Add(16*time.Minute))
	if !s.addingAt.IsZero() || s.status != "" {
		t.Fatal("abandoned add flow did not time out")
	}
}
