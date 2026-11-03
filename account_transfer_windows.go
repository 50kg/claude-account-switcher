//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	transferComdlg      = windows.NewLazySystemDLL("comdlg32.dll")
	transferSaveDialog  = transferComdlg.NewProc("GetSaveFileNameW")
	transferOpenDialog  = transferComdlg.NewProc("GetOpenFileNameW")
	transferDialogError = transferComdlg.NewProc("CommDlgExtendedError")
	transferMessageBox  = user32.NewProc("MessageBoxW")
)

type transferOpenFilename struct {
	Size                         uint32
	Owner, Instance              uintptr
	Filter, CustomFilter         *uint16
	MaxCustomFilter, FilterIndex uint32
	File                         *uint16
	MaxFile                      uint32
	FileTitle                    *uint16
	MaxFileTitle                 uint32
	InitialDir, Title            *uint16
	Flags                        uint32
	FileOffset, FileExtension    uint16
	DefaultExtension             *uint16
	CustData, Hook, TemplateName uintptr
	Reserved                     uintptr
	ReservedInt, FlagsEx         uint32
}

func accountTransferFilter() []uint16 {
	parts := []string{tr("Claude account export (*.cas-account)", "Claude 账号导出文件 (*.cas-account)"), "*.cas-account", tr("All files", "所有文件"), "*.*"}
	var filter []uint16
	for _, part := range parts {
		filter = append(filter, windows.StringToUTF16(part)...)
	}
	return append(filter, 0)
}

func transferFileDialog(save bool, defaultName string) (string, error) {
	return transferFileDialogWithHook(save, defaultName, 0)
}

// The optional hook lets isolated native tests operate only their own dialog.
// Production calls do not install a hook or read/write an account here.
func transferFileDialogWithHook(save bool, defaultName string, hook uintptr) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	filter := accountTransferFilter()
	file := make([]uint16, 32768)
	var initialDir []uint16
	if filepath.IsAbs(defaultName) {
		initialDir = windows.StringToUTF16(filepath.Dir(defaultName))
		defaultName = filepath.Base(defaultName)
	}
	if defaultName != "" {
		encoded := windows.StringToUTF16(defaultName)
		copy(file, encoded)
	}
	title := windows.StringToUTF16(tr("Import account", "导入账号"))
	flags := uint32(0x00080000 | 0x00000800 | 0x00000008 | 0x00000004 | 0x02000000) // Explorer, path required, no directory change, hide read-only, no recent-file entry
	if hook != 0 {
		flags |= 0x00000020 // OFN_ENABLEHOOK, used only by isolated native tests.
	}
	proc := transferOpenDialog
	if save {
		title = windows.StringToUTF16(tr("Export account", "导出账号"))
		flags |= 0x00000002 // Confirm overwrite.
		proc = transferSaveDialog
	} else {
		flags |= 0x00001000 // File must exist.
	}
	extension := windows.StringToUTF16("cas-account")
	// Keep the exact OPENFILENAMEW field order from the Windows SDK. In
	// particular, each file pointer is followed by its own buffer length.
	filename := transferOpenFilename{
		Filter: &filter[0], FilterIndex: 1, File: &file[0], MaxFile: uint32(len(file)),
		Title: &title[0], Flags: flags, DefaultExtension: &extension[0], Hook: hook,
	}
	if len(initialDir) != 0 {
		filename.InitialDir = &initialDir[0]
	}
	filename.Size = uint32(unsafe.Sizeof(filename))
	result, _, _ := proc.Call(uintptr(unsafe.Pointer(&filename)))
	if result == 0 {
		code, _, _ := transferDialogError.Call()
		if code == 0 {
			return "", nil // User cancelled.
		}
		return "", fmt.Errorf(tr("File dialog failed (%d)", "文件选择窗口失败（%d）"), code)
	}
	return windows.UTF16ToString(file), nil
}

func showUnencryptedTransferWarning(export bool) bool {
	title := tr("Unencrypted account file", "未加密的账号文件")
	body := tr("This file contains sign-in credentials without encryption. Anyone who gets it may use this account. Store it privately and delete it after importing. Continue?", "该文件会以明文保存登录凭证，拿到文件的人可能使用此账号。请妥善保管，并在导入后删除。是否继续？")
	if !export {
		title = tr("Import unencrypted account", "导入未加密账号")
		body = tr("Only import a file you created and trust. The switcher will add its account profile without changing the current Claude sign-in. Continue?", "只导入你自己创建并信任的文件。切换器会添加账号档案，不会更改 Claude 当前登录。是否继续？")
	}
	result, _, _ := transferMessageBox.Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(body))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))),
		0x00000004|0x00000030|0x00000100) // Yes/No, warning icon, default No.
	return result == 6 // IDYES
}

func handleAccountExport(s *switcher, uuid string) error {
	if !showUnencryptedTransferWarning(true) {
		return nil
	}
	snap := s.snapshot()
	name := "Claude account"
	for _, account := range snap.Accounts {
		if account.UUID == uuid {
			name = strings.TrimSpace(account.Email)
			if name == "" {
				name = account.Name
			}
			break
		}
	}
	name = strings.TrimSpace(strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_").Replace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "Claude account"
	}
	path, err := transferFileDialog(true, name+".cas-account")
	if err == nil && path == "" {
		return nil // Cancelled; no result dialog is needed.
	}
	if err == nil {
		err = s.exportAccount(uuid, path)
	}
	s.setStatus("")
	showAccountExportResult(path, err)
	return nil
}

func showAccountExportResult(path string, err error) {
	title := tr("Account export completed", "账号导出成功")
	body := fmt.Sprintf(tr("The account was exported.\n\nSaved to:\n%s", "账号已导出。\n\n保存位置：\n%s"), path)
	icon := uintptr(0x00000040) // MB_ICONINFORMATION
	if err != nil {
		title = tr("Account export failed", "账号导出失败")
		body = fmt.Sprintf(tr("The account could not be exported.\n\n%s", "账号未能导出。\n\n%s"), err)
		icon = 0x00000010 // MB_ICONERROR
	}
	transferMessageBox.Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(body))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))),
		icon|0x00010000) // MB_OK | MB_SETFOREGROUND
}

func handleAccountImport(s *switcher) error {
	if !showUnencryptedTransferWarning(false) {
		return nil
	}
	path, err := transferFileDialog(false, "")
	if err != nil || path == "" {
		return err
	}
	account, err := s.importAccount(path)
	if err != nil {
		return err
	}
	s.setStatus(fmt.Sprintf(tr("Imported %s. Switch to it when ready.", "已导入 %s，可在需要时切换。"), account.label()))
	return nil
}
