package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestQuotaPanelUsesRemainingAndNamesScopedExhaustion(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Now()
	account := Account{UUID: "synthetic-a", Name: "Account Alpha", Org: "private-org", Email: "private@example.invalid",
		Usage: &Usage{Session: 42, Weekly: 45, CheckedAt: now, ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 82, ResetsAt: now.Add(time.Hour)}}}}
	s := snapshot{Active: account.UUID, Accounts: []Account{{UUID: "b", Name: "Account Beta"}, account}, Threshold: 96}
	v := quotaPanelView(s, false, now)
	if len(v.Accounts) != 2 || !v.Accounts[0].Current || v.Accounts[0].Name != account.Name {
		t.Fatalf("current account not first: %+v", v)
	}
	row := v.Accounts[0]
	for i, expected := range []float64{58, 55, 18} {
		if row.Meters[i].Remaining == nil || *row.Meters[i].Remaining != expected {
			t.Fatalf("remaining meter %d = %+v", i, row.Meters[i])
		}
	}
	if row.Status != "Fable周余量偏低" || row.Tone != "low" {
		t.Fatalf("wrong scoped warning: %+v", row)
	}
	account.Usage.ModelWeekly[0].Percent = 100
	s.Accounts[1] = account
	row = quotaPanelView(s, false, now).Accounts[0]
	if row.Status != "Fable周已用尽" || *row.Meters[0].Remaining != 58 {
		t.Fatalf("model limit became account-wide exhaustion: %+v", row)
	}
	encoded, _ := json.Marshal(v)
	if strings.Contains(string(encoded), "private-org") || strings.Contains(string(encoded), "private@example") || strings.Contains(string(encoded), "usageCredentials") {
		t.Fatal("private state escaped into the presentation payload")
	}
}

func TestQuotaPanelUnknownAndCachedAreNotFreshZeroOrSignOut(t *testing.T) {
	useLanguage(t, "zh-CN")
	now := time.Now()
	unknown := Account{UUID: "a"}
	v := quotaPanelView(snapshot{Accounts: []Account{unknown}}, false, now)
	if len(v.Accounts[0].Meters) != 2 || v.Accounts[0].Meters[0].Remaining != nil || v.Accounts[0].Tone != "unknown" {
		t.Fatal("missing data was invented as a percentage")
	}
	unknown.Usage = &Usage{Session: 42, Weekly: 45, CheckedAt: now, ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 100}}}
	unknown.Problem = usageLimitedMessage
	row := quotaPanelView(snapshot{Accounts: []Account{unknown}}, false, now).Accounts[0]
	if row.Fresh || row.Tone != "unknown" || row.Status != "用量暂未更新" || !strings.Contains(row.Problem, "用量查询受限") {
		t.Fatalf("misleading failed query: %+v", row)
	}
	for _, meter := range row.Meters {
		if meter.Tone != "unknown" {
			t.Fatal("failed query still produced a fresh warning")
		}
	}
	unknown.Problem = ""
	unknown.Usage.CheckedAt = now.Add(-idleFresh - time.Second)
	row = quotaPanelView(snapshot{Accounts: []Account{unknown}}, false, now).Accounts[0]
	if row.Fresh || row.Status != "用量待更新" {
		t.Fatal("old usage was shown as current")
	}
	unknown.Usage.CheckedAt = now
	unknown.Usage.Session = 100
	row = quotaPanelView(snapshot{Accounts: []Account{unknown}}, false, now).Accounts[0]
	if row.Status != "5小时已用尽" || *row.Meters[0].Remaining != 0 {
		t.Fatal("real exhaustion was not identified")
	}
}

func TestQuotaPanelRejectsUnconfirmedStaleAndUnknownActions(t *testing.T) {
	s := snapshot{Active: "a", Accounts: []Account{{UUID: "a"}, {UUID: "b"}}}
	for _, a := range []panelAction{
		{Type: "switch", Account: "b", Active: "a"},
		{Type: "switch", Account: "unknown", Active: "a", Confirmed: true},
		{Type: "switch", Account: "b", Active: "old", Confirmed: true},
		{Type: "switch", Account: "a", Active: "a", Confirmed: true},
		{Type: "add", Active: "a"}, {Type: "import", Active: "a"},
		{Type: "threshold", Threshold: 1}, {Type: "exec"},
	} {
		if validatePanelAction(a, s) == nil {
			t.Fatalf("unsafe action allowed: %+v", a)
		}
	}
	a := panelAction{Type: "switch", Account: "b", Active: "a", Confirmed: true}
	if err := validatePanelAction(a, s); err != nil {
		t.Fatal(err)
	}
	s.Busy = true
	if validatePanelAction(a, s) == nil {
		t.Fatal("allowed a second operation while busy")
	}
}

func TestQuotaPanelHTMLNeverInterpolatesAccountMarkup(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		v := panelView{Language: lang, Accounts: []panelAccount{{Name: `</script><img src=x onerror=alert(1)>`}}}
		page, err := quotaPanelHTML(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(page, v.Accounts[0].Name) || strings.Contains(page, "/*PANEL_") {
			t.Fatal("unsafe or incomplete page template")
		}
		if !strings.Contains(page, "connect-src 'none'") || !strings.Contains(page, `"language":"`+lang+`"`) {
			t.Fatal("missing CSP or effective language")
		}
	}
}

func TestQuotaPanelUpdateWithoutWindowDoesNotReadOrLaunch(t *testing.T) {
	reads := 0
	p := &quotaPanel{view: func() panelView { reads++; return panelView{} }}
	p.update()
	p.close()
	p.show("")
	if reads != 0 || p.session != nil {
		t.Fatal("an unopened or closed panel accessed data or started a child")
	}
}

func TestQuotaPanelCoalescesDataWithoutDroppingIntent(t *testing.T) {
	p := &quotaPanel{}
	s := &panelSession{updates: make(chan panelMessage, 1)}
	p.enqueue(s, panelMessage{Show: true, Focus: "synthetic-b", RequestID: 7, Error: "synthetic rejection"})
	v := panelView{Active: "a"}
	p.enqueue(s, panelMessage{View: &v})
	got := <-s.updates
	if !got.Show || got.Focus != "synthetic-b" || got.RequestID != 7 || got.Error != "synthetic rejection" || got.View != &v {
		t.Fatalf("lost an action result: %+v", got)
	}
}
