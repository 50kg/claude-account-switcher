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
	Height         int      `json:"menuHeight"`
	RefreshMinutes int      `json:"refreshMinutes,omitempty"`
	SortMode       string   `json:"sortMode,omitempty"`
	AccountOrder   []string `json:"accountOrder,omitempty"`
	// Empty until the user picks one; the Windows display language decides.
	Language string `json:"language,omitempty"`
}

func validQuotaMenuHeight(height int) bool {
	return height == 0 || height >= quotaMenuMinHeight && height <= 8192
}

var quotaPreferencesMu sync.Mutex

var errInvalidQuotaMenuPreferences = errors.New("invalid interface preferences")

func validRefreshMinutes(minutes int) bool { return minutes >= 0 && minutes <= 1440 }

func validQuotaMenuSortMode(mode string) bool {
	return mode == "" || mode == "manual" || mode == "reset"
}

func validQuotaMenuAccountOrder(order []string) bool {
	if len(order) > 8192 {
		return false
	}
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		if id == "" || len(id) > 256 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

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
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return quotaMenuPreferences{}, err
	}
	var p quotaMenuPreferences
	if len(data) > 1<<20 || json.Unmarshal(data, &p) != nil || !validQuotaMenuHeight(p.Height) || !validRefreshMinutes(p.RefreshMinutes) || !validQuotaMenuSortMode(p.SortMode) || !validQuotaMenuAccountOrder(p.AccountOrder) || !validLanguagePreference(p.Language) {
		return quotaMenuPreferences{}, fmt.Errorf("%w: %s", errInvalidQuotaMenuPreferences, tr("Invalid interface preferences", "界面设置无效"))
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

func validLanguagePreference(lang string) bool {
	return lang == "" || lang == langEnglish || lang == langChinese
}

func saveLanguagePreference(path, lang string) error {
	if lang == "" || !validLanguagePreference(lang) {
		return errors.New(tr("Invalid language", "无效的语言"))
	}
	return updateQuotaMenuPreferences(path, func(p *quotaMenuPreferences) { p.Language = lang })
}

func saveQuotaMenuSortMode(path, mode string) error {
	if !validQuotaMenuSortMode(mode) || mode == "" {
		return errors.New(tr("Invalid sort mode", "无效的账号排序方式"))
	}
	return updateQuotaMenuPreferences(path, func(p *quotaMenuPreferences) { p.SortMode = mode })
}

func saveQuotaMenuAccountOrder(path string, order []string) error {
	if !validQuotaMenuAccountOrder(order) {
		return errors.New(tr("Invalid account order", "无效的账号顺序"))
	}
	return updateQuotaMenuPreferences(path, func(p *quotaMenuPreferences) {
		p.AccountOrder = append([]string(nil), order...)
		p.SortMode = "manual"
	})
}

func updateQuotaMenuPreferences(path string, update func(*quotaMenuPreferences)) error {
	if path == "" {
		return nil
	}
	quotaPreferencesMu.Lock()
	defer quotaPreferencesMu.Unlock()
	p, err := readQuotaMenuPreferences(path)
	// A damaged file is replaced rather than blocking every later save.
	if errors.Is(err, errInvalidQuotaMenuPreferences) {
		p, err = quotaMenuPreferences{}, nil
	}
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
