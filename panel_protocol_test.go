package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// Exercise the actual pipe/window bridge without creating any native window.
type testPanelWindow struct {
	mu        sync.Mutex
	bindings  map[string]interface{}
	evaluated []string
	page      string
	init      string
	done      chan struct{}
	once      sync.Once
}

func (w *testPanelWindow) Bind(name string, fn interface{}) error { w.bindings[name] = fn; return nil }
func (w *testPanelWindow) Dispatch(fn func())                     { fn() }
func (w *testPanelWindow) Eval(js string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.evaluated = append(w.evaluated, js)
}
func (w *testPanelWindow) Init(js string)      { w.init = js }
func (w *testPanelWindow) SetHtml(page string) { w.page = page }
func (w *testPanelWindow) Terminate()          { w.once.Do(func() { close(w.done) }) }
func (w *testPanelWindow) Run()                { <-w.done }

func TestPanelPipeUpdatesDoNotReopenAndEOFClosesWindow(t *testing.T) {
	w := &testPanelWindow{bindings: map[string]interface{}{}, done: make(chan struct{})}
	v := panelView{Language: "en", Accounts: []panelAccount{{ID: "synthetic", Name: "Account Alpha"}}}
	var input, output bytes.Buffer
	_ = json.NewEncoder(&input).Encode(panelMessage{View: &v})
	shows := 0
	err := runPanelWebView(w, panelMessage{View: &v, Focus: "synthetic"}, bufio.NewScanner(&input), &output, func(int) {}, func() {}, func() { shows++ })
	if err != nil {
		t.Fatal(err)
	}
	if shows != 0 {
		t.Fatal("background data unexpectedly opened the dismissed window")
	}
	w.mu.Lock()
	if len(w.evaluated) != 1 || !strings.Contains(w.evaluated[0], "window.panelReceive") {
		t.Fatal("cached update was not delivered to the page")
	}
	w.mu.Unlock()
	if !strings.Contains(w.init, "synthetic") || !strings.Contains(w.page, "Account Alpha") {
		t.Fatal("initial focus/data missing")
	}
	action := panelAction{RequestID: 1, Type: "switch", Account: "synthetic", Confirmed: true}
	if err := w.bindings["panelSend"].(func(panelAction) error)(action); err != nil {
		t.Fatal(err)
	}
	var received panelAction
	if json.Unmarshal(bytes.TrimSpace(output.Bytes()), &received) != nil || received != action {
		t.Fatal("explicit action was not serialized to the private pipe")
	}
}
