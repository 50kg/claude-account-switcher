package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in native smoke test. The child is a test runner, not the app's main
// process, and its WebView profile belongs to this test's temporary directory.
func TestQuotaPanelNativeSmoke(t *testing.T) {
	if os.Getenv("PANEL_NATIVE_SMOKE") != "1" {
		t.Skip("set PANEL_NATIVE_SMOKE=1 for isolated native verification")
	}
	profile, err := os.MkdirTemp("", "quota-native-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		absolute, _ := filepath.Abs(profile)
		root, _ := filepath.Abs(os.TempDir())
		if !strings.EqualFold(filepath.Dir(absolute), root) || !strings.HasPrefix(filepath.Base(absolute), "quota-native-smoke-") {
			return
		}
		for i := 0; i < 25; i++ {
			if os.RemoveAll(absolute) == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Log("temporary WebView profile is still releasing file handles:", absolute)
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestQuotaPanelNativeChild$", "-test.v")
	preparePanelCommand(cmd)
	cmd.Env = append(os.Environ(), "PANEL_NATIVE_CHILD=1", "PANEL_NATIVE_PROFILE="+profile, "WEBVIEW2_USER_DATA_FOLDER="+profile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native test host failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "NATIVE_PANEL_VERIFIED") {
		t.Fatalf("missing native result: %s", output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestQuotaPanelNativeChild(t *testing.T) {
	profile := os.Getenv("PANEL_NATIVE_PROFILE")
	if os.Getenv("PANEL_NATIVE_CHILD") != "1" || profile == "" {
		t.Skip("isolated child only")
	}
	useLanguage(t, "zh-CN")
	now := time.Now()
	view := quotaPanelView(snapshot{Active: "synthetic-only", Accounts: []Account{{UUID: "synthetic-only", Name: "Native Test Account", Usage: &Usage{
		Session: 42, Weekly: 45, CheckedAt: now, ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: 82}},
	}}}}, false, now)
	for _, pair := range []struct {
		id, name               string
		session, weekly, model float64
	}{
		{"synthetic-beta", "Native Test Beta", 17, 28, 36},
		{"synthetic-gamma", "Native Test Gamma", 100, 36, 54},
	} {
		row := quotaPanelView(snapshot{Accounts: []Account{{UUID: pair.id, Name: pair.name, Usage: &Usage{Session: pair.session, Weekly: pair.weekly, CheckedAt: now, ModelWeekly: []ModelWeeklyUsage{{Name: "Fable", Percent: pair.model}}}}}}, false, now).Accounts[0]
		view.Accounts = append(view.Accounts, row)
	}
	in, writer := io.Pipe()
	defer writer.Close()
	ready := make(chan nativePanelProbe, 1)
	finished := make(chan error, 1)
	var output bytes.Buffer
	var mu sync.Mutex
	input := nativePanelInput{inside: true, ownForeground: true, knownForeground: true}
	go func() {
		finished <- showQuotaPanelNative(panelMessage{View: &view, Show: true}, bufio.NewScanner(in), &output, nativePanelOptions{
			dataPath: profile, suppressActivation: true,
			onShown: func(probe nativePanelProbe) { ready <- probe },
			input:   func(uintptr) nativePanelInput { mu.Lock(); defer mu.Unlock(); return input },
		})
	}()
	var probe nativePanelProbe
	select {
	case probe = <-ready:
	case err := <-finished:
		t.Fatalf("host exited before showing: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("native window never became ready")
	}
	if !probe.visible() {
		t.Fatal("created native window was hidden")
	}
	probe.eval("window.dispatchEvent(new Event('blur'))")
	time.Sleep(500 * time.Millisecond)
	if !probe.visible() {
		t.Fatal("browser startup focus change hid the native window")
	}
	mu.Lock()
	input.buttons = 1
	input.inside = false
	input.ownForeground = false
	mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for probe.visible() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if probe.visible() {
		t.Fatal("native outside-click handling did not hide the window")
	}
	mu.Lock()
	input = nativePanelInput{inside: true, ownForeground: true, knownForeground: true}
	mu.Unlock()
	probe.show()
	deadline = time.Now().Add(2 * time.Second)
	for !probe.visible() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !probe.visible() {
		t.Fatal("explicit reopen left the window hidden")
	}
	writer.Close()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closing the parent pipe did not stop the native UI loop")
	}
	if output.Len() != 0 {
		t.Fatal("native read-only display unexpectedly sent an account action")
	}
	fmt.Println("NATIVE_PANEL_VERIFIED: visible; startup blur ignored; outside click dismissed; reopened; EOF closed; account actions=0")
}
