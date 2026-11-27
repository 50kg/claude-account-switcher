package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func useLanguage(t *testing.T, value string) {
	t.Helper()
	old := currentLanguage()
	t.Cleanup(func() { _ = setLanguage(old) })
	if err := setLanguage(value); err != nil {
		t.Fatal(err)
	}
}

// Tests start in English; only Settings or ui.json changes the language.
func TestDefaultLanguage(t *testing.T) {
	if currentLanguage() != langEnglish || tr("English", "中文") != "English" {
		t.Fatalf("tests must start in English, got %q", currentLanguage())
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
	if setLanguage("unsupported") == nil || currentLanguage() != langEnglish {
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
			if got, want := usageText(a, 96, now), tr(usageUnverifiedMessage, "用量授权待验证"); got != want {
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

func TestStartLanguage(t *testing.T) {
	useLanguage(t, "en")
	// A new install starts in the Windows language and keeps it, even once
	// its own state.json exists.
	dir := t.TempDir()
	want := systemLanguage()
	if got := startLanguage(dir); got != want {
		t.Fatalf("new install = %q, want %q", got, want)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := startLanguage(dir); got != want {
		t.Fatalf("second start = %q, want %q", got, want)
	}
	// A data folder from before 1.0 has state.json but no saved language.
	old := t.TempDir()
	if err := os.WriteFile(filepath.Join(old, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := startLanguage(old); got != langChinese {
		t.Fatalf("upgrade from a Chinese build = %q", got)
	}
	if err := saveLanguagePreference(filepath.Join(old, "ui.json"), langEnglish); err != nil {
		t.Fatal(err)
	}
	if got := startLanguage(old); got != langEnglish {
		t.Fatalf("saved choice ignored: %q", got)
	}
}
