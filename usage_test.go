package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type usageRoundTrip func(*http.Request) (*http.Response, error)

func (f usageRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A fake transport answers in memory: no real account or endpoint is contacted.
func TestUsageHTTPResponsesAreDistinct(t *testing.T) {
	for _, status := range []int{200, 401, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			oldClient := client
			t.Cleanup(func() { client = oldClient })
			client = &http.Client{Transport: usageRoundTrip(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/api/oauth/usage" {
					t.Fatalf("unexpected endpoint: %s", req.URL.Path)
				}
				w := httptest.NewRecorder()
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"five_hour":{"utilization":25},"seven_day":{"utilization":50}}`))
				return w.Result(), nil
			})}
			u, err := fetchUsage(context.Background(), "synthetic-test-token")
			var limited *rateLimited
			switch status {
			case 200:
				if err != nil || u == nil || u.Session != 25 || u.Weekly != 50 {
					t.Fatalf("successful usage = %+v, %v", u, err)
				}
			case 401:
				if !errors.Is(err, errUsageUnauthorized) || errors.As(err, &limited) {
					t.Fatalf("401 classification = %v", err)
				}
			case 429:
				if !errors.As(err, &limited) || limited.after != time.Hour || errors.Is(err, errUsageUnauthorized) {
					t.Fatalf("429 classification = %v", err)
				}
			default:
				if err == nil || errors.Is(err, errUsageUnauthorized) || errors.As(err, &limited) {
					t.Fatalf("HTTP %d was misclassified: %v", status, err)
				}
			}
		})
	}
}

func TestRetryAfterSecondsAndDates(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"3600", time.Hour},
		{" 60 ", time.Minute},
		{now.Add(time.Hour).Format(http.TimeFormat), time.Hour},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{"", 0}, {"invalid", 0}, {"-1", 0}, {"0", 0},
		{"9223372036854775807", 0},
	} {
		if got := retryAfter(tc.header, now); got != tc.want {
			t.Errorf("Retry-After %q = %s, want %s", tc.header, got, tc.want)
		}
	}
}

func TestTokenCacheDistinguishesExpiryAndMissingScope(t *testing.T) {
	now := int64(1000)
	entry := func(token string, expiry int64) map[string]any {
		return map[string]any{"token": token, "expiresAt": expiry}
	}
	for _, tc := range []struct {
		name  string
		cache map[string]any
		want  string
		err   error
	}{
		{"expired", map[string]any{"acct:a|user:profile": entry("synthetic-old", 999)}, "", errUsageTokenExpired},
		{"other account", map[string]any{"acct:b|user:profile": entry("synthetic-other", 2000)}, "", errUsageTokenUnavailable},
		{"wrong scope", map[string]any{"acct:a|other-scope": entry("synthetic-other", 2000)}, "", errUsageTokenUnavailable},
		{"empty", map[string]any{"acct:a|user:profile": entry("", 2000)}, "", errUsageTokenUnavailable},
		{"valid", map[string]any{"acct:a|user:profile": entry("synthetic-valid", 2000), "acct:a|second:user:profile": entry("synthetic-newer", 3000), "acct:a|empty:user:profile": entry("", 4000)}, "synthetic-newer", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.cache)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tokenFromCache(data, "a", now)
			if got != tc.want || !errors.Is(err, tc.err) || errors.Is(err, errUsageUnauthorized) {
				t.Fatalf("cached token classification = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
}
