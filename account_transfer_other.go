//go:build !windows

package main

import "errors"

func handleAccountExport(*switcher, string) error {
	return errors.New(tr("Account transfer is available in the Windows menu", "账号迁移功能目前仅在 Windows 菜单中提供"))
}

func handleAccountImport(*switcher) error {
	return errors.New(tr("Account transfer is available in the Windows menu", "账号迁移功能目前仅在 Windows 菜单中提供"))
}
