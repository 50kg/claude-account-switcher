package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const fableLimitFixture = `{"kind":"weekly_scoped","group":"weekly","percent":82,"severity":"warning","resets_at":"2026-11-03T20:00:00+01:00","scope":{"model":{"id":null,"display_name":"Fable"},"surface":null},"is_active":true}`

func fetchModelUsageFixture(t *testing.T, body string) *Usage {
	t.Helper()
	old := client
	t.Cleanup(func() { client = old })
	calls := 0
	client = &http.Client{Transport: usageRoundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.String() != apiBase+"/api/oauth/usage" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		w := httptest.NewRecorder()
		w.WriteString(body)
		return w.Result(), nil
	})}
	u, err := fetchUsage(context.Background(), "synthetic-model-usage-token")
	if err != nil || u == nil {
		t.Fatalf("fetchUsage = %+v, %v", u, err)
	}
	if calls != 1 {
		t.Fatalf("model usage made %d requests, want one shared request", calls)
	}
	if u.Session != 42 || u.Weekly != 45 {
		t.Fatalf("base usage changed: %+v", u)
	}
	return u
}

func TestModelWeeklyUsageUsesScopeAndResponseIndex(t *testing.T) {
	for _, index := range []int{0, 2} {
		t.Run(string(rune('0'+index)), func(t *testing.T) {
			limits := []json.RawMessage{
				json.RawMessage(`{"kind":"weekly_scoped","percent":99,"scope":{"surface":"claude_code"}}`),
				json.RawMessage(`{"kind":"weekly","percent":45}`),
			}
			limits = append(limits[:index], append([]json.RawMessage{json.RawMessage(fableLimitFixture)}, limits[index:]...)...)
			// Bad, inactive, surface-specific and incomplete entries must not
			// hide the real model cap or break total usage for this account.
			for _, bad := range []string{
				`{"kind":"weekly_scoped","percent":99,"is_active":false,"scope":{"model":{"display_name":"Inactive"}}}`,
				`{"kind":"weekly_scoped","scope":{"model":{"display_name":"Missing"}}}`,
				`{"kind":"weekly_scoped","percent":null,"scope":{"model":{"display_name":"Null"}}}`,
				`{"kind":"weekly_scoped","percent":"bad","scope":{"model":{"display_name":"Malformed"}}}`,
				`{"kind":"weekly_scoped","percent":99,"scope":{"model":{"display_name":"Surface only"},"surface":"cowork"}}`,
			} {
				limits = append(limits, json.RawMessage(bad))
			}
			body, err := json.Marshal(map[string]any{
				"five_hour": map[string]any{"utilization": 42},
				"seven_day": map[string]any{"utilization": 45},
				"limits":    limits,
				"weekly_scoped_shares": []any{
					map[string]any{"limit_index": index, "used_percent_of_weekly": 41, "allowance_percent_of_weekly": 50},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			u := fetchModelUsageFixture(t, string(body))
			if len(u.ModelWeekly) != 1 {
				t.Fatalf("model limits = %+v", u.ModelWeekly)
			}
			m := u.ModelWeekly[0]
			reset, _ := time.Parse(time.RFC3339, "2026-11-03T20:00:00+01:00")
			if m.Name != "Fable" || m.Percent != 82 || !m.ResetsAt.Equal(reset) || m.AllowancePercent == nil || *m.AllowancePercent != 50 {
				t.Fatalf("model cap = %+v", m)
			}
		})
	}
}

func TestOptionalModelMetadataCannotBreakBaseUsage(t *testing.T) {
	for _, metadata := range []string{
		``, `,"limits":null`, `,"limits":"unknown format"`,
		`,"limits":[{"kind":"weekly_scoped","percent":82,"scope":"unknown format"}]`,
	} {
		t.Run(metadata, func(t *testing.T) {
			u := fetchModelUsageFixture(t, `{"five_hour":{"utilization":42},"seven_day":{"utilization":45}`+metadata+`}`)
			if len(u.ModelWeekly) != 0 {
				t.Fatalf("invented model usage from absent/invalid data: %+v", u.ModelWeekly)
			}
		})
	}
	// The authoritative percentage is in limits, not a rounded calculation
	// from shares. Invalid share metadata only omits the allowance description.
	for _, shares := range []string{`null`, `"unknown"`, `[{"limit_index":"bad","allowance_percent_of_weekly":50}]`} {
		u := fetchModelUsageFixture(t, `{"five_hour":{"utilization":42},"seven_day":{"utilization":45},"limits":[`+fableLimitFixture+`],"weekly_scoped_shares":`+shares+`}`)
		if len(u.ModelWeekly) != 1 || u.ModelWeekly[0].Percent != 82 || u.ModelWeekly[0].AllowancePercent != nil {
			t.Fatalf("bad shares changed the model percentage: %+v", u.ModelWeekly)
		}
	}
	u := fetchModelUsageFixture(t, `{"five_hour":{"utilization":42},"seven_day":{"utilization":45},"limits":[{"kind":"weekly_scoped","percent":0,"scope":{"model":{"id":"model-zero"}}}]}`)
	if len(u.ModelWeekly) != 1 || u.ModelWeekly[0].Name != "model-zero" || u.ModelWeekly[0].Percent != 0 {
		t.Fatalf("a genuine zero was treated as missing: %+v", u.ModelWeekly)
	}
}

func TestModelResetAndPersistenceAreIndependent(t *testing.T) {
	now := time.Now()
	u := Usage{Session: 42, Weekly: 45, WeeklyReset: now.Add(time.Hour), CheckedAt: now,
		ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 82, ResetsAt: now}},
	}
	current := u.current(now)
	if current.ModelWeekly[0].Percent != 0 || current.Weekly != 45 || current.Session != 42 || u.ModelWeekly[0].Percent != 82 {
		t.Fatalf("reset mutated saved data or unrelated windows: current=%+v saved=%+v", current, u)
	}
	s := newSwitcher(t.TempDir())
	s.st.Accounts = []*Account{{UUID: "synthetic-account", Usage: &u}}
	s.saveState()
	reloaded := newSwitcher(s.dir)
	if models := reloaded.st.Accounts[0].Usage.ModelWeekly; len(models) != 1 || models[0].Name != "Fable" || models[0].Percent != 82 {
		t.Fatalf("model usage did not survive restart: %+v", models)
	}
	var legacy Usage
	if err := json.Unmarshal([]byte(`{"session":42,"weekly":45}`), &legacy); err != nil || len(legacy.ModelWeekly) != 0 {
		t.Fatalf("legacy usage no longer loads: %+v, %v", legacy, err)
	}
}

func TestModelCapsDoNotChangeAutomaticSwitching(t *testing.T) {
	now := time.Now()
	s := &switcher{st: State{AutoSwitch: true, Threshold: 96}, active: "a"}
	cur := &Account{UUID: "a", Usage: &Usage{Session: 42, Weekly: 45, CheckedAt: now,
		ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 100}},
	}}
	roomy := &Account{UUID: "b", Usage: &Usage{Session: 10, Weekly: 20, CheckedAt: now,
		WeeklyReset: now.Add(time.Hour), ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 100}},
	}}
	other := &Account{UUID: "c", Usage: &Usage{Session: 30, Weekly: 40, CheckedAt: now}}
	s.st.Accounts = []*Account{cur, roomy, other}
	if got, _ := s.autoTarget(true, now.Add(-time.Hour), now); got != nil {
		t.Fatal("model-only exhaustion triggered automatic switching")
	}
	cur.Usage.Session = 99
	if got, why := s.autoTarget(true, now.Add(-time.Hour), now); got != roomy || why != nearlyFull {
		t.Fatal("model cap changed automatic candidate ranking")
	}
	cur.Usage.Session, cur.Usage.WeeklyReset = 42, now.Add(5*24*time.Hour)
	s.st.AutoSwitch, s.st.SpendWeekly = false, true
	if got, why := s.autoTarget(true, now.Add(-time.Hour), now); got != roomy || why != spendWeekly {
		t.Fatal("model cap changed expiring-weekly switching")
	}
}
