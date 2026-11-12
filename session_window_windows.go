package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const sessionWindowPrompt = "Reply with only OK."

func handleSessionWindowAction(s *switcher, id string, login bool) error {
	if !s.busy.CompareAndSwap(false, true) {
		return errors.New(tr("An account operation is already running", "正在处理账号操作，请稍候"))
	}
	defer func() {
		s.busy.Store(false)
		s.onChange()
	}()
	snap := s.snapshot()
	if snap.Syncing {
		return errors.New(tr("Usage is syncing; try again when it finishes", "正在同步用量，请等待同步完成"))
	}
	for _, a := range snap.Accounts {
		if a.UUID != id {
			continue
		}
		if a.Email == "" || a.Org == "" || !validTransferAccount(accountTransferAccount{UUID: id, Org: a.Org, Email: a.Email}) {
			return errors.New(tr("This account is missing the identity needed to use Claude Code", "此账号缺少用于验证 Claude Code 登录的身份信息"))
		}
		cli, dir, err := sessionCode(s.dir, id)
		if err != nil {
			return err
		}
		if login {
			s.setStatus(tr("Complete Claude Code sign-in in the browser", "请在浏览器完成 Claude Code 登录"))
			if err := openSessionCodeLogin(cli, dir, a.Email); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := checkSessionCodeAccount(ctx, cli, dir, a); err != nil {
				return err
			}
			s.setStatus(tr("Claude Code signed in. You can now start the 5-hour window.", "Claude Code 已登录，现在可以启动 5 小时窗口。"))
			return nil
		}
		return s.startSessionWindow(cli, dir, a, snap.Active)
	}
	return errors.New(tr("Account no longer exists", "该账号已不存在"))
}

func (s *switcher) startSessionWindow(cli, dir string, a Account, active string) error {
	s.setStatus(tr("Checking the account's 5-hour window…", "正在检查此账号的 5 小时窗口…"))
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	now := time.Now()
	u := a.Usage
	if u == nil || a.Problem != "" || now.Sub(u.CheckedAt) >= activeFresh ||
		(!u.SessionReset.IsZero() && !u.SessionReset.After(now)) ||
		(!u.WeeklyReset.IsZero() && !u.WeeklyReset.After(now)) {
		var err error
		u, err = s.refreshSessionWindowUsage(ctx, a.UUID, active)
		if err != nil {
			return fmt.Errorf(tr("Could not verify usage for this account. %s", "暂时无法确认此账号的用量。%s"), err)
		}
	}
	if now = time.Now(); u.SessionReset.After(now) {
		s.setStatus(sessionWindowResetText(u.SessionReset, now))
		return nil
	}
	if u.Weekly >= 100 || u.Session >= 100 {
		return errors.New(tr("This account has reached its usage limit", "此账号的用量已达上限"))
	}
	if err := checkSessionCodeAccount(ctx, cli, dir, a); err != nil {
		return err
	}

	s.setStatus(tr("Sending one short message…", "正在发送一条短消息…"))
	cmd := sessionCodeCommand(ctx, cli, dir, "-p", "--safe-mode", "--no-session-persistence",
		"--model", "haiku", "--tools", "", "--disallowedTools", "*", "--max-turns", "1",
		"--system-prompt", "You are a helpful assistant.", "--output-format", "json", sessionWindowPrompt)
	out, err := hidden(cmd).Output()
	var result struct {
		Subtype string `json:"subtype"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err != nil || json.Unmarshal(out, &result) != nil || result.Subtype != "success" || result.IsError || strings.TrimSpace(result.Result) == "" {
		return errors.New(tr("Claude Code did not confirm a reply. Sync usage before trying again.", "Claude Code 未确认回复，请先同步用量再重试。"))
	}

	// Only the server can confirm the window. The request may have succeeded
	// even if usage is delayed or rate limited.
	u, err = s.refreshSessionWindowUsage(ctx, a.UUID, active)
	if err == nil && u.SessionReset.After(time.Now()) {
		s.setStatus(sessionWindowResetText(u.SessionReset, time.Now()))
	} else {
		s.setStatus(tr("Message sent. Waiting for Claude to report the 5-hour window.", "消息已发送，等待 Claude 返回 5 小时窗口。"))
	}
	return nil
}

func sessionWindowResetText(at, now time.Time) string {
	return tr("5-hour window active · ", "5 小时窗口已开启 · ") + quotaResetText(at, now)
}

func (s *switcher) refreshSessionWindowUsage(ctx context.Context, id, active string) (*Usage, error) {
	s.mu.Lock()
	wait := s.backoff[id]
	s.mu.Unlock()
	if wait.After(time.Now()) {
		return nil, errors.New(tr("Usage check is waiting to retry", "用量查询正在等待重试"))
	}
	u, err := s.queryAccountUsage(ctx, id, active)
	s.mu.Lock()
	s.recordUsageResult(id, u, err, time.Now())
	s.saveState()
	s.mu.Unlock()
	return u, err
}

func sessionCode(root, id string) (exe, dir string, err error) {
	exe, err = exec.LookPath("claude.exe")
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", "", homeErr
		}
		exe = filepath.Join(home, ".local", "bin", "claude.exe")
		if info, statErr := os.Stat(exe); statErr != nil || info.IsDir() {
			return "", "", errors.New(tr("Install the latest native Claude Code for Windows first", "请先安装最新版 Windows 原生 Claude Code"))
		}
	}
	dir = filepath.Join(root, "session-code", id)
	return exe, dir, os.MkdirAll(dir, 0o700)
}

func sessionCodeCommand(ctx context.Context, exe, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	for _, value := range cmd.Environ() {
		key, _, _ := strings.Cut(value, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "CLAUDE_") || strings.HasPrefix(key, "ANTHROPIC_") || key == "CLAUDECODE" {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+dir)
	return cmd
}

func checkSessionCodeAccount(ctx context.Context, exe, dir string, a Account) error {
	cmd := sessionCodeCommand(ctx, exe, dir, "auth", "status")
	out, err := hidden(cmd).Output()
	return validateSessionCodeStatus(out, err, dir, a)
}

func validateSessionCodeStatus(out []byte, runErr error, dir string, a Account) error {
	var status struct {
		LoggedIn        bool   `json:"loggedIn"`
		AuthMethod      string `json:"authMethod"`
		APIProvider     string `json:"apiProvider"`
		ConfigDirectory string `json:"configDirectory"`
		Email           string `json:"email"`
		OrgID           string `json:"orgId"`
	}
	if json.Unmarshal(out, &status) != nil {
		return errors.New(tr("Could not read Claude Code sign-in. Make sure Claude Code is up to date.", "无法读取 Claude Code 登录状态，请确认已更新到最新版。"))
	}
	if runErr != nil || !status.LoggedIn || status.AuthMethod != "claude.ai" || status.APIProvider != "firstParty" {
		return errors.New(tr("Choose “Sign in to Claude Code” for this account first", "请先在此账号的菜单中选择“为此账号登录 Claude Code”"))
	}
	if !samePath(status.ConfigDirectory, dir) || !strings.EqualFold(status.Email, a.Email) || status.OrgID != a.Org {
		return errors.New(tr("Claude Code sign-in does not match this account and organization. Sign in again.", "Claude Code 登录与所选账号或组织不匹配，请重新登录。"))
	}
	return nil
}

// The native executable gets a fresh console with working input for the
// official login flow, including the browser's manual-code fallback.
func openSessionCodeLogin(exe, dir, email string) error {
	cmd := sessionCodeCommand(context.Background(), exe, dir, "auth", "login", "--claudeai", "--email="+email)
	line, err := windows.UTF16FromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return err
	}
	app, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	work, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	slices.SortFunc(cmd.Env, func(a, b string) int {
		a, _, _ = strings.Cut(a, "=")
		b, _, _ = strings.Cut(b, "=")
		return strings.Compare(strings.ToUpper(a), strings.ToUpper(b))
	})
	env := utf16.Encode([]rune(strings.Join(cmd.Env, "\x00") + "\x00\x00"))
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var proc windows.ProcessInformation
	if err := windows.CreateProcess(app, &line[0], nil, nil, false,
		windows.CREATE_NEW_CONSOLE|windows.CREATE_UNICODE_ENVIRONMENT, &env[0], work, &startup, &proc); err != nil {
		return err
	}
	_ = windows.CloseHandle(proc.Thread)
	defer windows.CloseHandle(proc.Process)
	wait, err := windows.WaitForSingleObject(proc.Process, uint32((5*time.Minute)/time.Millisecond))
	var exit uint32
	if err == nil && wait == uint32(windows.WAIT_TIMEOUT) {
		_ = windows.TerminateProcess(proc.Process, 1)
	}
	if err != nil || wait != windows.WAIT_OBJECT_0 || windows.GetExitCodeProcess(proc.Process, &exit) != nil || exit != 0 {
		return errors.New(tr("Claude Code sign-in did not complete", "Claude Code 登录未完成"))
	}
	return nil
}
