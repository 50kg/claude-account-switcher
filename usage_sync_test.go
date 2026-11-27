package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRefreshIntervalPersistsWithoutOverwritingHeightOrAccountState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui.json")
	if err := saveQuotaMenuHeight(path, 650); err != nil {
		t.Fatal(err)
	}
	s := newSwitcher(dir)
	state := []byte(`{"accounts":[]}`)
	if err := os.WriteFile(s.statePath(), state, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.setRefreshMinutes(7); err != nil {
		t.Fatal(err)
	}
	if s.snapshot().Status != refreshIntervalSavedText(7) {
		t.Fatal("saved interval has no visible confirmation")
	}
	if got := newSwitcher(dir).snapshot().RefreshMinutes; got != 7 {
		t.Fatalf("interval not reloaded: %d", got)
	}
	if err := saveQuotaMenuHeight(path, 520); err != nil {
		t.Fatal(err)
	}
	p, err := loadQuotaMenuPreferences(path)
	if err != nil || p.Height != 520 || p.RefreshMinutes != 7 {
		t.Fatalf("preferences overwritten: %+v %v", p, err)
	}
	if got, _ := os.ReadFile(s.statePath()); string(got) != string(state) {
		t.Fatal("interval changed account state")
	}
	for _, bad := range []int{-1, 1441} {
		if s.setRefreshMinutes(bad) == nil {
			t.Fatal("invalid interval accepted")
		}
	}
	if s.snapshot().RefreshMinutes != 7 {
		t.Fatal("invalid interval changed current value")
	}
	if err := s.setRefreshMinutes(0); err != nil {
		t.Fatal(err)
	}
	if s.snapshot().Status != refreshIntervalSavedText(0) {
		t.Fatal("smart mode has no visible confirmation")
	}
	if got := newSwitcher(dir).snapshot().RefreshMinutes; got != 0 {
		t.Fatal("smart mode not restored")
	}
}

func TestCustomRefreshIntervalSchedulesAllAccountsAndHonorsRetryWaits(t *testing.T) {
	s := newSwitcher(t.TempDir())
	now := time.Now()
	s.refreshMinutes = 7
	s.st.Accounts = []*Account{{UUID: "active", Usage: &Usage{Session: 99, CheckedAt: now}}, {UUID: "other", Usage: &Usage{CheckedAt: now}}}
	if jobs := s.usageJobs("active", now.Add(6*time.Minute)); len(jobs) != 0 {
		t.Fatal("custom interval was overridden by hot/idle rules")
	}
	if jobs := s.usageJobs("active", now.Add(7*time.Minute)); len(jobs) != 2 {
		t.Fatal("custom interval did not cover all accounts")
	}
	s.backoff["other"] = now.Add(time.Hour)
	if jobs := s.usageJobs("active", now.Add(8*time.Minute)); len(jobs) != 1 || jobs[0].uuid != "active" {
		t.Fatal("custom interval bypassed backoff")
	}
	s.recordUsageResult("active", nil, errors.New("network"), now.Add(8*time.Minute))
	if jobs := s.usageJobs("active", now.Add(9*time.Minute)); len(jobs) != 0 {
		t.Fatal("failed check ignored custom interval")
	}
	s.refreshMinutes = 0
	if jobs := s.usageJobs("active", now.Add(9*time.Minute)); len(jobs) != 1 {
		t.Fatal("smart retry behavior was not restored")
	}
}

func TestManualUsageSyncForcesFreshAccountsButSkipsBackoffAndNeverSwitches(t *testing.T) {
	useLanguage(t, "zh-CN")
	s := newSwitcher(t.TempDir())
	now := time.Now()
	s.active = "active"
	s.st.AutoSwitch = true
	s.st.Accounts = []*Account{{UUID: "active", Usage: &Usage{Session: 100, CheckedAt: now}}, {UUID: "other", Usage: &Usage{CheckedAt: now}}, {UUID: "limited", Usage: &Usage{CheckedAt: now}}}
	s.backoff["limited"] = now.Add(time.Hour)
	old := s.account("limited").Usage
	calls := 0
	err := s.syncUsageNowWith(context.Background(), func(ctx context.Context, id, active string) (*Usage, error) {
		calls++
		if id == "limited" || active != "active" {
			t.Fatal("manual sync bypassed wait or changed identity")
		}
		if !s.snapshot().Syncing {
			t.Fatal("in-flight sync state not visible")
		}
		if s.beginUsageSync() {
			t.Fatal("concurrent sync was allowed")
		}
		return &Usage{Session: 20, Weekly: 40, CheckedAt: now.Add(time.Second)}, nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("manual sync: calls=%d err=%v", calls, err)
	}
	if s.active != "active" || !s.st.LastSwitch.IsZero() || s.account("limited").Usage != old || !s.backoff["limited"].Equal(now.Add(time.Hour)) {
		t.Fatal("manual sync changed switch state or waiting account")
	}
	if s.snapshot().Syncing {
		t.Fatal("sync flag was not cleared")
	}
	if s.snapshot().Status != "用量同步完成：已更新2个账号 · 1个等待重试" {
		t.Fatalf("missing completion feedback: %s", s.snapshot().Status)
	}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Fatalf("manual sync touched unexpected file: %s", e.Name())
		}
	}
}

func TestManualUsageSyncFailurePreservesCacheAndSerializesRequests(t *testing.T) {
	s := newSwitcher(t.TempDir())
	now := time.Now()
	old := &Usage{Session: 77, CheckedAt: now}
	s.st.Accounts = []*Account{{UUID: "a", Usage: old}}
	err := s.syncUsageNowWith(context.Background(), func(context.Context, string, string) (*Usage, error) { return nil, &rateLimited{after: time.Hour} })
	if err != nil || s.account("a").Usage != old || s.account("a").Problem != usageLimitedMessage {
		t.Fatal("failed sync destroyed or refreshed cached data")
	}
	if jobs := s.usageJobs("a", now.Add(time.Minute)); len(jobs) != 0 {
		t.Fatal("manual sync lost server retry wait")
	}
	s.busy.Store(true)
	if s.syncUsageNowWith(context.Background(), func(context.Context, string, string) (*Usage, error) { t.Fatal("busy sync queried"); return nil, nil }) == nil {
		t.Fatal("busy operation was ignored")
	}
	s.busy.Store(false)
	s.usageSyncing.Store(true)
	if s.syncUsageNowWith(context.Background(), nil) == nil {
		t.Fatal("overlapping sync accepted")
	}
	s.usageSyncing.Store(false)
}

func TestSmartUsageIntervalsRemainAdaptive(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		active  string
		used    float64
		elapsed time.Duration
		want    bool
	}{
		{"current-before-two", "a", 40, time.Minute, false},
		{"current-two", "a", 40, 2 * time.Minute, true},
		{"hot-one", "a", 85, time.Minute, true},
		{"other-before-ten", "other", 40, 9 * time.Minute, false},
		{"other-ten", "other", 40, 10 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSwitcher(t.TempDir())
			s.st.Accounts = []*Account{{UUID: "a", Usage: &Usage{Session: tc.used, CheckedAt: now}}}
			jobs := s.usageJobs(tc.active, now.Add(tc.elapsed))
			// An unseen active identity is a separate account-discovery job.
			due := false
			for _, j := range jobs {
				if j.uuid == "a" {
					due = true
				}
			}
			if due != tc.want {
				t.Fatalf("due=%v, want %v", due, tc.want)
			}
		})
	}
}

func TestCancelledManualSyncDoesNotMarkUnqueriedAccountsAsFailures(t *testing.T) {
	s := newSwitcher(t.TempDir())
	u := &Usage{Session: 30, CheckedAt: time.Now()}
	s.st.Accounts = []*Account{{UUID: "a", Usage: u}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.syncUsageNowWith(ctx, func(context.Context, string, string) (*Usage, error) {
		t.Fatal("cancelled sync queried")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.account("a").Usage != u || s.account("a").Problem != "" || s.snapshot().Syncing {
		t.Fatal("cancelled sync corrupted cached usage or stayed busy")
	}
}

func TestNextSyncClearsItsPreviousCompletionFeedback(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		t.Run(lang, func(t *testing.T) {
			useLanguage(t, lang)
			s := newSwitcher(t.TempDir())
			s.status = tr("Usage synced: 2 updated", "用量同步完成：已更新2个账号")
			v := makeQuotaMenuView(snapshot{Status: s.status, Syncing: true}, false, time.Now())
			if v.Status != "" {
				t.Fatal("in-flight header contradicted old completion banner")
			}
			if !s.beginUsageSync() {
				t.Fatal("sync did not begin")
			}
			if s.snapshot().Status != "" {
				t.Fatal("previous completion survived a new sync")
			}
			s.endUsageSync()
		})
	}
}

func TestIntervalSaveFailureKeepsCurrentSetting(t *testing.T) {
	s := newSwitcher(t.TempDir())
	s.refreshMinutes = 7
	if err := os.Mkdir(filepath.Join(s.dir, "ui.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if s.setRefreshMinutes(15) == nil {
		t.Fatal("save failure was hidden")
	}
	if s.snapshot().RefreshMinutes != 7 {
		t.Fatal("failed interval save changed active setting")
	}
}

// Archived accounts are set aside by the schedule and the subscription
// refresh; a manual sync still refreshes them.
func TestArchivedAccountsAreNotQueried(t *testing.T) {
	s := newSwitcher(t.TempDir())
	s.st.Accounts = []*Account{{UUID: "kept", Org: "o"}, {UUID: "archived", Org: "o", Archived: true}}
	now := time.Now()
	s.mu.Lock()
	jobs := s.usageJobs("", now)
	s.mu.Unlock()
	if len(jobs) != 1 || jobs[0].uuid != "kept" {
		t.Fatalf("scheduled %+v", jobs)
	}
	var queried []string
	err := s.syncUsageNowWith(context.Background(), func(_ context.Context, id, _ string) (*Usage, error) {
		queried = append(queried, id)
		return &Usage{CheckedAt: now}, nil
	})
	if err != nil || len(queried) != 2 {
		t.Fatalf("manual sync queried %v, %v", queried, err)
	}
	s.refreshBilling()
	if s.account("archived").Billing != nil {
		t.Fatal("subscription date queried for an archived account")
	}
	// After the manual sync, only the account Claude is signed in with is
	// due again on its short schedule, even though it is archived.
	later := now.Add(activeEvery + time.Second)
	s.mu.Lock()
	jobs = s.usageJobs("archived", later)
	s.mu.Unlock()
	if len(jobs) != 1 || jobs[0].uuid != "archived" {
		t.Fatalf("active archived account not scheduled: %+v", jobs)
	}
	// Once restored it is on the ordinary schedule again.
	later = now.Add(idleEvery + time.Second)
	s.mu.Lock()
	jobs = s.usageJobs("", later)
	restoredDue := len(jobs)
	s.account("archived").Archived = false
	jobs = s.usageJobs("", later)
	s.mu.Unlock()
	if restoredDue != 1 || len(jobs) != 2 {
		t.Fatalf("archived %d, restored %d jobs", restoredDue, len(jobs))
	}
}

// With a longer auto-sync interval, usage that arrives on schedule must not
// show as last known in between; the default keeps the 15-minute window.
func TestUsageFreshnessFollowsSyncInterval(t *testing.T) {
	now := time.Now()
	s := newSwitcher(t.TempDir())
	s.st.Accounts = []*Account{{UUID: "a", Usage: &Usage{CheckedAt: now.Add(-20 * time.Minute)}}}
	if freshModelUsage(s.snapshot().Accounts[0], now) {
		t.Fatal("20-minute-old usage counted as fresh in smart mode")
	}
	s.refreshMinutes = 30
	if !freshModelUsage(s.snapshot().Accounts[0], now) {
		t.Fatal("on-schedule usage greyed out with a 30-minute interval")
	}
	if makeQuotaMenuView(s.snapshot(), false, now).Accounts[0].Stale {
		t.Fatal("menu row marked stale")
	}
}
