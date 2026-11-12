package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionCodeRequiresMatchingSubscriptionIdentity(t *testing.T) {
	dir := t.TempDir()
	a := Account{Email: "owner@example.test", Org: "target-org"}
	for _, tc := range []struct{ field, value string }{
		{},
		{"email", "another@example.test"},
		{"orgId", "other-org"},
		{"orgId", ""},
		{"configDirectory", filepath.Dir(dir)},
		{"authMethod", "api_key"},
		{"apiProvider", "gateway"},
	} {
		status := map[string]any{
			"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty",
			"configDirectory": dir, "email": "OWNER@example.test", "orgId": a.Org,
		}
		if tc.field != "" {
			status[tc.field] = tc.value
		}
		out, err := json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateSessionCodeStatus(out, nil, dir, a); (err != nil) != (tc.field != "") {
			t.Errorf("%s = %q: unexpected verification result %v", tc.field, tc.value, err)
		}
	}
}

func TestSessionCodeIgnoresInheritedClaudeCredentials(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "default-profile")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "inherited-token")
	t.Setenv("ANTHROPIC_API_KEY", "inherited-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://example.test")
	dir := t.TempDir()
	cmd := sessionCodeCommand(context.Background(), filepath.Join(dir, "claude.exe"), dir, "auth", "status")
	got := 0
	for _, entry := range cmd.Env {
		key, _, _ := strings.Cut(entry, "=")
		if entry == "CLAUDE_CONFIG_DIR="+dir {
			got++
		} else if strings.HasPrefix(strings.ToUpper(key), "CLAUDE_") || strings.HasPrefix(strings.ToUpper(key), "ANTHROPIC_") {
			t.Errorf("unexpected inherited variable %s", key)
		}
	}
	if got != 1 || cmd.Dir != dir {
		t.Fatal("Claude Code did not get a single isolated directory")
	}
}

func TestSessionWindowSkipsActiveWindowAndUsedAllowance(t *testing.T) {
	s := newSwitcher(t.TempDir())
	s.active = "current"
	now := time.Now()
	target := Account{UUID: "target", Usage: &Usage{SessionReset: now.Add(time.Hour), CheckedAt: now}}
	missingCLI := filepath.Join(s.dir, "missing.exe")
	if err := s.startSessionWindow(missingCLI, s.dir, target, s.active); err != nil {
		t.Fatal(err)
	}
	if snap := s.snapshot(); snap.Active != "current" || snap.Status != sessionWindowResetText(target.Usage.SessionReset, now) {
		t.Fatalf("active window changed the desktop account or did not report its reset: %+v", snap)
	}
	target.Usage.SessionReset = time.Time{}
	target.Usage.Weekly = 100
	if err := s.startSessionWindow(missingCLI, s.dir, target, s.active); err == nil ||
		err.Error() != tr("This account has reached its usage limit", "此账号的用量已达上限") {
		t.Fatalf("exhausted account did not stop before running Claude Code: %v", err)
	}
}
