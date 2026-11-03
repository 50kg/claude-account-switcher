package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestQuotaMenuPreservesEmailResetAndModelAllowance(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		t.Run(lang, func(t *testing.T) {
			useLanguage(t, lang)
			now := time.Date(2026, 11, 3, 14, 20, 0, 0, time.Local)
			allowance := 50.0
			usage := &Usage{Session: 67, Weekly: 67, CheckedAt: now, SessionReset: now.Add(time.Hour), WeeklyReset: now.AddDate(0, 0, 2), ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 100, ResetsAt: now.AddDate(0, 0, 2), AllowancePercent: &allowance}}}
			s := snapshot{Active: "current", Accounts: []Account{{UUID: "other", Name: "Other"}, {UUID: "current", Name: "Riley", Email: "riley@example.com", Plan: "Max", Usage: usage}}}
			v := makeQuotaMenuView(s, false, now)
			if v.Accounts[0].ID != "current" || v.Accounts[0].Email != "riley@example.com" || v.Accounts[0].Plan != "Max" {
				t.Fatalf("lost current identity: %+v", v.Accounts[0])
			}
			q := v.Accounts[0].Quotas
			if len(q) != 3 || q[0].Used == nil || *q[0].Used != 67 || !strings.Contains(q[0].Reset, "15:20") || !strings.Contains(q[0].values(), "33%") {
				t.Fatalf("lost session fields: %+v", q)
			}
			if q[2].Used == nil || *q[2].Used != 100 || q[2].Allowance == nil || *q[2].Allowance != 50 || !strings.Contains(q[2].allowanceText(), "50%") || q[2].Notice != tr("Limit reached", "已用尽") {
				t.Fatalf("lost independent model cap: %+v", q[2])
			}
			if q[1].Notice != "" || !strings.Contains(q[1].values(), "33%") {
				t.Fatal("model exhaustion incorrectly exhausted total week")
			}
			*q[2].Used = 1
			*q[2].Allowance = 1
			if usage.ModelWeekly[0].Percent != 100 || allowance != 50 {
				t.Fatal("menu changed cached quota data")
			}
		})
	}
}

func TestQuotaMenuUnknownFailedStaleAndExpiredAreNotFreshBalances(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Now()
	view := func(a Account) quotaMenuAccount {
		return makeQuotaMenuView(snapshot{Accounts: []Account{a}}, false, now).Accounts[0]
	}
	a := Account{}
	r := view(a)
	if len(r.Quotas) != 2 || r.Problem != "等待用量更新" || r.Quotas[0].Used != nil || !strings.Contains(r.Quotas[0].values(), "—") || r.Quotas[0].Reset != "重置时间未知" {
		t.Fatalf("missing data was invented: %+v", r)
	}
	a.Usage = &Usage{Session: 42, Weekly: 45, CheckedAt: now, SessionReset: now, ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 100}}}
	r = view(a)
	if !r.Quotas[0].Stale || !strings.Contains(r.Quotas[0].Reset, "待更新") || !strings.Contains(r.Quotas[0].values(), "42%") || r.Quotas[0].Notice != "" {
		t.Fatalf("expired window became an invented fresh balance: %+v", r.Quotas[0])
	}
	if a.Usage.Session != 42 {
		t.Fatal("menu modified persisted usage")
	}
	a.Problem = usageLimitedMessage
	r = view(a)
	if !strings.Contains(r.Problem, "查询受限") || strings.Contains(r.Problem, "失效") || r.Quotas[2].Notice != "" || !r.Quotas[2].Stale {
		t.Fatalf("rate limit became sign-out or current exhaustion: %+v", r)
	}
	a.Problem = ""
	a.Usage.CheckedAt = now.Add(-idleFresh - time.Second)
	r = view(a)
	if !r.Stale || !strings.Contains(r.Quotas[2].values(), "上次") || r.Quotas[2].Notice != "" {
		t.Fatal("old data looked current")
	}
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		a.Usage.Session = value
		if view(a).Quotas[0].Used != nil {
			t.Fatalf("invalid percentage %v shown as real", value)
		}
	}
}

func TestQuotaMenuResetTimesIncludeDayAndUnknown(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Date(2026, 12, 31, 23, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{{time.Time{}, "未知"}, {now, "待更新"}, {now.Add(30 * time.Minute), "今天 23:30"}, {now.Add(2 * time.Hour), "明天 01:00"}, {now.AddDate(0, 0, 8), "1月8日"}} {
		if got := quotaResetText(tc.at, now); !strings.Contains(got, tc.want) {
			t.Fatalf("reset = %q, want %q", got, tc.want)
		}
	}
}

func TestQuotaMenuActionsRejectUnconfirmedStaleAndUnknownIntent(t *testing.T) {
	s := snapshot{Active: "a", Accounts: []Account{{UUID: "a"}, {UUID: "b"}}}
	for _, a := range []quotaMenuAction{{Kind: "switch", Account: "b", Active: "a"}, {Kind: "switch", Account: "b", Active: "old", Confirmed: true}, {Kind: "switch", Account: "a", Active: "a", Confirmed: true}, {Kind: "switch", Account: "missing", Active: "a", Confirmed: true}, {Kind: "add", Active: "a"}, {Kind: "import", Active: "a"}, {Kind: "threshold", Threshold: 1}, {Kind: "arbitrary"}} {
		if validateQuotaMenuAction(a, s) == nil {
			t.Fatalf("accepted unsafe action %+v", a)
		}
	}
	a := quotaMenuAction{Kind: "switch", Account: "b", Active: "a", Confirmed: true}
	if err := validateQuotaMenuAction(a, s); err != nil {
		t.Fatal(err)
	}
	s.Busy = true
	if validateQuotaMenuAction(a, s) == nil {
		t.Fatal("accepted switching while busy")
	}
}

func TestQuotaMenuPlacementStaysInsideNegativeAndScaledWorkAreas(t *testing.T) {
	for _, work := range []quotaRect{{0, 0, 1920, 1040}, {-2560, -200, 2560, 1400}, {0, 0, 800, 500}} {
		for _, point := range [][2]int{{work.X, work.Y}, {work.X + work.W, work.Y + work.H}, {work.X + work.W/2, work.Y + work.H/2}} {
			for _, scale := range []int{1, 2, 3} {
				r := placeQuotaMenu(work, point[0], point[1], 520*scale, 800*scale)
				if r.X < work.X || r.Y < work.Y || r.X+r.W > work.X+work.W || r.Y+r.H > work.Y+work.H {
					t.Fatalf("menu escaped work area: %+v in %+v", r, work)
				}
			}
		}
	}
}
