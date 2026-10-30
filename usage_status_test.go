package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLegacyUsageWarningIsRecheckedWithoutAddingAccount(t *testing.T) {
	now := time.Now()
	s := newSwitcher(t.TempDir())
	s.st.Accounts = []*Account{{UUID: "known", Name: "Example", Problem: signedOutMessage, Usage: &Usage{CheckedAt: now}}}
	s.saveState()
	before, err := os.ReadFile(s.statePath())
	if err != nil {
		t.Fatal(err)
	}
	reloaded := newSwitcher(s.dir)
	if got := reloaded.account("known").Problem; got != usageUnverifiedMessage {
		t.Fatalf("legacy warning = %q, want pending verification", got)
	}
	jobs := reloaded.usageJobs("known", now)
	if len(jobs) != 1 || jobs[0].uuid != "known" || jobs[0].new {
		t.Fatalf("existing active account was not scheduled for a refresh: %+v", jobs)
	}
	if len(reloaded.st.Accounts) != 1 {
		t.Fatal("existing account was added twice")
	}
	after, err := os.ReadFile(s.statePath())
	if err != nil || string(before) != string(after) {
		t.Fatal("loading state rewrote the stored file")
	}
}

func TestUsageRateLimitPersistsAndRecovers(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s := newSwitcher(t.TempDir())
	old := &Usage{Session: 20, Weekly: 40, CheckedAt: now.Add(-time.Minute)}
	s.st.Accounts = []*Account{{UUID: "a", Usage: old, Problem: signedOutMessage}}
	s.recordUsageResult("a", nil, &rateLimited{after: time.Hour}, now)
	a := s.account("a")
	if a.Problem != usageLimitedMessage || !a.UsageRetryAt.Equal(now.Add(time.Hour)) || a.Usage != old {
		t.Fatalf("rate limit did not replace the old warning and preserve usage: %+v", a)
	}
	s.saveState()
	reloaded := newSwitcher(s.dir)
	if jobs := reloaded.usageJobs("a", now.Add(time.Minute)); len(jobs) != 0 {
		t.Fatalf("restart bypassed Retry-After: %+v", jobs)
	}
	if jobs := reloaded.usageJobs("a", now.Add(time.Hour)); len(jobs) != 1 || jobs[0].new {
		t.Fatalf("account was not retried when the deadline elapsed: %+v", jobs)
	}
	reloaded.recordUsageResult("a", nil, errUsageUnauthorized, now.Add(time.Hour))
	if got := reloaded.account("a"); got.Problem != usageUnauthorizedMessage || !got.UsageRetryAt.IsZero() {
		t.Fatalf("HTTP 401 was confused with the previous rate limit: %+v", got)
	}
	reloaded.recordUsageResult("a", nil, &rateLimited{after: 2 * time.Minute}, now.Add(time.Hour))
	if got := reloaded.account("a").Problem; got != usageLimitedMessage {
		t.Fatalf("a newer rate limit kept a stale authentication warning: %q", got)
	}
	fresh := &Usage{Session: 25, Weekly: 45, CheckedAt: now.Add(2 * time.Hour)}
	reloaded.recordUsageResult("a", fresh, nil, fresh.CheckedAt)
	if got := reloaded.account("a"); got.Usage != fresh || got.Problem != "" || !got.UsageRetryAt.IsZero() {
		t.Fatalf("successful check did not clear warnings: %+v", got)
	}
	if len(reloaded.backoff) != 0 || len(reloaded.strikes) != 0 {
		t.Fatal("successful check left a retry delay behind")
	}
}

func TestUsageFailuresHaveDistinctStates(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unauthorized", fmt.Errorf("wrapped: %w", errUsageUnauthorized), usageUnauthorizedMessage},
		{"expired local token", errUsageTokenExpired, usageTokenExpiredMessage},
		{"missing scoped token", errUsageTokenUnavailable, usageTokenMissingMessage},
		{"timeout", context.DeadlineExceeded, usageFailedMessage},
		{"network", errors.New("synthetic network failure"), usageFailedMessage},
		{"wrapped rate limit", fmt.Errorf("wrapped: %w", &rateLimited{after: time.Hour}), usageLimitedMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSwitcher(t.TempDir())
			at := time.Now().Add(-time.Hour)
			old := &Usage{Session: 20, CheckedAt: at}
			s.st.Accounts = []*Account{{UUID: "a", Usage: old, Problem: signedOutMessage}}
			s.recordUsageResult("a", nil, tc.err, time.Now())
			a := s.account("a")
			if a.Problem != tc.want || a.Usage != old || !a.Usage.CheckedAt.Equal(at) {
				t.Fatalf("failed check changed the wrong state or refreshed stale data: %+v", a)
			}
		})
	}
}

func TestUsageBackoffCapsFallbackButHonorsServer(t *testing.T) {
	s := newSwitcher(t.TempDir())
	now := time.Now()
	for range 100 {
		s.recordUsageResult("new", nil, &rateLimited{}, now)
	}
	if got := s.backoff["new"].Sub(now); got != maxBackoff {
		t.Fatalf("fallback = %s, want %s", got, maxBackoff)
	}
	s.recordUsageResult("new", nil, &rateLimited{after: 2 * time.Hour}, now)
	if jobs := s.usageJobs("new", now.Add(time.Hour)); len(jobs) != 0 {
		t.Fatal("a new account bypassed the server's retry deadline")
	}
	if got := s.backoff["new"].Sub(now); got != 2*time.Hour {
		t.Fatalf("server retry = %s, want 2h", got)
	}
}

func TestFailedUsageIsNotUsedForAutoSwitch(t *testing.T) {
	now := time.Now()
	cur := &Account{UUID: "a", Usage: &Usage{Session: 99, CheckedAt: now}}
	other := &Account{UUID: "b", Usage: &Usage{Session: 20, CheckedAt: now}}
	s := &switcher{st: State{Accounts: []*Account{cur, other}, AutoSwitch: true, Threshold: 96}, active: "a"}
	for _, problem := range []string{usageLimitedMessage, usageUnauthorizedMessage, usageFailedMessage, usageUnverifiedMessage} {
		cur.Problem = problem
		if got, _ := s.autoTarget(true, time.Time{}, now); got != nil {
			t.Fatalf("switched using the active account's failed usage check: %q", problem)
		}
	}
	cur.Problem, other.Problem = "", usageLimitedMessage
	if got, _ := s.autoTarget(true, time.Time{}, now); got != nil {
		t.Fatal("selected a rate-limited destination")
	}
	other.Problem = ""
	if got, _ := s.autoTarget(true, time.Time{}, now); got != other {
		t.Fatal("successful checks did not restore normal auto-switch selection")
	}
}

func TestUsageStatusRenderingBothLanguages(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		problem string
		retry   time.Duration
		en, zh  string
	}{
		{signedOutMessage, 0, "usage access needs checking", "用量授权待验证"},
		{usageLimitedMessage, 90 * time.Second, "usage check rate limited, retry in about 2 min", "用量查询受限，约2分钟后重试"},
		{usageLimitedMessage, 45 * time.Second, "usage check rate limited, retry in 45 s", "用量查询受限，45秒后重试"},
		{usageLimitedMessage, -time.Second, "usage check rate limited, waiting to retry", "用量查询受限，等待重试"},
		{usageUnauthorizedMessage, 0, "usage authorization rejected (401); check sign-in in Claude", "用量授权被拒绝（401），请在 Claude 中确认登录"},
		{usageTokenExpiredMessage, 0, "usage token expired; check sign-in in Claude", "用量凭证已过期，请在 Claude 中确认登录"},
		{usageTokenMissingMessage, 0, "usage token unavailable; check sign-in in Claude", "用量凭证暂不可用，请在 Claude 中确认登录"},
		{usageFailedMessage, 0, "usage check temporarily failed, waiting to retry", "用量查询暂时失败，等待重试"},
	} {
		for _, lang := range []string{"en", "zh-CN"} {
			t.Run(tc.problem+lang+tc.retry.String(), func(t *testing.T) {
				useLanguage(t, lang)
				a := Account{Problem: tc.problem, UsageRetryAt: now.Add(tc.retry)}
				want, last := tc.en, " · last known: 5h 20% · week 40%"
				if lang == "zh-CN" {
					want, last = tc.zh, " · 上次用量：5小时 20% · 每周 40%"
				}
				if got := usageText(a, 96, now); got != want {
					t.Fatalf("status = %q, want %q", got, want)
				}
				a.Usage = &Usage{Session: 20, Weekly: 40, CheckedAt: now.Add(-time.Hour)}
				if got := usageText(a, 96, now); got != want+last {
					t.Fatalf("cached usage = %q, want %q", got, want+last)
				}
				if strings.Contains(usageText(a, 96, now), "登录已失效") {
					t.Fatal("usage failure was presented as a signed-out desktop session")
				}
			})
		}
	}
}

func TestUsageRetryStateIsLanguageNeutral(t *testing.T) {
	a := Account{Problem: usageLimitedMessage, UsageRetryAt: time.Now().Add(time.Hour)}
	data, err := json.Marshal(a)
	if err != nil || strings.Contains(string(data), "受限") {
		t.Fatal("localized text leaked into saved status")
	}
	var restored Account
	if err := json.Unmarshal(data, &restored); err != nil || !restored.UsageRetryAt.Equal(a.UsageRetryAt) {
		t.Fatal("retry deadline did not survive a state round trip")
	}
}
