package main

import webview "github.com/webview/webview_go"

// The page's width in points; its height comes from the page itself.
const resumeWidth = 460

// showWebDialog shows page in a WebKit window and returns what the page
// passed to resumeDone, or nil if the window was closed. The web view package
// keeps this process's main goroutine on the main thread, as Cocoa needs.
func showWebDialog(page string, chats int) ([]bool, error) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Continue chats?")
	w.SetSize(resumeWidth, 232+min(chats, 5)*64, webview.HintFixed)
	var keep []bool
	w.Bind("resumeFit", func(height int) {
		w.SetSize(resumeWidth, height, webview.HintFixed)
	})
	w.Bind("resumeDone", func(answer []bool) {
		keep = answer
		w.Terminate()
	})
	w.SetHtml(page)
	w.Run()
	return keep, nil
}
