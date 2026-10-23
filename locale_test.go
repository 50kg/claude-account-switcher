package main

import (
	"testing"
	"time"
)

func useLanguage(t *testing.T, value string) {
	t.Helper()
	old := language
	t.Cleanup(func() { language = old })
	if err := setLanguage(value); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultLanguage(t *testing.T) {
	if language != defaultLanguage {
		t.Fatalf("initial language %q does not match build default %q", language, defaultLanguage)
	}
	want := "English"
	if defaultLanguage == "zh-CN" {
		want = "中文"
	}
	if got := tr("English", "中文"); got != want {
		t.Fatalf("build default selected %q, want %q", got, want)
	}
}

func TestLanguageSelection(t *testing.T) {
	for _, value := range []string{"zh", "zh-CN", "zh_cn", "zh-Hans"} {
		t.Run(value, func(t *testing.T) {
			useLanguage(t, value)
			if tr("Add account", "添加账号") != "添加账号" {
				t.Fatal("Chinese language alias was not selected")
			}
		})
	}
	useLanguage(t, "en-US")
	if tr("Add account", "添加账号") != "Add account" {
		t.Fatal("English was not restored")
	}
	if setLanguage("unsupported") == nil || language != "en" {
		t.Fatal("invalid language must fail without changing the selection")
	}
}

func TestUsageTextBothLanguages(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	a := Account{Usage: &Usage{Session: 97, SessionReset: now.Add(time.Hour), Weekly: 97, WeeklyReset: now.Add(48 * time.Hour)}}
	for _, tc := range []struct{ lang, want string }{
		{"en", "5h 97% until 13:00 · week 97% until Thu 12:00"},
		{"zh-CN", "5小时 97%，重置于 13:00 · 每周 97%，重置于 周四 12:00"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			useLanguage(t, tc.lang)
			if got := usageText(a, 96, now); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			a := Account{Problem: signedOutMessage}
			if got, want := usageText(a, 96, now), tr(signedOutMessage, "登录已失效，请重新添加账号"); got != want {
				t.Fatalf("persisted problem = %q, want %q", got, want)
			}
		})
	}
}

func TestUsageTextShowsWeeklyResetChinese(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	a := Account{Usage: &Usage{Session: 20, Weekly: 40, WeeklyReset: now.Add(2 * 24 * time.Hour)}}
	if got, want := usageText(a, 96, now), "5小时 20% · 每周 40%，重置于 周四 12:00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	a.Usage.Weekly = 97
	if got, want := usageText(a, 96, now), "5小时 20% · 每周 97%，重置于 周四 12:00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	a.Usage.Weekly, a.Usage.WeeklyReset = 0, now.Add(6*24*time.Hour+time.Hour)
	if got, want := usageText(a, 96, now), "5小时 20% · 每周 0%，重置于 10月12日 周一 13:00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAutoNoteChinese(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.Local)
	s := &switcher{}
	s.st.Accounts = []*Account{{UUID: "a", Name: "Pepe", Plan: "Pro"}}
	s.st.LastSwitch = now.Add(-6 * time.Hour)
	if got := s.autoNote(now); got != "" {
		t.Fatalf("note after a switch by hand: %q", got)
	}
	s.st.LastCause, s.st.LastFrom = nearlyFull, "a"
	if got, want := s.autoNote(now), "已于 03:00 自动切换，Pepe (Pro) 的额度接近用尽"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	s.st.LastCause, s.st.SpendUntil = spendWeekly, now.Add(9*time.Hour)
	if got, want := s.autoNote(now), "已于 03:00 自动切换，以使用此账号将在 18:00 重置的周额度"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	s.st.LastSwitch = now.Add(-noteFor - time.Minute)
	if got := s.autoNote(now); got != "" {
		t.Fatalf("note still shown after %s: %q", noteFor, got)
	}
}
