//go:build !windows

package main

import "errors"

func runAppleNote() int { return 1 }

func verifyBillingWeb(*switcher, string) error { return errBillingUnavailable }
func runBillingWeb() int                       { return 1 }

func handleAccountExport(*switcher, string) error {
	return errors.New(tr("Account transfer is available in the Windows menu", "账号迁移功能目前仅在 Windows 菜单中提供"))
}

func handleAccountImport(*switcher) error {
	return errors.New(tr("Account transfer is available in the Windows menu", "账号迁移功能目前仅在 Windows 菜单中提供"))
}
