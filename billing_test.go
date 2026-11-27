package main

import (
	"context"
	"database/sql"
	"errors"
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
		if req.URL.Host != "api.anthropic.com" || req.URL.Scheme != "https" || req.Header.Get("Cookie") != "sessionKey=test-session" || req.Header.Get("Authorization") != "" {
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
	// The calendar day wins over a timestamp that may fall on another local day.
	body = `{"next_charge_at":"2099-03-05T23:30:00-05:00","next_charge_date":"2099-03-05"}`
	a.Billing.NextCheck = time.Time{}
	s.refreshBilling()
	if got := row(); got != "下次续费：2099-03-05" {
		t.Fatal(got)
	}
	body = `{"status":"active","next_charge_date":null,"plan_ending_at":null,"subscription_details_url":"https://apps.apple.com/account/subscriptions"}`
	a.Billing.NextCheck = time.Time{}
	s.refreshBilling()
	if got := row(); got != "Apple 订阅 · 日期请在 Apple 订阅中查看" {
		t.Fatal(got)
	}
}

// The active account's live cookie store is locked while Claude runs. Without
// a saved copy, the menu says so instead of reporting a generic failure, and
// the next start retries it at once.
func TestBillingSessionLockedWhileClaudeRuns(t *testing.T) {
	useLanguage(t, "zh-CN")
	// Keep the test away from a real Claude cookie store on this machine.
	empty := t.TempDir()
	for _, v := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(v, empty)
	}
	s := newSwitcher(t.TempDir())
	a := Account{UUID: "a", Org: "org"}
	if _, err := s.webSessionFor(context.Background(), a, "a"); !errors.Is(err, errBillingSessionBusy) {
		t.Fatalf("active account without a saved copy: %v", err)
	}
	if _, err := s.webSessionFor(context.Background(), a, "other"); !errors.Is(err, errBillingSession) || errors.Is(err, errBillingSessionBusy) {
		t.Fatalf("inactive account without a saved copy: %v", err)
	}
	b := &billingInfo{Failed: true, Reason: "busy", NextCheck: time.Now().Add(time.Hour)}
	if got := b.text(time.Now()); got != "会员日期：切换账号或导入聊天后读取" {
		t.Fatal(got)
	}
	limited := &billingInfo{Failed: true, Reason: "limited", NextCheck: time.Now().Add(time.Hour)}
	s.st.Accounts = []*Account{{UUID: "a", Org: "org", Billing: b}, {UUID: "b", Org: "org", Billing: limited}}
	s.mu.Lock()
	s.saveState()
	s.mu.Unlock()
	s = newSwitcher(s.dir)
	if !s.account("a").Billing.NextCheck.IsZero() || s.account("b").Billing.NextCheck.IsZero() {
		t.Fatal("start must retry a failed read but keep a rate limit")
	}
}

// An account never switched away from has no saved sign-in, but the
// switcher's own backup from its last restart holds its session.
func TestBillingSessionFromSwitcherBackup(t *testing.T) {
	empty := t.TempDir()
	for _, v := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(v, empty)
	}
	s := newSwitcher(t.TempDir())
	backup := func(name, account, session string) {
		dir := filepath.Join(s.backupsDir(), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "config.json"), `{"lastKnownAccountUuid":"`+account+`","oauth:tokenCacheV2":"x"}`)
		writeTestSession(t, filepath.Join(dir, "Cookies"), session)
	}
	backup("20991231-000000", "other", "other-session") // newest, another account
	backup("20990102-000000", "a", "newer-session")
	backup("20990101-000000", "a", "older-session")
	a := Account{UUID: "a", Org: "org"}
	if key, err := s.webSessionFor(context.Background(), a, "a"); err != nil || key != "newer-session" {
		t.Fatalf("session = %q, %v", key, err)
	}
	// A saved sign-in, once there, wins over any backup.
	if err := os.MkdirAll(s.profileDir("a"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestSession(t, filepath.Join(s.profileDir("a"), "Cookies"), "saved-session")
	if key, err := s.webSessionFor(context.Background(), a, "a"); err != nil || key != "saved-session" {
		t.Fatalf("session = %q, %v", key, err)
	}
}

func writeTestSession(t *testing.T, path, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cookies(host_key TEXT, name TEXT, path TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER, last_access_utc INTEGER);
		INSERT INTO cookies VALUES('.claude.ai','sessionKey','/',?,NULL,0,1)`, value); err != nil {
		t.Fatal(err)
	}
}
