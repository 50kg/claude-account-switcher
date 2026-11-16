package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Synthetic encryption runs in a child so a real Claude key can never be used.
func TestDeleteArchivedAccount(t *testing.T) {
	if os.Getenv("ACCOUNT_DELETE_SYNTHETIC_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDeleteArchivedAccount$", "-test.v")
		cmd.Env = append(os.Environ(), "ACCOUNT_DELETE_SYNTHETIC_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("synthetic deletion: %v\n%s", err, out)
		}
		return
	}
	keyOnce.Do(func() { keyVal = bytes.Repeat([]byte{0x42}, 32) })
	if keyErr != nil {
		t.Fatal(keyErr)
	}

	t.Run("cleans credentials and preserves other accounts and chats", func(t *testing.T) {
		s, d := deletionFixture(t)
		s.st.Spending, s.st.LastFrom = "target", "target"
		s.st.SpendUntil = time.Now().Add(time.Hour)
		s.usageCredentialState("target").Working = "target-hash"
		s.usageCredentialState("other").Working = "other-hash"
		s.backoff["target"], s.lastUsageAttempt["target"], s.strikes["target"] = time.Now(), time.Now(), 2
		s.saveState()
		otherCookies := filepath.Join(s.profileDir("other"), "Cookies")
		write(t, otherCookies, "other cookies")
		writeDeletionConfig(t, filepath.Join(s.backupsDir(), "target-backup", "config.json"), "target")
		write(t, filepath.Join(s.backupsDir(), "target-backup", "Cookies"), "target cookies")
		otherBackup := filepath.Join(s.backupsDir(), "other-backup", "config.json")
		writeDeletionConfig(t, otherBackup, "other")
		chat := filepath.Join(d.sessionsDir(), "target", "org", "chat.json")
		export := filepath.Join(t.TempDir(), "target.cas-account")
		write(t, chat, "chat sentinel")
		write(t, export, "export sentinel")
		prefsPath := filepath.Join(s.dir, "ui.json")
		if err := saveQuotaMenuAccountOrder(prefsPath, []string{"target", "other"}); err != nil {
			t.Fatal(err)
		}
		if err := saveQuotaMenuHeight(prefsPath, 600); err != nil {
			t.Fatal(err)
		}
		liveBefore := read(t, d.configPath())
		if err := s.deleteArchivedAccountFrom("target", d); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{s.profileDir("target"), filepath.Join(s.dir, "session-code", "target"), filepath.Join(s.backupsDir(), "target-backup")} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("account files remain: %s (%v)", path, err)
			}
		}
		for _, path := range []string{filepath.Join(s.profileDir("other"), "config.json"), otherBackup} {
			cfg, err := readObject(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"oauth:tokenCache", "oauth:tokenCacheV2"} {
				plain, err := decryptTransferToken(cfg.vals[field])
				if err != nil || bytes.Contains(plain, []byte("acct:target|")) || !bytes.Contains(plain, []byte("other-secret")) {
					t.Fatalf("shared credentials were not scoped correctly: %s", field)
				}
				clear(plain)
			}
		}
		if read(t, otherCookies) != "other cookies" || read(t, chat) != "chat sentinel" || read(t, export) != "export sentinel" || read(t, d.configPath()) != liveBefore {
			t.Fatal("unrelated files changed")
		}
		if strings.Contains(read(t, s.statePath()), "target") || s.account("target") != nil || s.st.Spending != "" || !s.st.SpendUntil.IsZero() || s.st.LastFrom != "" {
			t.Fatal("account metadata remains")
		}
		if _, ok := s.backoff["target"]; ok || s.strikes["target"] != 0 || !s.lastUsageAttempt["target"].IsZero() || s.st.UsageCredentials["other"].Working != "other-hash" {
			t.Fatal("runtime metadata cleanup affected another account")
		}
		p, err := loadQuotaMenuPreferences(prefsPath)
		if err != nil || p.Height != 600 || len(p.AccountOrder) != 1 || p.AccountOrder[0] != "other" {
			t.Fatalf("preferences not preserved: %+v %v", p, err)
		}
		if newSwitcher(s.dir).account("target") != nil || s.busy.Load() {
			t.Fatal("deletion did not persist or left busy set")
		}
	})

	for _, kind := range []string{"unarchived", "current", "live-current", "sync", "billing", "verification", "busy", "bad-path", "bad-config"} {
		t.Run("rejects "+kind, func(t *testing.T) {
			s, d := deletionFixture(t)
			id := "target"
			switch kind {
			case "unarchived":
				s.account(id).Archived = false
			case "current":
				s.active = id
			case "live-current":
				writeDeletionConfig(t, d.configPath(), id)
			case "sync":
				s.usageSyncing.Store(true)
			case "billing":
				s.billingSyncing.Store(true)
			case "verification":
				s.billingWindow.Store(true)
			case "busy":
				s.busy.Store(true)
			case "bad-path":
				id = ".."
			case "bad-config":
				write(t, d.configPath(), "broken config")
			}
			before := read(t, s.statePath())
			if err := s.deleteArchivedAccountFrom(id, d); err == nil {
				t.Fatal("unsafe deletion was accepted")
			}
			if read(t, s.statePath()) != before || read(t, filepath.Join(s.profileDir("target"), "Cookies")) != "target cookies" {
				t.Fatal("rejected deletion changed saved account data")
			}
		})
	}

	t.Run("locked file leaves account available for retry", func(t *testing.T) {
		s, d := deletionFixture(t)
		path, _ := windows.UTF16PtrFromString(filepath.Join(s.profileDir("target"), "Cookies"))
		handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = s.deleteArchivedAccountFrom("target", d)
		windows.CloseHandle(handle)
		if err == nil || s.account("target") == nil || newSwitcher(s.dir).account("target") == nil {
			t.Fatal("failed deletion was reported as complete")
		}
		if err := s.deleteArchivedAccountFrom("target", d); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("linked account path cannot delete external data", func(t *testing.T) {
		s, d := deletionFixture(t)
		outside := t.TempDir()
		write(t, filepath.Join(outside, "sentinel"), "external data")
		linked := filepath.Join(s.dir, "session-code", "target")
		if err := os.RemoveAll(linked); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, linked); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if s.deleteArchivedAccountFrom("target", d) == nil || read(t, filepath.Join(outside, "sentinel")) != "external data" || s.account("target") == nil {
			t.Fatal("linked path was not rejected")
		}
	})
	t.Run("state write failure keeps the record for retry", func(t *testing.T) {
		s, d := deletionFixture(t)
		before := read(t, s.statePath())
		blocker := s.statePath() + ".switcher-tmp"
		if err := os.Mkdir(blocker, 0o700); err != nil {
			t.Fatal(err)
		}
		if s.deleteArchivedAccountFrom("target", d) == nil || s.account("target") == nil || read(t, s.statePath()) != before {
			t.Fatal("failed state save discarded the archived record")
		}
		if err := os.Remove(blocker); err != nil {
			t.Fatal(err)
		}
		if err := s.deleteArchivedAccountFrom("target", d); err != nil {
			t.Fatal(err)
		}
	})
}

func deletionFixture(t *testing.T) (*switcher, desktop) {
	t.Helper()
	root := t.TempDir()
	s := newSwitcher(filepath.Join(root, "switcher"))
	d := desktop{dir: filepath.Join(root, "desktop")}
	s.active = "other"
	s.st.Accounts = []*Account{{UUID: "target", Archived: true}, {UUID: "other"}}
	for _, id := range []string{"target", "other"} {
		writeDeletionConfig(t, filepath.Join(s.profileDir(id), "config.json"), id)
	}
	write(t, filepath.Join(s.profileDir("target"), "Cookies"), "target cookies")
	write(t, filepath.Join(s.dir, "session-code", "target", ".credentials.json"), "synthetic CLI login")
	writeDeletionConfig(t, d.configPath(), "other")
	s.saveState()
	return s, d
}

func writeDeletionConfig(t *testing.T, path, owner string) {
	t.Helper()
	plain := []byte(`{"acct:target|client:org:https://api.anthropic.com:user:profile":{"token":"target-secret"},"acct:other|client:org:https://api.anthropic.com:user:profile":{"token":"other-secret"}}`)
	cipher, err := encryptValue(plain)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(cipher)
	data, err := json.Marshal(map[string]any{"lastKnownAccountUuid": owner, "oauth:tokenCacheV2": encoded, "oauth:tokenCache": encoded})
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(data))
}
