package main

import (
	"errors"
	"fmt"
	"log"
	"time"
)

// Canonical English values keep saved state independent of the UI language.
const (
	usageUnverifiedMessage   = "usage access needs checking"
	usageLimitedMessage      = "usage check rate limited"
	usageUnauthorizedMessage = "usage authorization rejected (401)"
	usageForbiddenMessage    = "usage permission denied (403)"
	usageTokenExpiredMessage = "usage token expired"
	usageTokenMissingMessage = "usage token unavailable"
	usageFailedMessage       = "usage check temporarily failed"
)

type usageJob struct {
	uuid string
	new  bool
}

// usageJobs runs with s.mu held and only schedules checks; it never reads or
// changes Claude's files. Existing accounts are refreshed rather than added.
func (s *switcher) usageJobs(active string, now time.Time) []usageJob {
	var jobs []usageJob
	if active != "" && s.account(active) == nil && !now.Before(s.backoff[active]) {
		jobs = append(jobs, usageJob{active, true})
	}
	for _, a := range s.st.Accounts {
		every := idleEvery
		if a.UUID == active {
			every = activeEvery
			if a.Usage != nil && a.Usage.current(now).peak() >= hotAt {
				every = hotEvery
			}
		}
		due := a.Problem != "" || a.Usage == nil || now.Sub(a.Usage.CheckedAt) >= every-5*time.Second
		if s.refreshMinutes > 0 {
			at := s.lastUsageAttempt[a.UUID]
			if a.Usage != nil && a.Usage.CheckedAt.After(at) {
				at = a.Usage.CheckedAt
			}
			due = at.IsZero() || now.Sub(at) >= time.Duration(s.refreshMinutes)*time.Minute-5*time.Second
		}
		if due && !now.Before(s.backoff[a.UUID]) {
			jobs = append(jobs, usageJob{a.UUID, false})
		}
	}
	return jobs
}

// recordUsageResult runs with s.mu held. An API failure never establishes that
// the separate Claude desktop session has been signed out. Failed checks keep
// the old usage and its original timestamp, so it cannot appear freshly fetched.
func (s *switcher) recordUsageResult(uuid string, u *Usage, err error, now time.Time) {
	if s.lastUsageAttempt == nil {
		s.lastUsageAttempt = map[string]time.Time{}
	}
	s.lastUsageAttempt[uuid] = now
	problem := ""
	var retryAt time.Time
	meta := s.usageCredentialState(uuid)
	if s.backoff[uuid].After(meta.RetryAt) {
		meta.RetryAt = s.backoff[uuid]
	}
	var limited *rateLimited
	if errors.As(err, &limited) {
		s.strikes[uuid]++
		wait := limited.after
		if wait <= 0 {
			// Cap the shift as well as the delay, even after many responses.
			wait = min(activeEvery<<min(s.strikes[uuid]-1, 4), maxBackoff)
		}
		retryAt = now.Add(wait)
		problem = usageLimitedMessage
		log.Printf("usage %s: rate limited, next try in %s", uuid[:min(len(uuid), 8)], wait)
	} else {
		delete(s.backoff, uuid)
		delete(s.strikes, uuid)
		switch {
		case err == nil:
			meta.AuthFailures = 0
			meta.LastRejection = 0
		case errors.Is(err, errUsageUnauthorized):
			problem = usageUnauthorizedMessage
			meta.AuthFailures++
			retryAt = now.Add(min(usageAuthRetryMin<<min(meta.AuthFailures-1, 3), maxBackoff))
		case errors.Is(err, errUsageForbidden):
			problem = usageForbiddenMessage
			meta.AuthFailures++
			retryAt = now.Add(min(usageAuthRetryMin<<min(meta.AuthFailures-1, 3), maxBackoff))
		case errors.Is(err, errUsageTokenExpired):
			problem = usageTokenExpiredMessage
		case errors.Is(err, errUsageTokenUnavailable):
			problem = usageTokenMissingMessage
		default:
			problem = usageFailedMessage
		}
		if err != nil {
			log.Printf("usage %s: %v", uuid[:min(len(uuid), 8)], err)
		}
	}
	// A later result must not shorten a server wait already recorded for this
	// account. The working credential and rejected hashes survive success.
	if meta.RetryAt.After(now) && meta.RetryAt.After(retryAt) {
		retryAt = meta.RetryAt
	}
	meta.RetryAt = retryAt
	if retryAt.After(now) {
		s.backoff[uuid] = retryAt
	}
	if a := s.account(uuid); a != nil {
		a.Problem, a.UsageRetryAt = problem, retryAt
		if err == nil {
			a.Usage = u
		}
	}
}

func usageProblemText(a Account, now time.Time) string {
	switch a.Problem {
	case signedOutMessage, usageUnverifiedMessage:
		return tr(usageUnverifiedMessage, "用量授权待验证")
	case usageLimitedMessage:
		return tr("usage check rate limited", "用量查询受限") + usageRetrySuffix(a.UsageRetryAt, now)
	case usageUnauthorizedMessage:
		return tr("usage authorization rejected (401)", "用量授权被拒绝（401）") + usageRetrySuffix(a.UsageRetryAt, now)
	case usageForbiddenMessage:
		return tr("usage permission denied (403)", "用量权限不足（403）") + usageRetrySuffix(a.UsageRetryAt, now)
	case usageTokenExpiredMessage:
		return tr("usage token expired; check sign-in in Claude", "用量凭证已过期，请在 Claude 中确认登录")
	case usageTokenMissingMessage:
		return tr("usage token unavailable; check sign-in in Claude", "用量凭证暂不可用，请在 Claude 中确认登录")
	case usageFailedMessage:
		return tr("usage check temporarily failed, waiting to retry", "用量查询暂时失败，等待重试")
	default:
		return a.Problem
	}
}

func usageRetrySuffix(at, now time.Time) string {
	remaining := at.Sub(now)
	if remaining <= 0 {
		return tr(", waiting to retry", "，等待重试")
	}
	if remaining >= time.Minute {
		minutes := int(remaining / time.Minute)
		if remaining%time.Minute != 0 {
			minutes++
		}
		return fmt.Sprintf(tr(", retry in about %d min", "，约%d分钟后重试"), minutes)
	}
	seconds := int(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	return fmt.Sprintf(tr(", retry in %d s", "，%d秒后重试"), seconds)
}
