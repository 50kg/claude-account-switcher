package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// A separate process owns this user-opened browser. The host-only session
// cookie travels over stdin, never in a URL, process argument or script.
type billingWebRequest struct{ Account, Org, Key, Dir, Language string }
type billingWebAnswer struct {
	Date     string
	Ending   bool
	Status   string
	Provider string
}

func verifyBillingWeb(s *switcher, id string) error {
	if !s.billingWindow.CompareAndSwap(false, true) {
		return errors.New(tr("A billing window is already open", "账单核验窗口已打开"))
	}
	defer s.billingWindow.Store(false)
	snap := s.snapshot()
	for _, a := range snap.Accounts {
		if a.UUID != id {
			continue
		}
		if a.Billing != nil && a.Billing.Provider == "apple" {
			return editAppleBillingNote(s, a)
		}
		if a.Billing != nil && a.Billing.Reason == "limited" && time.Now().Before(a.Billing.NextCheck) {
			return errors.New(a.Billing.text(time.Now()))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		key, err := s.webSessionFor(ctx, a, snap.Active)
		if err != nil {
			return errors.New(tr("No readable Claude web session for this account", "无法读取此账号的 Claude 网页登录会话"))
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		dir, err := os.MkdirTemp("", "claude-billing-")
		if err != nil {
			return err
		}
		defer func() { // WebView2 releases its files shortly after the child exits.
			for i := 0; i < 100; i++ {
				if os.RemoveAll(dir) == nil {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
		in, _ := json.Marshal(billingWebRequest{id, a.Org, key, dir, language})
		defer clear(in)
		cmd := exec.CommandContext(ctx, exe, "-billing-web")
		cmd.Stdin = bytes.NewReader(in)
		out, err := cmd.Output()
		if err != nil {
			return errors.New(tr("Could not complete the Claude billing window", "Claude 账单核验窗口未能完成查询"))
		}
		var ans billingWebAnswer
		if json.Unmarshal(out, &ans) != nil {
			return errBillingUnavailable
		}
		switch ans.Status {
		case "closed":
			return nil
		case "owner":
			return errors.New(tr("The web session belongs to a different account or organization", "网页登录身份与该账号或组织不匹配"))
		case "denied":
			return errors.New(tr("Claude did not grant access to this account's billing", "Claude 未授权读取此账号的账单"))
		case "done":
		default:
			return errors.New(tr("Claude did not return a readable billing response", "Claude 未返回可读取的账单结果"))
		}
		b := billingInfo{Date: ans.Date, Ending: ans.Ending, NextCheck: time.Now().Add(billingInterval)}
		if ans.Provider == "apple" {
			b.Provider = "apple"
		}
		if b.Date != "" && billingDay(b.Date) == "" {
			return errBillingUnavailable
		}
		s.mu.Lock()
		dest := s.account(id)
		if dest == nil || dest.Org != a.Org {
			s.mu.Unlock()
			return errBillingUnavailable
		}
		dest.Billing = &b
		s.saveState()
		s.mu.Unlock()
		a.Billing = &b
		s.setStatus(a.label() + " · " + a.billingText(time.Now()))
		return nil
	}
	return errBillingUnavailable
}

func runBillingWeb() int {
	runtime.LockOSThread()
	var req billingWebRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 32<<10)).Decode(&req) != nil || req.Key == "" || req.Dir == "" || req.Org == "" || req.Account == "" {
		return 2
	}
	_ = setLanguage(req.Language)
	answer, err := billingBrowser(req)
	if err != nil {
		return 1
	}
	if json.NewEncoder(os.Stdout).Encode(answer) != nil {
		return 1
	}
	return 0
}

func billingBrowser(req billingWebRequest) (billingWebAnswer, error) {
	accountURL := "https://claude.ai/api/account"
	detailsURL := "https://claude.ai/api/organizations/" + url.PathEscape(req.Org) + "/subscription_details"
	current := accountURL
	answer := billingWebAnswer{Status: "closed"}
	browser := edge.NewChromium()
	browser.DataPath = req.Dir
	browser.SetGlobalPermission(edge.CoreWebView2PermissionStateDeny)
	var ready bool
	proc := windows.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
		switch msg {
		case 5:
			if ready {
				browser.Resize()
			}
			return 0
		case 2:
			qmPostQuit.Call(0)
			return 0
		}
		r, _, _ := qmDefWindowProc.Call(hwnd, msg, wp, lp)
		return r
	})
	var inst windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &inst)
	class := windows.StringToUTF16Ptr("ClaudeBillingCheck")
	cursor, _, _ := qmLoadCursor.Call(0, 32512)
	wc := qmWindowClass{Proc: proc, Instance: uintptr(inst), ClassName: class, Cursor: cursor}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if v, _, err := qmRegisterClass.Call(uintptr(unsafe.Pointer(&wc))); v == 0 {
		return answer, err
	}
	title := windows.StringToUTF16Ptr(tr("Claude billing · complete web verification if asked", "Claude 会员日期 · 如出现网页验证，请在此窗口完成"))
	hwnd, _, err := qmCreateWindow.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0x00cf0000, 0x80000000, 0x80000000, 710, 515, 0, 0, uintptr(inst), 0)
	if hwnd == 0 {
		return answer, err
	}
	defer qmDestroyWindow.Call(hwnd)
	if !browser.Embed(hwnd) {
		return answer, errBillingUnavailable
	}
	ready = true
	browser.Resize()
	// A fresh private directory, a session cookie and no default browser profile.
	manager, e := browser.GetCookieManager()
	if e != nil {
		return answer, e
	}
	defer manager.Release()
	cookie, e := manager.CreateCookie("sessionKey", req.Key, "claude.ai", "/")
	if e != nil {
		return answer, e
	}
	defer cookie.Release()
	if e = cookie.PutIsHttpOnly(true); e != nil {
		return answer, e
	}
	if e = cookie.PutIsSecure(true); e != nil {
		return answer, e
	}
	if e = manager.AddOrUpdateCookie(cookie); e != nil {
		return answer, e
	}
	defer manager.DeleteAllCookies()
	finish := func(status string) { answer.Status = status; qmPostQuit.Call(0) }
	browser.NavigationCompletedCallback = func(sender *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		src, _ := sender.GetSource()
		// Leave real HTML verification pages intact. Read only the exact two
		// top-level JSON documents once the site's own verification completes.
		if src != current {
			return
		}
		target, _ := json.Marshal(current)
		extract := `({uuid:x.uuid,orgs:(x.memberships||[]).map(m=>m.organization?.uuid),error:!!x.error})`
		if current == detailsURL {
			extract = `({plan_ending_at:x.plan_ending_at,plan_ending_before:x.plan_ending_before,next_charge_at:x.next_charge_at,next_charge_date:x.next_charge_date,subscription_details_url:x.subscription_details_url==="https://apps.apple.com/account/subscriptions"?x.subscription_details_url:null,error:!!x.error})`
		}
		browser.Eval(`(()=>{if(location.href!==` + string(target) + ` || !document.contentType.includes("json"))return;
		try{const x=JSON.parse((document.querySelector("pre")||document.body).textContent);
		const data=` + extract + `;document.body.textContent="Claude";chrome.webview.postMessage(JSON.stringify(data))}
		catch{chrome.webview.postMessage('{"error":true}')}})()`)
	}
	browser.MessageCallback = func(msg string, sender *edge.ICoreWebView2, args *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		source, _ := args.GetSource()
		top, _ := sender.GetSource()
		if source != current || top != current || len(msg) > 32768 {
			return
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(msg), &fields) != nil || string(fields["error"]) != "false" {
			finish("denied")
			return
		}
		if current == accountURL {
			var identity struct {
				UUID string
				Orgs []string
			}
			if json.Unmarshal([]byte(msg), &identity) != nil || identity.UUID != req.Account {
				finish("owner")
				return
			}
			for _, org := range identity.Orgs {
				if org == req.Org {
					current = detailsURL
					browser.Navigate(current)
					return
				}
			}
			finish("owner")
			return
		}
		b := parseBillingDetails(fields)
		answer.Date, answer.Ending = b.Date, b.Ending
		answer.Provider = b.Provider
		finish("done")
	}
	qmShowWindow.Call(hwnd, 5)
	browser.Navigate(current)
	var msg qmMessage
	for {
		v, _, _ := qmGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(v) <= 0 {
			break
		}
		qmTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		qmDispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
	browser.ShuttingDown()
	runtime.KeepAlive(browser)
	return answer, nil
}
