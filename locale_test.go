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
			for _, stored := range []string{signedOutMessage, "登录已失效，请重新添加账号"} {
				a := Account{Problem: stored}
				if got, want := usageText(a, 96, now), tr(signedOutMessage, "登录已失效，请重新添加账号"); got != want {
					t.Fatalf("persisted problem = %q, want %q", got, want)
				}
			}
		})
	}
}
