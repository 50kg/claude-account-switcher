package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (s *switcher) setRefreshMinutes(minutes int) error {
	if err := saveRefreshMinutes(filepath.Join(s.dir, "ui.json"), minutes); err != nil {
		return err
	}
	s.mu.Lock()
	s.refreshMinutes = minutes
	s.mu.Unlock()
	s.setStatus(refreshIntervalSavedText(minutes))
	return nil
}

func refreshIntervalSavedText(minutes int) string {
	if minutes == 0 {
		return tr("Smart usage sync enabled", "已启用智能用量同步")
	}
	return fmt.Sprintf(tr("Usage auto-sync set to every %d min", "自动同步已设为每%d分钟"), minutes)
}

func (s *switcher) beginUsageSync() bool {
	if !s.usageSyncing.CompareAndSwap(false, true) {
		return false
	}
	if s.busy.Load() {
		s.usageSyncing.Store(false)
		return false
	}
	s.mu.Lock()
	if isUsageSyncStatus(s.status) {
		s.status = ""
	}
	s.mu.Unlock()
	s.onChange()
	return true
}

func isUsageSyncStatus(status string) bool {
	return status == tr("Syncing usage…", "正在同步用量…") || strings.HasPrefix(status, tr("Usage synced:", "用量同步完成："))
}

func (s *switcher) endUsageSync() {
	s.usageSyncing.Store(false)
	s.onChange()
}

func (s *switcher) syncUsageNow(ctx context.Context) error {
	return s.syncUsageNowWith(ctx, s.queryAccountUsage)
}

func (s *switcher) queryAccountUsage(ctx context.Context, uuid, active string) (*Usage, error) {
	credentials, err := s.credentialsFor(uuid, active)
	if err != nil {
		return nil, err
	}
	_, u, err := s.queryUsageCandidates(ctx, uuid, credentials, false)
	return u, err
}

// Manual sync only queries known accounts. It does not inspect chat files,
// discover sign-ins, import chats, restart Claude or run automatic switching.
// A test query can replace the credential/network boundary entirely.
func (s *switcher) syncUsageNowWith(ctx context.Context, query func(context.Context, string, string) (*Usage, error)) error {
	if s.busy.Load() || !s.beginUsageSync() {
		return errors.New(tr("A sync or account operation is already running", "正在同步或处理账号操作，请稍候"))
	}
	defer s.endUsageSync()
	s.setStatus(tr("Syncing usage…", "正在同步用量…"))
	s.mu.Lock()
	active := s.active
	var ids []string
	waiting := 0
	for _, a := range s.st.Accounts {
		if time.Now().Before(s.backoff[a.UUID]) {
			waiting++
		} else {
			ids = append(ids, a.UUID)
		}
	}
	s.mu.Unlock()
	updated, failed, pending := 0, 0, 0
	for i, id := range ids {
		if ctx.Err() != nil {
			pending = len(ids) - i
			break
		}
		u, err := query(ctx, id, active)
		s.mu.Lock()
		s.recordUsageResult(id, u, err, time.Now())
		s.mu.Unlock()
		if err == nil {
			updated++
		} else {
			failed++
		}
	}
	s.mu.Lock()
	s.saveState()
	s.mu.Unlock()
	text := fmt.Sprintf(tr("Usage synced: %d updated", "用量同步完成：已更新%d个账号"), updated)
	if waiting > 0 {
		text += fmt.Sprintf(tr(" · %d waiting to retry", " · %d个等待重试"), waiting)
	}
	if failed > 0 {
		text += fmt.Sprintf(tr(" · %d failed", " · %d个查询失败"), failed)
	}
	if pending > 0 {
		text += fmt.Sprintf(tr(" · %d not queried before timeout", " · %d个因超时未查询"), pending)
	}
	s.setStatus(text)
	return nil
}
