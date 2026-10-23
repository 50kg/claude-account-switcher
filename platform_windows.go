package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// claudeDesktop finds the app data folder. Since 2.42304 (October 2026) the
// Microsoft Store app keeps it in %LOCALAPPDATA%\Claude-Data\claude_<publisher>
// and leaves a .user-data-left marker behind. Older Store builds keep it in
// their package folder, which processes started by that app see as
// %APPDATA%\Claude. The folder with the newest config.json wins.
func claudeDesktop() desktop {
	dirs, _ := filepath.Glob(filepath.Join(os.Getenv("LOCALAPPDATA"), "Packages", "Claude_*", "LocalCache", "Roaming", "Claude"))
	moved, _ := filepath.Glob(filepath.Join(os.Getenv("LOCALAPPDATA"), "Claude-Data", "*"))
	dirs = append(dirs, moved...)
	dirs = append(dirs, filepath.Join(os.Getenv("APPDATA"), "Claude"))
	best, newest := dirs[len(dirs)-1], time.Time{}
	for _, dir := range dirs {
		if info, err := os.Stat(filepath.Join(dir, "config.json")); err == nil && info.ModTime().After(newest) {
			best, newest = dir, info.ModTime()
		}
	}
	return desktop{dir: best}
}

// insideStoreApp reports whether this process was started by a Microsoft
// Store app, for example from a Claude Code terminal. Such processes get the
// app's redirected AppData, which shows as a probe file landing in a package
// folder.
func insideStoreApp() bool {
	name := fmt.Sprintf(".switcher-probe-%d", os.Getpid())
	probe := filepath.Join(os.Getenv("APPDATA"), name)
	if os.WriteFile(probe, nil, 0o600) != nil {
		return false
	}
	defer os.Remove(probe)
	hits, _ := filepath.Glob(filepath.Join(os.Getenv("LOCALAPPDATA"), "Packages", "*", "LocalCache", "Roaming", name))
	return len(hits) > 0
}

// relaunchOutsideDesktop starts the switcher again through Explorer when it
// runs inside the Store app, so it does not share the app's redirections.
// A marker file stops a relaunch loop should the probe ever misjudge.
func relaunchOutsideDesktop(dataDir string) bool {
	if !insideStoreApp() {
		return false
	}
	marker := filepath.Join(dataDir, "relaunched")
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < 30*time.Second {
		return false
	}
	exe, err := os.Executable()
	if err != nil || os.WriteFile(marker, nil, 0o600) != nil {
		return false
	}
	return start("explorer.exe", exe) == nil
}

// desktopProcesses returns the running Claude desktop processes and the app's
// executable. The Claude Code CLI is also called claude.exe, so a process
// only counts when its folder holds the Electron bundle (resources\app.asar).
func desktopProcesses() (pids []uint32, exe string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, ""
	}
	defer windows.CloseHandle(snap)
	isApp := map[string]bool{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), "claude.exe") {
			continue
		}
		path := processPath(e.ProcessID)
		if path == "" {
			continue
		}
		dir := filepath.Dir(path)
		ok, seen := isApp[dir]
		if !seen {
			_, statErr := os.Stat(filepath.Join(dir, "resources", "app.asar"))
			ok = statErr == nil
			isApp[dir] = ok
		}
		if ok {
			pids = append(pids, e.ProcessID)
			exe = path
		}
	}
	return pids, exe
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func desktopRunning() bool {
	pids, _ := desktopProcesses()
	return len(pids) > 0
}

// quitDesktop asks the app to close, then ends it if it is still running.
func quitDesktop() error {
	pids, _ := desktopProcesses()
	if len(pids) == 0 {
		return nil
	}
	began := time.Now()
	run("taskkill", pidArgs(pids)...)
	if waitGone(8 * time.Second) {
		log.Printf("Claude closed after %s", time.Since(began).Round(100*time.Millisecond))
		return nil
	}
	pids, _ = desktopProcesses()
	// No /T: that would also end anything started from a Claude terminal,
	// including this switcher. Every app process is listed in pids anyway.
	run("taskkill", append([]string{"/F"}, pidArgs(pids)...)...)
	if waitGone(10 * time.Second) {
		log.Printf("Claude did not close on request, ended it after %s", time.Since(began).Round(100*time.Millisecond))
		return nil
	}
	return errors.New("Claude did not quit")
}

func pidArgs(pids []uint32) []string {
	var args []string
	for _, p := range pids {
		args = append(args, "/PID", strconv.FormatUint(uint64(p), 10))
	}
	return args
}

func waitGone(timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if !desktopRunning() {
			// Give the file system a moment to release the cookie store.
			time.Sleep(700 * time.Millisecond)
			return true
		}
	}
	return false
}

var appIDPattern = regexp.MustCompile(`<Application\s+Id="([^"]+)"`)

// startDesktop launches the app. Store (MSIX) installs cannot be started from
// their exe, they are started through their app user model ID instead.
func startDesktop(exe string) error {
	if aumid := storeAppID(exe); aumid != "" {
		return start("explorer.exe", `shell:AppsFolder\`+aumid)
	}
	if exe != "" {
		if _, err := os.Stat(exe); err == nil {
			return start(exe)
		}
	}
	for _, p := range []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "AnthropicClaude", "claude.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Claude", "Claude.exe"),
	} {
		if _, err := os.Stat(p); err == nil {
			return start(p)
		}
	}
	out, err := output("powershell", "-NoProfile", "-Command", "(Get-StartApps | Where-Object Name -eq 'Claude' | Select-Object -First 1).AppID")
	if id := strings.TrimSpace(out); err == nil && id != "" {
		return start("explorer.exe", `shell:AppsFolder\`+id)
	}
	return errors.New("could not find the Claude app")
}

// storeAppID derives "Claude_<publisher>!Claude" from a path like
// ...\WindowsApps\Claude_2.7032.0.0_x64__<publisher>\app\Claude.exe.
func storeAppID(exe string) string {
	if !strings.Contains(strings.ToLower(exe), `\windowsapps\`) {
		return ""
	}
	pkgDir := filepath.Dir(filepath.Dir(exe))
	base := filepath.Base(pkgDir)
	name, _, ok1 := strings.Cut(base, "_")
	sep := strings.LastIndex(base, "__")
	manifest, err := os.ReadFile(filepath.Join(pkgDir, "AppxManifest.xml"))
	if !ok1 || sep < 0 || err != nil {
		return ""
	}
	m := appIDPattern.FindSubmatch(manifest)
	if m == nil {
		return ""
	}
	return name + "_" + base[sep+2:] + "!" + string(m[1])
}

// shellOpen opens a folder, or a web page in the default browser.
func shellOpen(target string) { _ = start("explorer.exe", target) }

// launch starts the switcher at exe on its own.
func launch(exe string) error { return start(exe) }

// endOldSwitcher closes a switcher from before 0.3.3 that holds the instance
// port without answering hello. It only touches a process whose file name
// starts with claude-account-switcher, and asks it to close before ending it.
func endOldSwitcher() bool {
	out, err := output("netstat", "-ano", "-p", "TCP")
	if err != nil {
		return false
	}
	// The state column is translated, a listening port shows as 0.0.0.0:0 on
	// the other end in every language.
	var pid uint64
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 5 && f[1] == instancePort && f[2] == "0.0.0.0:0" {
			pid, _ = strconv.ParseUint(f[4], 10, 32)
		}
	}
	if pid == 0 || pid == uint64(os.Getpid()) {
		return false
	}
	name := strings.ToLower(filepath.Base(processPath(uint32(pid))))
	if !strings.HasPrefix(name, "claude-account-switcher") {
		return false
	}
	id := strconv.FormatUint(pid, 10)
	run("taskkill", "/PID", id)
	for range 15 {
		time.Sleep(200 * time.Millisecond)
		if processPath(uint32(pid)) == "" {
			return true
		}
	}
	run("taskkill", "/F", "/PID", id)
	return true
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "ClaudeAccountSwitcher"

// approvedKey holds the on/off switch that Task Manager and Settings show for
// each Run entry. Windows 11 skipped our Run entry at login while it had no
// record here, so enabling writes one the way Task Manager does.
const approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

// approvedOn is Task Manager's "enabled" record: 02 followed by 11 zero bytes.
// An odd first byte (03) means the user disabled the entry there.
var approvedOn = append([]byte{2}, make([]byte, 11)...)

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	if _, _, err = k.GetStringValue(runValue); err != nil {
		return false
	}
	a, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer a.Close()
	b, _, err := a.GetBinaryValue(runValue)
	return err != nil || len(b) == 0 || b[0]&1 == 0
}

func setAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	a, _, err := registry.CreateKey(registry.CURRENT_USER, approvedKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer a.Close()
	if !on {
		_ = a.DeleteValue(runValue)
		return k.DeleteValue(runValue)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := k.SetStringValue(runValue, `"`+exe+`"`); err != nil {
		return err
	}
	return a.SetBinaryValue(runValue, approvedOn)
}

func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

func run(name string, args ...string) { _ = hidden(exec.Command(name, args...)).Run() }

func output(name string, args ...string) (string, error) {
	out, err := hidden(exec.Command(name, args...)).Output()
	return string(out), err
}

func start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
