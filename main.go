// Command claude-account-switcher is a tray tool for the Claude desktop app.
// It switches the app between signed-in accounts without signing in again,
// keeps Claude Code chats visible in every account and shows plan usage.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"fyne.io/systray"
)

const version = "0.3.2"

// Only one switcher may run; a second start exits quietly.
const instancePort = "127.0.0.1:47823"

func main() {
	status := flag.Bool("status", false, tr("print accounts and usage, then exit", "显示账号及用量，然后退出"))
	lang := flag.String("lang", defaultLanguage, "interface language / 界面语言: en, zh-CN")
	flag.Parse()
	if err := setLanguage(*lang); err != nil {
		log.Fatal(err)
	}

	// The home folder, because the Store app redirects AppData for everything
	// it starts but leaves the home folder alone.
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Join(home, ".claude-account-switcher")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	setupLog(filepath.Join(dir, "switcher.log"), *status)

	if *status {
		printStatus(newSwitcher(dir))
		return
	}

	if relaunchOutsideDesktop(dir) {
		return
	}

	lock, err := net.Listen("tcp", instancePort)
	if err != nil {
		log.Print("already running")
		return
	}
	defer lock.Close()

	log.Printf("claude-account-switcher %s, app data in %s", version, claudeDesktop().dir)
	t := &tray{s: newSwitcher(dir), updates: make(chan struct{}, 1)}
	systray.Run(t.onReady, func() {})
}

// setupLog writes to switcher.log and starts it over once it passes 1 MB.
// A tray app has no usable stderr on Windows, so only -status also prints there.
func setupLog(path string, console bool) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return
	}
	if console {
		log.SetOutput(io.MultiWriter(f, os.Stderr))
	} else {
		log.SetOutput(f)
	}
}

// printStatus refreshes once and prints what the menu would show.
func printStatus(s *switcher) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.refresh(ctx)
	snap := s.snapshot()
	if len(snap.Accounts) == 0 {
		fmt.Println(tr("no accounts yet, sign in to Claude", "尚未添加账号，请先在 Claude 中登录"))
	}
	for _, a := range snap.Accounts {
		mark := " "
		if a.UUID == snap.Active {
			mark = "*"
		}
		fmt.Printf("%s %s  %s\n", mark, a.label(), usageText(a, snap.Threshold, time.Now()))
	}
}
