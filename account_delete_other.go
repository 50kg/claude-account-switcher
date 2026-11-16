//go:build !windows

package main

import "errors"

func (s *switcher) deleteArchivedAccount(string) error {
	return errors.New(tr("Account deletion is available in the Windows menu", "请在 Windows 菜单中删除账号"))
}
