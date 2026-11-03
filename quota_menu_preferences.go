package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const quotaMenuMinHeight = 244

// UI preferences live separately from account state, credentials and chats.
// Heights use 96-DPI logical pixels so another display can clamp them safely.
type quotaMenuPreferences struct {
	Height int `json:"menuHeight"`
}

func validQuotaMenuHeight(height int) bool {
	return height == 0 || height >= quotaMenuMinHeight && height <= 8192
}

func loadQuotaMenuHeight(path string) (int, error) {
	if path == "" {
		return 0, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return 0, err
	}
	var p quotaMenuPreferences
	if len(data) > 4096 || json.Unmarshal(data, &p) != nil || !validQuotaMenuHeight(p.Height) {
		return 0, errors.New(tr("Invalid menu height preference", "菜单高度配置无效"))
	}
	return p.Height, nil
}

func saveQuotaMenuHeight(path string, height int) error {
	if !validQuotaMenuHeight(height) {
		return fmt.Errorf(tr("Invalid menu height: %d", "无效的菜单高度：%d"), height)
	}
	if path == "" {
		return nil
	}
	data, err := json.Marshal(quotaMenuPreferences{Height: height})
	if err != nil {
		return err
	}
	if err = writeFileAtomic(path, data); err != nil {
		_ = os.Remove(path + ".switcher-tmp")
	}
	return err
}
