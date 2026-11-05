package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	webview "github.com/jchv/go-webview2"
)

type appleDateForm struct {
	Account, Language string
	Note              *billingNote
}
type appleDateResult struct {
	Action string
	Note   billingNote
}

func editAppleBillingNote(s *switcher, a Account) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	in, _ := json.Marshal(appleDateForm{a.label() + " · " + a.Email, language, a.AppleDate})
	cmd := exec.CommandContext(ctx, exe, "-apple-date")
	cmd.Stdin = bytes.NewReader(in)
	data, err := cmd.Output()
	if err != nil {
		return errors.New(tr("Could not open the subscription date form", "无法打开订阅日期窗口"))
	}
	var result appleDateResult
	if err = json.Unmarshal(data, &result); err != nil {
		return err
	}
	return saveAppleNote(s, a, result)
}

func saveAppleNote(s *switcher, a Account, result appleDateResult) error {
	switch result.Action {
	case "cancel":
		return nil
	case "save":
		if _, err := time.Parse(time.DateOnly, result.Note.Date); err != nil {
			return errBillingUnavailable
		}
	case "clear":
	default:
		return errBillingUnavailable
	}
	s.mu.Lock()
	dest := s.account(a.UUID)
	if dest == nil || dest.Org != a.Org || dest.Billing == nil || dest.Billing.Provider != "apple" {
		s.mu.Unlock()
		return errBillingUnavailable
	}
	if result.Action == "clear" {
		dest.AppleDate = nil
	} else {
		dest.AppleDate = &result.Note
	}
	s.saveState()
	s.mu.Unlock()
	s.onChange()
	return nil
}

// A local-only form. The Apple link opens in the system browser without
// any Claude credential. No Apple login is requested or stored here.
func runAppleNote() int {
	var f appleDateForm
	if json.NewDecoder(io.LimitReader(os.Stdin, 32<<10)).Decode(&f) != nil {
		return 1
	}
	_ = setLanguage(f.Language)
	home, _ := os.UserHomeDir()
	w := webview.NewWithOptions(webview.WebViewOptions{AutoFocus: true, DataPath: filepath.Join(home, ".claude-account-switcher", "webview"), WindowOptions: webview.WindowOptions{Title: tr("Apple subscription date", "Apple 订阅日期"), Width: 520, Height: 410}})
	if w == nil {
		return 1
	}
	defer w.Destroy()
	result := appleDateResult{Action: "cancel"}
	if err := w.Bind("openApple", func() { shellOpen(appleSubscriptionsURL) }); err != nil {
		return 1
	}
	if err := w.Bind("finishDate", func(action, date string, ending bool) {
		result = appleDateResult{action, billingNote{date, ending}}
		w.Terminate()
	}); err != nil {
		return 1
	}
	page := template.Must(template.New("form").Funcs(template.FuncMap{"tr": tr}).Parse(appleDateHTML))
	var html bytes.Buffer
	if page.Execute(&html, f) != nil {
		return 1
	}
	w.SetHtml(html.String())
	w.Run()
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		return 1
	}
	return 0
}

const appleDateHTML = `<!doctype html><meta charset=utf-8><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; form-action 'none'">
<style>*{box-sizing:border-box}body{font:14px system-ui;margin:24px;color:#20242b}h2{font-size:19px;margin:0 0 8px}p{margin:8px 0;line-height:1.55}small{color:#475569;overflow-wrap:anywhere}button,input,select{font:inherit;padding:6px 9px}button{cursor:pointer}label{display:flex;flex-direction:column;gap:5px}section,footer{display:flex;gap:12px;margin-top:16px}footer{justify-content:flex-end}.link{border:0;background:none;color:#1357a6;padding:0}.save{background:#215fac;color:white;border:1px solid #215fac;border-radius:4px}</style>
<h2>{{tr "Apple subscription date" "Apple 订阅日期"}}</h2><small>{{.Account}}</small>
<p>{{tr "Check the subscription in the Apple Account used to purchase it. The date you save here will be labelled as manual." "请使用购买这个会员的 Apple 账户查看订阅。填入的日期只保存在本机，托盘会标注“手动记录”。"}}</p>
<button class=link onclick="openApple()">{{tr "Open Apple Subscriptions ↗" "打开 Apple 订阅 ↗"}}</button>
<form onsubmit="event.preventDefault(); finishDate('save',day.value,kind.value==='end')"><section>
<label>{{tr "Date type" "日期类型"}}<select id=kind><option value=renew>{{tr "Next renewal" "下次续费"}}</option><option value=end {{if .Note}}{{if .Note.Ending}}selected{{end}}{{end}}>{{tr "Membership ends" "会员到期"}}</option></select></label>
<label>{{tr "Date shown by Apple" "Apple 显示的日期"}}<input id=day type=date required value="{{if .Note}}{{.Note.Date}}{{end}}"></label></section>
<footer>{{if .Note}}<button type=button onclick="finishDate('clear','',false)">{{tr "Clear date" "清除记录"}}</button>{{end}}<button type=button onclick="finishDate('cancel','',false)">{{tr "Cancel" "取消"}}</button><button class=save>{{tr "Save" "保存"}}</button></footer></form>`
