package main

import (
	"errors"
	"fmt"
	"math"
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
	Busy, Auto, Spend, Startup bool
	Threshold                  float64
	Updated                    time.Time
}

type quotaMenuAccount struct {
	ID, Name, Email, Plan, Problem string
	Current, Stale                 bool
	Quotas                         []quotaMenuReading
}

type quotaMenuReading struct {
	Name, Reset, Notice string
	Used, Allowance     *float64
	Stale               bool
}

func makeQuotaMenuView(s snapshot, startup bool, now time.Time) quotaMenuView {
	v := quotaMenuView{Active: s.Active, Status: s.Status, Update: s.Update,
		Busy: s.Busy, Auto: s.AutoSwitch, Spend: s.SpendWeekly, Startup: startup, Threshold: s.Threshold}
	if v.Status == "" {
		v.Status = s.Note
	}
	for _, a := range s.Accounts {
		row := quotaMenuAccount{ID: a.UUID, Name: menuSingleLine(a.Name), Email: menuSingleLine(a.Email), Plan: menuSingleLine(a.Plan),
			Current: a.UUID == s.Active, Problem: usageProblemText(a, now), Stale: !freshModelUsage(a, now)}
		if row.Name == "" {
			row.Name = tr("Account", "账号")
		}
		switch row.Plan {
		case "Team":
			row.Plan = tr("Team", "团队版")
		case "Enterprise":
			row.Plan = tr("Enterprise", "企业版")
		}
		add := func(name string, used *float64, at time.Time, allowance *float64) {
			q := quotaMenuReading{Name: name, Reset: quotaResetText(at, now), Stale: row.Stale || (!at.IsZero() && !at.After(now))}
			if used != nil && validQuotaPercent(*used) {
				value := *used
				q.Used = &value
				if !q.Stale {
					switch {
					case value >= 100:
						q.Notice = tr("Limit reached", "已用尽")
					case value >= warnAt:
						q.Notice = tr("Low", "偏低")
					}
				}
			}
			if allowance != nil && *allowance > 0 && *allowance <= 100 && validQuotaPercent(*allowance) {
				value := *allowance
				q.Allowance = &value
			}
			row.Quotas = append(row.Quotas, q)
		}
		session, weekly := tr("Current session · 5h", "当前会话 · 5小时"), tr("Total week", "本周总额度")
		if a.Usage == nil {
			if row.Problem == "" {
				row.Problem = tr("Waiting for usage", "等待用量更新")
			}
			add(session, nil, time.Time{}, nil)
			add(weekly, nil, time.Time{}, nil)
		} else {
			// Keep the last observed values after an unconfirmed reset. The menu
			// says 'reset due, waiting' instead of inventing a fresh 100% balance.
			u := a.Usage
			if u.CheckedAt.After(v.Updated) {
				v.Updated = u.CheckedAt
			}
			add(session, &u.Session, u.SessionReset, nil)
			add(weekly, &u.Weekly, u.WeeklyReset, nil)
			for _, model := range u.ModelWeekly {
				add(fmt.Sprintf(tr("%s week", "%s 周额度"), model.Name), &model.Percent, model.ResetsAt, model.AllowancePercent)
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
		return tr("Used —   Left —", "已用 —   剩余 —")
	}
	prefix := tr("Used", "已用")
	if q.Stale {
		prefix = tr("Last used", "上次已用")
	}
	return fmt.Sprintf(tr("%s %.0f%%   Left %.0f%%", "%s %.0f%%   剩余 %.0f%%"), prefix, *q.Used, math.Max(0, 100-*q.Used))
}

func (q quotaMenuReading) allowanceText() string {
	if q.Allowance == nil {
		return ""
	}
	return fmt.Sprintf(tr("Up to %.0f%% of the total week", "上限为总周额度的 %.0f%%"), *q.Allowance)
}

type quotaMenuAction struct {
	Kind, Account, Active string
	Enabled, Confirmed    bool
	Threshold             float64
}

func validateQuotaMenuAction(a quotaMenuAction, s snapshot) error {
	reject := func() error {
		return errors.New(tr("Menu data changed; open the menu again", "菜单状态已变化，请重新打开后操作"))
	}
	switch a.Kind {
	case "switch", "add", "import":
		if !a.Confirmed || s.Busy || a.Active != s.Active {
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
	case "auto", "spend", "autostart", "folder", "update", "quit":
	default:
		return reject()
	}
	return nil
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

func quotaAccountHeight(a quotaMenuAccount, collapsed bool) int {
	if collapsed {
		return 54
	}
	height := 56 + 16
	if a.Problem != "" || a.Stale {
		height += 26
	}
	for i, q := range a.Quotas {
		height += 31
		if i > 0 {
			height += 11
		}
		if q.Allowance != nil {
			height += 25
		}
	}
	return height
}

func menuSingleLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\x00", "")), " ")
}
