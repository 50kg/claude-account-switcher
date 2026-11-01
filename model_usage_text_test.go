package main

import (
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
				tr("up to 50% of total week", "上限为总周额度的50%"),
				resetText(now.Add(48*time.Hour), now),
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("display %q is missing %q", text, want)
				}
			}
			if notice := modelWeeklyNotice(a, now); !strings.HasPrefix(notice, "⚠ ") || !strings.Contains(notice, "82%") {
				t.Fatalf("warning not visible in dedicated notice: %q", notice)
			}
			if tip := accountTooltip(a, 96, now); !strings.Contains(tip, tr("near limit", "接近上限")) || strings.Contains(tip, resetText(now.Add(48*time.Hour), now)) {
				t.Fatalf("compact tooltip lost the warning: %q", tip)
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
