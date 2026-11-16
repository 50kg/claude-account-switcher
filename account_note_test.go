package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAccountNotePersistsClearsAndPreservesAccountData(t *testing.T) {
	s := newSwitcher(t.TempDir())
	u := &Usage{Session: 25, CheckedAt: time.Now()}
	s.st.Accounts = []*Account{{UUID: "active", Name: "Original", Email: "original@example.com", Org: "org", Plan: "Ultra", Usage: u}, {UUID: "archived", Archived: true}}
	for _, id := range []string{"active", "archived"} {
		if err := s.setAccountNote(id, "  工作主账号 🚀  "); err != nil {
			t.Fatal(err)
		}
		if newSwitcher(s.dir).account(id).Note != "工作主账号 🚀" {
			t.Fatal("note did not persist")
		}
	}
	a := s.account("active")
	if a.Name != "Original" || a.Email != "original@example.com" || a.Org != "org" || a.Plan != "Ultra" || a.Usage != u {
		t.Fatal("note changed account identity or usage")
	}
	v := makeQuotaMenuView(s.snapshot(), false, time.Now())
	if v.Accounts[0].Note != "工作主账号 🚀" || v.ArchivedAccounts[0].Note != "工作主账号 🚀" {
		t.Fatal("notes missing from normal or archived list")
	}
	for _, bad := range []string{strings.Repeat("长", accountNoteMaxLength+1), "line\nbreak", "nul\x00text", string([]byte{0xff})} {
		if s.setAccountNote("active", bad) == nil || s.account("active").Note != "工作主账号 🚀" {
			t.Fatal("invalid note changed the saved note")
		}
	}
	if s.setAccountNote("missing", "note") == nil {
		t.Fatal("unknown account accepted a note")
	}
	if err := s.setAccountNote("active", "   "); err != nil || newSwitcher(s.dir).account("active").Note != "" {
		t.Fatal("blank note did not clear saved value")
	}
}

func TestAccountNoteSaveFailureRetainsPreviousValue(t *testing.T) {
	s := newSwitcher(t.TempDir())
	s.st.Accounts = []*Account{{UUID: "account", Note: "before"}}
	s.saveState()
	blocker := s.statePath() + ".switcher-tmp"
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.setAccountNote("account", "after"); err == nil || s.account("account").Note != "before" || newSwitcher(s.dir).account("account").Note != "before" {
		t.Fatal("failed save changed the note")
	}
}
