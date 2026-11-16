package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func (s *switcher) deleteArchivedAccount(id string) error {
	return s.deleteArchivedAccountFrom(id, s.app())
}

// The desktop argument lets deletion be exercised entirely with synthetic data.
// Never remove the live sign-in: it would otherwise be rediscovered on refresh.
func (s *switcher) deleteArchivedAccountFrom(id string, d desktop) error {
	if !validTransferAccount(accountTransferAccount{UUID: id}) {
		return errors.New(tr("Invalid account identity", "账号标识无效"))
	}
	if !s.busy.CompareAndSwap(false, true) {
		return errors.New(tr("Wait for the current operation to finish", "请等待当前操作完成"))
	}
	defer func() { s.busy.Store(false); s.onChange() }()
	if s.usageSyncing.Load() || s.billingSyncing.Load() || s.billingWindow.Load() {
		return errors.New(tr("Wait for syncing or account verification to finish", "请等待同步或账号核验完成后再删除"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.account(id)
	if a == nil || !a.Archived {
		return errors.New(tr("Archive the account before deleting it", "请先归档账号，再彻底删除"))
	}
	active, err := d.activeAccount()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(tr("Cannot verify the current account: %w", "无法核对当前账号：%w"), err)
	}
	if id == s.active || id == active {
		return errors.New(tr("Switch to another account before deleting this one", "请先切换到其他账号，再删除此账号"))
	}
	if _, err := loadQuotaMenuPreferences(filepath.Join(s.dir, "ui.json")); err != nil {
		return err
	}

	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	plan, err := planAccountDeletion(root, id)
	if err != nil {
		return err
	}
	// Keep the archived record until every credential copy has been removed.
	// A locked file or write failure leaves the record available for a retry.
	for _, cfg := range plan.configs {
		if err := cfg.object.write(filepath.Join(s.dir, cfg.path)); err != nil {
			return fmt.Errorf(tr("Could not clean saved credentials; retry deletion: %w", "清理保存的登录资料失败，请重试删除：%w"), err)
		}
	}
	for _, dir := range plan.dirs {
		if err := removeAccountSignIn(root, dir); err != nil {
			return fmt.Errorf(tr("Could not remove saved account files; retry deletion: %w", "删除账号文件失败，请重试删除：%w"), err)
		}
	}
	if err := updateQuotaMenuPreferences(filepath.Join(s.dir, "ui.json"), func(p *quotaMenuPreferences) {
		p.AccountOrder = slices.DeleteFunc(p.AccountOrder, func(uuid string) bool { return uuid == id })
	}); err != nil {
		return err
	}
	next := s.st
	next.Accounts = slices.DeleteFunc(slices.Clone(next.Accounts), func(a *Account) bool { return a.UUID == id })
	next.UsageCredentials = maps.Clone(next.UsageCredentials)
	delete(next.UsageCredentials, id)
	if next.Spending == id {
		next.Spending, next.SpendUntil = "", time.Time{}
	}
	if next.LastFrom == id {
		next.LastFrom, next.LastCause = "", byHand
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err == nil {
		err = writeFileAtomic(s.statePath(), data)
	}
	if err != nil {
		return fmt.Errorf(tr("Could not save account removal; retry deletion: %w", "保存删除结果失败，请重试删除：%w"), err)
	}
	s.st = next
	delete(s.backoff, id)
	delete(s.strikes, id)
	delete(s.lastUsageAttempt, id)
	s.status = tr("Account permanently deleted", "账号已彻底删除")
	return nil
}

type accountDeletionPlan struct {
	dirs    []string
	configs []accountDeletionConfig
}

type accountDeletionConfig struct {
	path   string
	object *object
}

func planAccountDeletion(root *os.Root, id string) (accountDeletionPlan, error) {
	var plan accountDeletionPlan
	for _, dir := range []string{filepath.Join("accounts", id), filepath.Join("session-code", id)} {
		if err := checkAccountDeletionPath(root, dir); err != nil {
			return plan, err
		}
		plan.dirs = append(plan.dirs, dir)
	}
	for _, parent := range []string{"accounts", "backups"} {
		if err := checkAccountDeletionPath(root, parent); err != nil {
			return plan, err
		}
		entries, err := os.ReadDir(filepath.Join(root.Name(), parent))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return plan, err
		}
		for _, entry := range entries {
			dir := filepath.Join(parent, entry.Name())
			if slices.Contains(plan.dirs, dir) {
				continue
			}
			if err := checkAccountDeletionPath(root, dir); err != nil {
				return plan, err
			}
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, "config.json")
			if err := checkAccountDeletionPath(root, path); err != nil {
				return plan, err
			}
			cfg, err := readObject(filepath.Join(root.Name(), path))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return plan, err
			}
			var owner string
			_ = json.Unmarshal(cfg.vals["lastKnownAccountUuid"], &owner)
			if parent == "backups" && owner == id {
				plan.dirs = append(plan.dirs, dir)
				continue
			}
			changed, err := removeAccountTokens(cfg, id)
			if err != nil {
				return plan, fmt.Errorf(tr("Cannot clean credentials in %s: %w", "无法清理 %s 中的登录资料：%w"), path, err)
			}
			if changed {
				plan.configs = append(plan.configs, accountDeletionConfig{path, cfg})
			}
		}
	}
	return plan, nil
}

// A snapshot can contain several accounts' OAuth entries. Remove just this
// account's entries and keep the others encrypted with Claude's existing key.
func removeAccountTokens(cfg *object, id string) (bool, error) {
	changed := false
	for _, field := range []string{"oauth:tokenCache", "oauth:tokenCacheV2"} {
		raw, ok := cfg.get(field)
		if !ok {
			continue
		}
		plain, err := decryptTransferToken(raw)
		if err != nil {
			return false, err
		}
		var cache map[string]json.RawMessage
		err = json.Unmarshal(plain, &cache)
		clear(plain)
		if err != nil || cache == nil {
			return false, errors.New(tr("Unsupported token cache structure", "登录资料格式不受支持"))
		}
		removed := false
		for key := range cache {
			if strings.HasPrefix(key, "acct:"+id+"|") {
				clear(cache[key])
				delete(cache, key)
				removed = true
			}
		}
		if !removed {
			continue
		}
		remaining, err := json.Marshal(cache)
		if err != nil {
			return false, err
		}
		cipher, err := encryptValue(remaining)
		clear(remaining)
		if err != nil {
			return false, err
		}
		encoded, _ := json.Marshal(base64.StdEncoding.EncodeToString(cipher))
		cfg.set(field, encoded)
		changed = true
	}
	return changed, nil
}

// Reject directory aliases as well as path traversal. Root also enforces the
// boundary during removal, even if a link changes after this preflight check.
func checkAccountDeletionPath(root *os.Root, path string) error {
	if path == "." || !filepath.IsLocal(path) {
		return errors.New(tr("Unsafe account data path", "账号数据路径无效"))
	}
	part := ""
	for _, name := range strings.Split(path, string(filepath.Separator)) {
		part = filepath.Join(part, name)
		info, err := root.Lstat(part)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New(tr("Account data contains a linked path; deletion stopped", "账号数据包含链接路径，已停止删除"))
		}
	}
	return nil
}

// Keep config.json until last so a partially removed backup can still be
// identified on retry. Nested links are removed themselves, never followed.
func removeAccountSignIn(root *os.Root, path string) error {
	if err := checkAccountDeletionPath(root, path); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(root.Name(), path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "config.json" {
			if err := root.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	if err := root.Remove(filepath.Join(path, "config.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return root.Remove(path)
}
