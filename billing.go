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
	"os"
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
			case "busy":
				return tr("Subscription date: read after a switch or chat import", "会员日期：切换账号或导入聊天后读取")
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

// errBillingSessionBusy: Claude holds its cookie store open exclusively while
// it runs, and the account has neither a saved copy nor a backup yet. The
// next switch or chat import saves one.
var errBillingSessionBusy = errors.New("billing session locked by Claude")

// No redirect may carry a desktop web session away from the intended origin.
var billingClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// billingGet reads Anthropic's own API host with the desktop's web session.
// claude.ai/api puts Cloudflare's browser challenge in front of every
// non-browser client, so the same read-only endpoints are asked on
// api.anthropic.com, which accepts the session and serves them directly.
func billingGet(ctx context.Context, key, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/api/"+path, nil)
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: "sessionKey", Value: key})
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-account-switcher/"+version)
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
	// next_charge_date is the calendar day Claude's billing page shows;
	// next_charge_at can fall on the next local day east of UTC.
	for _, field := range []string{"plan_ending_at", "plan_ending_before", "next_charge_date", "next_charge_at"} {
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
	// The subscription details name an App Store agreement themselves; the
	// OAuth profile only answers that when no web session is readable.
	key, err := s.webSessionFor(ctx, a, active)
	if err != nil {
		if s.isAppleSubscription(ctx, a, active) {
			return billingInfo{Provider: "apple"}, nil
		}
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
	// The live store is the freshest, but Claude locks it while it runs.
	if a.UUID == active {
		if key, err := billingSessionKey(ctx, s.app().cookieFiles()[0]); err == nil {
			return key, nil
		}
	}
	saved := filepath.Join(s.profileDir(a.UUID), "Cookies")
	key, err := billingSessionKey(ctx, saved)
	if err == nil {
		return key, nil
	}
	if _, statErr := os.Stat(saved); !errors.Is(statErr, os.ErrNotExist) {
		return "", errBillingSession // a saved copy without a usable session
	}
	// Never switched away from, so no saved sign-in yet: every restart by the
	// switcher backed up the store of the account signed in at the time.
	if key := s.backupSessionKey(ctx, a.UUID); key != "" {
		return key, nil
	}
	if a.UUID == active {
		return "", errBillingSessionBusy
	}
	return "", errBillingSession
}

// backupSessionKey reads the web session from the newest switcher backup
// taken while uuid was signed in. fetchBilling still checks that the session
// belongs to this account and organization before a date is accepted.
func (s *switcher) backupSessionKey(ctx context.Context, uuid string) string {
	entries, err := os.ReadDir(s.backupsDir())
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- { // timestamp names, oldest first
		if !entries[i].IsDir() {
			continue
		}
		dir := filepath.Join(s.backupsDir(), entries[i].Name())
		cfg, err := readObject(filepath.Join(dir, "config.json"))
		if err != nil {
			continue
		}
		var id string
		if raw, ok := cfg.get("lastKnownAccountUuid"); !ok || json.Unmarshal(raw, &id) != nil || id != uuid {
			continue
		}
		if key, err := billingSessionKey(ctx, filepath.Join(dir, "Cookies")); err == nil {
			return key
		}
	}
	return ""
}

// storeBilling saves b for the account a was read from. Callers hold s.mu.
func (s *switcher) storeBilling(a Account, b billingInfo) bool {
	dest := s.account(a.UUID)
	if dest == nil || dest.Org != a.Org {
		return false
	}
	dest.Billing = &b
	s.saveState()
	return true
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
		if a.Archived && a.UUID != snap.Active {
			continue
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
			case errors.Is(err, errBillingSessionBusy):
				b.Reason = "busy"
			case errors.Is(err, errBillingSession):
				b.Reason = "session"
			}
		}
		if !legacyFailure || !b.Failed {
			b.NextCheck = time.Now().Add(delay)
		}
		s.mu.Lock()
		if dest := s.account(a.UUID); dest != nil && dest.Billing == a.Billing {
			s.storeBilling(a, b)
		}
		s.mu.Unlock()
		s.onChange()
	}
}
