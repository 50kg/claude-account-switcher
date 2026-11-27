// Command claude-account-switcher is a Windows tray tool for the Claude
// desktop app. It switches the app between signed-in accounts without signing
// in again, keeps Claude Code chats visible in every account and shows plan
// usage and subscription dates.
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
	"golang.org/x/sys/windows"
)

const version = "1.0.0"

// Helper processes started by the switcher itself, one flag each.
const (
	appleDateFlag  = "-apple-date"
	billingWebFlag = "-billing-web"
)

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case appleDateFlag:
			os.Exit(runAppleNote())
		case billingWebFlag:
			os.Exit(runBillingWeb())
		case resumeFlag: // started by askInDialogProcess
			os.Exit(runResumeDialog())
		}
	}
	status := flag.Bool("status", false, "print accounts and usage, then exit / 显示账号及用量后退出")
	// Before 1.0 the language was a flag; shortcuts with it must still start.
	langFlag := flag.String("lang", "", "deprecated: saves en or zh-CN as the language / 已弃用：保存界面语言")
	flag.Parse()

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
	if lang, ok := normalizeLanguage(*langFlag); ok {
		_ = saveLanguagePreference(filepath.Join(dir, "ui.json"), lang)
	}
	_ = setLanguage(startLanguage(dir))

	if *status {
		attachConsole()
		setupLog(filepath.Join(dir, "switcher.log"), true)
		printStatus(newSwitcher(dir), !instanceRunning())
		return
	}
	setupLog(filepath.Join(dir, "switcher.log"), false)

	if relaunchOutsideDesktop(dir) {
		return
	}

	lock, ok := lockInstance(instancePort)
	if !ok {
		return
	}
	defer lock.Close()
	go removeReplaced()

	log.Printf("claude-account-switcher %s, app data in %s", version, claudeDesktop().dir)
	t := &tray{s: newSwitcher(dir), updates: make(chan struct{}, 1)}
	// When a newer version starts, a running switch finishes first and no new
	// one begins before this one quits.
	go serveInstance(lock, func() {
		for !t.s.busy.CompareAndSwap(false, true) {
			time.Sleep(200 * time.Millisecond)
		}
		systray.Quit()
	})
	systray.Run(t.onReady, func() {
		if t.menu != nil {
			t.menu.close()
		}
	})
}

// startLanguage is the language saved in Settings. 1.0 and later always save
// one on the first start, so a data folder without it comes from the releases
// before 1.0, which were Chinese builds: they stay Chinese. A new install
// starts in the Windows display language.
func startLanguage(dir string) string {
	path := filepath.Join(dir, "ui.json")
	p, err := loadQuotaMenuPreferences(path)
	if err == nil && p.Language != "" {
		return p.Language
	}
	lang := systemLanguage()
	if _, statErr := os.Stat(filepath.Join(dir, "state.json")); statErr == nil {
		lang = langChinese
	}
	_ = saveLanguagePreference(path, lang)
	return lang
}

// setupLog writes to switcher.log and starts it over once it passes 1 MB.
// The tray app has no usable stderr, so only -status also prints there.
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

// attachConsole lets -status print in the terminal it was typed in: the
// release build is a GUI program, which Windows starts without a console.
// Output redirected to a file or pipe already has a handle and is kept.
func attachConsole() {
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		return
	}
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	if ok, _, _ := procAttachConsole.Call(attachParentProcess); ok == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = out, out
		fmt.Println()
	}
}

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// instanceRunning reports whether the tray switcher holds the instance port.
func instanceRunning() bool {
	l, err := net.Listen("tcp", instancePort)
	if err != nil {
		return true
	}
	l.Close()
	return false
}

// printStatus prints what the menu would show. While the tray runs it only
// reads the saved state, so the two never write state.json at once; on its
// own it checks usage first. A switch that is due is left to the tray.
func printStatus(s *switcher, refresh bool) {
	if refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.check(ctx)
	}
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
