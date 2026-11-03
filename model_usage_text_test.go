package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestModelUsageDisplayAndWarningsBothLanguages(t *testing.T) {
	now := time.Date(2026, 11, 1, 17, 0, 0, 0, time.Local)
	for _, lang := range []string{"en", "zh-CN"} {
		t.Run(lang, func(t *testing.T) {
			useLanguage(t, lang)
			allowance := 50.0
			a := Account{Name: "Synthetic", Usage: &Usage{Session: 42, Weekly: 45, CheckedAt: now,
				ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 82, ResetsAt: now.Add(48 * time.Hour), AllowancePercent: &allowance}},
			}}
			text := usageText(a, 96, now)
			for _, want := range []string{
				tr("total week 45%", "总周 45%"),
				tr("Fable week 82% (near limit)", "Fable周 82%（接近上限）"),
				resetText(now.Add(48*time.Hour), now),
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("display %q is missing %q", text, want)
				}
			}
			if strings.Contains(text, tr("up to 50%", "上限为总周额度的")) {
				t.Fatalf("removed allowance hint is still displayed: %q", text)
			}
			if notice := modelWeeklyNotice(a, now); !strings.HasPrefix(notice, "⚠ ") || !strings.Contains(notice, "82%") {
				t.Fatalf("warning not visible in dedicated notice: %q", notice)
			}
			if tip := accountTooltip(a, 96, now); !strings.Contains(tip, tr("running low", "余量偏低")) || !strings.Contains(tip, "18%") || strings.Contains(tip, resetText(now.Add(48*time.Hour), now)) {
				t.Fatalf("compact tooltip lost the warning: %q", tip)
			}
			if got, want := compactRemainingText(a, now), tr("5h left 58% · week left 55% · Fable left 18%", "5小时余58% · 总周余55% · Fable余18%"); got != want {
				t.Fatalf("compact remaining allowance = %q, want %q", got, want)
			}
			for _, tc := range []struct {
				percent float64
				warning string
			}{
				{0, ""}, {74, ""}, {75, tr("near limit", "接近上限")}, {100, tr("limit reached", "已用尽")},
			} {
				a.Usage.ModelWeekly[0].Percent = tc.percent
				notice := modelWeeklyNotice(a, now)
				if (tc.warning == "" && notice != "") || (tc.warning != "" && !strings.Contains(notice, tc.warning)) {
					t.Fatalf("warning for %.0f%% = %q", tc.percent, notice)
				}
			}
			a.Usage.ModelWeekly[0].Percent = 0
			if text := usageText(a, 96, now); !strings.Contains(text, tr("Fable week 0%", "Fable周 0%")) {
				t.Fatalf("genuine zero hidden: %q", text)
			}
			a.Usage.ModelWeekly = nil
			if text := usageText(a, 96, now); strings.Contains(text, "Fable") || modelWeeklyNotice(a, now) != "" {
				t.Fatalf("invented a model cap for a legacy account: %q", text)
			}
		})
	}
}

func TestRemainingTextDistinguishesUnknownZeroAndInvalid(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Now()
	a := Account{Name: "Synthetic"}
	if got := compactRemainingText(a, now); got != "等待用量更新" {
		t.Fatalf("missing usage = %q", got)
	}
	if tip := accountTooltip(a, 96, now); !strings.Contains(tip, "5小时 — · 总周 —") || strings.Contains(tip, "100%") || strings.Contains(tip, "0%") {
		t.Fatalf("missing usage was shown as a real percentage: %q", tip)
	}
	for _, tc := range []struct {
		used float64
		want string
	}{{0, "100%"}, {42, "58%"}, {100, "0%"}, {120, "0%"}, {-1, "—"}, {math.NaN(), "—"}, {math.Inf(1), "—"}} {
		a.Usage = &Usage{Session: tc.used, Weekly: 45, CheckedAt: now}
		if got := compactRemainingText(a, now); !strings.Contains(got, "5小时余"+tc.want) || strings.Contains(got, "Fable") {
			t.Fatalf("remaining text for %v = %q", tc.used, got)
		}
		if tip := accountTooltip(a, 96, now); !strings.Contains(tip, "5小时 "+tc.want) || strings.Contains(tip, "Fable") {
			t.Fatalf("remaining tooltip for %v = %q", tc.used, tip)
		}
	}
}

func TestRemainingTextKeepsFailedAndStaleUsageSeparateFromLogin(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		t.Run(lang, func(t *testing.T) {
			useLanguage(t, lang)
			now := time.Now()
			a := Account{Name: "Synthetic", Problem: usageLimitedMessage, Usage: &Usage{Session: 42, Weekly: 45, CheckedAt: now,
				ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 100}},
			}}
			if got := compactRemainingText(a, now); !strings.HasPrefix(got, tr("usage check rate limited", "用量查询受限")) {
				t.Fatalf("rate limit lost its explanation: %q", got)
			}
			tip := accountTooltip(a, 96, now)
			if !strings.Contains(tip, tr("Last known remaining: ", "上次剩余：")) || !strings.Contains(tip, "0%") || strings.Contains(tip, tr("limit reached", "已用尽")) || strings.Contains(tip, tr("signed out", "登录失效")) {
				t.Fatalf("rate limit was presented as current exhaustion or sign-out: %q", tip)
			}
			a.Problem = ""
			a.Usage.CheckedAt = now.Add(-idleFresh - time.Second)
			if got := compactRemainingText(a, now); !strings.HasPrefix(got, tr("Last known: ", "上次：")) {
				t.Fatalf("stale menu usage was not labelled: %q", got)
			}
			if tip = accountTooltip(a, 96, now); !strings.Contains(tip, tr("Last known remaining: ", "上次剩余：")) || strings.Contains(tip, tr("limit reached", "已用尽")) {
				t.Fatalf("stale tooltip produced a current alert: %q", tip)
			}
			a.Usage.CheckedAt = now
			a.Usage.Session = 80
			if tip = accountTooltip(a, 96, now); !strings.Contains(tip, tr("Fable week limit reached", "Fable周已用尽")) || strings.Contains(tip, tr("5h running low", "5小时余量偏低")) {
				t.Fatalf("scoped exhaustion did not take precedence: %q", tip)
			}
			a.Usage.ModelWeekly[0].ResetsAt = now
			if got := compactRemainingText(a, now); !strings.Contains(got, tr("Fable left 100%", "Fable余100%")) || a.Usage.ModelWeekly[0].Percent != 100 {
				t.Fatalf("reset display mutated the cached counter: %q", got)
			}
		})
	}
}

func TestModelWarningsDoNotPresentFailedOrStaleDataAsCurrent(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Now()
	a := Account{Problem: usageLimitedMessage, Usage: &Usage{Session: 42, Weekly: 45, CheckedAt: now,
		ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 100, ResetsAt: now.Add(time.Hour)}},
	}}
	if notice := modelWeeklyNotice(a, now); notice != "" {
		t.Fatalf("failed lookup produced a new alert: %q", notice)
	}
	if text := usageText(a, 96, now); !strings.Contains(text, "上次用量") || !strings.Contains(text, "Fable周 100%") || strings.Contains(text, "已用尽") {
		t.Fatalf("failed lookup did not label cached model usage: %q", text)
	}
	a.Problem = ""
	a.Usage.CheckedAt = now.Add(-idleFresh - time.Second)
	if notice := modelWeeklyNotice(a, now); notice != "" {
		t.Fatalf("old model usage produced a new alert: %q", notice)
	}
	a.Usage.CheckedAt = now
	a.Usage.ModelWeekly[0].ResetsAt = now
	if notice := modelWeeklyNotice(a, now); notice != "" {
		t.Fatalf("expired model window produced a new alert: %q", notice)
	}
}
