package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// ModelWeeklyUsage is a cap within the total weekly allowance, not an extra
// allowance. An absent slice means the endpoint did not provide model caps.
type ModelWeeklyUsage struct {
	ModelID          string    `json:"modelId,omitempty"`
	Name             string    `json:"name"`
	Percent          float64   `json:"percent"`
	ResetsAt         time.Time `json:"resetsAt"`
	AllowancePercent *float64  `json:"allowancePercent,omitempty"`
}

// Optional limit metadata is decoded separately: malformed model entries must
// not turn valid five-hour/total-weekly usage into a failed account query.
func modelWeeklyUsage(limitsJSON, sharesJSON json.RawMessage) []ModelWeeklyUsage {
	var limits []json.RawMessage
	if json.Unmarshal(limitsJSON, &limits) != nil {
		return nil
	}
	var shares []struct {
		Index     *int     `json:"limit_index"`
		Allowance *float64 `json:"allowance_percent_of_weekly"`
	}
	if json.Unmarshal(sharesJSON, &shares) != nil {
		shares = nil
	}
	var models []ModelWeeklyUsage
	for index, raw := range limits {
		var limit struct {
			Kind     string   `json:"kind"`
			Group    string   `json:"group"`
			Percent  *float64 `json:"percent"`
			ResetsAt string   `json:"resets_at"`
			Active   *bool    `json:"is_active"`
			Scope    struct {
				Model *struct {
					ID   string `json:"id"`
					Name string `json:"display_name"`
				} `json:"model"`
				Surface json.RawMessage `json:"surface"`
			} `json:"scope"`
		}
		if json.Unmarshal(raw, &limit) != nil || limit.Kind != "weekly_scoped" ||
			(limit.Group != "" && limit.Group != "weekly") || limit.Scope.Model == nil ||
			(limit.Active != nil && !*limit.Active) || limit.Percent == nil || !validQuotaPercent(*limit.Percent) {
			continue
		}
		// Surface-only or combined surface/model restrictions are not a cap on
		// this model everywhere. Do not label them as a general model allowance.
		if surface := strings.TrimSpace(string(limit.Scope.Surface)); surface != "" && surface != "null" {
			continue
		}
		name := strings.Join(strings.Fields(limit.Scope.Model.Name), " ")
		id := strings.TrimSpace(limit.Scope.Model.ID)
		if name == "" {
			name = strings.Join(strings.Fields(id), " ")
		}
		if name == "" {
			continue
		}
		model := ModelWeeklyUsage{ModelID: id, Name: name, Percent: *limit.Percent}
		model.ResetsAt, _ = time.Parse(time.RFC3339Nano, limit.ResetsAt)
		for _, share := range shares {
			if share.Index != nil && *share.Index == index && share.Allowance != nil &&
				*share.Allowance > 0 && validQuotaPercent(*share.Allowance) {
				allowance := *share.Allowance
				model.AllowancePercent = &allowance
				break
			}
		}
		models = append(models, model)
	}
	return models
}

func validQuotaPercent(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Display-only warnings deliberately do not participate in Usage.peak,
// account eligibility, polling cadence or either automatic switching policy.
func modelWarning(percent float64) string {
	switch {
	case percent >= 100:
		return tr("limit reached", "已用尽")
	case percent >= warnAt:
		return tr("near limit", "接近上限")
	default:
		return ""
	}
}

func freshModelUsage(a Account, now time.Time) bool {
	return a.Usage != nil && a.Problem == "" && !a.Usage.CheckedAt.IsZero() &&
		now.Sub(a.Usage.CheckedAt) <= idleFresh
}

func modelWeeklyText(model ModelWeeklyUsage, warning bool, now time.Time) string {
	text := fmt.Sprintf(tr("%s week %.0f%%", "%s周 %.0f%%"), model.Name, model.Percent)
	if warning {
		if hint := modelWarning(model.Percent); hint != "" {
			text += tr(" (", "（") + hint + tr(")", "）")
		}
	}
	if !model.ResetsAt.IsZero() && model.ResetsAt.After(now) {
		text += tr(", resets ", "，重置于 ") + resetText(model.ResetsAt, now)
	}
	if model.AllowancePercent != nil {
		text += fmt.Sprintf(tr(" (up to %.0f%% of total week)", "（上限为总周额度的%.0f%%）"), *model.AllowancePercent)
	}
	return text
}

func modelWeeklyNotice(a Account, now time.Time) string {
	if !freshModelUsage(a, now) {
		return ""
	}
	var notices []string
	for _, model := range a.Usage.current(now).ModelWeekly {
		if modelWarning(model.Percent) != "" {
			notices = append(notices, modelWeeklyText(model, true, now))
		}
	}
	if len(notices) == 0 {
		return ""
	}
	return "⚠ " + strings.Join(notices, " · ")
}

// Keep remaining allowance readable in Windows' bounded tray tooltip.
// This formatter only uses the cached account snapshot, without a window.
func accountTooltip(a Account, _ float64, now time.Time) string {
	fresh := freshModelUsage(a, now)
	parts := []string{tr("5h —", "5小时 —"), tr("Total week —", "总周 —")}
	var empty, low string
	if a.Usage != nil {
		parts = nil
		add := func(name string, used float64) {
			parts = append(parts, name+" "+remainingPercentText(used))
			if !fresh || !validQuotaPercent(used) {
				return
			}
			if used >= 100 && empty == "" {
				empty = name
			} else if used >= warnAt && low == "" {
				low = name
			}
		}
		u := a.Usage.current(now)
		add(tr("5h", "5小时"), u.Session)
		add(tr("Total week", "总周"), u.Weekly)
		for _, model := range u.ModelWeekly {
			add(fmt.Sprintf(tr("%s week", "%s周"), model.Name), model.Percent)
		}
	}
	prefix := tr("Remaining: ", "剩余：")
	if !fresh {
		prefix = tr("Last known remaining: ", "上次剩余：")
	}
	tip := a.label() + "\n" + prefix + strings.Join(parts, " · ")
	if empty != "" {
		tip += "\n" + fmt.Sprintf(tr("%s limit reached", "%s已用尽"), empty)
	} else if low != "" {
		tip += "\n" + fmt.Sprintf(tr("%s running low", "%s余量偏低"), low)
	}
	if problem := usageProblemText(a, now); problem != "" {
		tip += "\n" + problem
	}
	return tip
}

func remainingPercentText(used float64) string {
	if !validQuotaPercent(used) {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", math.Max(0, 100-used))
}

func compactRemainingText(a Account, now time.Time) string {
	if problem := usageProblemText(a, now); problem != "" {
		return problem
	}
	if a.Usage == nil {
		return tr("Waiting for usage", "等待用量更新")
	}
	u := a.Usage.current(now)
	parts := []string{fmt.Sprintf(tr("5h left %s", "5小时余%s"), remainingPercentText(u.Session)),
		fmt.Sprintf(tr("week left %s", "总周余%s"), remainingPercentText(u.Weekly))}
	for _, model := range u.ModelWeekly {
		parts = append(parts, fmt.Sprintf(tr("%s left %s", "%s余%s"), model.Name, remainingPercentText(model.Percent)))
	}
	text := strings.Join(parts, " · ")
	if !freshModelUsage(a, now) {
		return tr("Last known: ", "上次：") + text
	}
	return text
}
