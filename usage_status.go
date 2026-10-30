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
	problem := ""
	var retryAt time.Time
	var limited *rateLimited
	if errors.As(err, &limited) {
		s.strikes[uuid]++
		wait := limited.after
		if wait <= 0 {
			// Cap the shift as well as the delay, even after many responses.
			wait = min(activeEvery<<min(s.strikes[uuid]-1, 4), maxBackoff)
		}
		retryAt = now.Add(wait)
		s.backoff[uuid] = retryAt
		problem = usageLimitedMessage
		log.Printf("usage %s: rate limited, next try in %s", uuid[:min(len(uuid), 8)], wait)
	} else {
		delete(s.backoff, uuid)
		delete(s.strikes, uuid)
		switch {
		case err == nil:
		case errors.Is(err, errUsageUnauthorized):
			problem = usageUnauthorizedMessage
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
		if remaining := a.UsageRetryAt.Sub(now); remaining > 0 {
			if remaining >= time.Minute {
				minutes := int((remaining + time.Minute - 1) / time.Minute)
				return fmt.Sprintf(tr("usage check rate limited, retry in about %d min", "用量查询受限，约%d分钟后重试"), minutes)
			}
			seconds := int((remaining + time.Second - 1) / time.Second)
			return fmt.Sprintf(tr("usage check rate limited, retry in %d s", "用量查询受限，%d秒后重试"), seconds)
		}
		return tr("usage check rate limited, waiting to retry", "用量查询受限，等待重试")
	case usageUnauthorizedMessage:
		return tr("usage authorization rejected (401); check sign-in in Claude", "用量授权被拒绝（401），请在 Claude 中确认登录")
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
