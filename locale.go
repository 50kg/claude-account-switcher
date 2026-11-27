package main

import (
	"fmt"
	"strings"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

const (
	langEnglish = "en"
	langChinese = "zh-CN"
)

// The interface language can change while the menu, workers and dialogs run,
// so every reader goes through this atomic.
var chinese atomic.Bool

// normalizeLanguage maps the accepted spellings to "en" or "zh-CN".
func normalizeLanguage(value string) (string, bool) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-")) {
	case "en", "en-us":
		return langEnglish, true
	case "zh", "zh-cn", "zh-hans":
		return langChinese, true
	}
	return "", false
}

func setLanguage(value string) error {
	lang, ok := normalizeLanguage(value)
	if !ok {
		return fmt.Errorf("unsupported language %q; use en or zh-CN / 不支持的语言，请使用 en 或 zh-CN", value)
	}
	chinese.Store(lang == langChinese)
	return nil
}

func currentLanguage() string {
	if chinese.Load() {
		return langChinese
	}
	return langEnglish
}

// systemLanguage follows the Windows display language until the user picks
// one in Settings: any Chinese display language selects Simplified Chinese.
func systemLanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err == nil && len(langs) > 0 && strings.HasPrefix(strings.ToLower(langs[0]), "zh") {
		return langChinese
	}
	return langEnglish
}

// languageLabel names a language in itself, so it reads right in either UI.
func languageLabel(lang string) string {
	if lang == langChinese {
		return "简体中文"
	}
	return "English"
}

func tr(english, zh string) string {
	if chinese.Load() {
		return zh
	}
	return english
}
