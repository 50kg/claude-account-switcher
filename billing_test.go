package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBillingFromSavedDesktopSession(t *testing.T) {
	useLanguage(t, "zh-CN")
	s := newSwitcher(t.TempDir())
	a := &Account{UUID: "a", Org: "org", Usage: &Usage{Weekly: 73}}
	s.st.Accounts = []*Account{a}
	dir := s.profileDir(a.UUID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "Cookies"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE cookies(host_key TEXT, name TEXT, path TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER, last_access_utc INTEGER);
		INSERT INTO cookies VALUES('.claude.ai','sessionKey','/','test-session',NULL,0,1),
		('.example.com','sessionKey','/','wrong-host',NULL,0,3),
		('.claude.ai','sessionKey','/','expired',NULL,123,4)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	old := billingClient
	t.Cleanup(func() { billingClient = old })
	body := `{"next_charge_date":"2099-01-05"}`
	owner := "a"
	status := 200
	queries := 0
	billingClient = &http.Client{CheckRedirect: old.CheckRedirect, Transport: usageRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "claude.ai" || req.URL.Scheme != "https" || req.Header.Get("Cookie") != "sessionKey=test-session" || req.Header.Get("Authorization") != "" {
			t.Fatal("incorrect destination or credential")
		}
		w := httptest.NewRecorder()
		switch req.URL.Path {
		case "/api/account":
			w.WriteString(`{"uuid":"` + owner + `","memberships":[{"organization":{"uuid":"org"}}]}`)
		case "/api/organizations/org/subscription_details":
			queries++
			w.Header().Set("Retry-After", "86400")
			w.WriteHeader(status)
			w.WriteString(body)
		default:
			t.Fatal("unexpected billing request")
		}
		return w.Result(), nil
	})}
	s.refreshBilling()
	row := func() string { return makeQuotaMenuView(s.snapshot(), false, time.Now()).Accounts[0].Billing }
	if got := row(); got != "下次续费：2099-01-05" {
		t.Fatal(got)
	}
	s.refreshBilling()
	if queries != 1 {
		t.Fatal("fresh billing was re-queried")
	}
	body = `{"next_charge_at":"2099-02-01T00:00:00Z","plan_ending_before":"2099-01-15"}`
	a.Billing.NextCheck = time.Time{}
	s.refreshBilling()
	if got := row(); got != "会员到期：2099-01-15" {
		t.Fatal(got)
	}
	status = 429
	a.Billing.NextCheck = time.Time{}
	s.refreshBilling()
	if got := row(); got != "上次查询 · 会员到期：2099-01-15" || time.Until(a.Billing.NextCheck) < 23*time.Hour || a.Usage.Weekly != 73 || a.Problem != "" {
		t.Fatal(got)
	}
	saved, err := os.ReadFile(s.statePath())
	if err != nil || strings.Contains(string(saved), "test-session") {
		t.Fatal("invalid billing persistence")
	}
	s = newSwitcher(s.dir)
	a = s.st.Accounts[0]
	if !strings.HasPrefix(row(), "上次查询") {
		t.Fatal("stale marker lost on restart")
	}
	owner = "different"
	a.Billing.NextCheck = time.Time{}
	s.refreshBilling()
	if queries != 3 {
		t.Fatal("queried billing using another account's session")
	}
	owner, status, body = "a", 200, `{"expiresAt":99999999,"resets_at":"2099-02-28"}`
	a.Billing.NextCheck = time.Time{}
	s.refreshBilling()
	if got := row(); got != "暂无会员日期" {
		t.Fatal(got)
	}
	body = `{"status":"active","next_charge_date":null,"plan_ending_at":null,"subscription_details_url":"https://apps.apple.com/account/subscriptions"}`
	a.Billing.NextCheck = time.Time{}
	s.refreshBilling()
	if got := row(); got != "Apple 订阅 · 日期请在 Apple 订阅中查看" {
		t.Fatal(got)
	}
}
