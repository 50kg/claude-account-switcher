package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Only presentation data crosses the panel pipe. Never serialize State,
// encrypted caches, credentials, organization IDs or filesystem paths here.
type panelView struct {
	Language    string         `json:"language"`
	Accounts    []panelAccount `json:"accounts"`
	Active      string         `json:"active"`
	Busy        bool           `json:"busy"`
	Status      string         `json:"status"`
	Note        string         `json:"note"`
	AutoSwitch  bool           `json:"autoSwitch"`
	SpendWeekly bool           `json:"spendWeekly"`
	Threshold   float64        `json:"threshold"`
	Autostart   bool           `json:"autostart"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

type panelAccount struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Plan      string       `json:"plan"`
	Current   bool         `json:"current"`
	Fresh     bool         `json:"fresh"`
	Status    string       `json:"status"`
	Tone      string       `json:"tone"`
	Problem   string       `json:"problem"`
	CheckedAt time.Time    `json:"checkedAt"`
	Meters    []panelMeter `json:"meters"`
	Reset     string       `json:"reset"`
}

type panelMeter struct {
	Name      string    `json:"name"`
	Remaining *float64  `json:"remaining"`
	Tone      string    `json:"tone"`
	ResetsAt  time.Time `json:"resetsAt"`
	Allowance *float64  `json:"allowance,omitempty"`
}

func quotaPanelView(s snapshot, autostart bool, now time.Time) panelView {
	v := panelView{Language: language, Active: s.Active, Busy: s.Busy, Status: s.Status, Note: s.Note,
		AutoSwitch: s.AutoSwitch, SpendWeekly: s.SpendWeekly, Threshold: s.Threshold, Autostart: autostart,
		Accounts: []panelAccount{}}
	for _, a := range s.Accounts {
		name := a.Name
		if name == "" {
			name = a.Email
		}
		if name == "" {
			name = tr("Unnamed account", "未命名账号")
		}
		row := panelAccount{ID: a.UUID, Name: name, Plan: a.Plan, Current: a.UUID == s.Active,
			Fresh: freshModelUsage(a, now), Problem: usageProblemText(a, now), Tone: "unknown"}
		row.Status = tr("Waiting for usage", "等待用量更新")
		if a.Usage == nil {
			row.Meters = []panelMeter{{Name: tr("5h", "5小时"), Tone: "unknown"}, {Name: tr("Total week", "总周"), Tone: "unknown"}}
		} else {
			u := a.Usage.current(now)
			row.CheckedAt = u.CheckedAt
			if u.CheckedAt.After(v.UpdatedAt) {
				v.UpdatedAt = u.CheckedAt
			}
			add := func(name string, used float64, at time.Time, allowance *float64) {
				meter := panelMeter{Name: name, ResetsAt: at, Allowance: allowance, Tone: "unknown"}
				if validQuotaPercent(used) {
					remaining := math.Max(0, math.Min(100, 100-used))
					meter.Remaining = &remaining
					if row.Fresh {
						switch {
						case used >= 100:
							meter.Tone = "empty"
						case used >= warnAt:
							meter.Tone = "low"
						default:
							meter.Tone = "healthy"
						}
					}
				}
				row.Meters = append(row.Meters, meter)
			}
			add(tr("5h", "5小时"), u.Session, u.SessionReset, nil)
			add(tr("Total week", "总周"), u.Weekly, u.WeeklyReset, nil)
			for _, model := range u.ModelWeekly {
				add(fmt.Sprintf(tr("%s week", "%s周"), model.Name), model.Percent, model.ResetsAt, model.AllowancePercent)
			}
			if row.Fresh {
				row.Tone, row.Status = "healthy", tr("Room available", "余量充足")
			}
			// A scoped cap must name its scope, never declare the account signed out.
			for _, tone := range []string{"empty", "low"} {
				for _, meter := range row.Meters {
					if meter.Tone == tone && row.Tone != "empty" {
						row.Tone = tone
						if tone == "empty" {
							row.Status = fmt.Sprintf(tr("%s limit reached", "%s已用尽"), meter.Name)
						} else {
							row.Status = fmt.Sprintf(tr("%s running low", "%s余量偏低"), meter.Name)
						}
						row.Reset = panelReset(meter.Name, meter.ResetsAt, now)
						break
					}
				}
			}
			if row.Reset == "" {
				row.Reset = panelReset(tr("Total week", "总周"), u.WeeklyReset, now)
			}
			if !row.Fresh {
				row.Status = tr("Usage needs updating", "用量待更新")
			}
		}
		if row.Problem != "" {
			row.Status = tr("Usage not updated", "用量暂未更新")
		}
		v.Accounts = append(v.Accounts, row)
	}
	sort.SliceStable(v.Accounts, func(i, j int) bool { return v.Accounts[i].Current && !v.Accounts[j].Current })
	return v
}

func panelReset(name string, at, now time.Time) string {
	if at.IsZero() || !at.After(now) {
		return ""
	}
	if wait := at.Sub(now); wait < 24*time.Hour {
		minutes := int(math.Ceil(wait.Minutes()))
		if minutes < 60 {
			return fmt.Sprintf(tr("%s · resets in %d min", "%s · %d分钟后重置"), name, minutes)
		}
		return fmt.Sprintf(tr("%s · resets in %dh %dm", "%s · %d小时%d分后重置"), name, minutes/60, minutes%60)
	}
	return name + tr(" · resets ", " · ") + resetText(at, now) + tr("", "重置")
}

func compactRemainingText(a Account, now time.Time) string {
	if a.Problem != "" || a.Usage == nil {
		if problem := usageProblemText(a, now); problem != "" {
			return problem
		}
		return tr("Waiting for usage", "等待用量更新")
	}
	u := a.Usage.current(now)
	parts := []string{fmt.Sprintf(tr("5h left %.0f%%", "5小时余%.0f%%"), math.Max(0, 100-u.Session)),
		fmt.Sprintf(tr("week left %.0f%%", "总周余%.0f%%"), math.Max(0, 100-u.Weekly))}
	for _, m := range u.ModelWeekly {
		parts = append(parts, fmt.Sprintf(tr("%s left %.0f%%", "%s余%.0f%%"), m.Name, math.Max(0, 100-m.Percent)))
	}
	return strings.Join(parts, " · ")
}
