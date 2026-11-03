//go:build !windows

package main

func newQuotaMenu(func() quotaMenuView, func(quotaMenuAction) error, func(string)) quotaMenu {
	return nil
}
