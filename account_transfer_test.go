//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Use an isolated test process with a synthetic key so no call can load a real
// Claude key, profile, token or Cookie database during the round trip.
func TestAccountTransferSyntheticRoundTrip(t *testing.T) {
	if os.Getenv("ACCOUNT_TRANSFER_SYNTHETIC_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAccountTransferSyntheticRoundTrip$", "-test.v")
		cmd.Env = append(os.Environ(), "ACCOUNT_TRANSFER_SYNTHETIC_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("synthetic migration failed: %v\n%s", err, out)
		}
		return
	}
	keyOnce.Do(func() { keyVal = bytes.Repeat([]byte{0x32}, 32) })
	if keyErr != nil {
		t.Fatal("synthetic key was not initialized")
	}
	root := t.TempDir()
	source := newSwitcher(filepath.Join(root, "source"))
	account := &Account{UUID: "synthetic-migration-account", Org: "synthetic-org", Name: "虚构账号", Email: "synthetic@example.com", Plan: "Max"}
	source.st.Accounts = []*Account{account}
	source.active = "synthetic-source-current"
	profile := source.profileDir(account.UUID)
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	selectedKey := usageCacheKey(account.UUID, "desktop", account.Org, "user:profile user:inference")
	selectedEntry := usageCacheEntry("synthetic-selected-account-token", 9000)
	selectedAuth, err := json.Marshal(map[string]any{selectedKey: selectedEntry})
	if err != nil {
		t.Fatal(err)
	}
	plainAuth, err := json.Marshal(map[string]any{
		selectedKey: selectedEntry,
		usageCacheKey("another-account", "desktop", "other-org", "user:profile"): usageCacheEntry("foreign-account-secret-never-export", 9000),
		"unscoped-legacy-data": "unscoped-secret-never-export",
	})
	if err != nil {
		t.Fatal(err)
	}
	cipherAuth, err := encryptValue(plainAuth)
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{
		"oauth:tokenCacheV2":   base64.StdEncoding.EncodeToString(cipherAuth),
		"lastKnownAccountUuid": account.UUID,
		"unrelated":            "must never be exported",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "config.json"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "chat-must-not-export.json"), []byte("synthetic chat sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	cookiePlain := []byte("synthetic-cookie-value")
	cookieCipher, err := encryptValue(cookiePlain)
	if err != nil {
		t.Fatal(err)
	}
	cookiePath := filepath.Join(profile, "Cookies")
	db, err := sql.Open("sqlite", cookiePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cookies VALUES (?, ?, '', ?)`, ".example.com", "synthetic-session", cookieCipher); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cookiePath)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "迁移文件.cas-account")
	if err := source.exportAccount(account.UUID, destination); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(cookiePath)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("export modified the original Cookie database")
	}
	if after, err := os.ReadFile(filepath.Join(profile, "config.json")); err != nil || !bytes.Equal(after, config) {
		t.Fatal("export modified the source sign-in")
	}
	archive, err := zip.OpenReader(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 {
		archive.Close()
		t.Fatal("export must contain only account.json and Cookies")
	}
	for _, entry := range archive.File {
		switch entry.Name {
		case "account.json":
			data, err := readAccountTransferEntry(entry, 16<<20)
			var manifest accountTransferManifest
			if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Auth) != 2 {
				t.Fatal("invalid synthetic export manifest")
			}
			var plain string
			if json.Unmarshal(manifest.Auth["oauth:tokenCacheV2"], &plain) != nil || plain != string(selectedAuth) {
				t.Fatal("exported synthetic token must be portable plaintext")
			}
			if bytes.Contains(data, []byte("foreign-account-secret")) || bytes.Contains(data, []byte("unscoped-secret")) {
				t.Fatal("export leaked credentials outside the selected account")
			}
		case "Cookies":
		default:
			t.Fatalf("unexpected export entry: %s", entry.Name)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if work, err := filepath.Glob(filepath.Join(root, ".claude-account-export-*")); err != nil || len(work) != 0 {
		t.Fatal("export left temporary plaintext files")
	}

	// A different synthetic destination key must replace source encryption.
	keyVal = bytes.Repeat([]byte{0x79}, 32)
	target := newSwitcher(filepath.Join(root, "target"))
	current := &Account{UUID: "synthetic-target-current", Name: "Preserved"}
	target.active = current.UUID
	target.st.Accounts = []*Account{current}
	currentProfile := target.profileDir(current.UUID)
	if err := os.MkdirAll(currentProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("existing synthetic sign-in must remain byte-identical")
	if err := os.WriteFile(filepath.Join(currentProfile, "config.json"), sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	imported, err := target.importAccount(destination)
	if err != nil {
		t.Fatal(err)
	}
	if imported.UUID != account.UUID || target.active != current.UUID || len(target.st.Accounts) != 2 {
		t.Fatal("import replaced the current account or omitted the migrated account")
	}
	if got, err := os.ReadFile(filepath.Join(currentProfile, "config.json")); err != nil || !bytes.Equal(got, sentinel) {
		t.Fatal("import modified the existing target profile")
	}
	saved, err := readObject(filepath.Join(target.profileDir(account.UUID), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.get("unrelated"); ok {
		t.Fatal("imported unrelated config data")
	}
	if raw, ok := saved.get("oauth:tokenCacheV2"); !ok {
		t.Fatal("missing imported sign-in")
	} else if got, err := decryptTransferToken(raw); err != nil || !bytes.Equal(got, selectedAuth) {
		t.Fatal("target key could not decrypt the imported sign-in")
	}
	db, err = sql.Open("sqlite", filepath.Join(target.profileDir(account.UUID), "Cookies"))
	if err != nil {
		t.Fatal(err)
	}
	var importedCipher []byte
	err = db.QueryRow(`SELECT encrypted_value FROM cookies WHERE name = 'synthetic-session'`).Scan(&importedCipher)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decryptValue(importedCipher); err != nil || !bytes.Equal(got, cookiePlain) || bytes.Equal(importedCipher, cookieCipher) {
		t.Fatal("Cookie was not re-encrypted with the target key")
	}
	importedDatabase, err := os.ReadFile(filepath.Join(target.profileDir(account.UUID), "Cookies"))
	if err != nil || bytes.Contains(importedDatabase, cookiePlain) {
		t.Fatal("imported Cookie database retained plaintext in its pages")
	}
	if _, err := target.importAccount(destination); err == nil || len(target.st.Accounts) != 2 {
		t.Fatal("duplicate import overwrote an account")
	}
	// Old exports may contain a mixed cache. Import must also scope it.
	legacyToken, _ := json.Marshal(string(plainAuth))
	auth, err := encryptTransferAuth(accountTransferManifest{Account: accountTransferAccount{UUID: account.UUID}, Auth: map[string]json.RawMessage{"oauth:tokenCacheV2": legacyToken}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decryptTransferToken(auth["oauth:tokenCacheV2"]); err != nil || !bytes.Equal(got, selectedAuth) {
		t.Fatal("import accepted another account's credentials from an older export")
	}
	protectedPath := filepath.Join(root, "existing-config.json")
	if err := os.WriteFile(protectedPath, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := source.exportAccount(account.UUID, protectedPath); err == nil {
		t.Fatal("export allowed replacing a non-export file")
	}
	if got, err := os.ReadFile(protectedPath); err != nil || !bytes.Equal(got, sentinel) {
		t.Fatal("export overwrote a protected file")
	}
}

func TestAccountTransferCacheRejectsUnknownScopes(t *testing.T) {
	for _, plain := range []string{`null`, `[]`, `"token"`, `{`} {
		if _, err := filterTransferTokenCache([]byte(plain), "selected"); err == nil {
			t.Fatalf("accepted unsupported token cache: %s", plain)
		}
	}
	for _, plain := range []string{`{}`, `{"acct:other|client:org:scope":{"token":"foreign-secret"}}`, `{"acct:selected-extra|client:org:scope":{"token":"foreign-secret"}}`, `{"unscoped":{"token":"unknown-secret"}}`} {
		if selected, err := filterTransferTokenCache([]byte(plain), "selected"); err != nil || len(selected) != 0 {
			t.Fatalf("accepted an unscoped or different-account cache: %v", err)
		}
	}
}

func TestAccountTransferExportFailureCleansWorkingFiles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Cookies"), []byte("invalid synthetic SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "failed.cas-account")
	if err := writeAccountTransfer(destination, source, accountTransferManifest{}); err == nil {
		t.Fatal("export silently accepted a corrupt Cookie database")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed export left a completed export file")
	}
	if work, err := filepath.Glob(filepath.Join(root, ".claude-account-export-*")); err != nil || len(work) != 0 {
		t.Fatal("failed export left temporary credential files")
	}
}
