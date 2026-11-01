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

// Windows bounds the tray tooltip. Put model warnings before long reset dates
// and cap descriptions, which remain fully available in the account menu.
func accountTooltip(a Account, threshold float64, now time.Time) string {
	if a.Usage == nil || len(a.Usage.ModelWeekly) == 0 {
		return a.label() + "\n" + usageText(a, threshold, now)
	}
	u := a.Usage.current(now)
	parts := []string{fmt.Sprintf(tr("5h %.0f%% · total week %.0f%%", "5小时 %.0f%% · 总周 %.0f%%"), u.Session, u.Weekly)}
	for _, model := range u.ModelWeekly {
		model.ResetsAt = time.Time{}
		model.AllowancePercent = nil
		parts = append(parts, modelWeeklyText(model, freshModelUsage(a, now), now))
	}
	detail := strings.Join(parts, " · ")
	if problem := usageProblemText(a, now); problem != "" {
		detail = problem + tr(" · last known: ", " · 上次用量：") + detail
	}
	return a.label() + "\n" + detail
}
