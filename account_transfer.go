//go:build windows

package main

import (
	"archive/zip"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	accountTransferFormat  = "claude-account-switcher-account"
	accountTransferVersion = 1
	accountTransferMaxSize = 256 << 20
)

type accountTransferManifest struct {
	Format  string                     `json:"format"`
	Version int                        `json:"version"`
	Account accountTransferAccount     `json:"account"`
	Auth    map[string]json.RawMessage `json:"auth"`
}

type accountTransferAccount struct {
	UUID  string `json:"uuid"`
	Org   string `json:"org"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Plan  string `json:"plan"`
}

func (s *switcher) exportAccount(uuid, destination string) error {
	if s.usageSyncing.Load() || !s.busy.CompareAndSwap(false, true) {
		return errors.New(tr("Wait for the current operation to finish", "请等待当前操作完成后再导出"))
	}
	defer func() {
		s.busy.Store(false)
		s.onChange()
	}()
	snap := s.snapshot()
	var account *Account
	for i := range snap.Accounts {
		if snap.Accounts[i].UUID == uuid {
			copy := snap.Accounts[i]
			account = &copy
			break
		}
	}
	if account == nil || destination == "" {
		return errors.New(tr("Account export was cancelled", "已取消账号导出"))
	}
	if !validTransferAccount(accountTransferAccount{UUID: account.UUID, Org: account.Org, Name: account.Name, Email: account.Email, Plan: account.Plan}) {
		return errors.New(tr("Invalid account identity", "账号标识无效"))
	}
	if !strings.EqualFold(filepath.Ext(destination), ".cas-account") {
		return errors.New(tr("Choose a .cas-account export file", "请选择以 .cas-account 结尾的导出文件，避免覆盖其他文件。"))
	}
	sourceDir := s.profileDir(uuid)
	if uuid == snap.Active {
		if desktopRunning() {
			return errors.New(tr("Close Claude before exporting the current account. The switcher will not close it for you.", "导出当前账号前请先手动关闭 Claude；切换器不会替你关闭它。"))
		}
		sourceDir = s.app().dir
	}
	cfg, err := readObject(filepath.Join(sourceDir, "config.json"))
	if err != nil {
		return fmt.Errorf(tr("Read account sign-in: %w", "读取账号登录资料失败：%w"), err)
	}
	manifest := accountTransferManifest{
		Format: accountTransferFormat, Version: accountTransferVersion,
		Account: accountTransferAccount{UUID: account.UUID, Org: account.Org, Name: account.Name, Email: account.Email, Plan: account.Plan},
		Auth:    map[string]json.RawMessage{},
	}
	for _, key := range authKeys {
		raw, ok := cfg.get(key)
		if !ok {
			continue
		}
		if key == "lastKnownAccountUuid" {
			var id string
			if json.Unmarshal(raw, &id) != nil || id != uuid {
				return errors.New(tr("The saved account identity does not match", "保存的账号标识不匹配"))
			}
			manifest.Auth[key] = raw
			continue
		}
		plain, err := decryptTransferToken(raw)
		if err != nil {
			return fmt.Errorf(tr("Export sign-in data: %w", "导出登录资料失败：%w"), err)
		}
		selected, err := filterTransferTokenCache(plain, uuid)
		clear(plain)
		if err != nil {
			return err
		}
		if len(selected) != 0 {
			manifest.Auth[key], _ = json.Marshal(string(selected))
			clear(selected)
		}
	}
	if _, ok := manifest.Auth["oauth:tokenCacheV2"]; !ok {
		return errors.New(tr("This account has no portable Claude sign-in", "此账号缺少可迁移的 Claude 登录资料"))
	}
	if _, ok := manifest.Auth["lastKnownAccountUuid"]; !ok {
		manifest.Auth["lastKnownAccountUuid"], _ = json.Marshal(uuid)
	}

	return writeAccountTransfer(destination, sourceDir, manifest)
}

func decryptTransferToken(raw json.RawMessage) ([]byte, error) {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, errors.New(tr("unexpected token cache format", "Token 缓存格式不受支持"))
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	plain, err := decryptValue(ciphertext)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || len(plain) > 16<<20 || !json.Valid(plain) {
		return nil, errors.New(tr("invalid token cache data", "Token 缓存内容无效"))
	}
	return plain, nil
}

// Desktop caches can retain credentials for more than one account. Never
// export or import another account's entries, including in older exports.
func filterTransferTokenCache(plain []byte, uuid string) ([]byte, error) {
	var cache map[string]json.RawMessage
	if err := json.Unmarshal(plain, &cache); err != nil || cache == nil {
		return nil, errors.New(tr("Unsupported token cache structure", "Token 缓存结构不受支持，无法确认账号范围。"))
	}
	selected := make(map[string]json.RawMessage)
	for key, value := range cache {
		if strings.HasPrefix(key, "acct:"+uuid+"|") {
			selected[key] = value
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	return json.Marshal(selected)
}

func writeAccountTransfer(destination, sourceDir string, manifest accountTransferManifest) (err error) {
	work, err := os.MkdirTemp(filepath.Dir(destination), ".claude-account-export-work-*")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(work); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf(tr("Remove temporary account data: %w", "清理临时账号数据失败：%w"), cleanupErr))
		}
	}()
	_ = os.Chmod(work, 0o700)

	var cookiesPath string
	for _, source := range (desktop{dir: sourceDir}).cookieFiles() {
		if _, statErr := os.Stat(source); statErr == nil {
			cookiesPath = filepath.Join(work, "Cookies")
			if err := copyFile(source, cookiesPath); err != nil {
				return err
			}
			// Preserve SQLite recovery data in the private working copy. The
			// app is closed before exporting its active account.
			for _, suffix := range []string{"-journal", "-wal"} {
				if _, sidecarErr := os.Stat(source + suffix); sidecarErr == nil {
					if err := copyFile(source+suffix, cookiesPath+suffix); err != nil {
						return err
					}
				} else if !errors.Is(sidecarErr, os.ErrNotExist) {
					return sidecarErr
				}
			}
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}
	if cookiesPath != "" {
		if err := transformCookieValues(cookiesPath, decryptValue); err != nil {
			return fmt.Errorf(tr("Prepare cookie data: %w", "处理 Cookie 数据失败：%w"), err)
		}
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(manifestBytes) > 16<<20 {
		return errors.New(tr("Account export is too large", "账号导出文件过大"))
	}
	parent := filepath.Dir(destination)
	temp, err := os.CreateTemp(parent, ".claude-account-export-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if err != nil {
			if cleanupErr := os.Remove(tempPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf(tr("Remove incomplete export: %w", "清理未完成的导出文件失败：%w"), cleanupErr))
			}
		}
	}()
	if err = temp.Chmod(0o600); err != nil {
		return err
	}
	archive := zip.NewWriter(temp)
	writeEntry := func(name string, source io.Reader, size int64) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, UncompressedSize64: uint64(size)}
		header.SetMode(0o600)
		writer, createErr := archive.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		_, copyErr := io.Copy(writer, source)
		return copyErr
	}
	if err = writeEntry("account.json", strings.NewReader(string(manifestBytes)), int64(len(manifestBytes))); err == nil && cookiesPath != "" {
		var cookies *os.File
		cookies, err = os.Open(cookiesPath)
		if err == nil {
			info, statErr := cookies.Stat()
			if statErr != nil {
				err = statErr
			} else if info.Size() > accountTransferMaxSize {
				err = errors.New(tr("Cookie data is too large", "Cookie 数据过大"))
			} else {
				err = writeEntry("Cookies", cookies, info.Size())
			}
			_ = cookies.Close()
		}
	}
	closeErr := archive.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = temp.Sync()
	}
	if err == nil {
		err = temp.Close()
	}
	if err == nil {
		err = os.Rename(tempPath, destination)
	}
	return err
}

func (s *switcher) importAccount(source string) (*Account, error) {
	if s.usageSyncing.Load() || !s.busy.CompareAndSwap(false, true) {
		return nil, errors.New(tr("Wait for the current operation to finish", "请等待当前操作完成后再导入"))
	}
	defer func() {
		s.busy.Store(false)
		s.onChange()
	}()
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > accountTransferMaxSize+(32<<20) {
		return nil, errors.New(tr("Account export is too large or empty", "账号导出文件为空或过大"))
	}
	reader, err := zip.OpenReader(source)
	if err != nil {
		return nil, fmt.Errorf(tr("Open account export: %w", "打开账号导出文件失败：%w"), err)
	}
	defer reader.Close()
	if len(reader.File) == 0 || len(reader.File) > 2 {
		return nil, errors.New(tr("This is not a valid account export", "这不是有效的账号导出文件"))
	}
	files := make(map[string]*zip.File, 2)
	for _, file := range reader.File {
		if file.Name != "account.json" && file.Name != "Cookies" || file.Mode()&os.ModeSymlink != 0 || file.FileInfo().IsDir() {
			return nil, errors.New(tr("This is not a valid account export", "这不是有效的账号导出文件"))
		}
		if _, duplicate := files[file.Name]; duplicate {
			return nil, errors.New(tr("This is not a valid account export", "这不是有效的账号导出文件"))
		}
		files[file.Name] = file
	}
	manifestFile := files["account.json"]
	if manifestFile == nil || manifestFile.UncompressedSize64 > 16<<20 {
		return nil, errors.New(tr("This is not a valid account export", "这不是有效的账号导出文件"))
	}
	manifestBytes, err := readAccountTransferEntry(manifestFile, 16<<20)
	if err != nil {
		return nil, err
	}
	var manifest accountTransferManifest
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.Format != accountTransferFormat || manifest.Version != accountTransferVersion || !validTransferAccount(manifest.Account) {
		return nil, errors.New(tr("This is not a supported account export", "账号导出文件格式无效或版本不受支持"))
	}
	auth, err := encryptTransferAuth(manifest)
	if err != nil {
		return nil, err
	}

	root := filepath.Join(s.dir, "accounts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(root, ".import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	saved := &object{vals: map[string]json.RawMessage{}}
	for _, key := range authKeys {
		if raw, ok := auth[key]; ok {
			saved.set(key, raw)
		}
	}
	if err := saved.write(filepath.Join(stage, "config.json")); err != nil {
		return nil, err
	}
	if cookieFile := files["Cookies"]; cookieFile != nil {
		if cookieFile.UncompressedSize64 == 0 || cookieFile.UncompressedSize64 > accountTransferMaxSize {
			return nil, errors.New(tr("Cookie data is invalid or too large", "Cookie 数据无效或过大"))
		}
		cookiePath := filepath.Join(stage, "Cookies")
		if err := copyAccountTransferEntry(cookieFile, cookiePath, accountTransferMaxSize); err != nil {
			return nil, err
		}
		if err := transformCookieValues(cookiePath, encryptValue); err != nil {
			return nil, fmt.Errorf(tr("Restore cookie data: %w", "恢复 Cookie 数据失败：%w"), err)
		}
	}

	account := &Account{UUID: manifest.Account.UUID, Org: manifest.Account.Org, Name: manifest.Account.Name, Email: manifest.Account.Email, Plan: manifest.Account.Plan}
	if err := s.commitImportedAccount(account, stage); err != nil {
		return nil, err
	}
	return account, nil
}

func encryptTransferAuth(manifest accountTransferManifest) (map[string]json.RawMessage, error) {
	if len(manifest.Auth) == 0 || len(manifest.Auth) > len(authKeys) {
		return nil, errors.New(tr("The export has invalid sign-in data", "导出文件中的登录资料无效"))
	}
	out := make(map[string]json.RawMessage, len(manifest.Auth))
	for key, raw := range manifest.Auth {
		if key == "lastKnownAccountUuid" {
			var id string
			if json.Unmarshal(raw, &id) != nil || id != manifest.Account.UUID {
				return nil, errors.New(tr("The export account identity does not match", "导出文件中的账号标识不匹配"))
			}
			out[key] = raw
			continue
		}
		if key != "oauth:tokenCacheV2" && key != "oauth:tokenCache" {
			return nil, errors.New(tr("The export contains an unknown sign-in field", "导出文件包含未知的登录字段"))
		}
		var plain string
		if json.Unmarshal(raw, &plain) != nil || plain == "" || len(plain) > 16<<20 || !json.Valid([]byte(plain)) {
			return nil, errors.New(tr("The export token cache is invalid", "导出文件中的 Token 缓存无效"))
		}
		selected, err := filterTransferTokenCache([]byte(plain), manifest.Account.UUID)
		if err != nil {
			return nil, err
		}
		if len(selected) == 0 {
			continue
		}
		ciphertext, err := encryptValue(selected)
		clear(selected)
		if err != nil {
			return nil, err
		}
		out[key], _ = json.Marshal(base64.StdEncoding.EncodeToString(ciphertext))
	}
	if _, ok := out["oauth:tokenCacheV2"]; !ok {
		return nil, errors.New(tr("The export has no portable Claude sign-in", "导出文件中没有可迁移的 Claude 登录资料"))
	}
	if _, ok := out["lastKnownAccountUuid"]; !ok {
		out["lastKnownAccountUuid"], _ = json.Marshal(manifest.Account.UUID)
	}
	return out, nil
}

func readAccountTransferEntry(file *zip.File, limit uint64) ([]byte, error) {
	if file.UncompressedSize64 > limit {
		return nil, errors.New(tr("Account export entry is too large", "账号导出文件内容过大"))
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) > limit || uint64(len(data)) != file.UncompressedSize64 {
		return nil, errors.New(tr("Account export entry is incomplete", "账号导出文件内容不完整"))
	}
	return data, nil
}

func copyAccountTransferEntry(file *zip.File, destination string, limit uint64) error {
	if file.UncompressedSize64 == 0 || file.UncompressedSize64 > limit {
		return errors.New(tr("Account export entry is empty or too large", "账号导出内容为空或过大"))
	}
	r, err := file.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(r, int64(limit)+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	info, err := os.Stat(destination)
	if err != nil {
		_ = os.Remove(destination)
		return err
	}
	if uint64(info.Size()) != file.UncompressedSize64 || uint64(info.Size()) > limit {
		_ = os.Remove(destination)
		return errors.New(tr("Account export entry is incomplete", "账号导出内容不完整"))
	}
	return nil
}

func validTransferAccount(a accountTransferAccount) bool {
	if a.UUID == "" || len(a.UUID) > 128 || strings.TrimSpace(a.UUID) != a.UUID || strings.HasSuffix(a.UUID, ".") {
		return false
	}
	for _, r := range a.UUID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	device := strings.ToUpper(strings.SplitN(a.UUID, ".", 2)[0])
	if device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" || len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && device[3] >= '1' && device[3] <= '9' {
		return false
	}
	return len(a.Org) <= 512 && len(a.Name) <= 512 && len(a.Email) <= 512 && len(a.Plan) <= 128
}

func transformCookieValues(path string, transform func([]byte) ([]byte, error)) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=3000`); err != nil {
		return err
	}
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&journalMode); err != nil {
		return err
	}
	if !strings.EqualFold(journalMode, "delete") {
		return errors.New(tr("Could not prepare a standalone Cookie database", "无法准备独立的 Cookie 数据库"))
	}
	// Updating an encrypted BLOB can leave its old plaintext in freed pages.
	// Scrub deleted cells and rebuild the working database before saving it.
	if _, err := db.Exec(`PRAGMA secure_delete=ON`); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	type encryptedCookie struct {
		id    int64
		value []byte
	}
	const batchSize = 256
	var lastID int64
	hasLastID := false
	total := 0
	for {
		query := `SELECT rowid, encrypted_value FROM cookies WHERE length(encrypted_value) > 0 ORDER BY rowid LIMIT ?`
		args := []any{batchSize}
		if hasLastID {
			query = `SELECT rowid, encrypted_value FROM cookies WHERE rowid > ? AND length(encrypted_value) > 0 ORDER BY rowid LIMIT ?`
			args = []any{lastID, batchSize}
		}
		rows, err := tx.Query(query, args...)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		var cookies []encryptedCookie
		for rows.Next() {
			var cookie encryptedCookie
			if err := rows.Scan(&cookie.id, &cookie.value); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return err
			}
			cookies = append(cookies, cookie)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return err
		}
		if err := rows.Close(); err != nil {
			_ = tx.Rollback()
			return err
		}
		if len(cookies) == 0 {
			break
		}
		total += len(cookies)
		if total > 50000 {
			_ = tx.Rollback()
			return errors.New(tr("Cookie database contains too many entries", "Cookie 数据库条目过多"))
		}
		for _, cookie := range cookies {
			value, err := transform(cookie.value)
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			if _, err := tx.Exec(`UPDATE cookies SET encrypted_value = ? WHERE rowid = ?`, value, cookie.id); err != nil {
				_ = tx.Rollback()
				return err
			}
			lastID, hasLastID = cookie.id, true
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM`)
	return err
}

func (s *switcher) commitImportedAccount(account *Account, stagedProfile string) error {
	if !validTransferAccount(accountTransferAccount{UUID: account.UUID, Org: account.Org, Name: account.Name, Email: account.Email, Plan: account.Plan}) {
		return errors.New(tr("Invalid account identity", "账号标识无效"))
	}
	profile := s.profileDir(account.UUID)
	if _, err := os.Lstat(profile); err == nil {
		return errors.New(tr("This account is already imported", "此账号已经导入"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.mu.Lock()
	for _, existing := range s.st.Accounts {
		if existing.UUID == account.UUID {
			s.mu.Unlock()
			return errors.New(tr("This account is already in the list", "账号列表中已存在此账号"))
		}
	}
	root := filepath.Dir(profile)
	if err := os.MkdirAll(root, 0o700); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := os.Rename(stagedProfile, profile); err != nil {
		s.mu.Unlock()
		return err
	}
	oldAccounts := s.st.Accounts
	s.st.Accounts = append(s.st.Accounts, account)
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err == nil {
		err = writeFileAtomic(s.statePath(), data)
	}
	if err != nil {
		s.st.Accounts = oldAccounts
		s.mu.Unlock()
		_ = os.RemoveAll(profile)
		return err
	}
	s.mu.Unlock()
	s.onChange()
	return nil
}
