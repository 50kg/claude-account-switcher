package main

import (
	"fmt"
	"strings"
)

// English stays the upstream default. A localized build can set this with
// -ldflags "-X main.defaultLanguage=zh-CN"; -lang overrides it at runtime.
var defaultLanguage = "en"

// Selected before the tray and its worker goroutines start; immutable afterwards.
var language = defaultLanguage

func setLanguage(value string) error {
	switch strings.ToLower(strings.ReplaceAll(value, "_", "-")) {
	case "en", "en-us":
		language = "en"
	case "zh", "zh-cn", "zh-hans":
		language = "zh-CN"
	default:
		return fmt.Errorf("unsupported language %q; use en or zh-CN / 不支持的语言，请使用 en 或 zh-CN", value)
	}
	return nil
}

func tr(english, chinese string) string {
	if language == "zh-CN" {
		return chinese
	}
	return english
}
