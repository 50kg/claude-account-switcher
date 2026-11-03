package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const quotaMenuMinHeight = 244

// UI preferences live separately from account state, credentials and chats.
// Heights use 96-DPI logical pixels so another display can clamp them safely.
type quotaMenuPreferences struct {
	Height         int `json:"menuHeight"`
	RefreshMinutes int `json:"refreshMinutes,omitempty"`
}

func validQuotaMenuHeight(height int) bool {
	return height == 0 || height >= quotaMenuMinHeight && height <= 8192
}

var quotaPreferencesMu sync.Mutex

func validRefreshMinutes(minutes int) bool { return minutes >= 0 && minutes <= 1440 }

func loadQuotaMenuHeight(path string) (int, error) {
	p, err := loadQuotaMenuPreferences(path)
	return p.Height, err
}

func loadQuotaMenuPreferences(path string) (quotaMenuPreferences, error) {
	quotaPreferencesMu.Lock()
	defer quotaPreferencesMu.Unlock()
	return readQuotaMenuPreferences(path)
}

func readQuotaMenuPreferences(path string) (quotaMenuPreferences, error) {
	if path == "" {
		return quotaMenuPreferences{}, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return quotaMenuPreferences{}, nil
	}
	if err != nil {
		return quotaMenuPreferences{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return quotaMenuPreferences{}, err
	}
	var p quotaMenuPreferences
	if len(data) > 4096 || json.Unmarshal(data, &p) != nil || !validQuotaMenuHeight(p.Height) || !validRefreshMinutes(p.RefreshMinutes) {
		return quotaMenuPreferences{}, errors.New(tr("Invalid interface preferences", "界面设置无效"))
	}
	return p, nil
}

func saveQuotaMenuHeight(path string, height int) error {
	if !validQuotaMenuHeight(height) {
		return fmt.Errorf(tr("Invalid menu height: %d", "无效的菜单高度：%d"), height)
	}
	return updateQuotaMenuPreferences(path, func(p *quotaMenuPreferences) { p.Height = height })
}

func saveRefreshMinutes(path string, minutes int) error {
	if !validRefreshMinutes(minutes) {
		return errors.New(tr("Enter 1–1440 minutes", "请输入1–1440分钟"))
	}
	return updateQuotaMenuPreferences(path, func(p *quotaMenuPreferences) { p.RefreshMinutes = minutes })
}

func updateQuotaMenuPreferences(path string, update func(*quotaMenuPreferences)) error {
	if path == "" {
		return nil
	}
	quotaPreferencesMu.Lock()
	defer quotaPreferencesMu.Unlock()
	p, err := readQuotaMenuPreferences(path)
	if err != nil {
		return err
	}
	update(&p)
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = writeFileAtomic(path, data); err != nil {
		_ = os.Remove(path + ".switcher-tmp")
	}
	return err
}
