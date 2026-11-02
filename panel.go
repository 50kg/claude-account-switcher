package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const panelFlag = "-quota-panel"

//go:embed panel.html
var panelPage string

//go:embed panel.css
var panelCSS string

//go:embed panel.js
var panelJS string

type panelAction struct {
	RequestID uint64  `json:"requestId"`
	Type      string  `json:"type"`
	Account   string  `json:"account,omitempty"`
	Active    string  `json:"active"`
	Confirmed bool    `json:"confirmed,omitempty"`
	Enabled   bool    `json:"enabled,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}

type panelMessage struct {
	View      *panelView `json:"view,omitempty"`
	Show      bool       `json:"show,omitempty"`
	Focus     string     `json:"focus,omitempty"`
	RequestID uint64     `json:"requestId,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type panelSession struct {
	input   io.WriteCloser
	updates chan panelMessage
	done    chan struct{}
}

type quotaPanel struct {
	mu       sync.Mutex
	session  *panelSession
	closed   bool
	view     func() panelView
	onAction func(panelAction) error
	onError  func(string)
}

// A child process owns the window, as in the existing chat-resume dialog.
// The only transport is inherited pipes: there is no local HTTP server,
// browser navigation, token access or extra account query on opening.
func (p *quotaPanel) show(focus string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	v := p.view()
	if p.session != nil {
		p.enqueue(p.session, panelMessage{View: &v, Show: true, Focus: focus})
		return
	}
	exe, err := os.Executable()
	if err != nil {
		p.onError(err.Error())
		return
	}
	cmd := exec.Command(exe, panelFlag)
	preparePanelCommand(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		p.onError(err.Error())
		return
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		p.onError(err.Error())
		return
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		p.onError(err.Error())
		return
	}
	s := &panelSession{input: in, updates: make(chan panelMessage, 1), done: make(chan struct{})}
	p.session = s
	// Write the initial state before coalesced updates: the child must receive
	// its language and data before constructing any native window.
	go func() {
		defer in.Close()
		encoder := json.NewEncoder(in)
		if encoder.Encode(panelMessage{View: &v, Show: true, Focus: focus}) != nil {
			return
		}
		for {
			select {
			case <-s.done:
				return
			case msg := <-s.updates:
				if encoder.Encode(msg) != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer out.Close()
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			var action panelAction
			if json.Unmarshal(scanner.Bytes(), &action) != nil {
				continue
			}
			err := p.onAction(action)
			reply := panelMessage{RequestID: action.RequestID}
			if err != nil {
				reply.Error = err.Error()
			}
			p.mu.Lock()
			if p.session == s && !p.closed {
				current := p.view()
				reply.View = &current
				p.enqueue(s, reply)
			}
			p.mu.Unlock()
		}
	}()
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if p.session == s {
			p.session = nil
		}
		close(s.done)
		closed := p.closed
		p.mu.Unlock()
		if err != nil && !closed {
			p.onError(tr("Quota panel unavailable; use the right-click menu", "额度面板暂不可用，请使用托盘右键菜单"))
		}
	}()
}

// Publishing a view must never block the tray or its polling goroutine.
func (p *quotaPanel) enqueue(s *panelSession, message panelMessage) {
	select {
	case s.updates <- message:
	default:
		select {
		case old := <-s.updates:
			// Preserve show requests and acknowledgements when replacing data.
			message.Show = message.Show || old.Show
			if message.Focus == "" {
				message.Focus = old.Focus
			}
			if message.RequestID == 0 {
				message.RequestID, message.Error = old.RequestID, old.Error
			}
		default:
		}
		select {
		case s.updates <- message:
		default:
		}
	}
}

func (p *quotaPanel) update() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed && p.session != nil {
		v := p.view()
		p.enqueue(p.session, panelMessage{View: &v})
	}
}

func (p *quotaPanel) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.session != nil {
		p.session.input.Close()
	}
}

func validatePanelAction(a panelAction, s snapshot) error {
	if s.Busy {
		return errors.New(tr("Another operation is in progress", "另一项操作正在进行，请稍后再试"))
	}
	switch a.Type {
	case "switch", "add", "import":
		if !a.Confirmed {
			return errors.New(tr("Confirm the restart first", "请先确认重启Claude"))
		}
		if a.Active != s.Active {
			return errors.New(tr("The active account changed; review it again", "当前账号已变化，请重新确认"))
		}
		if a.Type == "switch" {
			for _, account := range s.Accounts {
				if account.UUID == a.Account && a.Account != s.Active {
					return nil
				}
			}
			return errors.New(tr("This account is no longer available to switch to", "此账号当前无法切换"))
		}
	case "auto", "spend", "autostart", "folder", "update":
	case "threshold":
		for _, value := range thresholds {
			if a.Threshold == value {
				return nil
			}
		}
		return errors.New(tr("Invalid switching threshold", "切换阈值不受支持"))
	default:
		return errors.New(tr("Unknown panel action", "不支持的面板操作"))
	}
	return nil
}

func quotaPanelHTML(v panelView) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if !strings.Contains(panelPage, "/*PANEL_DATA*/null") {
		return "", errors.New("missing panel data slot")
	}
	page := strings.Replace(panelPage, "/*PANEL_DATA*/null", string(data), 1)
	page = strings.Replace(page, "/*PANEL_CSS*/", panelCSS, 1)
	page = strings.Replace(page, "/*PANEL_JS*/", panelJS, 1)
	return page, nil
}

func runQuotaPanel() int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 512<<10)
	var initial panelMessage
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &initial) != nil || initial.View == nil {
		return 2
	}
	if setLanguage(initial.View.Language) != nil {
		return 2
	}
	if err := showQuotaPanel(initial, scanner, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "quota panel unavailable")
		return 1
	}
	return 0
}

type panelWebView interface {
	Bind(string, interface{}) error
	Dispatch(func())
	Eval(string)
	Terminate()
	SetHtml(string)
	Init(string)
	Run()
}

func runPanelWebView(w panelWebView, initial panelMessage, scanner *bufio.Scanner, output io.Writer, fit func(int), hide, show func()) error {
	page, err := quotaPanelHTML(*initial.View)
	if err != nil {
		return err
	}
	var writeMu sync.Mutex
	for name, handler := range map[string]interface{}{
		"panelFit":  fit,
		"panelHide": hide,
		"panelSend": func(action panelAction) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return json.NewEncoder(output).Encode(action)
		},
	} {
		if err := w.Bind(name, handler); err != nil {
			return err
		}
	}
	go func() {
		for scanner.Scan() {
			var message panelMessage
			if json.Unmarshal(scanner.Bytes(), &message) != nil {
				continue
			}
			data, err := json.Marshal(message)
			if err != nil {
				continue
			}
			w.Dispatch(func() {
				w.Eval("if (window.panelReceive) { window.panelReceive(" + string(data) + ") } else { window.__panelQueued = " + string(data) + " }")
				if message.Show {
					show()
				}
			})
		}
		w.Terminate() // The tray exited or closed its pipe; do not leave an orphan.
	}()
	focus, _ := json.Marshal(initial.Focus)
	w.Init("window.__panelInitialFocus = " + string(focus))
	w.SetHtml(page)
	w.Run()
	return nil
}
