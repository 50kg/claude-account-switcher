package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"time"
)

const (
	maxUsageCandidates = 2
	usageAuthRetryMin  = 5 * time.Minute
	usageAuthRejectFor = 30 * time.Minute
)

type usageCredential struct {
	token, org, fingerprint, key string
	profileOnly                  bool
	expiresAt                    int64
}

// This metadata lives in the switcher's own state, not Claude's cache.
type usageCredentialState struct {
	Working       string               `json:"working,omitempty"`
	RejectedUntil map[string]time.Time `json:"rejectedUntil,omitempty"`
	AuthFailures  int                  `json:"authFailures,omitempty"`
	RetryAt       time.Time            `json:"retryAt,omitempty"`
	LastRejection int                  `json:"lastRejection,omitempty"`
}

func desktopUsageCredentials(encrypted, account, org string) ([]usageCredential, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}
	plain, err := decryptValue(data)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	return usageCredentialsFromCache(plain, account, org, time.Now().UnixMilli())
}

// Only the requested account/org and exact API audience qualify. A profile-only
// desktop credential is preferred; Claude Code-specific entries are excluded.
func usageCredentialsFromCache(plain []byte, account, org string, now int64) ([]usageCredential, error) {
	var cache map[string]struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(plain, &cache); err != nil {
		return nil, err
	}
	var candidates []usageCredential
	hadToken := false
	for key, entry := range cache {
		tail, ok := strings.CutPrefix(key, "acct:"+account+"|")
		if !ok {
			continue
		}
		identity, scopes, ok := strings.Cut(tail, ":"+apiBase+":")
		if !ok || strings.Contains(scopes, ":claude_code") {
			continue
		}
		clientID, tokenOrg, ok := strings.Cut(identity, ":")
		if !ok || clientID == "" || tokenOrg == "" || strings.Contains(tokenOrg, ":") || (org != "" && tokenOrg != org) {
			continue
		}
		fields := strings.Fields(scopes)
		hasProfile := false
		for _, scope := range fields {
			if scope == "user:profile" {
				hasProfile = true
			}
		}
		if !hasProfile || entry.Token == "" {
			continue
		}
		hadToken = true
		if entry.ExpiresAt <= now {
			continue
		}
		hash := sha256.Sum256([]byte(entry.Token))
		candidates = append(candidates, usageCredential{token: entry.Token, org: tokenOrg, fingerprint: hex.EncodeToString(hash[:]), key: key, profileOnly: len(fields) == 1, expiresAt: entry.ExpiresAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.profileOnly != b.profileOnly {
			return a.profileOnly
		}
		if a.expiresAt != b.expiresAt {
			return a.expiresAt > b.expiresAt
		}
		return a.key < b.key
	})
	unique := candidates[:0]
	seen := map[string]bool{}
	for _, c := range candidates {
		if !seen[c.fingerprint] {
			unique = append(unique, c)
			seen[c.fingerprint] = true
		}
	}
	if len(unique) == 0 {
		if hadToken {
			return nil, errUsageTokenExpired
		}
		return nil, errUsageTokenUnavailable
	}
	return unique, nil
}

// Callers hold s.mu. Only hashes, deadlines and counters are persisted.
func (s *switcher) usageCredentialState(uuid string) *usageCredentialState {
	if s.st.UsageCredentials == nil {
		s.st.UsageCredentials = map[string]*usageCredentialState{}
	}
	meta := s.st.UsageCredentials[uuid]
	if meta == nil {
		meta = &usageCredentialState{RejectedUntil: map[string]time.Time{}}
		s.st.UsageCredentials[uuid] = meta
	}
	if meta.RejectedUntil == nil {
		meta.RejectedUntil = map[string]time.Time{}
	}
	return meta
}

// Only a 401 or 403 allows a bounded fallback. A 429, timeout or other failure stops
// immediately, so trying another credential cannot bypass a server rate limit.
func (s *switcher) queryUsageCandidates(ctx context.Context, uuid string, credentials []usageCredential, needProfile bool) (profile, *Usage, error) {
	s.mu.Lock()
	meta := s.usageCredentialState(uuid)
	working := meta.Working
	lastRejection := errUsageUnauthorized
	if meta.LastRejection == 403 {
		lastRejection = errUsageForbidden
	}
	for hash, until := range meta.RejectedUntil {
		if !until.After(time.Now()) {
			delete(meta.RejectedUntil, hash)
		}
	}
	s.mu.Unlock()
	ordered := append([]usageCredential(nil), credentials...)
	for i, c := range ordered {
		if c.fingerprint == working {
			copy(ordered[1:i+1], ordered[:i])
			ordered[0] = c
			break
		}
	}
	tried := 0
	for _, c := range ordered {
		s.mu.Lock()
		until := meta.RejectedUntil[c.fingerprint]
		if !until.IsZero() && !until.After(time.Now()) {
			delete(meta.RejectedUntil, c.fingerprint)
		}
		s.mu.Unlock()
		if until.After(time.Now()) {
			continue
		}
		if tried == maxUsageCandidates {
			break
		}
		if err := ctx.Err(); err != nil {
			return profile{}, nil, err
		}
		tried++
		var p profile
		var u *Usage
		var err error
		if needProfile {
			p, err = fetchProfile(ctx, c.token)
			if err == nil && (p.Account.UUID != uuid || p.Organization.UUID != c.org) {
				err = errUsageUnauthorized
			}
		}
		if err == nil {
			u, err = fetchUsage(ctx, c.token)
		}
		if err == nil {
			s.mu.Lock()
			meta.Working = c.fingerprint
			s.mu.Unlock()
			return p, u, nil
		}
		if !errors.Is(err, errUsageUnauthorized) && !errors.Is(err, errUsageForbidden) {
			return profile{}, nil, err
		}
		lastRejection = err
		s.mu.Lock()
		meta.RejectedUntil[c.fingerprint] = time.Now().Add(usageAuthRejectFor)
		status := 401
		if errors.Is(err, errUsageForbidden) {
			status = 403
		}
		meta.LastRejection = status
		if meta.Working == c.fingerprint {
			meta.Working = ""
		}
		s.mu.Unlock()
		log.Printf("usage %s: desktop credential rejected (%d)", uuid[:min(len(uuid), 8)], status)
	}
	if len(credentials) == 0 {
		return profile{}, nil, errUsageTokenUnavailable
	}
	return profile{}, nil, lastRejection
}
