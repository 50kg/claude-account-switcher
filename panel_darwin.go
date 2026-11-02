package main

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"

	webview "github.com/webview/webview_go"
)

func preparePanelCommand(*exec.Cmd) {}

func confirmAccountSwitch(account string) bool {
	const script = `on run argv
try
set dialogResult to display dialog (item 1 of argv) with title (item 2 of argv) buttons {item 3 of argv, item 4 of argv} default button 1 cancel button 1 with icon caution
return button returned of dialogResult
on error
return ""
end try
end run`
	confirm := tr("Confirm switch", "确认切换")
	message := fmt.Sprintf(tr("Switch to %s? Claude will restart and running replies may stop.", "切换到%s？这会重启Claude，正在运行的回复可能中断。"), account)
	answer, err := exec.Command("osascript", "-e", script, message, tr("Switch account", "切换账号"), tr("Cancel", "取消"), confirm).Output()
	return err == nil && strings.TrimSpace(string(answer)) == confirm
}

// Cocoa owns its main thread. The same isolated panel uses a standard small
// WebKit window on macOS; Windows supplies the taskbar-anchored flyout.
func showQuotaPanel(initial panelMessage, scanner *bufio.Scanner, output io.Writer) error {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(tr("Account quotas", "账号额度"))
	w.SetSize(424, 700, webview.HintFixed)
	return runPanelWebView(w, initial, scanner, output,
		func(height int) { w.SetSize(424, max(240, min(height, 760)), webview.HintFixed) },
		w.Terminate, func() { w.Eval("window.panelShown && window.panelShown()") })
}
