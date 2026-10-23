package main

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func claudeDesktop() desktop {
	home, _ := os.UserHomeDir()
	return desktop{dir: filepath.Join(home, "Library", "Application Support", "Claude")}
}

// The main process is named "Claude"; helpers are "Claude Helper (...)".
func desktopRunning() bool {
	return exec.Command("pgrep", "-x", "Claude").Run() == nil
}

func desktopProcesses() ([]uint32, string) {
	if desktopRunning() {
		return []uint32{0}, ""
	}
	return nil, ""
}

func quitDesktop() error {
	if !desktopRunning() {
		return nil
	}
	_ = exec.Command("osascript", "-e", `tell application "Claude" to quit`).Run()
	if waitGone(8 * time.Second) {
		return nil
	}
	_ = exec.Command("pkill", "-9", "-x", "Claude").Run()
	if waitGone(10 * time.Second) {
		return nil
	}
	return errors.New(tr("Claude did not quit", "Claude 未能退出，请手动关闭后重试"))
}

func waitGone(timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if !desktopRunning() {
			time.Sleep(700 * time.Millisecond)
			return true
		}
	}
	return false
}

func startDesktop(string) error {
	return exec.Command("open", "-a", "Claude").Run()
}

// shellOpen opens a folder, or a web page in the default browser.
func shellOpen(target string) { _ = exec.Command("open", target).Start() }

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "io.github.snlperstripes.claude-account-switcher.plist")
}

func autostartEnabled() bool {
	_, err := os.Stat(launchAgentPath())
	return err == nil
}

func setAutostart(on bool) error {
	if !on {
		return os.Remove(launchAgentPath())
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>io.github.snlperstripes.claude-account-switcher</string>
	<key>ProgramArguments</key><array><string>%s</string></array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
`, html.EscapeString(exe))
	if err := os.MkdirAll(filepath.Dir(launchAgentPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(launchAgentPath(), []byte(plist), 0o644)
}

func relaunchOutsideDesktop(string) bool { return false }
