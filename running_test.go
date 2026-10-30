package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTurnOpen(t *testing.T) {
	const (
		prompt  = `{"type":"user","message":{"role":"user","content":"fix the bug"}}`
		toolUse = `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use"}]}}`
		result  = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
		done    = `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done"}]}}`
		stopped = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
		side    = `{"type":"assistant","isSidechain":true,"message":{"stop_reason":"tool_use"}}`
		meta    = `{"type":"last-prompt","lastPrompt":"x"}`
	)
	for _, c := range []struct {
		name  string
		lines []string
		open  bool
	}{
		{"waiting for the first answer", []string{prompt}, true},
		{"tool running", []string{prompt, toolUse}, true},
		{"tool answered", []string{prompt, toolUse, result}, true},
		{"turn finished", []string{prompt, toolUse, result, done, meta}, false},
		{"stopped by the user", []string{prompt, toolUse, stopped}, false},
		{"subagent after a finished turn", []string{done, side}, false},
		{"cut-off first line", []string{`"stop_reason":"tool_use"}}`, done}, false},
		{"empty", nil, false},
	} {
		if got := turnOpen([]byte(strings.Join(c.lines, "\n") + "\n")); got != c.open {
			t.Errorf("%s: open = %v, want %v", c.name, got, c.open)
		}
	}
}

func TestRunningChats(t *testing.T) {
	dir := t.TempDir()
	open := `{"type":"assistant","message":{"stop_reason":"tool_use"}}`
	write(t, filepath.Join(dir, "D--proj", "busy.jsonl"), open)
	write(t, filepath.Join(dir, "D--proj", "idle.jsonl"), `{"type":"assistant","message":{"stop_reason":"end_turn"}}`)
	old := filepath.Join(dir, "D--other", "old.jsonl")
	write(t, old, open)
	at := time.Now()
	os.Chtimes(old, at.Add(-2*runningWithin), at.Add(-2*runningWithin))
	got := runningChats(dir, at)
	if len(got) != 1 || !got["busy"] {
		t.Fatalf("running = %v, want only busy", got)
	}
}

func TestTakeCutChatsMarksRunningChat(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "b", "ob", "local_one.json")
	write(t, dst, `{"cliSessionId":"run","title":"Running"}`)
	write(t, filepath.Join(root, "b", "ob", "local_two.json"), `{"cliSessionId":"idle","title":"Idle"}`)
	since := time.UnixMilli(1234)
	cut := takeCutChats(root, "b", "ob", since, map[string]bool{"run": true, "elsewhere": true})
	if len(cut) != 1 || cut[0].title != "Running" {
		t.Fatalf("cut = %+v, want the running chat", cut)
	}
	o, err := readObject(dst)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := o.get("interruptedByQuitAt"); string(v) != "1234" {
		t.Fatalf("mark = %s, want the quit time", v)
	}
	if v, _ := o.get("interruptedAwaitingPerson"); string(v) != "false" {
		t.Fatalf("awaiting = %s, want false", v)
	}
}
