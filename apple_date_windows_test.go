package main

import (
	"testing"
	"time"
)

func TestAppleDateStaysWithSelectedAccount(t *testing.T) {
	useLanguage(t, "zh-CN")
	s := newSwitcher(t.TempDir())
	first := &Account{UUID: "first", Org: "one", Billing: &billingInfo{Provider: "apple"}}
	second := &Account{UUID: "second", Org: "two", Billing: &billingInfo{Provider: "apple"}}
	s.st.Accounts = []*Account{first, second}
	store := func(action, date string) error {
		return saveAppleNote(s, *second, appleDateResult{action, billingNote{Date: date, Ending: true}})
	}
	if store("save", "2028-02-30") == nil {
		t.Fatal("invalid date accepted")
	}
	if err := store("save", "2028-02-29"); err != nil {
		t.Fatal(err)
	}
	s = newSwitcher(s.dir)
	first, second = s.account("first"), s.account("second")
	now := time.Date(2028, 2, 28, 12, 0, 0, 0, time.Local)
	if first.AppleDate != nil || second.billingText(now) != "Apple · 手动记录 · 会员到期：2028-02-29" {
		t.Fatal("manual date lost or attached to wrong account")
	}
	if err := store("cancel", ""); err != nil || second.AppleDate == nil {
		t.Fatal("cancel changed the note")
	}
	if got := second.billingText(now.AddDate(0, 0, 2)); got != "Apple · 上次手动记录 · 会员到期：2028-02-29" {
		t.Fatal(got)
	}
	if err := store("clear", ""); err != nil || newSwitcher(s.dir).account("second").AppleDate != nil {
		t.Fatal("clear failed")
	}
}
