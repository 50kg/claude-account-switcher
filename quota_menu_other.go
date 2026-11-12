//go:build !windows

package main

import "errors"

func handleSessionWindowAction(_ *switcher, _ string, _ bool) error {
	return errors.New("starting a session window from the tray requires Windows")
}

func newQuotaMenu(func() quotaMenuView, func(quotaMenuAction) error, func(string), string) quotaMenu {
	return nil
}
