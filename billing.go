package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const billingInterval = 6 * time.Hour
const appleSubscriptionsURL = "https://apps.apple.com/account/subscriptions"

type billingInfo struct {
	Date      string    `json:"date,omitempty"` // calendar date or timestamp, as supplied
	Ending    bool      `json:"ending,omitempty"`
	NextCheck time.Time `json:"nextCheck"`
	Failed    bool      `json:"failed,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Provider  string    `json:"provider,omitempty"`
}

type billingNote struct {
	Date   string `json:"date"`
	Ending bool   `json:"ending,omitempty"`
}

func (a Account) billingText(now time.Time) string {
	if a.Billing != nil && a.Billing.Provider == "apple" && a.Billing.Date == "" {
		if n := a.AppleDate; n != nil && billingDay(n.Date) != "" {
			label := tr("Next renewal: ", "下次续费：")
			if n.Ending {
				label = tr("Membership ends: ", "会员到期：")
			}
			if n.Date < now.Local().Format(time.DateOnly) {
				return tr("Apple · past manual date · ", "Apple · 上次手动记录 · ") + label + n.Date
			}
			return tr("Apple · manual · ", "Apple · 手动记录 · ") + label + n.Date
		}
		return tr("Apple subscription · check date in Apple Subscriptions", "Apple 订阅 · 日期请在 Apple 订阅中查看")
	}
	return a.Billing.text(now)
}

func billingDay(value string) string {
	if t, err := time.Parse(time.DateOnly, value); err == nil {
		return t.Format(time.DateOnly)
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Local().Format(time.DateOnly)
	}
	return ""
}

func (b *billingInfo) text(now time.Time) string {
	if b == nil {
		return tr("Subscription date pending", "会员日期待查询")
	}
	day := billingDay(b.Date)
	if day == "" {
		if b.Failed {
			switch b.Reason {
			case "web":
				return tr("Billing needs web verification", "会员日期需网页验证")
			case "limited":
				return tr("Billing rate limited · retry later", "会员日期查询受限，稍后重试")
			case "session":
				return tr("Billing session unavailable", "无法读取账单登录会话")
			case "access":
				return tr("Billing access denied", "无法访问此账号的账单")
			default:
				return tr("Billing query failed", "会员日期查询失败")
			}
		}
		return tr("Subscription date unavailable", "暂无会员日期")
	}
	label := tr("Next renewal: ", "下次续费：")
	if b.Ending {
		label = tr("Membership ends: ", "会员到期：")
	}
	if b.Failed || !now.Before(b.NextCheck) || day < now.Local().Format(time.DateOnly) {
		label = tr("Last known · ", "上次查询 · ") + label
	}
	return label + day
}

// Only the desktop's claude.ai sessionKey is needed. Open the live or saved
// database read-only; never copy the decrypted cookie into the profile.
func billingSessionKey(ctx context.Context, path string) (string, error) {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows drive letter in a file URI
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return "", err
	}
	defer db.Close()
	var host, value string
	var encrypted []byte
	const chromeEpochMicros = int64(11644473600000000)
	err = db.QueryRowContext(ctx, `SELECT host_key, value, encrypted_value FROM cookies
		WHERE host_key IN ('claude.ai','.claude.ai') AND name='sessionKey' AND path='/'
		AND (expires_utc=0 OR expires_utc>?) ORDER BY last_access_utc DESC LIMIT 1`,
		time.Now().UnixMicro()+chromeEpochMicros).Scan(&host, &value, &encrypted)
	if err != nil {
		return "", err
	}
	if len(encrypted) != 0 {
		plain, err := decryptValue(encrypted)
		if err != nil {
			return "", err
		}
		defer clear(plain)
		// Chromium v24+ prepends the SHA-256 of the cookie domain.
		sum := sha256.Sum256([]byte(host))
		value = string(bytes.TrimPrefix(plain, sum[:]))
	}
	c := http.Cookie{Name: "sessionKey", Value: value}
	if value == "" || c.Valid() != nil {
		return "", errBillingUnavailable
	}
	return value, nil
}

var errBillingUnavailable = errors.New("subscription details unavailable")
var errBillingWeb = errors.New("web verification required")
var errBillingAccess = errors.New("billing access denied")
var errBillingSession = errors.New("billing session unavailable")

// No redirect may carry a desktop web session away from the intended origin.
var billingClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func billingGet(ctx context.Context, key, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://claude.ai/api/"+path, nil)
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: "sessionKey", Value: key})
	req.Header.Set("Accept", "application/json")
	res, err := billingClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.Header.Get("cf-mitigated") == "challenge" {
		return errBillingWeb
	}
	if res.StatusCode == 429 {
		return &rateLimited{after: retryAfter(res.Header.Get("Retry-After"), time.Now())}
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return errBillingAccess
	}
	if res.StatusCode != 200 {
		return errBillingUnavailable
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

func fetchBilling(ctx context.Context, key, account, org string) (billingInfo, error) {
	var identity struct {
		UUID        string
		Memberships []struct{ Organization struct{ UUID string } }
	}
	if err := billingGet(ctx, key, "account", &identity); err != nil {
		return billingInfo{}, err
	}
	member := false
	for _, m := range identity.Memberships {
		if m.Organization.UUID == org {
			member = true
			break
		}
	}
	if account == "" || org == "" || identity.UUID != account || !member {
		return billingInfo{}, errBillingUnavailable
	}
	var details map[string]json.RawMessage
	if err := billingGet(ctx, key, "organizations/"+url.PathEscape(org)+"/subscription_details", &details); err != nil {
		return billingInfo{}, err
	}
	return parseBillingDetails(details), nil
}

func parseBillingDetails(details map[string]json.RawMessage) billingInfo {
	var b billingInfo
	var managementURL string
	if json.Unmarshal(details["subscription_details_url"], &managementURL) == nil && managementURL == appleSubscriptionsURL {
		b.Provider = "apple"
	}
	for _, field := range []string{"plan_ending_at", "plan_ending_before", "next_charge_at", "next_charge_date"} {
		var value string
		if json.Unmarshal(details[field], &value) == nil && billingDay(value) != "" {
			b.Date, b.Ending = value, strings.HasPrefix(field, "plan_ending")
			return b
		}
	}
	return b
}

func (s *switcher) accountBilling(ctx context.Context, a Account, active string) (billingInfo, error) {
	if !validBillingIdentity(a) {
		return billingInfo{}, errBillingSession
	}
	if s.isAppleSubscription(ctx, a, active) {
		return billingInfo{Provider: "apple"}, nil
	}
	key, err := s.webSessionFor(ctx, a, active)
	if err != nil {
		return billingInfo{}, err
	}
	return fetchBilling(ctx, key, a.UUID, a.Org)
}

func validBillingIdentity(a Account) bool {
	return a.UUID != "." && filepath.IsLocal(a.UUID) && filepath.Base(a.UUID) == a.UUID && a.Org != ""
}

func (s *switcher) isAppleSubscription(ctx context.Context, a Account, active string) bool {
	// App Store agreements have no charge/end date in Claude billing.
	// Ask the existing identity-scoped OAuth profile before opening the web store.
	if creds, err := s.credentialsFor(a.UUID, active); err == nil && len(creds) > 0 {
		p, err := fetchProfile(ctx, creds[0].token)
		if err == nil && p.Account.UUID == a.UUID && p.Organization.UUID == a.Org && p.Organization.BillingType == "apple_subscription" {
			return true
		}
	}
	return false
}

func (s *switcher) webSessionFor(ctx context.Context, a Account, active string) (string, error) {
	if !validBillingIdentity(a) {
		return "", errBillingSession
	}
	var key string
	if a.UUID == active {
		key, _ = billingSessionKey(ctx, s.app().cookieFiles()[0])
	}
	if key == "" { // The live database may be locked while Claude is open.
		var err error
		key, err = billingSessionKey(ctx, filepath.Join(s.profileDir(a.UUID), "Cookies"))
		if err != nil {
			return "", errBillingSession
		}
	}
	return key, nil
}

// Independent of quota polling: a blocked web session must never mark an
// account signed out, delay switching, or change its usage backoff.
func (s *switcher) refreshBilling() {
	if !s.billingSyncing.CompareAndSwap(false, true) {
		return
	}
	defer s.billingSyncing.Store(false)
	snap := s.snapshot()
	for _, a := range snap.Accounts {
		if s.busy.Load() {
			return
		}
		waiting := a.Billing != nil && time.Now().Before(a.Billing.NextCheck)
		legacyFailure := waiting && a.Billing.Failed && a.Billing.Reason == ""
		if waiting && !legacyFailure {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		var b billingInfo
		var err error
		if legacyFailure {
			// Preserve the old billing retry deadline; only ask the OAuth
			// profile for the provider during this one-time upgrade.
			if validBillingIdentity(a) && s.isAppleSubscription(ctx, a, snap.Active) {
				b.Provider = "apple"
			} else {
				b = *a.Billing
				b.Reason = "query"
			}
		} else {
			b, err = s.accountBilling(ctx, a, snap.Active)
		}
		cancel()
		delay := billingInterval
		if err != nil {
			if a.Billing != nil {
				b = *a.Billing
			}
			b.Failed = true
			b.Reason = "query"
			delay = 15 * time.Minute
			var limit *rateLimited
			switch {
			case errors.As(err, &limit):
				b.Reason = "limited"
				delay = max(delay, limit.after)
			case errors.Is(err, errBillingWeb):
				b.Reason = "web"
				delay = billingInterval
			case errors.Is(err, errBillingAccess):
				b.Reason = "access"
			case errors.Is(err, errBillingSession):
				b.Reason = "session"
			}
		}
		if !legacyFailure || !b.Failed {
			b.NextCheck = time.Now().Add(delay)
		}
		s.mu.Lock()
		if dest := s.account(a.UUID); dest != nil && dest.Org == a.Org && dest.Billing == a.Billing {
			dest.Billing = &b
			s.saveState()
		}
		s.mu.Unlock()
		s.onChange()
	}
}
