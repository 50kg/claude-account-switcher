package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func usageCacheEntry(token string, expiry int64) map[string]any {
	return map[string]any{"token": token, "expiresAt": expiry}
}

func usageCacheKey(account, clientID, org, scopes string) string {
	return "acct:" + account + "|" + clientID + ":" + org + ":" + apiBase + ":" + scopes
}

func parseUsageFixture(t *testing.T, cache map[string]any) []usageCredential {
	t.Helper()
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := usageCredentialsFromCache(data, "a", "org", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return candidates
}

func desktopUsageFixture(t *testing.T) []usageCredential {
	return parseUsageFixture(t, map[string]any{
		usageCacheKey("a", "profile", "org", "user:profile"):                                 usageCacheEntry("synthetic-profile", 2000),
		usageCacheKey("a", "desktop", "org", "user:inference user:file_upload user:profile"): usageCacheEntry("synthetic-desktop", 9000),
		usageCacheKey("a", "code", "org", "user:profile user:sessions:claude_code"):          usageCacheEntry("synthetic-code-never-use", 99999),
	})
}

func mockUsageClient(t *testing.T, handler func(string, string) (int, string)) *[]string {
	t.Helper()
	old := client
	t.Cleanup(func() { client = old })
	var calls []string
	client = &http.Client{Transport: usageRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != apiBase+"/api/oauth/profile" && req.URL.String() != apiBase+"/api/oauth/usage" {
			t.Fatal("unexpected endpoint")
		}
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		calls = append(calls, req.URL.Path+" "+token)
		status, body := handler(req.URL.Path, token)
		w := httptest.NewRecorder()
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return w.Result(), nil
	})}
	return &calls
}

func TestUsageCredentialSelectionIsScopedAndDeterministic(t *testing.T) {
	cache := map[string]any{
		usageCacheKey("a", "profile", "org", "user:profile"):                                usageCacheEntry("synthetic-profile", 2000),
		usageCacheKey("a", "desktop", "org", "user:inference user:profile"):                 usageCacheEntry("synthetic-desktop", 9000),
		usageCacheKey("a", "duplicate", "org", "user:profile"):                              usageCacheEntry("synthetic-desktop", 8000),
		usageCacheKey("b", "other", "org", "user:profile"):                                  usageCacheEntry("synthetic-other-account", 99999),
		usageCacheKey("a", "other", "wrong-org", "user:profile"):                            usageCacheEntry("synthetic-other-org", 99999),
		usageCacheKey("a", "code", "org", "user:profile user:sessions:claude_code"):         usageCacheEntry("synthetic-code", 99999),
		usageCacheKey("a", "code-spaces", "org", "user:profile user:sessions:claude_code "): usageCacheEntry("synthetic-code-with-space", 99999),
		usageCacheKey("a", "wrong-scope", "org", "user:profile_extra"):                      usageCacheEntry("synthetic-wrong-scope", 99999),
		"acct:a|client:org:https://api.anthropic.com.evil:user:profile":                     usageCacheEntry("synthetic-wrong-audience", 99999),
		"acct:a|client:org:https://elsewhere.example:user:profile":                          usageCacheEntry("synthetic-other-audience", 99999),
	}
	for range 10 {
		cs := parseUsageFixture(t, cache)
		if len(cs) != 2 || cs[0].token != "synthetic-desktop" || !cs[0].profileOnly || cs[1].token != "synthetic-profile" {
			t.Fatal("candidate selection was not scoped, deduplicated or deterministic")
		}
	}
	cs := desktopUsageFixture(t)
	if len(cs) != 2 || cs[0].token != "synthetic-profile" || cs[1].token != "synthetic-desktop" {
		t.Fatal("longer expiry beat the profile-only credential, or Claude Code was included")
	}
}

func TestUsageFallbackRemembersWorkingDesktopCredential(t *testing.T) {
	for _, rejection := range []int{401, 403} {
		t.Run(http.StatusText(rejection), func(t *testing.T) {
			s := newSwitcher(t.TempDir())
			s.st.Accounts = []*Account{{UUID: "a", Org: "org"}}
			cs := desktopUsageFixture(t)
			calls := mockUsageClient(t, func(path, token string) (int, string) {
				if token == "synthetic-profile" {
					return rejection, `{}`
				}
				if token != "synthetic-desktop" || path != "/api/oauth/usage" {
					t.Fatal("used a disallowed credential or endpoint")
				}
				return 200, `{"five_hour":{"utilization":25}}`
			})
			_, u, err := s.queryUsageCandidates(context.Background(), "a", cs, false)
			if err != nil || u == nil || u.Session != 25 || len(*calls) != 2 {
				t.Fatalf("fallback failed: %v, %d requests", err, len(*calls))
			}
			s.recordUsageResult("a", u, nil, time.Now())
			if len(s.usageCredentialState("a").RejectedUntil) != 1 {
				t.Fatal("forgot rejected credential after success")
			}
			s.saveState()
			data, err := os.ReadFile(s.statePath())
			if err != nil || strings.Contains(string(data), "synthetic-") {
				t.Fatal("state contained a credential value")
			}
			reloaded := newSwitcher(s.dir)
			// Even after the reject mark expires, the known working credential stays first.
			for hash := range reloaded.usageCredentialState("a").RejectedUntil {
				reloaded.usageCredentialState("a").RejectedUntil[hash] = time.Now().Add(-time.Second)
			}
			*calls = nil
			_, _, err = reloaded.queryUsageCandidates(context.Background(), "a", cs, false)
			if err != nil || len(*calls) != 1 || !strings.HasSuffix((*calls)[0], "synthetic-desktop") {
				t.Fatal("restart retried the bad credential before the working one")
			}
		})
	}
}

func TestUsageFallbackStopsOnRateLimitAndOtherFailures(t *testing.T) {
	for _, status := range []int{429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := newSwitcher(t.TempDir())
			calls := mockUsageClient(t, func(_, _ string) (int, string) { return status, `{}` })
			_, _, err := s.queryUsageCandidates(context.Background(), "a", desktopUsageFixture(t), false)
			if err == nil || len(*calls) != 1 {
				t.Fatal("tried a second credential after a non-auth failure")
			}
			if len(s.usageCredentialState("a").RejectedUntil) != 0 {
				t.Fatal("a temporary failure blacklisted a credential")
			}
			if status == 429 {
				var limited *rateLimited
				if !errors.As(err, &limited) || limited.after != time.Hour {
					t.Fatal("lost the server retry deadline")
				}
				s.recordUsageResult("a", nil, err, time.Now())
				s.saveState()
				reloaded := newSwitcher(s.dir)
				if len(reloaded.usageJobs("a", time.Now().Add(time.Minute))) != 0 {
					t.Fatal("unknown account bypassed rate limit after restart")
				}
			}
		})
	}
	t.Run("timeout", func(t *testing.T) {
		s := newSwitcher(t.TempDir())
		old := client
		t.Cleanup(func() { client = old })
		calls := 0
		client = &http.Client{Transport: usageRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, context.DeadlineExceeded })}
		_, _, err := s.queryUsageCandidates(context.Background(), "a", desktopUsageFixture(t), false)
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
			t.Fatal("timeout triggered credential cycling")
		}
	})
}

func TestUsageAllRejectedCredentialsBackOffAndSurviveRestart(t *testing.T) {
	s := newSwitcher(t.TempDir())
	s.st.Accounts = []*Account{{UUID: "a", Org: "org"}}
	cs := desktopUsageFixture(t)
	calls := mockUsageClient(t, func(_, _ string) (int, string) { return 401, `{}` })
	_, _, err := s.queryUsageCandidates(context.Background(), "a", cs, false)
	if !errors.Is(err, errUsageUnauthorized) || len(*calls) != 2 {
		t.Fatal("all-rejected check was not bounded")
	}
	now := time.Now()
	s.recordUsageResult("a", nil, err, now)
	if len(s.usageJobs("a", now.Add(time.Minute))) != 0 {
		t.Fatal("401 was retried after one minute")
	}
	s.saveState()
	reloaded := newSwitcher(s.dir)
	if len(reloaded.usageJobs("a", now.Add(time.Minute))) != 0 {
		t.Fatal("restart bypassed auth backoff")
	}
	*calls = nil
	_, _, err = reloaded.queryUsageCandidates(context.Background(), "a", cs, false)
	if !errors.Is(err, errUsageUnauthorized) || len(*calls) != 0 {
		t.Fatal("known rejected credentials were sent again")
	}
	for range 20 {
		reloaded.recordUsageResult("a", nil, errUsageUnauthorized, now)
	}
	if reloaded.backoff["a"].Sub(now) != maxBackoff {
		t.Fatal("auth retry backoff was not capped")
	}
	// A newly issued desktop token can work without changing any sign-in files.
	cs = parseUsageFixture(t, map[string]any{usageCacheKey("a", "profile", "org", "user:profile"): usageCacheEntry("synthetic-new", 3000)})
	client = &http.Client{Transport: usageRoundTrip(func(*http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.Write([]byte(`{"five_hour":{"utilization":10}}`))
		return w.Result(), nil
	})}
	_, u, err := reloaded.queryUsageCandidates(context.Background(), "a", cs, false)
	if err != nil || u == nil {
		t.Fatal("a new credential inherited the old rejection")
	}
	reloaded.recordUsageResult("a", u, nil, now.Add(time.Hour))
	if reloaded.account("a").Problem != "" || reloaded.usageCredentialState("a").AuthFailures != 0 {
		t.Fatal("recovery did not clear the auth warning")
	}
}

func TestUsageNewAccountChecksProfileIdentityBeforeUsage(t *testing.T) {
	s := newSwitcher(t.TempDir())
	calls := mockUsageClient(t, func(path, token string) (int, string) {
		if path == "/api/oauth/profile" {
			if token == "synthetic-profile" {
				return 200, `{"account":{"uuid":"wrong"},"organization":{"uuid":"org"}}`
			}
			return 200, `{"account":{"uuid":"a","display_name":"Example"},"organization":{"uuid":"org"}}`
		}
		if token != "synthetic-desktop" {
			t.Fatal("queried usage with a mismatched profile")
		}
		return 200, `{"five_hour":{"utilization":10}}`
	})
	p, u, err := s.queryUsageCandidates(context.Background(), "a", desktopUsageFixture(t), true)
	if err != nil || u == nil || p.Account.UUID != "a" || len(*calls) != 3 {
		t.Fatal("new account identity check failed")
	}
}

func TestUsageAuthFailureDoesNotShortenServerWait(t *testing.T) {
	s := newSwitcher(t.TempDir())
	now := time.Now()
	s.recordUsageResult("a", nil, &rateLimited{after: time.Hour}, now)
	s.recordUsageResult("a", nil, errUsageUnauthorized, now.Add(time.Minute))
	if !s.backoff["a"].Equal(now.Add(time.Hour)) {
		t.Fatal("auth backoff shortened the server's wait")
	}
}
