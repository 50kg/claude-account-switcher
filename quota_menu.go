package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The native menu consumes presentation data only. Opening it never discovers
// Claude, polls usage, loads credentials or reads a session directory.
type quotaMenu interface {
	show()
	update()
	close()
}

type quotaMenuView struct {
	Accounts                   []quotaMenuAccount
	Active, Status, Update     string
	SortMode                   string
	Busy, Auto, Spend, Startup bool
	Threshold                  float64
	RefreshMinutes             int
	Syncing                    bool
	Updated                    time.Time
}

type quotaMenuAccount struct {
	ID, Name, Email, Plan, Problem string
	Billing                        string
	AppleSubscription              bool
	Current, Stale                 bool
	Quotas                         []quotaMenuReading
}

type quotaMenuReading struct {
	Name, Reset string
	Used        *float64
	Stale       bool
	ResetAt     time.Time
}

func makeQuotaMenuView(s snapshot, startup bool, now time.Time) quotaMenuView {
	v := quotaMenuView{Active: s.Active, Status: s.Status, Update: s.Update,
		Busy: s.Busy || s.Syncing, Syncing: s.Syncing, RefreshMinutes: s.RefreshMinutes, Auto: s.AutoSwitch, Spend: s.SpendWeekly, Startup: startup, Threshold: s.Threshold}
	if v.Status == "" {
		v.Status = s.Note
	}
	// The header owns in-flight feedback; a previous completion must not
	// contradict it or be repeated beneath it.
	if v.Syncing && isUsageSyncStatus(v.Status) {
		v.Status = ""
	}
	for _, a := range s.Accounts {
		row := quotaMenuAccount{ID: a.UUID, Name: menuSingleLine(a.Name), Email: menuSingleLine(a.Email), Plan: menuSingleLine(a.Plan),
			Current: a.UUID == s.Active, Problem: usageProblemText(a, now), Stale: !freshModelUsage(a, now), Billing: a.billingText(now), AppleSubscription: a.Billing != nil && a.Billing.Provider == "apple"}
		if row.Name == "" {
			row.Name = tr("Account", "账号")
		}
		switch row.Plan {
		case "Team":
			row.Plan = tr("Team", "团队版")
		case "Enterprise":
			row.Plan = tr("Enterprise", "企业版")
		}
		add := func(name string, used *float64, at time.Time) {
			q := quotaMenuReading{Name: name, Reset: quotaResetText(at, now), ResetAt: at, Stale: row.Stale || (!at.IsZero() && !at.After(now))}
			if used != nil && validQuotaPercent(*used) {
				value := *used
				q.Used = &value

			}
			row.Quotas = append(row.Quotas, q)
		}
		session, weekly := tr("Current session · 5h", "当前会话 · 5小时"), tr("Total week", "本周总额度")
		if a.Usage == nil {
			if row.Problem == "" {
				row.Problem = tr("Waiting for usage", "等待用量更新")
			}
			add(session, nil, time.Time{})
			add(weekly, nil, time.Time{})
		} else {
			// Keep the last observed values after an unconfirmed reset. The menu
			// says 'reset due, waiting' instead of inventing a fresh 100% balance.
			u := a.Usage
			if u.CheckedAt.After(v.Updated) {
				v.Updated = u.CheckedAt
			}
			add(session, &u.Session, u.SessionReset)
			add(weekly, &u.Weekly, u.WeeklyReset)
			for _, model := range u.ModelWeekly {
				add(fmt.Sprintf(tr("%s week", "%s 周额度"), model.Name), &model.Percent, model.ResetsAt)
			}
		}
		v.Accounts = append(v.Accounts, row)
	}
	sort.SliceStable(v.Accounts, func(i, j int) bool { return v.Accounts[i].Current && !v.Accounts[j].Current })
	return v
}

func quotaResetText(at, now time.Time) string {
	if at.IsZero() {
		return tr("Reset time unknown", "重置时间未知")
	}
	if !at.After(now) {
		return tr("Reset due, waiting", "重置已到，待更新")
	}
	at, now = at.Local(), now.Local()
	day := resetText(at, now)
	if at.Year() == now.Year() && at.YearDay() == now.YearDay() {
		day = tr("Today ", "今天 ") + at.Format("15:04")
	} else if tomorrow := now.AddDate(0, 0, 1); at.Year() == tomorrow.Year() && at.YearDay() == tomorrow.YearDay() {
		day = tr("Tomorrow ", "明天 ") + at.Format("15:04")
	}
	return day + tr(" reset", " 重置")
}

func (q quotaMenuReading) values() string {
	if q.Used == nil {
		return tr("Used —", "已用 —")
	}
	prefix := tr("Used", "已用")
	if q.Stale {
		prefix = tr("Last used", "上次已用")
	}
	return fmt.Sprintf("%s %.0f%%", prefix, *q.Used)
}

type quotaMenuAction struct {
	Kind, Account, Active string
	SortMode              string
	AccountOrder          []string
	Enabled, Confirmed    bool
	Minutes               int
	Threshold             float64
}

func validateQuotaMenuAction(a quotaMenuAction, s snapshot) error {
	reject := func() error {
		return errors.New(tr("Menu data changed; open the menu again", "菜单状态已变化，请重新打开后操作"))
	}
	switch a.Kind {
	case "switch", "add", "import":
		if !a.Confirmed || s.Busy || s.Syncing || a.Active != s.Active {
			return reject()
		}
		if a.Kind == "switch" {
			if a.Account == "" || a.Account == s.Active {
				return reject()
			}
			for _, account := range s.Accounts {
				if account.UUID == a.Account {
					return nil
				}
			}
			return reject()
		}
	case "threshold":
		for _, threshold := range thresholds {
			if a.Threshold == threshold {
				return nil
			}
		}
		return reject()
	case "interval":
		if !validRefreshMinutes(a.Minutes) {
			return reject()
		}
	case "sort":
		if !validQuotaMenuSortMode(a.SortMode) {
			return reject()
		}
	case "order":
		if len(a.AccountOrder) > 8192 {
			return reject()
		}
		known := make(map[string]bool, len(s.Accounts))
		for _, account := range s.Accounts {
			known[account.UUID] = true
		}
		seen := make(map[string]bool, len(a.AccountOrder))
		for _, id := range a.AccountOrder {
			if id == "" || !known[id] || seen[id] {
				return reject()
			}
			seen[id] = true
		}
	case "account-import":
		if s.Busy || s.Syncing {
			return reject()
		}
	case "account-export", "billing-web":
		if s.Busy || s.Syncing || a.Account == "" {
			return reject()
		}
		for _, account := range s.Accounts {
			if account.UUID == a.Account {
				return nil
			}
		}
		return reject()
	case "sync":
		if s.Busy || s.Syncing || len(s.Accounts) == 0 {
			return reject()
		}
	case "auto", "spend", "autostart", "folder", "update", "quit":
	default:
		return reject()
	}
	return nil
}

func applyQuotaMenuPreferences(accounts []quotaMenuAccount, preferences quotaMenuPreferences, now time.Time) []quotaMenuAccount {
	ordered := append([]quotaMenuAccount(nil), accounts...)
	if preferences.SortMode == "reset" {
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].Current != ordered[j].Current {
				return ordered[i].Current
			}
			a, b := nextQuotaReset(ordered[i], now), nextQuotaReset(ordered[j], now)
			if a.IsZero() != b.IsZero() {
				return !a.IsZero()
			}
			return !a.IsZero() && a.Before(b)
		})
		return ordered
	}
	if len(preferences.AccountOrder) == 0 {
		return ordered
	}
	rank := make(map[string]int, len(preferences.AccountOrder))
	for i, id := range preferences.AccountOrder {
		rank[id] = i
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		ri, iok := rank[ordered[i].ID]
		rj, jok := rank[ordered[j].ID]
		if iok != jok {
			return iok
		}
		return iok && ri < rj
	})
	return ordered
}

func nextQuotaReset(account quotaMenuAccount, now time.Time) time.Time {
	var next time.Time
	for _, quota := range account.Quotas {
		if quota.ResetAt.After(now) && (next.IsZero() || quota.ResetAt.Before(next)) {
			next = quota.ResetAt
		}
	}
	return next
}

// Both drawing and input use these same logical rectangles.
type quotaRect struct{ X, Y, W, H int }

func (r quotaRect) contains(x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

func placeQuotaMenu(work quotaRect, anchorX, anchorY, width, height int) quotaRect {
	width, height = min(width, max(1, work.W-16)), min(height, max(1, work.H-16))
	x, y := anchorX-width+20, anchorY-height-8
	x = max(work.X+8, min(x, work.X+work.W-width-8))
	y = max(work.Y+8, min(y, work.Y+work.H-height-8))
	return quotaRect{x, y, width, height}
}

// Shared layout values keep the painter, scrolling and hit targets in step.
const (
	quotaMenuHeaderHeight    = 46
	quotaMenuFooterHeight    = 74
	quotaMenuMaxBodyHeight   = 560
	quotaAccountHeaderHeight = 44
	quotaBillingHeight       = 21
	quotaReadingHeight       = 34
)

func quotaAccountHeight(a quotaMenuAccount) int {
	height := quotaAccountHeaderHeight + 4
	if a.Billing != "" {
		height += quotaBillingHeight
	}
	if a.Problem != "" || a.Stale {
		height += 26
	}
	height += len(a.Quotas) * quotaReadingHeight
	return height
}

func menuSingleLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\x00", "")), " ")
}
